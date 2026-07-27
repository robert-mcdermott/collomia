#Requires -Version 5.1

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version 2.0

. (Join-Path $PSScriptRoot '..\install.ps1')

function Assert-Equal {
    param($Expected, $Actual, [string]$Message)
    if ($Expected -ne $Actual) {
        throw "$Message (expected '$Expected', got '$Actual')"
    }
}

Assert-Equal 'collo-windows-amd64.exe' (Get-CollomiaAsset -Architecture X64) 'x64 asset'
Assert-Equal 'collo-windows-arm64.exe' (Get-CollomiaAsset -Architecture Arm64) 'arm64 asset'
foreach ($alias in @('AMD64', 'amd64', 'x64', 'x86_64', 'EM64T', ' AMD64 ')) {
    Assert-Equal 'collo-windows-amd64.exe' (Get-CollomiaAsset -Architecture $alias) "amd64 alias '$alias'"
}
foreach ($alias in @('ARM64', 'Arm64', 'arm64', 'aarch64', ' ARM64 ')) {
    Assert-Equal 'collo-windows-arm64.exe' (Get-CollomiaAsset -Architecture $alias) "arm64 alias '$alias'"
}
try {
    Get-CollomiaAsset -Architecture 'x86' | Out-Null
    throw 'a 32-bit architecture was accepted'
} catch {
    if ($_.Exception.Message -eq 'a 32-bit architecture was accepted') { throw }
}

# Detection must succeed on this host without an explicit architecture, and it
# must never depend on RuntimeInformation, which is missing or incomplete on
# some .NET Framework hosts.
$detected = Get-CollomiaArchitecture
if ($detected -notin @('amd64', 'arm64')) { throw "unexpected detected architecture: $detected" }
Assert-Equal (Get-CollomiaAsset -Architecture $detected) (Get-CollomiaAsset) 'detected architecture matches default asset'

Assert-Equal 'v0.2.0-beta.1' (Resolve-CollomiaVersion '0.2.0-beta.1') 'version normalization'
Assert-Equal 'latest' (Resolve-CollomiaVersion 'latest') 'latest version'

# A staged file name containing an installer keyword makes Windows interpose a
# UAC consent dialog instead of running the binary, which breaks the version
# check for every standard user. Administrators, including CI runners, never see
# it, so this has to be asserted rather than observed.
foreach ($rejected in @(
    'C:\x\.collo.install.9d36bcad.exe',
    'C:\x\collo-setup.exe',
    'C:\x\collo.update.exe',
    'C:\x\collo-patch-1.exe'
)) {
    if (Test-CollomiaStagingName -Path $rejected) { throw "staging name should be rejected: $rejected" }
}
foreach ($accepted in @(
    'C:\x\collo.staged.9d36bcad-20a2-4af1.exe',
    'C:\x\collo.previous.9d36bcad-20a2-4af1.exe',
    'C:\x\collo.exe'
)) {
    if (-not (Test-CollomiaStagingName -Path $accepted)) { throw "staging name should be accepted: $accepted" }
}

