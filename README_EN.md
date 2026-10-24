# NAS Toolbox for fnOS

[简体中文](README.md) · [Testing](docs/TESTING.md) · [Security](docs/SECURITY.md) · [Third-party notices](docs/THIRD_PARTY.md) · [Releases](https://github.com/codercaizh/fnos-toolbox/releases)

A community fnOS NAS application combining **Directory Mapping** (previously Mount Manager) and **Quick Copy** (rclone) in **one Go daemon, one FPK, and one application-private state directory**.

**Repository renamed to [codercaizh/fnos-toolbox](https://github.com/codercaizh/fnos-toolbox). The fnOS app ID and internal Go module path remain `fnos-mount-manager` for backward compatibility.**

**Current version: v2.0.6** · ARM64 / x86-64 · fnOS 1.2.0604 or later · Requires root/CAP_SYS_ADMIN for bind mounts · Not an official fnOS product.

## What it does

### Directory Mapping

Map an SMB/CIFS directory **already mounted by fnOS** into a local user share under a friendly alias, using a Linux bind mount. This creates **another view of remote data**, not a copy or new SMB connection.

- Read-only or read/write alias, multiple mappings, enable/disable, retry, rename, change source, remove.
- Reconcile at startup and periodically (approximately once per minute), after fnOS has restored the remote SMB connection.
- When an alias created by this app loses its target path, it can recreate **all missing subdirectories beneath an existing volume/user root** such as /vol1/1000, after validating each path component.
- Never fabricate a missing disk/volume or user root, or overwrite nonempty destinations, symlinks, foreign mounts, or targets not owned by the app.

Example: separate remote Dad and Mom photo sources may be mapped to /vol1/1000/网盘备份/爸爸相册 and /vol1/1000/网盘备份/妈妈相册 without conflating the two SMB sources.

### Quick Copy

- Native fnOS SDK picker for file(s) and folders, including nested directories of existing remote mounts. Picker multi-selection and mixed-type selection depend on fnOS host support.
- Bundled **rclone 1.75.2**. Concurrency defaults to **logical CPU count × 2**, or can be configured directly.
- **Fast mode:** path/name and size comparison, ignoring modification time.
- **Strict mode:** checksums for change detection followed by a full source/destination content read verification. This is much slower and consumes additional I/O.
- Live bytes, percentage, transfer speed, ETA in h/m/s, active file transfers, current file names, and count of transferred or skipped files.
- Pause/resume, manual retry and persisted task records. An interrupted single file may need to be copied again; file-level skip/resume **is not guaranteed byte-range resumption**.
- Concurrent independent source items share a total concurrency budget. Up to eight independent files can be copied concurrently with concurrency 8; one source file normally uses one transfer.
- **Recent Transfers:** retain the latest **three completed records** (active, paused and failed jobs remain available). Expand a record to see individual source and destination paths.

Copy does **not** delete source files and does **not** use rclone sync; an existing destination file may be overwritten if its validation fails. Test on disposable files first.

## Installation and upgrades

1. Download the appropriate NAS Toolbox ARM64 or x86-64 FPK from [GitHub Releases](https://github.com/codercaizh/fnos-toolbox/releases). Install through fnOS App Center → Manual install.
2. **Upgrade Mount Manager in place. Do not uninstall it first.** NAS Toolbox deliberately retains the application ID **fnos-mount-manager** to keep existing settings.
3. If you have the separate old Quick Copy app installed, finish or pause any running tasks and **keep it installed for the upgrade**. On startup NAS Toolbox attempts to import the latest three **completed** records from the old app. Active jobs are deliberately not migrated or started twice.
4. Verify both mapping records and transfer history in NAS Toolbox; you can then uninstall the old separate Quick Copy app to save idle RAM.
5. Ideally let a large copy finish before upgrading. Upgrades interrupt running rclone processes; resume manually afterwards.

If the mobile app shows cached icons or an old copy page, close the fnOS mobile app fully and reopen it.

## How to use

**Directory Mapping:** first connect your SMB/CIFS share using the built-in fnOS Remote Mount feature. Choose the mounted source, a local shared-folder parent, alias and read/write mode, and create a mapping. Check it is mounted before using the stock Backup application.

**Quick Copy:** choose source files/folders, a destination directory, transfer concurrency and validation mode; start copying. Expand the running transfer status to see the active filenames, or pause/resume from the job card.

## Architecture and state

| Component | Location / behavior |
|---|---|
| App identifier | fnos-mount-manager (kept for migration) |
| Always-on process | One Go daemon: mountmgr |
| Web API | fnOS gateway over a Unix socket; no additional publicly exposed TCP port |
| rclone process | Spawned only when a copy job is active |
| Mount records | TRIM_PKGVAR/private/mounts.json |
| Copy records | TRIM_PKGVAR/private/copies.json |
| Service log | TRIM_PKGVAR/service.log |

The daemon restricts source and destination paths to supported fnOS storage roots. It rejects overlapping source/destination trees and symlink-based paths. See [Security](docs/SECURITY.md) for details and limitations.

## Build and test

Requirements: Linux x86-64 development machine, **Go 1.23+**, Node.js 22+, npm, curl, unzip and official fnpack v1.2.3.

~~~bash
git clone https://github.com/codercaizh/fnos-toolbox.git
cd fnos-toolbox
npm ci --ignore-scripts
npm run prepare:sdk

go test ./... -count=1
go test -race ./... -count=1
go vet ./...
npm run test:syntax
npx playwright install chromium
npm run test:ui

# Downloads rclone v1.75.2 for both architectures, SHA-256 verified
bash scripts/fetch-rclone.sh

mkdir -p dist
curl -fsSL https://static2.fnnas.com/fnpack/fnpack-1.2.3-linux-amd64 -o dist/fnpack-linux-amd64
echo "54b97fa7b70968c4d05c79840f5daeff508957d0bb2062fdb0376d00d9615c93  dist/fnpack-linux-amd64" | sha256sum -c -
chmod +x dist/fnpack-linux-amd64
bash scripts/build.sh
python3 scripts/verify-fpk.py
~~~

The build produces **dist/nas-toolbox-2.0.6-arm64.fpk** and **dist/nas-toolbox-2.0.6-x86_64.fpk**, plus checksums. The CI workflow runs Go unit tests, the race detector, vet, Playwright UI mocks, and both architectures' package verification.

CI uses **mocked fnOS gateway/picker and mount syscalls**; it is not evidence of successful installation, SMB reconnection, or backup/restore on physical hardware. Follow the [hardware validation matrix](docs/TESTING.md) before production use.

## Repository layout

- cmd/mountmgr: directory mappings, shared HTTP API, Web UI, packaging entry point
- internal/copyengine: rclone engine, concurrent jobs, live statistics, persistence
- packaging/fpk: fnOS metadata, privilege configuration, icons, install/start scripts
- scripts: asset generation, dependency downloads, tests, dual-arch FPK build
- docs: testing, security and third-party attributions

The standalone Mount Manager v1.2.5 documentation is archived as [README_MOUNT_MANAGER.md](README_MOUNT_MANAGER.md).

## Security, licensing, disclaimer

This is a privileged program requiring **root/CAP_SYS_ADMIN** for directory mapping. Treat your NAS data and mappings as important: test changes on a disposable share and never share SMB passwords, tokens or unredacted logs in public issues.

Original code is [MIT-licensed](LICENSE). Bundled rclone, the fnOS SDK and the fnpack tool retain their upstream license terms. This project is **independent community software, not endorsed by or affiliated with fnOS**.
