# fnOS Mount Manager

[简体中文](README.md) · [Testing](docs/TESTING.md) · [Security](docs/SECURITY.md) · [Releases](https://github.com/codercaizh/fnos-mount-manager/releases)

A lightweight folder-alias manager for **fnOS NAS**, powered by Linux bind mounts. It exposes a remote SMB/CIFS folder already mounted by fnOS through a local shared folder, so the stock Backup app can access remote NAS data **without modifying built-in system files**.

![Mobile user interface](docs/screenshot-mobile.png)

> **v1.2.5** · ARM64 and x86_64 · fnOS ≥ 1.2.0604 · MIT (first-party source) · Requires root/CAP_SYS_ADMIN. Test against disposable folders before production use.

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
- **Multiple mappings**, independent names, enabled states, health indicators, retry, change source, rename and removal.
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

Outputs: `dist/fnos-mount-manager-1.2.5-arm64.fpk` and `dist/fnos-mount-manager-1.2.5-x86_64.fpk`, plus individual SHA-256 checksums. The built FPKs declare the respective `platform=arm` and `platform=x86` values and embed architecture-specific Linux binaries.

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

## v1.2.2: explicit label provenance and correct fallback

Previously, an empty or incomplete fnOS mount-list response stopped alias resolution, leaving a `share · server` label. Unresolved mounts now continue to `trim.file.convertPath` without overwriting already matched native aliases.

The SMB picker marks labels as **fnOS mount alias**, **fnOS semantic path**, or **SMB share address (native alias unavailable)**. Saved mount paths and settings are unchanged. API availability may vary by fnOS firmware version.

## v1.2.1: two-step source picker and fnOS SMB aliases

Click **Source Directory** to open a two-option dialog:

- **Choose Folder**: launch the official fnOS folder picker for an individual subdirectory.
- **Choose Remote Directory**: open the list of currently mounted SMB share roots, including empty shares. Labels prefer fnOS's configured mount `comment`, plus `username@server` using the same convention as File Manager. Opaque `/vol02/...` IDs remain internal mapping values and are never the primary list label.

The daemon first attempts to read `appcgi.mountmgr.list` over the authenticated fnOS API-scope socket, then `trim.file.convertPath`, and finally displays the SMB share/server parsed from the kernel mount source if those APIs are inaccessible. The last resort may differ from a user-defined fnOS alias on older firmware. SMB credentials are not exposed to the browser.

The **Local Directory** tile only invokes the official picker with `directory: true` and `creatable: true`. The separate **New Folder** button and privileged folder-creation endpoint are gone. Whether fnOS shows its own New Folder action depends on host support (especially standalone App Auth).

## v1.2.3: native SMB mount aliases from the on-device configuration

On some fnOS versions, third-party API-scope calls for mount aliases return no data. In that case the picker previously showed a generic SMB share/server label.

**The preferred metadata source is now a read-only lookup of `/etc/mountmgr/mount_info.json`** when running as an authorized privileged app. Only `mountPoint`, `comment`, `userName`, and `address` are decoded, and records are matched by exact mount point. The UI uses File Manager's two-line convention: **custom alias** above `username@server`.

No system configuration is changed. Credential fields (including SMB passwords) are never passed to the web UI or written to logs. If the configuration is absent or inaccessible, the app still attempts the fnOS API and semantic-path fallbacks before showing SMB share/server. Native NAS firmware validation remains required.

## v1.2.4: fix nested fnOS mount metadata parsing

The v1.2.3 read-only configuration reader handled flat maps and arrays but missed the **user-ID → mount-ID → connection record** nested JSON layout used by fnOS. It silently skipped records and fell back to underlying SMB share names even when the file was readable.

This release walks nested objects and arrays up to a bounded depth, recognizes leaves by their `mountPoint` and `comment` fields, and projects only the display allowlist. Records are matched to live mounts by exact mount point. The system file is never modified and stored credentials are never logged or returned.

Regression tests reproduce the two-level layout and verify cross-user ordering, duplicate/share matching, non-mount-object filtering and depth limits. Evidence: [published fnOS mount manager JSON traversal](https://www.wangzipei.cn/archives/fei-niu-jiang-gua-zai-pan-gua-zai-dao-ben-di-mu-lu). Actual firmware integration remains a real NAS acceptance test.

## v1.2.5: prevent two aliases from accidentally using the same SMB source

### Root cause

The form previously cleared only the *alias name* after a successful creation while retaining the last SMB source path. Creating “Dad Photos” and “Mom Photos” consecutively under the same local parent could therefore bind **both target folders to the same remote share**. A local alias name never determines the remote source.

### Safeguards

- Clear the source selection after successful creation, while **keeping the local parent folder** for sequential mappings.
- Suggest the matching alias for a selected fnOS SMB mount; confirm an intentional difference between a known source alias and the local mapping name.
- Show the full source → target pair before creation and both paths separately in the management list.
- Reject duplicate source paths on the backend (HTTP 409) with the existing mapping name.
- Add **Change source** for existing mappings. The mapping must first be disabled and completely unmounted, avoiding changes under an active backup process. The existing local folder remains intact. Metadata updates roll back on persistence failure.

### Repair existing mappings safely

1. Stop all backup/restore jobs using the alias.
2. Disable the incorrect mapping and wait until it is unmounted. If busy, stop the processes using it rather than force-detaching.
3. Use **Change source**, choose the correct SMB mount root, save, and re-enable.
4. Confirm the management list now shows distinct sources for Dad and Mom Photos.
5. Existing mappings are **not changed automatically** during upgrade. If the underlying fnOS SMB mounts themselves show the same remote content, fix those native mounts first.

No content is copied or recursively deleted by a bind mount.
