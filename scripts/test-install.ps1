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
Assert-Equal 'v0.2.0-beta.1' (Resolve-CollomiaVersion '0.2.0-beta.1') 'version normalization'
Assert-Equal 'latest' (Resolve-CollomiaVersion 'latest') 'latest version'

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

    Invoke-CollomiaInstall -InstallDir $installDir -Repository 'example/collomia'
    $installed = Join-Path $installDir 'collo.exe'
    $identity = & $installed --version
    if (-not $identity.StartsWith('collo v0.1.3 (')) { throw "unexpected initial identity: $identity" }

    Invoke-CollomiaInstall -Version v0.1.4 -InstallDir $installDir -Repository 'example/collomia'
    $identity = & $installed --version
    if (-not $identity.StartsWith('collo v0.1.4 (')) { throw "upgrade did not install v0.1.4: $identity" }

    Invoke-CollomiaInstall -Version v0.1.3 -InstallDir $installDir -Repository 'example/collomia'
    $identity = & $installed --version
    if (-not $identity.StartsWith('collo v0.1.3 (')) { throw "rollback did not restore v0.1.3: $identity" }

    $before = (Get-FileHash -LiteralPath $installed -Algorithm SHA256).Hash
    Set-Content -LiteralPath (Join-Path (Join-Path $script:FixtureRoot 'v0.1.4') 'checksums.txt') -Value (('0' * 64) + "  $asset")
    $failed = $false
    try {
        Invoke-CollomiaInstall -Version v0.1.4 -InstallDir $installDir -Repository 'example/collomia'
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
