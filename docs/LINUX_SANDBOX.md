# Linux sandbox setup and Landlock compatibility

Collomia uses the Linux kernel's Landlock security module to contain commands
started by the agent. Landlock is an in-kernel feature: a normal Ubuntu,
Debian, Fedora, or other distribution installation does not need a separate
sandbox package, privileged helper, daemon, container runtime, or root access.
The running kernel must provide and enable Landlock.

This guide explains the exact requirements, recommended configurations, and
failure modes. The broader cross-platform threat model is in
[SECURITY.md](SECURITY.md).

## What the Linux sandbox covers

When `permissions.sandbox` is `auto` or `require`, Collomia re-executes each
agent shell command through its internal `collo __landlock` launcher. The
launcher installs an irreversible Landlock ruleset and then executes the
requested program. Children inherit that ruleset.

The sandbox covers:

- foreground `run_command` calls;
- pseudo-terminal commands (`run_command` with `pty: true`); and
- background commands started by `start_process`.

It does not wrap Collomia's own provider requests, remote MCP connections,
configured hooks, or language servers. Disabling command networking therefore
does not disable model-provider or remote-MCP access.

Filesystem writes are restricted to the workspace, temporary helper paths,
and `sandbox_writable_roots`. Outside-workspace reads remain broad by default
for developer-tool compatibility. Set
`sandbox_allow_read_outside_workspace` to `false` to confine user-data reads
to the workspace and explicit grants while retaining the system files needed
to launch ordinary Linux tools.

## Kernel and Landlock ABI requirements

Collomia queries the running kernel's Landlock ABI at runtime. The ABI result
is authoritative; a distribution name or `uname` version alone is not,
because vendors may backport features and containers use the host kernel.

| Landlock support | Upstream kernel orientation | Collomia behavior |
| --- | --- | --- |
| Unavailable | Before Linux 5.13, disabled LSM, or blocked syscall | `auto` runs without an OS sandbox and emits a warning; `require` refuses the command. |
| ABI v1 | Linux 5.13 | Basic filesystem read/write rules. Cross-directory reparenting is always denied rather than configurable, and standalone file truncation cannot be denied. |
| ABI v2 | Linux 5.19 | Adds controlled file linking and renaming across directories. Standalone truncation remains outside Landlock. |
| ABI v3 | Linux 6.2 | Adds `truncate` protection. This is the recommended minimum for robust filesystem write confinement. |
| ABI v4–v9 | Linux 6.7 introduced ABI v4 | Adds TCP connect/bind control. UDP, including DNS datagrams, remains outside the network policy. |
| ABI v10+ | Query the running ABI; vendors may backport it | Adds UDP bind/connect/send control. Collomia can then satisfy complete TCP/UDP command-network denial. |

