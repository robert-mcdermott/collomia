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

Pin a stable or prerelease version by replacing `vX.Y.Z` with the release tag
you want — the [releases page](https://github.com/robert-mcdermott/collomia/releases)
lists them:

```sh
COLLO_PIN=vX.Y.Z
curl --proto '=https' --tlsv1.2 -fsSL \
  https://raw.githubusercontent.com/robert-mcdermott/collomia/main/install.sh |
  COLLO_VERSION="$COLLO_PIN" sh
```

Choose a different user-writable installation directory:

```sh
curl --proto '=https' --tlsv1.2 -fsSL \
  https://raw.githubusercontent.com/robert-mcdermott/collomia/main/install.sh |
  COLLO_INSTALL_DIR="$HOME/bin" sh
```

A downloaded installer accepts the equivalent options:

```sh
sh install-collo.sh --version vX.Y.Z --install-dir "$HOME/bin"
```

`COLLO_REPOSITORY=owner/repository` selects a fork. For a system-wide install,
download and inspect the script before deliberately running it with an
administrator-owned destination. Never pipe a network response directly into
`sudo sh`.

## Native Windows with PowerShell

```powershell
irm https://raw.githubusercontent.com/robert-mcdermott/collomia/main/install.ps1 | iex
```

The default executable location is
`$env:LOCALAPPDATA\Programs\Collomia\collo.exe`, and that directory is added to
the current user's PATH. Already-open terminals keep their old PATH, so open a
new one before running `collo`. The installer detects AMD64 or ARM64,
downloads the binary and checksum manifest, requires exactly one matching
SHA-256 entry, tests the downloaded executable, and replaces the old
installation only after every check succeeds. It does not require elevation,
create application configuration, or start Collomia. Windows may refuse to
replace an executable that is currently running, so close active Collomia
processes before an upgrade.

### Execution policy

`irm ... | iex` is not affected by the PowerShell execution policy. The
execution policy governs *script files*; this form downloads the script into
memory and evaluates it, so it works unchanged under `Restricted`, `AllSigned`,
and `RemoteSigned`. There is no need to run `Set-ExecutionPolicy`, and no need
for `Unblock-File`.

The policy does apply if you save the installer and run it as a file. In that
case pass the bypass to a single invocation rather than changing the
machine-wide setting:

```powershell
powershell -ExecutionPolicy Bypass -File .\install-collo.ps1
```

### Options

Piping into `iex` cannot pass parameters. Either use the environment variables,
which the piped form reads:

```powershell
$env:COLLO_VERSION = 'vX.Y.Z'
$env:COLLO_INSTALL_DIR = "$HOME\bin"
irm https://raw.githubusercontent.com/robert-mcdermott/collomia/main/install.ps1 | iex
```

or build a script block, which does accept parameters:

```powershell
& ([scriptblock]::Create((irm https://raw.githubusercontent.com/robert-mcdermott/collomia/main/install.ps1))) `
  -Version vX.Y.Z -InstallDir "$HOME\bin"
```

| Parameter | Environment variable | Purpose |
| --- | --- | --- |
| `-Version` | `COLLO_VERSION` | Release tag to install. Defaults to `latest`. |
| `-InstallDir` | `COLLO_INSTALL_DIR` | Absolute installation directory. |
| `-Repository` | `COLLO_REPOSITORY` | GitHub `owner/repository`, for forks. |
| `-Architecture` | `COLLO_ARCH` | Force `amd64` or `arm64` instead of detecting. |
| `-NoPathUpdate` | `COLLO_NO_PATH_UPDATE` | Leave the user PATH unchanged. |

A command-line parameter takes precedence over the environment variable.
`-Architecture` is an escape hatch for locked-down hosts where the installer
cannot read the machine's CPU architecture; it is not normally needed.

### Reviewing the installer first

```powershell
$Installer = Join-Path $env:TEMP 'install-collo.ps1'
Invoke-WebRequest -UseBasicParsing `
  'https://raw.githubusercontent.com/robert-mcdermott/collomia/main/install.ps1' `
  -OutFile $Installer

Get-Content $Installer
powershell -ExecutionPolicy Bypass -File $Installer
```

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

For a deliberate rollback, replace `vX.Y.Z` with a known compatible earlier
release tag:

```sh
ROLLBACK_VERSION=vX.Y.Z
COLLO_VERSION="$ROLLBACK_VERSION" sh install-collo.sh
```

```powershell
$env:COLLO_VERSION = 'vX.Y.Z'
irm https://raw.githubusercontent.com/robert-mcdermott/collomia/main/install.ps1 | iex
```

Read the intervening release notes first. A binary downgrade never rewinds
session/configuration schemas or workspace changes.

## First run and uninstall

After installation:

```sh
collo --version
collo
```

When no provider is configured, interactive startup opens provider setup. It
finds the local model runtimes that are actually running, notices provider API
keys the environment already exports, lets you choose a model from the
endpoint's own catalog, and proves the choice with two real requests before
writing anything. If it cannot verify the configuration it writes nothing and
says which of the endpoint, credential, or model is at fault. After a successful
write, pressing Enter continues directly into the session.

Run `collo setup` at any later time to add a provider or reconfigure one you
already have. The setup screen lists configured providers as actions; the
`--provider <name>` option remains a shortcut for jumping directly to one.

To write a configuration file by hand instead — or to get the annotated
reference alongside it — use `collo init` and check the result:

```sh
collo init --global --with-reference
collo doctor
```

`collo init` writes a *static*, provider-free settings file and an editor schema;
it does not detect or assume an endpoint or model. For unattended use, add an
explicit provider from the generated reference, provide its credential through
the environment, and run `collo config validate --strict` before starting.

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