# The version check must report what the binary actually did. Reading
# $LASTEXITCODE bare used to fail with VariableIsUndefined whenever the
# invocation did not set it, hiding the real problem behind a PowerShell error.
$identityRoot = Join-Path ([IO.Path]::GetTempPath()) ("collo-identity-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $identityRoot | Out-Null
try {
    function New-IdentityFixture {
        param([string]$Name, [string[]]$Body)
        $path = Join-Path $identityRoot "$Name.cmd"
        Set-Content -LiteralPath $path -Value (@('@echo off') + $Body)
        return $path
    }

    $good = New-IdentityFixture 'good' @('echo collo v0.1.8 (test)')
    $banner = New-IdentityFixture 'banner' @('echo loading', 'echo collo v0.1.8 (test)')
    $silent = New-IdentityFixture 'silent' @('exit /b 0')
    $junk = New-IdentityFixture 'junk' @('echo something else entirely')
    $failing = New-IdentityFixture 'failing' @('echo collo v0.1.8 (test)', 'exit /b 3')

    foreach ($case in @($good, $banner)) {
        # A fresh session has never set $LASTEXITCODE; the check must survive it.
        Remove-Variable -Name LASTEXITCODE -Scope Global -Force -ErrorAction SilentlyContinue
        Assert-Equal 'collo v0.1.8 (test)' (Get-CollomiaIdentity -Path $case) "identity from $case"
    }

    foreach ($case in @($silent, $junk, $failing, (Join-Path $identityRoot 'absent.cmd'))) {
        Remove-Variable -Name LASTEXITCODE -Scope Global -Force -ErrorAction SilentlyContinue
        $message = $null
        $errorId = $null
        try {
            Get-CollomiaIdentity -Path $case | Out-Null
        } catch {
            $message = $_.Exception.Message
            $errorId = $_.FullyQualifiedErrorId
        }
        if (-not $message) { throw "Get-CollomiaIdentity accepted $case" }
        if ($errorId -eq 'VariableIsUndefined') { throw "Get-CollomiaIdentity leaked a `$LASTEXITCODE error for $case" }
        if ($message -notmatch 'binary') { throw "unhelpful failure for ${case}: $message" }
    }
} finally {
    Remove-Item -LiteralPath $identityRoot -Recurse -Force -ErrorAction SilentlyContinue
}

# `irm ... | iex` runs the installer in the caller's own scope, so the script
# must not leave that session in strict mode or with a changed
# $ErrorActionPreference. COLLO_ARCH is invalid on purpose so the run fails
# inside Invoke-CollomiaInstall, after any leak would already have happened.
$probe = @'
$ErrorActionPreference = 'Continue'
try { Get-Content -Raw -LiteralPath $env:COLLO_TEST_INSTALLER | Invoke-Expression } catch { }
$empty = New-Object psobject
$leaked = $false
try { $empty.MissingProperty | Out-Null } catch { $leaked = $true }
if ($leaked) { throw 'install.ps1 leaked Set-StrictMode into the caller session' }
if ($ErrorActionPreference -ne 'Continue') { throw "install.ps1 leaked ErrorActionPreference=$ErrorActionPreference" }
'@

$probePath = Join-Path ([IO.Path]::GetTempPath()) ("collo-installer-scope-" + [guid]::NewGuid() + '.ps1')
Set-Content -LiteralPath $probePath -Value $probe
$previousArch = $env:COLLO_ARCH
try {
    $env:COLLO_TEST_INSTALLER = (Resolve-Path (Join-Path $PSScriptRoot '..\install.ps1')).Path
    $env:COLLO_ARCH = 'nonesuch'
    # Windows PowerShell 5.1 specifically: it is the host that exposed the
    # RuntimeInformation and strict-mode problems this guards against.
    $output = & powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -File $probePath 2>&1
    if ($LASTEXITCODE -ne 0) { throw "session hygiene probe failed: $output" }
} finally {
    $env:COLLO_ARCH = $previousArch
    $env:COLLO_TEST_INSTALLER = $null
    Remove-Item -LiteralPath $probePath -Force -ErrorAction SilentlyContinue
}

$temporary = Join-Path ([IO.Path]::GetTempPath()) ("collo-installer-test-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $temporary | Out-Null
try {
    $manifest = Join-Path $temporary 'checksums.txt'
    $hash = '0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef'
    Set-Content -LiteralPath $manifest -Value "$hash  collo-windows-amd64.exe"
    Assert-Equal $hash (Get-CollomiaExpectedChecksum -ManifestPath $manifest -Asset 'collo-windows-amd64.exe') 'checksum parsing'

    Set-Content -LiteralPath $manifest -Value @(
        "$hash  collo-windows-amd64.exe",
        "$hash *collo-windows-amd64.exe"
    )
    try {
        Get-CollomiaExpectedChecksum -ManifestPath $manifest -Asset 'collo-windows-amd64.exe' | Out-Null
        throw 'duplicate checksum was accepted'
    } catch {
        if ($_.Exception.Message -eq 'duplicate checksum was accepted') { throw }
    }

    try {
        Resolve-CollomiaVersion '../bad' | Out-Null
        throw 'unsafe version was accepted'
    } catch {
        if ($_.Exception.Message -eq 'unsafe version was accepted') { throw }
    }

    $asset = Get-CollomiaAsset
    $script:FixtureRoot = Join-Path $temporary 'releases'
    $installDir = Join-Path $temporary 'install'
    foreach ($release in @('latest', 'v0.1.3', 'v0.1.4')) {
        New-Item -ItemType Directory -Force -Path (Join-Path $script:FixtureRoot $release) | Out-Null
    }

    function New-FixtureBinary {
        param([string]$Release)
        $output = Join-Path (Join-Path $script:FixtureRoot $Release) $asset
        $ldflags = "-s -w -X github.com/robert-mcdermott/collomia/internal/version.Version=$Release -X github.com/robert-mcdermott/collomia/internal/version.Commit=fixture -X github.com/robert-mcdermott/collomia/internal/version.Date=2026-07-22T00:00:00Z"
        & go build -trimpath -ldflags $ldflags -o $output ./cmd/collo
        if ($LASTEXITCODE -ne 0) { throw "failed to build fixture $Release" }
        $digest = (Get-FileHash -LiteralPath $output -Algorithm SHA256).Hash.ToLowerInvariant()
        Set-Content -LiteralPath (Join-Path (Join-Path $script:FixtureRoot $Release) 'checksums.txt') -Value "$digest  $asset"
    }

    New-FixtureBinary 'v0.1.3'
    New-FixtureBinary 'v0.1.4'
    Copy-Item -LiteralPath (Join-Path (Join-Path $script:FixtureRoot 'v0.1.3') $asset) -Destination (Join-Path (Join-Path $script:FixtureRoot 'latest') $asset)
    Copy-Item -LiteralPath (Join-Path (Join-Path $script:FixtureRoot 'v0.1.3') 'checksums.txt') -Destination (Join-Path (Join-Path $script:FixtureRoot 'latest') 'checksums.txt')

    function Invoke-WebRequest {
        param([switch]$UseBasicParsing, [string]$Uri, [string]$OutFile)
        $release = 'latest'
        if ($Uri -match '/releases/download/(?<release>v[^/]+)/') {
            $release = $Matches['release']
        }
        $name = ([Uri]$Uri).Segments[-1]
        Copy-Item -LiteralPath (Join-Path (Join-Path $script:FixtureRoot $release) $name) -Destination $OutFile
    }

    # Reproduce a fresh interactive session, where no native command has run and
    # $LASTEXITCODE is therefore undefined. The fixture build above sets it, so
    # CI would otherwise never exercise the state a real user starts from.
    Remove-Variable -Name LASTEXITCODE -Scope Global -Force -ErrorAction SilentlyContinue
    Invoke-CollomiaInstall -InstallDir $installDir -Repository 'example/collomia' -NoPathUpdate
    $installed = Join-Path $installDir 'collo.exe'
    $identity = & $installed --version
    if (-not $identity.StartsWith('collo v0.1.3 (')) { throw "unexpected initial identity: $identity" }

    Invoke-CollomiaInstall -Version v0.1.4 -InstallDir $installDir -Repository 'example/collomia' -NoPathUpdate
    $identity = & $installed --version
    if (-not $identity.StartsWith('collo v0.1.4 (')) { throw "upgrade did not install v0.1.4: $identity" }

    Invoke-CollomiaInstall -Version v0.1.3 -InstallDir $installDir -Repository 'example/collomia' -NoPathUpdate
    $identity = & $installed --version
    if (-not $identity.StartsWith('collo v0.1.3 (')) { throw "rollback did not restore v0.1.3: $identity" }

    $before = (Get-FileHash -LiteralPath $installed -Algorithm SHA256).Hash
    Set-Content -LiteralPath (Join-Path (Join-Path $script:FixtureRoot 'v0.1.4') 'checksums.txt') -Value (('0' * 64) + "  $asset")
    $failed = $false
    try {
        Invoke-CollomiaInstall -Version v0.1.4 -InstallDir $installDir -Repository 'example/collomia' -NoPathUpdate
    } catch {
        $failed = $true
    }
    if (-not $failed) { throw 'installer accepted a corrupt upgrade' }
    $after = (Get-FileHash -LiteralPath $installed -Algorithm SHA256).Hash
    Assert-Equal $before $after 'failed upgrade preservation'
} finally {
    Remove-Item -LiteralPath $temporary -Recurse -Force -ErrorAction SilentlyContinue
}

Write-Host 'PowerShell installer tests passed'