Landlock itself was introduced in Linux 5.13. The kernel must be built with
`CONFIG_SECURITY_LANDLOCK=y`, and `landlock` must be present in the active LSM
list. The upstream [Landlock userspace API documentation](https://docs.kernel.org/userspace-api/landlock.html)
describes the runtime ABI query, build/boot requirements, compatibility model,
and access-right limitations.

For Collomia, the practical recommendations are:

- Linux 5.13 / ABI v1 is the absolute minimum for any Landlock enforcement.
- Linux 6.2 / ABI v3 is the recommended minimum for filesystem confinement.
- Linux 6.7 / ABI v4 is the recommended minimum if TCP denial matters.
- ABI v10 is required if `sandbox_allow_network: false` must deny both TCP and
  UDP rather than only TCP.

### Ubuntu 26.04 LTS

Ubuntu 26.04 LTS ships the Linux 7.0 kernel. Its upstream Landlock interface is
ABI v8, so a normal Ubuntu 26.04 host provides:

- filesystem write confinement, including truncation;
- optional workspace-scoped user-data read confinement; and
- TCP connect/bind denial.

It does not provide Landlock's ABI v10 UDP controls. On Ubuntu 26.04,
`sandbox: "require"` therefore works when command networking is allowed, but
`require` combined with `sandbox_allow_network: false` refuses to run because
the requested UDP denial cannot be enforced. Use `auto` only if TCP-only
denial with a visible UDP warning is acceptable. If complete offline execution
is mandatory, use a host kernel exposing ABI v10 or an independently managed
network boundary.

Canonical's standard kernels have enabled Landlock by default since Ubuntu
22.04 ([Canonical kernel-team change](https://lists.ubuntu.com/archives/kernel-team/2021-December/126896.html)).
A custom kernel, nonstandard cloud image, container runtime, or WSL kernel may
differ, so verify the running environment rather than assuming.
Canonical documents Ubuntu 26.04's kernel choice in
[Ubuntu 26.04 LTS shipping with Linux 7.0](https://discourse.ubuntu.com/t/26-04-lts-resolute-raccoon-shipping-with-the-final-7-0-linux-kernel/80838),
and the [Linux 7.0 Landlock documentation](https://docs.kernel.org/7.0/userspace-api/landlock.html)
documents the ABI v8 interface.

## Verify Landlock before enabling `require`

Start with Collomia's runtime diagnostic:

```sh
collo doctor
```

On stock Ubuntu 26.04, the sandbox detail should identify Landlock ABI v8 and
filesystem plus TCP support. The diagnostic also reports the effective read
policy, whether command networking is allowed, and any protection requested by
the current configuration that the kernel cannot enforce.

Useful host-level checks are:

```sh
uname -r
grep '^CONFIG_SECURITY_LANDLOCK=' "/boot/config-$(uname -r)"
grep '^CONFIG_LSM=' "/boot/config-$(uname -r)"
cat /sys/kernel/security/lsm
sudo journalctl -kb -g 'landlock|LSM:'
```

Expected indicators include:

```text
CONFIG_SECURITY_LANDLOCK=y
landlock
landlock: Up and running.
```

Some distributions do not expose `/boot/config-*`, restrict the kernel log,
or do not mount `securityfs`. In those cases, `collo doctor` remains the most
direct test because it invokes the same ABI query used before running a
sandboxed command.

## Configuration recipes

Sandbox configuration belongs in the `permissions` object in either
`~/.collomia/config.json` or a trusted project `.collomia.json`. Project
configuration is ignored until the project is trusted; after changing it, run
`collo trust` again and confirm the effective configuration with:

```sh
collo config show
collo doctor
```

### Compatibility-first development

This is the least disruptive way to add filesystem write containment. It
preserves dependency reads and package downloads:

```json
{
  "permissions": {
    "sandbox": "auto",
    "sandbox_allow_network": true,
    "sandbox_allow_read_outside_workspace": true,
    "command_env": "minimal"
  }
}
```

This is also Collomia's behavior when only `sandbox: "auto"` is selected:
the two `sandbox_allow_*` compatibility switches default to `true`.

### Fail-closed filesystem and user-data read confinement

This is a strong configuration for Ubuntu 26.04 when commands still need
Internet access:

```json
{
  "permissions": {
    "sandbox": "require",
    "sandbox_allow_network": true,
    "sandbox_allow_read_outside_workspace": false,
    "sandbox_readable_roots": [
      "${HOME}/go/pkg/mod"
    ],
    "sandbox_writable_roots": [
      "${HOME}/.cache/go-build"
    ],
    "command_env": "minimal"
  }
}
```

With read confinement enabled, Collomia grants read/execute access to:

- the workspace;
- temporary and writable roots;
- conventional runtime/configuration roots such as `/usr`, `/lib`, and
  `/etc`;
- executable directories present in `PATH`; and
- each explicit `sandbox_readable_roots` entry.

Everything else that Landlock can mediate is denied. A writable root is
implicitly readable. Relative roots resolve from the workspace, environment
variables expand when the command runs, and existing symlinks are resolved
before the policy is installed.

Grant immutable dependency stores and SDKs as readable. Grant only caches or
output directories that genuinely need changes as writable. Avoid granting
the entire home directory because that defeats most of the read boundary.

### Deny command networking

On a kernel exposing Landlock ABI v10 or newer, complete command-network
denial can be required:

```json
{
  "permissions": {
    "sandbox": "require",
    "sandbox_allow_network": false,
    "sandbox_allow_read_outside_workspace": false,
    "command_env": "minimal"
  }
}
```

On ABI v4–v9, including stock Ubuntu 26.04:

- `sandbox: "auto"` installs the filesystem and TCP restrictions, but reports
  that UDP denial is missing;
- `sandbox: "require"` refuses the command rather than presenting TCP-only
  containment as complete network denial.

Do not treat TCP-only Landlock as an offline environment: UDP and DNS
datagrams may still leave the process. Collomia does not currently provide
domain-scoped grants or configure a network namespace, firewall, or proxy on
the user's behalf.

## Configuration field reference

| Field | Values/default | Linux effect |
| --- | --- | --- |
| `sandbox` | `off` (default), `auto`, `require` | `off` skips Landlock. `auto` applies available protection and warns on degradation. `require` refuses when the backend or a requested capability is unavailable. |
| `sandbox_allow_network` | `true` by default | `false` requests network denial: TCP on ABI v4+, and TCP plus UDP on ABI v10+. |
| `sandbox_allow_read_outside_workspace` | `true` by default | `false` adds Landlock read/execute rights to the handled policy and limits user-data reads to allowed roots. |
| `sandbox_readable_roots` | empty list | Additional read/execute-only files or directories. Used when read confinement is enabled. |
| `sandbox_writable_roots` | empty list | Additional writable files or directories; these are automatically readable. |
| `command_env` | `minimal` automatically when sandboxed | `minimal` reduces inherited environment variables and credentials; `full` deliberately passes the parent environment. |

`sandbox_allow_network` controls only the command's network access. It is not
an allowlist of hosts or domains. `sandbox_allow_read_outside_workspace`
controls content access, but Landlock does not make every kind of file metadata
invisible. See [Security limitations](#security-limitations).

## Custom kernels and boot configuration

If `collo doctor` reports `Landlock is unavailable (kernel too old or
disabled)`, check all of the following:

1. The host kernel is Linux 5.13 or newer.
2. The kernel was built with `CONFIG_SECURITY_LANDLOCK=y`.
3. The active LSM list contains `landlock`.
4. A container seccomp policy or other syscall filter is not blocking the
   Landlock system calls.

For a custom kernel, `CONFIG_LSM` should include `landlock`. If the kernel has
Landlock built in but the boot-time LSM list omits it, the `lsm=` kernel
parameter can enable it. Preserve every existing security module and add
`landlock`; do not replace the list with `landlock` alone. For example, if the
existing list is:

```text
lockdown,yama,integrity,apparmor,bpf
```

the corresponding value would be:

```text
lsm=landlock,lockdown,yama,integrity,apparmor,bpf
```

Changing kernel boot parameters is a system-administration action. Follow the
distribution's bootloader documentation, preserve its existing LSM ordering,
reboot, and rerun `collo doctor`. Stock Ubuntu installations should not need
this change.

## Containers, WSL, and cloud images

- A container uses its host's kernel and Landlock ABI, not the version implied
  by the container's `/etc/os-release`.
- A container runtime may block one or more Landlock system calls with seccomp.
  `auto` warns and continues without the OS sandbox; `require` refuses.
- Ubuntu under WSL uses Microsoft's WSL kernel. Check `collo doctor`; the
  Ubuntu userspace version alone does not prove Landlock availability.
- Minimal, appliance, Raspberry Pi, cloud, or vendor kernels may use a
  different `CONFIG_LSM` list from the standard Ubuntu generic kernel.

Do not weaken a host or container security profile merely to silence a
diagnostic. Decide whether Landlock is part of the intended boundary and use
`require` when running without it would be unacceptable.

## Troubleshooting

### `require` reports missing UDP network denial

The running kernel is ABI v4–v9. This is expected on Ubuntu 26.04's Linux 7.0
kernel. Keep command networking enabled, select `auto` and accept the reported
TCP-only boundary, upgrade to an ABI v10 kernel, or supply a separate network
boundary. Do not assume the command is offline.

### A build works in the shell but not in Collomia

Look for three common causes:

- A dependency or SDK is outside the workspace: add a narrow
  `sandbox_readable_roots` entry.
- A build cache must change: add a narrow `sandbox_writable_roots` entry.
- The command needs a package registry or documentation site: set
  `sandbox_allow_network` to `true`.

If the tool needs proxy, registry, compiler, or cloud environment variables,
`command_env: "minimal"` may intentionally omit them. Use `full` only after
considering the credential exposure to agent-generated commands.

### Landlock is available on the host but unavailable in a container

The container's syscall policy is probably different from the host process.
Confirm with `collo doctor` inside the container. The container runtime must
permit `landlock_create_ruleset`, `landlock_add_rule`, and
`landlock_restrict_self`; Landlock itself does not require root or additional
capabilities.

### Project sandbox settings appear ignored

Project configuration is security-sensitive and must be trusted. Run:

```sh
collo trust --status
collo trust
collo config show
collo doctor
```

Trust the project only after reviewing the complete `.collomia.json`, because
it can also define providers, hooks, MCP servers, and other executable
integration settings.

## Security limitations

Landlock is an additional restriction layer, not a VM or container:

- ABI v1–v2 cannot mediate standalone truncation; ABI v3+ is recommended.
- ABI v4–v9 cannot deny UDP.
- Collomia deliberately keeps public system runtime/configuration roots
  readable so dynamically linked tools, TLS, identity lookup, and package
  clients continue to work.
- Landlock has documented limitations for special filesystems and operations
  not represented by its handled access rights.
- Existing permissions, AppArmor/SELinux policy, container confinement, and
  filesystem ACLs still apply. Landlock can only remove access; it cannot
  grant access the user did not already have.
- Collomia's current network switch is all-or-nothing. Domain- and
  endpoint-scoped command grants are not implemented yet.

Use `sandbox: "require"` for workflows where silently losing an available
boundary would be unacceptable, but interpret its guarantees together with
the running Landlock ABI and the limitations above.
