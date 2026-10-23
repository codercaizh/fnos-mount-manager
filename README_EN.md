# fnOS Mount Manager

[简体中文](README.md) · [Testing](docs/TESTING.md) · [Security](docs/SECURITY.md) · [Releases](https://github.com/codercaizh/fnos-mount-manager/releases)

A lightweight folder-alias manager for **fnOS NAS**, powered by Linux bind mounts. It exposes a remote SMB/CIFS folder already mounted by fnOS through a local shared folder, so the stock Backup app can access remote NAS data **without modifying built-in system files**.

![Mobile user interface](docs/screenshot-mobile.png)

> **v1.1.2** · ARM64 and x86_64 · fnOS ≥ 1.2.0604 · MIT (first-party source) · Requires root/CAP_SYS_ADMIN. Test against disposable folders before production use.

## The problem

Certain versions of the built-in fnOS Backup app do not directly expose mounted remote folders in their file picker. This application bridges that gap:

| Role | Example |
|---|---|
| Remote source managed by fnOS | `/vol02/1000-0-<mount-id>/Photos` |
| Local shared parent folder | `/vol1/1000/share` |
| Desired alias | `Family-Photos` |
| Resulting local folder | `/vol1/1000/share/Family-Photos` |

The resulting path is a **second view of the same remote data**, not a copy, sync operation, or new SMB connection.

## Features

- **Native fnOS folder picker** via pinned `@trimjs/web-app` SDK; remote sources and local shared destinations are selected using the system UI.
- **Read-only and read/write bind mounts** (use read-only for backup sources, read/write when restoring files to a remote destination).
- **Automatic reconciliation** on service startup and every 60 seconds; re-establishes aliases after their remote sources recover.
- **Multiple mappings**, independent names, enabled states, health indicators, retry, rename and removal.
- **Defensive path validation**: reject non-empty destinations, symlinks, unsupported system paths, overlapping mappings, and foreign mounts.
- **Small footprint**: Go native service, standard fnOS FPK, private Unix-socket HTTP gateway, no Docker or exposed management port.

## Installation

1. Download the matching FPK from [GitHub Releases](https://github.com/codercaizh/fnos-mount-manager/releases): **arm64** for ARM-based NAS or **x86_64** for Intel/AMD.
2. Install it using fnOS **App Center → Manual install**, then start **Mount Manager**.
3. **First mount the SMB/CIFS share with the built-in fnOS Remote Mount feature**. This app does not store SMB credentials or establish remote sessions.
4. In Mount Manager, pick the remote source folder and a **local shared parent folder** (e.g., `/vol1/1000/share`).
5. Enter a name for the new mount directory. Select **Read Only** for backup-source use or **Read/Write** for restore targets.
6. Create the mapping and verify that its status is **Mounted**. Browse the resulting local folder in the stock Backup app.

Read-only applies to the new bind alias only. Read/write remains subject to the CIFS server's access policy and the fnOS permissions/ACL of the account used by the backup service.

### Mapping management

**Retry** rechecks a mapping; **Disable** unmounts the alias while preserving its configuration; **Enable** resumes reconciliation; **Rename** changes the local alias; **Delete** unregisters and unmounts the alias without deleting remote content. Only an empty placeholder directory originally created by the app may be removed. Stop backup/restore tasks before changing mappings to avoid `EBUSY` errors.

## Architecture, limitations and safety

The Go daemon reads `/proc/self/mountinfo` and performs bind/unmount syscalls, running with elevated privileges requested through the FPK. The web interface is accessed through the fnOS application gateway and its local Unix socket; it does not open a public TCP port.

**Important limitations:**

- This is **not an SMB reconnection client**. If the remote CIFS connection drops, fnOS itself must reconnect it before the alias can be restored.
- The daemon does not actively ping the remote SMB server. A stale kernel mount can temporarily appear online.
- Bind mounts are non-recursive; independent submounts beneath the source may not be included.
- Sources must be permitted remote mounts under `/vol02/<mount-id>/...` or supported local user share folders. Destination parents must be located under `/volN/<numeric-user-id>/<share>/...`. Arbitrary system paths are intentionally prohibited.
- Busy mounts are not forcibly detached. Foreign mounts and populated directories are never knowingly overwritten.
- Installation requires **root/CAP_SYS_ADMIN**. Do not bypass fnOS restrictions if third-party privileged applications are blocked.
- Automated builds and tests **do not constitute a successful hardware-level installation or end-to-end backup/restore test**. Test on your own target firmware; see [the validation matrix](docs/TESTING.md).

## Build from source

Build requirements: Go 1.22+, Node.js 22+, npm, Linux x86_64 development host, and the official fnOS `fnpack` v1.2.3.

```bash
git clone https://github.com/codercaizh/fnos-mount-manager.git
cd fnos-mount-manager
npm ci --ignore-scripts
npm run prepare:sdk
go test ./...
go test -race ./...
go vet ./...
npm run test:syntax
npx playwright install chromium
npm run test:ui

mkdir -p dist
curl -fsSL https://static2.fnnas.com/fnpack/fnpack-1.2.3-linux-amd64 -o dist/fnpack-linux-amd64
echo '54b97fa7b70968c4d05c79840f5daeff508957d0bb2062fdb0376d00d9615c93  dist/fnpack-linux-amd64' | sha256sum -c -
chmod +x dist/fnpack-linux-amd64
bash scripts/build.sh
```

Outputs: `dist/fnos-mount-manager-1.1.2-arm64.fpk` and `dist/fnos-mount-manager-1.1.2-x86_64.fpk`, plus individual SHA-256 checksums. The built FPKs declare the respective `platform=arm` and `platform=x86` values and embed architecture-specific Linux binaries.

The official fnOS SDK is downloaded using a locked npm dependency (`@trimjs/web-app@0.5.0`); its minified distribution is **not committed as original project source**.

## Repository layout

```text
cmd/mountmgr/main.go            HTTP API, daemon, bind/unmount operations
cmd/mountmgr/*_test.go          backend unit / security / lifecycle tests
cmd/mountmgr/web/               white-background UI and native picker callback
packaging/fpk/                  fnOS manifest, privileges, icons and lifecycle scripts
scripts/build.sh                dual-architecture build using official fnpack
scripts/prepare-sdk.cjs         assemble pinned official fnOS SDK
scripts/ui-smoke.cjs            Playwright UI and picker integration smoke tests
scripts/icon.svg                original icon artwork
.github/workflows/ci.yml        CI and build artifacts
docs/                           testing, threat model and third-party notices
```

Persistent state: `TRIM_PKGVAR/private/mounts.json` (atomic update, file mode `0600`). Logs: `TRIM_PKGVAR/service.log`. Gateway socket: `TRIM_APPDEST/app.sock`.

## License and disclaimer

Original project code is licensed under [MIT](LICENSE). fnOS, its SDK, and fnpack belong to their respective maintainers; see [third-party attributions](docs/THIRD_PARTY.md). **This is an independent community project, not an official fnOS application.** Include an fnOS version, CPU architecture and sanitized logs when reporting issues.
