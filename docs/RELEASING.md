# Releasing Collomia

This guide defines the beta release process, artifact contract, verification
gates, and rollback procedure. The repository-root `VERSION` file is the
single source of truth. A release tag must match it exactly, including the
leading `v` and any prerelease suffix.

## Release contract

Every release draft contains:

- `collo-darwin-amd64`
- `collo-darwin-arm64`
- `collo-linux-amd64`
- `collo-linux-arm64`
- `collo-windows-amd64.exe`
- `collo-windows-arm64.exe`
- `checksums.txt`, covering all binaries and the SBOM
- `collomia.cdx.json`, the deterministic CycloneDX module SBOM

These names are an installer compatibility contract. Update `install.sh`,
`install.ps1`, installer tests, and user documentation together before changing
one.

GitHub Actions creates SLSA build-provenance attestations for every manifest
subject and a CycloneDX SBOM attestation for every binary. The signatures use a
short-lived Sigstore certificate issued from GitHub's workflow identity; no
long-lived signing secret is stored in the repository.

Apple notarization, Apple code signing, and Windows Authenticode signing are
not part of the beta workflow because they require separately managed platform
accounts and certificates. Do not describe the current raw binaries as
platform-signed.

## What the tag workflow enforces

`.github/workflows/release.yml` does not immediately publish a release. It:

1. Checks out the exact tag on Linux, macOS, and Windows.
2. Builds, tests without cached results, runs the race detector, runs `go vet`,
   and tests the native installer for each platform.
3. Verifies modules, runs `govulncheck`, the deterministic evaluation suite,
   and bounded parser fuzz campaigns.
4. Requires a semantic tag equal to `VERSION` and requires the tagged commit
   to be contained in `origin/main`.
5. Cross-builds all six `CGO_ENABLED=0` artifacts with a commit-derived build
   timestamp, generates the SBOM, and verifies the complete checksum manifest.
6. Downloads and executes the produced artifact on Linux, macOS, and Windows,
   checking its embedded version and full commit identity.
7. Creates provenance/SBOM attestations and a **draft** GitHub Release.

A tag containing a prerelease suffix, such as `vX.Y.Z-beta.1`, produces a draft
marked as a prerelease. GitHub's `/releases/latest/download` URL continues to
refer to the latest stable, non-prerelease release. Beta users must pin the beta
tag explicitly.

## Prepare a release

1. On the release branch, update `VERSION` to the intended semantic version,
   for example `vX.Y.Z-beta.1`, and update release notes plus [beta
   limitations](BETA.md) if behavior changed.
1. Re-review [the feature and security summary](FEATURES.md) against the
   release and update its version and commit header. It is a hand-written
   prose summary rather than a generated artifact — unlike
   [the capability matrix](CAPABILITIES.md), which `collo capabilities
   --markdown` regenerates and a test keeps in step — so nothing else catches
   it going stale. It once claimed a policy setting that could make audit
   writes mandatory, which has never existed.
1. Refresh `UserAgent` in `internal/web/client.go` to the current desktop
   Chrome release if it has fallen more than a few major versions behind.
   Presenting a browser is what keeps `web_fetch` working against CDN rules
   that refuse non-browser clients, and a version old enough to look
   implausible starts attracting the same rules it exists to satisfy.
2. Run the local preflight below, then merge that reviewed release commit into
   `main` and wait for required CI checks on the exact commit.
3. Use a clean checkout of that `main` commit for the tag. Do not tag or build
   a release from a normal working tree containing unrelated or untracked
   files.

Local preflight:

```sh
git status --porcelain
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
scripts/test-install.sh
scripts/build-release.sh --clean
```

On Windows, also run:

```powershell
./scripts/test-install.ps1
```

The build script validates `VERSION`, runs tests unless `--skip-tests` is
explicitly supplied, cross-compiles into a private staging directory, and only
replaces `dist/` artifacts after every build succeeds. It embeds the current
commit date by default, making repeated clean builds from the same commit
deterministic. A tracked dirty worktree adds `-dirty` to the embedded commit.

Verify the local checksums and native binary:

```sh
cd dist
shasum -a 256 -c checksums.txt # use sha256sum --check on Linux
cd ..
./dist/collo-darwin-arm64 --version # select the native artifact
```

Local `build-release.sh` output intentionally contains binaries and checksums;
the tag workflow adds the standardized SBOM and attestations.

## Tag and create the draft

Read the version directly from the reviewed file:

```sh
version="$(tr -d '[:space:]' < VERSION)"
git tag -s "$version" -m "Collomia $version" # use -a if tag signing is unavailable
git push origin "$version"
```

Do not move or recreate a tag after it has been pushed. Watch the Release
workflow. If qualification succeeds, open the generated draft and review:

- the tag, commit, title, and generated notes;
- all eight expected assets;
- the three native artifact smoke-test results;
- checksum and attestation steps;
- supported platforms, installation instructions, breaking changes, security
  fixes, known limitations, and unsigned/notarization status.

Download the draft assets into a new directory and verify them instead of
relying on the original workflow workspace:

```sh
check_dir="$(mktemp -d)"
gh release download "$version" --dir "$check_dir"
(cd "$check_dir" && shasum -a 256 -c checksums.txt)
gh attestation verify "$check_dir/collo-darwin-arm64" \
  --repo robert-mcdermott/collomia \
  --signer-workflow robert-mcdermott/collomia/.github/workflows/release.yml
```

Run the downloaded native binary. Draft assets are not available through the
public `/releases/download/` URL used by installers, so publish only after the
draft and release notes are reviewed:

```sh
gh release edit "$version" --draft=false
```

For a stable release, confirm GitHub marks it Latest. A prerelease must remain
a prerelease and must not replace the stable `latest` installer target.

## Manual emergency publication

Prefer the tag workflow. If GitHub attestations are unavailable, do not claim
that manually uploaded files are attested. Build from a clean tagged checkout,
run the complete preflight on all available platforms, upload the exact
`dist/` artifacts as a draft, download them again, and verify them before
publication. Record the missing provenance in the release notes.

## Failed and withdrawn releases

- Before publication, leave a failed release as a draft while investigating or
  delete only the draft. Never reuse its pushed version tag for different bits.
- After publication, prefer a new patch or prerelease version containing the
  correction. Mark the affected release notes clearly rather than silently
  replacing assets.
- For a serious security or data-loss defect, mark the release unavailable,
  publish an advisory, and provide a fixed version. The checksum and
  attestation identify bytes; they do not make an unsafe version safe.
- Test rollback by pinning the prior version. Never imply that replacing the
  executable reverses configuration, sessions, workspace mutations, or
  external side effects.

## Post-release verification

After publication:

1. Confirm every public asset downloads without authentication.
2. Verify every entry in `checksums.txt`.
3. Verify provenance for at least one artifact from each operating system.
4. Run the macOS/Linux installer with the exact tag and test the PowerShell
   installer on Windows.
5. Run `collo --version`, `collo doctor`, and a credential-free local smoke
   session.
6. Confirm README and documentation links use the published asset names.
7. Record any exception or manual step in the release notes.
