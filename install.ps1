#Requires -Version 5.1

<#
.SYNOPSIS
    Installs the Collomia `collo` command on native Windows.

.DESCRIPTION
    Downloads the release binary and checksums.txt for this machine's CPU
    architecture, verifies the SHA-256 digest against the manifest, runs the
    downloaded executable's own version check, and only then publishes it as
    collo.exe. The user PATH is updated unless -NoPathUpdate is supplied.

    Running the script through Invoke-Expression never touches disk as a
    script file, so it is unaffected by the PowerShell execution policy.

.EXAMPLE
    irm https://raw.githubusercontent.com/robert-mcdermott/collomia/main/install.ps1 | iex

.EXAMPLE
    & ([scriptblock]::Create((irm https://raw.githubusercontent.com/robert-mcdermott/collomia/main/install.ps1))) -Version v0.1.8

.EXAMPLE
    powershell -ExecutionPolicy Bypass -File .\install.ps1 -InstallDir "$HOME\bin"
#>

[CmdletBinding()]
param(
    [string]$Version = $env:COLLO_VERSION,
    [string]$InstallDir = $env:COLLO_INSTALL_DIR,
    [string]$Repository = $env:COLLO_REPOSITORY,
    [string]$Architecture = $env:COLLO_ARCH,
    [switch]$NoPathUpdate,
    # Accepted for compatibility with earlier documentation. The user PATH is
    # now updated by default, so this switch is a no-op; use -NoPathUpdate to
    # opt out.
    [switch]$AddToPath
)

function Get-CollomiaArchitecture {
    <#
        Reports the native Windows CPU architecture as 'amd64' or 'arm64'.

        The machine-scoped PROCESSOR_ARCHITECTURE value is read first because
        it comes from the registry and therefore describes the real hardware
        even when PowerShell itself is emulated: Windows PowerShell 5.1 has no
        native ARM64 build, so on Windows 11 ARM64 it runs as emulated x64.
        Every probe is optional; a host that lacks one must not fail the
        install. In particular [Runtime.InteropServices.RuntimeInformation]
        is absent or incomplete on some .NET Framework hosts, where reading
        OSArchitecture under Set-StrictMode raises PropertyNotFoundStrict.
    #>
    [CmdletBinding()]
    param()

    $probes = @(
        { [Environment]::GetEnvironmentVariable('PROCESSOR_ARCHITECTURE', 'Machine') },
        { $env:PROCESSOR_ARCHITEW6432 },
        { [Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString() },
        { $env:PROCESSOR_ARCHITECTURE }
    )

    $seen = @()
    foreach ($probe in $probes) {
        $value = $null
        try { $value = & $probe } catch { $value = $null }
        if (-not $value) { continue }

        $value = ([string]$value).Trim()
        if ($seen -notcontains $value) { $seen += $value }

        switch -Regex ($value) {
            '^(arm64|aarch64)$'            { return 'arm64' }
            '^(amd64|x64|x86_64|em64t)$'   { return 'amd64' }
        }
    }

    $observed = if ($seen) { $seen -join ', ' } else { 'nothing' }
    throw "Could not determine the Windows CPU architecture (probes reported $observed). Re-run with -Architecture amd64 or -Architecture arm64, or set COLLO_ARCH."
}

function Get-CollomiaAsset {
    param([string]$Architecture)

    if (-not $Architecture) { $Architecture = Get-CollomiaArchitecture }

    switch -Regex ($Architecture.Trim()) {
        '^(arm64|aarch64)$'          { return 'collo-windows-arm64.exe' }
        '^(amd64|x64|x86_64|em64t)$' { return 'collo-windows-amd64.exe' }
        default { throw "Unsupported Windows architecture: $Architecture (expected amd64 or arm64)" }
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

function Publish-CollomiaEnvironmentChange {
    <#
        Tells Explorer that the environment changed, so that terminals started
        from the Start menu see the new PATH without a sign-out. Best effort:
        Add-Type is unavailable in constrained language mode.
    #>
    try {
        if (-not ('Collomia.NativeMethods' -as [type])) {
            Add-Type -Namespace 'Collomia' -Name 'NativeMethods' -MemberDefinition @'
[System.Runtime.InteropServices.DllImport("user32.dll", SetLastError = true, CharSet = System.Runtime.InteropServices.CharSet.Auto)]
public static extern System.IntPtr SendMessageTimeout(System.IntPtr hWnd, uint Msg, System.IntPtr wParam, string lParam, uint fuFlags, uint uTimeout, out System.UIntPtr lpdwResult);
'@
        }
        $result = [UIntPtr]::Zero
        # HWND_BROADCAST, WM_SETTINGCHANGE, SMTO_ABORTIFHUNG, 5s timeout.
        [void][Collomia.NativeMethods]::SendMessageTimeout([IntPtr]0xffff, 0x1A, [IntPtr]::Zero, 'Environment', 2, 5000, [ref]$result)
    } catch {
        Write-Verbose "Could not broadcast the environment change: $($_.Exception.Message)"
    }
}

function Add-CollomiaUserPath {
    <#
        Appends a directory to the current user's PATH and reports whether the
        stored value changed.

        The registry is written directly rather than through
        [Environment]::SetEnvironmentVariable, which always stores REG_SZ. A
        user PATH is normally REG_EXPAND_SZ, and rewriting it as REG_SZ
        permanently breaks entries such as %USERPROFILE%\bin. Reading with
        DoNotExpandEnvironmentNames keeps those entries unexpanded.
    #>
    param([Parameter(Mandatory = $true)][string]$Directory)

    $normalized = $Directory.TrimEnd('\')
    $changed = $false

    $key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment', $true)
    if (-not $key) { throw 'Could not open HKEY_CURRENT_USER\Environment for writing' }
    try {
        $current = [string]$key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
        $kind = if ($key.GetValueNames() -contains 'Path') {
            $key.GetValueKind('Path')
        } else {
            [Microsoft.Win32.RegistryValueKind]::ExpandString
        }

        $entries = @($current -split ';' | Where-Object { $_ })
        $present = $false
        foreach ($entry in $entries) {
            if ($entry.TrimEnd('\').Equals($normalized, [StringComparison]::OrdinalIgnoreCase)) {
                $present = $true
                break
            }
        }
        if (-not $present) {
            $key.SetValue('Path', ((@($entries) + $Directory) -join ';'), $kind)
            $changed = $true
        }
    } finally {
        $key.Close()
    }

    if ($changed) { Publish-CollomiaEnvironmentChange }

    if (-not (($env:Path -split ';') | Where-Object { $_.TrimEnd('\').Equals($normalized, [StringComparison]::OrdinalIgnoreCase) })) {
        $env:Path = "$env:Path;$Directory"
    }
    return $changed
}

function Invoke-CollomiaInstall {
    [CmdletBinding()]
    param(
        [string]$Version = 'latest',
        [string]$InstallDir,
        [string]$Repository = 'robert-mcdermott/collomia',
        [string]$Architecture,
        [switch]$NoPathUpdate
    )

    # Scoped to this function so that piping the script into Invoke-Expression
    # does not leave the caller's session in strict mode.
    $ErrorActionPreference = 'Stop'
    Set-StrictMode -Version 2.0
    # Windows PowerShell renders a progress bar for every Invoke-WebRequest
    # buffer, which makes a 25 MB download take minutes instead of seconds.
    $ProgressPreference = 'SilentlyContinue'

    if (-not $env:OS -or $env:OS -ne 'Windows_NT') {
        throw 'install.ps1 supports native Windows only'
    }
    if (-not $InstallDir) {
        if (-not $env:LOCALAPPDATA) {
            throw 'LOCALAPPDATA is not set; pass -InstallDir explicitly'
        }
        $InstallDir = Join-Path $env:LOCALAPPDATA 'Programs\Collomia'
    }
    if ($Repository -notmatch '^[0-9A-Za-z_.-]+/[0-9A-Za-z_.-]+$') {
        throw "Repository must use owner/repository syntax: $Repository"
    }
    if (-not [IO.Path]::IsPathRooted($InstallDir)) {
        throw "Installation directory must be an absolute path: $InstallDir"
    }

    $updatePath = -not $NoPathUpdate
    if ($updatePath -and $env:COLLO_NO_PATH_UPDATE -and $env:COLLO_NO_PATH_UPDATE -notmatch '^(0|false|no)$') {
        $updatePath = $false
    }

    $Version = Resolve-CollomiaVersion $Version
    $asset = Get-CollomiaAsset -Architecture $Architecture
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

        $pathChanged = $false
        $pathUpdated = $updatePath
        if ($updatePath) {
            # The binary is already installed at this point, so a PATH failure
            # must not fail the installation.
            try {
                $pathChanged = Add-CollomiaUserPath -Directory $InstallDir
            } catch {
                $pathUpdated = $false
                Write-Warning "Could not update the user PATH: $($_.Exception.Message)"
            }
        }

        Write-Host "==> $identity"
        Write-Host "==> Installed at $destination"
        if ($pathUpdated) {
            if ($pathChanged) {
                Write-Host "==> Added $InstallDir to the user PATH"
                Write-Host '    Already-open terminals need to be restarted to see it.'
            }
        } else {
            Write-Warning "$InstallDir is not on PATH; run $destination directly"
        }
        Write-Host ''
        Write-Host 'Next steps:'
        if ($pathUpdated) {
            Write-Host '  collo init --global --with-reference'
            Write-Host '  collo doctor'
        } else {
            Write-Host "  & '$destination' init --global --with-reference"
            Write-Host "  & '$destination' doctor"
        }
        Write-Host ''
        Write-Host "Documentation: https://github.com/$Repository/blob/main/docs/USER_GUIDE.md"
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
    $arguments = @{}
    if ($Version) { $arguments['Version'] = $Version }
    if ($InstallDir) { $arguments['InstallDir'] = $InstallDir }
    if ($Repository) { $arguments['Repository'] = $Repository }
    if ($Architecture) { $arguments['Architecture'] = $Architecture }
    if ($NoPathUpdate) { $arguments['NoPathUpdate'] = $true }
    Invoke-CollomiaInstall @arguments
}
