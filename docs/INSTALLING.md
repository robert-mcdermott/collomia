# Installing Collomia

Collomia is distributed as a self-contained `collo` executable. Release
binaries do not require Go, Node.js, npm, or Python. The embedded browser
terminal assets are already included in the same file.

## Supported release assets

| Operating system | Architecture | Release asset |
| --- | --- | --- |
| macOS | Intel/AMD64 | `collo-darwin-amd64` |
| macOS | Apple silicon/ARM64 | `collo-darwin-arm64` |
| Linux | AMD64/x86-64 | `collo-linux-amd64` |
| Linux | ARM64 | `collo-linux-arm64` |
| Windows 11 | AMD64/x86-64 | `collo-windows-amd64.exe` |
| Windows 11 | ARM64 | `collo-windows-arm64.exe` |

Each release also contains `checksums.txt` and `collomia.cdx.json`, a
CycloneDX software bill of materials (SBOM). GitHub-hosted provenance and SBOM
attestations provide stronger verification than the checksum manifest alone.

## macOS and Linux

Install the latest stable, non-prerelease version into
`$HOME/.local/bin/collo`:

```sh
curl --proto '=https' --tlsv1.2 -fsSL \
  https://raw.githubusercontent.com/robert-mcdermott/collomia/main/install.sh |
  sh
```

The installer detects the operating system and CPU, downloads the raw binary
and `checksums.txt` from the same GitHub Release, requires exactly one matching
SHA-256 entry, runs the downloaded binary's `--version`, and atomically
publishes it as `collo`. It does not use `sudo`, modify `PATH`, create
`~/.collomia`, or start Collomia. If `$HOME/.local/bin` is not on `PATH`, the
installer prints the exact path to run.

To inspect the installer first:

```sh
curl --proto '=https' --tlsv1.2 -fsSLo install-collo.sh \
  https://raw.githubusercontent.com/robert-mcdermott/collomia/main/install.sh
less install-collo.sh
sh install-collo.sh
```

Pin a stable or prerelease version:

```sh
curl --proto '=https' --tlsv1.2 -fsSL \
  https://raw.githubusercontent.com/robert-mcdermott/collomia/main/install.sh |
  COLLO_VERSION=v0.1.6 sh
```

Choose a different user-writable installation directory:

```sh
curl --proto '=https' --tlsv1.2 -fsSL \
  https://raw.githubusercontent.com/robert-mcdermott/collomia/main/install.sh |
  COLLO_INSTALL_DIR="$HOME/bin" sh
```

A downloaded installer accepts the equivalent options:

```sh
sh install-collo.sh --version v0.1.6 --install-dir "$HOME/bin"
```

`COLLO_REPOSITORY=owner/repository` selects a fork. For a system-wide install,
download and inspect the script before deliberately running it with an
administrator-owned destination. Never pipe a network response directly into
`sudo sh`.

## Native Windows with PowerShell

Download and inspect the repository-owned installer, then run it. `-AddToPath`
is explicit: omit it if you do not want the script to modify your user PATH.

