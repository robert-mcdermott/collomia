#Requires -Version 5.1

[CmdletBinding()]
param(
    [string]$Version = $(if ($env:COLLO_VERSION) { $env:COLLO_VERSION } else { 'latest' }),
    [string]$InstallDir = $(if ($env:COLLO_INSTALL_DIR) { $env:COLLO_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\Collomia' }),
    [string]$Repository = $(if ($env:COLLO_REPOSITORY) { $env:COLLO_REPOSITORY } else { 'robert-mcdermott/collomia' }),
    [switch]$AddToPath
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version 2.0

function Get-CollomiaAsset {
    param([string]$Architecture = [Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString())

    switch ($Architecture) {
        'X64'   { return 'collo-windows-amd64.exe' }
        'AMD64' { return 'collo-windows-amd64.exe' }
        'Arm64' { return 'collo-windows-arm64.exe' }
        default { throw "Unsupported Windows architecture: $Architecture" }
    }
}

function Resolve-CollomiaVersion {
    param([Parameter(Mandatory = $true)][string]$Value)

    if ($Value -eq 'latest') { return $Value }
    if ($Value -match '^[0-9]') { $Value = "v$Value" }
    if ($Value -notmatch '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?(\+[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?$') {
        throw "Invalid release version: $Value"
    }
    return $Value
}

function Get-CollomiaExpectedChecksum {
    param(
        [Parameter(Mandatory = $true)][string]$ManifestPath,
        [Parameter(Mandatory = $true)][string]$Asset
    )

    $escaped = [regex]::Escape($Asset)
    $entries = @(
        Get-Content -LiteralPath $ManifestPath | ForEach-Object {
            if ($_ -match "^(?<hash>[0-9A-Fa-f]{64})[ `t]+[*]?$escaped$") {
                $Matches['hash'].ToLowerInvariant()
            }
        }
    )
    if ($entries.Count -ne 1) {
        throw "checksums.txt does not contain exactly one entry for $Asset"
    }
    return $entries[0]
}

function Add-CollomiaUserPath {
    param([Parameter(Mandatory = $true)][string]$Directory)

    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    $entries = @($userPath -split ';' | Where-Object { $_ })
    $normalized = $Directory.TrimEnd('\')
    $present = $false
    foreach ($entry in $entries) {
        if ($entry.TrimEnd('\').Equals($normalized, [StringComparison]::OrdinalIgnoreCase)) {
            $present = $true
            break
        }
    }
    if (-not $present) {
        [Environment]::SetEnvironmentVariable('Path', ((@($entries) + $Directory) -join ';'), 'User')
    }
    if (-not (($env:Path -split ';') | Where-Object { $_.TrimEnd('\').Equals($normalized, [StringComparison]::OrdinalIgnoreCase) })) {
        $env:Path = "$env:Path;$Directory"
    }
}

function Invoke-CollomiaInstall {
    [CmdletBinding()]
    param(
        [string]$Version = 'latest',
        [string]$InstallDir = (Join-Path $env:LOCALAPPDATA 'Programs\Collomia'),
        [string]$Repository = 'robert-mcdermott/collomia',
        [switch]$AddToPath
    )

    if (-not $env:OS -or $env:OS -ne 'Windows_NT') {
        throw 'install.ps1 supports native Windows only'
    }
    if ($Repository -notmatch '^[0-9A-Za-z_.-]+/[0-9A-Za-z_.-]+$') {
        throw "Repository must use owner/repository syntax: $Repository"
    }
    if (-not [IO.Path]::IsPathRooted($InstallDir)) {
        throw "Installation directory must be an absolute path: $InstallDir"
    }

    $Version = Resolve-CollomiaVersion $Version
    $asset = Get-CollomiaAsset
    $base = if ($Version -eq 'latest') {
        "https://github.com/$Repository/releases/latest/download"
    } else {
        "https://github.com/$Repository/releases/download/$Version"
    }
    $versionLabel = if ($Version -eq 'latest') { 'latest stable release' } else { $Version }

    $temporary = Join-Path ([IO.Path]::GetTempPath()) ("collo-install-" + [guid]::NewGuid())
    $binaryPath = Join-Path $temporary $asset
    $checksumPath = Join-Path $temporary 'checksums.txt'
    $destination = Join-Path $InstallDir 'collo.exe'
    $destinationTemporary = Join-Path $InstallDir ('.collo.install.' + [guid]::NewGuid() + '.exe')
    $backup = Join-Path $InstallDir ('.collo.backup.' + [guid]::NewGuid() + '.exe')
    $oldProtocol = [Net.ServicePointManager]::SecurityProtocol
    $hadExisting = $false

    Write-Host "==> Installing Collomia $versionLabel ($asset)"
    New-Item -ItemType Directory -Force -Path $temporary | Out-Null
    try {
        [Net.ServicePointManager]::SecurityProtocol = $oldProtocol -bor [Net.SecurityProtocolType]::Tls12

        Write-Host "==> Downloading $asset"
        Invoke-WebRequest -UseBasicParsing -Uri "$base/$asset" -OutFile $binaryPath
        Write-Host '==> Downloading checksums.txt'
        Invoke-WebRequest -UseBasicParsing -Uri "$base/checksums.txt" -OutFile $checksumPath

        $expected = Get-CollomiaExpectedChecksum -ManifestPath $checksumPath -Asset $asset
        $actual = (Get-FileHash -LiteralPath $binaryPath -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($actual -ne $expected) {
            throw "Checksum verification failed for $asset (expected $expected, got $actual)"
        }
        Write-Host '==> Checksum verified'

        New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
        Copy-Item -LiteralPath $binaryPath -Destination $destinationTemporary
        $identity = & $destinationTemporary --version
        if ($LASTEXITCODE -ne 0 -or -not $identity) {
            throw 'The downloaded binary did not pass its version check'
        }
        if ($Version -ne 'latest' -and -not $identity.StartsWith("collo $Version (", [StringComparison]::Ordinal)) {
            throw "Downloaded binary reports an unexpected version: $identity"
        }

        $hadExisting = Test-Path -LiteralPath $destination
        if ($hadExisting) {
            Copy-Item -LiteralPath $destination -Destination $backup
        }
        try {
            Move-Item -LiteralPath $destinationTemporary -Destination $destination -Force
        } catch {
            if ($hadExisting -and -not (Test-Path -LiteralPath $destination) -and (Test-Path -LiteralPath $backup)) {
                try {
                    Move-Item -LiteralPath $backup -Destination $destination -Force
                } catch {
                    throw "Installation failed and the previous binary could not be restored; its backup remains at $backup"
                }
            }
            throw
        }
        Remove-Item -LiteralPath $backup -Force -ErrorAction SilentlyContinue

        if ($AddToPath) {
            Add-CollomiaUserPath -Directory $InstallDir
            Write-Host "==> Added $InstallDir to the user PATH"
        }

        Write-Host "==> $identity"
        Write-Host "==> Installed at $destination"
        if (-not $AddToPath) {
            Write-Warning "$InstallDir was not added to PATH; use -AddToPath or run $destination directly"
        }
        Write-Host ''
        Write-Host 'Next steps:'
        Write-Host "  & '$destination' init --global --with-reference"
        Write-Host "  & '$destination' doctor"
    } finally {
        [Net.ServicePointManager]::SecurityProtocol = $oldProtocol
        Remove-Item -LiteralPath $destinationTemporary -Force -ErrorAction SilentlyContinue
        if (Test-Path -LiteralPath $destination) {
            Remove-Item -LiteralPath $backup -Force -ErrorAction SilentlyContinue
        }
        Remove-Item -LiteralPath $temporary -Recurse -Force -ErrorAction SilentlyContinue
    }
}

if ($MyInvocation.InvocationName -ne '.') {
    Invoke-CollomiaInstall -Version $Version -InstallDir $InstallDir -Repository $Repository -AddToPath:$AddToPath
}