```powershell
$Installer = Join-Path $env:TEMP 'install-collo.ps1'
[Net.ServicePointManager]::SecurityProtocol = `
  [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
Invoke-WebRequest -UseBasicParsing `
  'https://raw.githubusercontent.com/robert-mcdermott/collomia/main/install.ps1' `
  -OutFile $Installer

Get-Content $Installer
Unblock-File $Installer
& $Installer -AddToPath
```

The default executable location is
`$env:LOCALAPPDATA\Programs\Collomia\collo.exe`. The installer detects AMD64
or ARM64, downloads the binary and checksum manifest, requires exactly one
matching SHA-256 entry, tests the downloaded executable, and replaces the old
installation only after every check succeeds. It does not create application
configuration or start Collomia. Windows may refuse to replace an executable
that is currently running, so close active Collomia processes before an
upgrade.

Install a particular version or choose another directory:

```powershell
& $Installer -Version v0.1.6 -InstallDir "$HOME\bin" -AddToPath
```

The environment variables `COLLO_VERSION`, `COLLO_INSTALL_DIR`, and
`COLLO_REPOSITORY` provide the same defaults. A command-line parameter takes
precedence.

Organizations that prohibit downloaded PowerShell scripts can perform the
same verification directly. This AMD64 example downloads into the current
directory; use the ARM64 asset on an ARM64 machine:

```powershell
$Asset = 'collo-windows-amd64.exe'
$Base = 'https://github.com/robert-mcdermott/collomia/releases/latest/download'
[Net.ServicePointManager]::SecurityProtocol = `
  [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
Invoke-WebRequest -UseBasicParsing "$Base/$Asset" -OutFile $Asset
Invoke-WebRequest -UseBasicParsing "$Base/checksums.txt" -OutFile checksums.txt

$Pattern = '^(?<hash>[0-9A-Fa-f]{64})\s+[*]?' + [regex]::Escape($Asset) + '$'
$Entries = @(Get-Content checksums.txt | Where-Object { $_ -match $Pattern })
if ($Entries.Count -ne 1) { throw "Expected exactly one checksum for $Asset" }
$Entries[0] -match $Pattern | Out-Null
$Expected = $Matches.hash.ToLowerInvariant()
$Actual = (Get-FileHash -Algorithm SHA256 $Asset).Hash.ToLowerInvariant()
if ($Actual -ne $Expected) { throw 'Checksum verification failed' }
& ".\$Asset" --version
```

## Manual installation and verification

Download the platform binary, `checksums.txt`, and optionally
`collomia.cdx.json` from the same release. On macOS or Linux:

```sh
ASSET=collo-linux-amd64 # change for your platform
curl -fLO "https://github.com/robert-mcdermott/collomia/releases/latest/download/$ASSET"
curl -fLO 'https://github.com/robert-mcdermott/collomia/releases/latest/download/checksums.txt'
grep "  $ASSET$" checksums.txt | sha256sum --check -
chmod 0755 "$ASSET"
./"$ASSET" --version
```

Use `shasum -a 256 -c -` instead of `sha256sum --check -` on macOS.

The checksum detects corruption and a binary that differs from the published
manifest. Because both files come from the same release, it cannot by itself
protect against an attacker able to replace both. With GitHub CLI installed,
verify the release workflow's Sigstore-backed provenance as well:

```sh
gh attestation verify "$ASSET" \
  --repo robert-mcdermott/collomia \
  --signer-workflow robert-mcdermott/collomia/.github/workflows/release.yml
```

The attestation proves which repository and workflow produced the exact file.
It does not mean the source is vulnerability-free. Review `collomia.cdx.json`
for the release dependency inventory.

Collomia's raw macOS and Windows beta binaries are not yet Apple-notarized or
platform code-signed. Gatekeeper, SmartScreen, or organizational policy may
therefore require confirmation. Build from source when unsigned executables
are prohibited.

## Upgrading and rollback

Running the appropriate installer again performs an upgrade. Configuration,
sessions, logs, skills, and other state under `~/.collomia` or
`%USERPROFILE%\.collomia` are not modified. A failed download, checksum,
version check, or final replacement leaves the existing binary in place.

For a deliberate rollback, install a known compatible earlier tag:

```sh
COLLO_VERSION=v0.1.4 sh install-collo.sh
```

```powershell
& $Installer -Version v0.1.4 -AddToPath
```

Read the intervening release notes first. A binary downgrade never rewinds
session/configuration schemas or workspace changes.

## First run and uninstall

After installation:

```sh
collo --version
collo init --global --with-reference
collo doctor
```

Remove only the executable to uninstall while retaining user state. Delete
`~/.collomia` (macOS/Linux) or `%USERPROFILE%\.collomia` (Windows) separately
only when you intentionally want to erase configuration, sessions, logs,
skills, trust decisions, and audit data.

## Building from source

Building requires the Go version declared by `go.mod`:

```sh
git clone https://github.com/robert-mcdermott/collomia.git
cd collomia
go test ./...
go build -o collo ./cmd/collo
./collo --version
```

Maintainers producing release artifacts should follow
[Releasing Collomia](RELEASING.md), not upload an arbitrary local build.
