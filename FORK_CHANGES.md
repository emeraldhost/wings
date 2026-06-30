# EmeraldHost Wings — Divergences from Upstream Pterodactyl Wings

This file tracks **which changes are our own** (EmeraldHost-specific) versus upstream
[`pterodactyl/wings`](https://github.com/pterodactyl/wings). Use it during upgrades so
our customizations are **not accidentally reverted** when pulling in upstream changes.

- **Baseline for this comparison:** upstream tag **`v1.13.1`** (`e771816`)
- **Last reviewed:** 2026-06-30
- **Module path:** this fork is `github.com/Rene-Roscher/wings` (upstream is
  `github.com/pterodactyl/wings`). Version is injected at build time via ldflags
  (`-X .../system.Version=<tag>`); `system/const.go` stays `develop` and is **not** a divergence.
- **How to regenerate the picture:**
  ```bash
  git fetch upstream --tags
  git diff --name-status <upstream-tag> HEAD        # what differs
  git diff <upstream-tag> HEAD -- <path>            # inspect a single file
  ```
  Diff direction: in `git diff <upstream-tag> HEAD`, a `+` line is **in our fork**,
  a `-` line is **upstream**.

> ⚠️ The biggest divergence by far is the **backup subsystem** (~6 000+ lines): an
> operation registry/queue, retry, WebSocket progress, multi-format compression and a
> heavily customized restore path. Upstream merges in `server/backup*`, `router/router_server_backup.go`,
> `server/server.go` and `sftp/server.go` will almost always conflict — resolve by **keeping ours**
> and grafting upstream's functional/security changes on top (that is exactly how v1.13.1 was merged).

---

## 1. Our own changes — PRESERVE on every upgrade

### 1.1 Module rename (mechanical, but must stay)

| Path | What | Note |
|------|------|------|
| `go.mod` | `module github.com/pterodactyl/wings` → `github.com/Rene-Roscher/wings` | Root of the rename; every Go import of the module changes accordingly. |
| `Dockerfile`, `Makefile`, `.github/workflows/{push,release}.yaml`, `wings.go` | ldflags / `SRC_PATH` / import path use the `Rene-Roscher` module | Required so `system.Version` is injected and builds resolve. |
| ~every `*.go` file | `github.com/pterodactyl/wings/...` → `github.com/Rene-Roscher/wings/...` | **Incidental noise** — appears as conflicts on most merges but carries no behavior. Always "keep ours (Rene-Roscher)". |

### 1.2 Backup subsystem — the largest divergence

> Upstream's backup path is small (`s.Backup(b)` / `s.RestoreBackup(b)` in a bare goroutine).
> The fork replaced it with a queued, cancellable, progress-reporting, multi-format pipeline.

**Operation registry, queue, retry (server package)**

| Path | What |
|------|------|
| `server/backup_operations.go` | **Fork-new.** `BackupOperationRegistry` + global `GetBackupOperationRegistry()`: concurrency-limited (`maxConcurrentBackups/Restores = 8`), queueing, cancellation. `Register()` returns the 5-tuple `(*op, ctx, cancel, err, wasQueued)` consumed by the router. `Cancel/Complete/CleanupStaleOperations` block-receive the semaphore token to release slots. Background `StartBackupOperationCleanup`. |
| `server/backup.go` | `Backup()`/`RestoreBackup()` are now compat wrappers over `BackupWithContext`/`RestoreBackupWithContext` (context + 6h/4h timeouts, atomic state transitions). `BackupWithRetry(ctx,b,2)` with exponential backoff. Restore does compression auto-detection, progress, restore-stats and per-dir `MkdirAll`+`Chown`. On panel-notify failure for a **successful** backup it **does not** delete the archive (upstream does). |
| `server/server.go` | `Server.backingUp *AtomicBool` (+ init), `AtomicStateTransition` + `ApplyAtomicStateTransition()`, `CleanupForDestroy()` cancels in-flight ops + cleans orphaned backup files, `PublishActivity()`. |
| `server/install.go` | `IsBackingUp()` / `SetBackingUp()`. (Note: `IsInProtectedState()` was deliberately **not** extended with `backingUp`.) |
| `server/power.go`, `server/errors.go` | `HandlePowerAction()` also blocks while `IsBackingUp()`, returning new sentinel `ErrServerIsBackingUp`. |
| `environment/environment.go` (+ docker state whitelist) | New states `backup`, `restore`, `backup_queued`, `restore_queued`. |

**Progress & activity over WebSocket**

| Path | What |
|------|------|
| `server/backup_progress.go` | **Fork-new.** `BackupProgressUpdate` WS payload `{backup_id,type,percentage,bytes_written,bytes_total}`, throttled tracker, S3 80/20 archive-vs-upload split. |
| `server/backup/download_progress.go` | **Fork-new.** `DownloadProgressReader` / `NewDownloadProgressReader` — S3 restore download progress. |
| `server/events.go` | New `BackupProgressEvent`, `DownloadProgressEvent`, `ActivityEvent`. **Frontend contract.** |
| `server/activity.go` | New `ActivityFile{Downloaded,Compressed,Decompressed,Chmod}`; `SaveActivity` also publishes `ActivityEvent` over WS. |

**Multi-format compression & checksums**

| Path | What |
|------|------|
| `server/backup/backup.go` | **SHA-256** checksums + `ChecksumType: "sha256"` (upstream uses **sha1**). ⚠️ **Protocol-facing** — a careless merge reverts to sha1 and breaks checksum compatibility with our Panel. `PathForLocalBackup()` helper. |
| `server/backup/compression.go` | **Fork-new.** `CompressionRegistry` (gzip/zstd/tar/none) + `IsValidBackupContentType()` — used by the router content-type gate; without it the router won't compile. |
| `server/backup/backup_local.go` | `foundPath` + extension-probing `LocateLocal` (`.tar.gz/.tar.zst/.tar`), auto-detecting `Restore`, `CleanupBackupFilesForServer`. |
| `server/backup/backup_s3.go` | Two-phase backup reuse, success-flag cleanup (failed uploads kept for retry), orphaned-part logging, upload progress (`ProgressReader`/`ProgressTracker`), custom HTTP/1.1 transport, part-retry with 100MB memory-buffer threshold / 5GB cap, `Restore()` expects an **already-decompressed** tar stream. |
| `server/filesystem/archive.go` | Archiver no longer skips directory entries → **empty directories are preserved** in archives. `createCompressor()` refactor. |
| `server/filesystem/archive_restore.go` | **Fork-new.** `DetectCompressionFormat` (magic bytes) + `CreateDecompressor`, wired into restore. |
| `server/filesystem/compress_binary_test.go`, `server/backup/*_test.go`, `router/content_type_test.go`, `server/backup_*_test.go` | Fork-new regression suites for the above. Keep them passing. |

**Router API (fork-only endpoints + customized handlers)**

| Path | What |
|------|------|
| `router/router.go` | Fork-only routes `GET /backup/operations` and `DELETE /backup/:backup/cancel`. |
| `router/router_server_backup.go` | `cancelServerBackup` + `getServerBackupOperations` (fork-only). `postServerBackup` / `postServerRestoreBackup` rewritten: 409 concurrency guards, registry queueing, timeouts, panic recovery, retry, S3 download progress, and content-type via `backup.IsValidBackupContentType` (gzip+zstd+tar) instead of upstream's gzip-only check. |

### 1.3 SFTP activity streaming

| Path | What |
|------|------|
| `sftp/event.go`, `sftp/handler.go` | `EventPublisher` interface + `publisher` on the event handler → SFTP file actions streamed to the panel via `Server.PublishActivity` (in addition to DB persistence). |

> The fork's previous `SmartSecurityProtector` SFTP brute-force/IP-reputation system
> (and its `sftp.security.*` config) was **removed** — `sftp/server.go` now matches upstream
> (vanilla SFTP auth) apart from the module rename. SFTP abuse protection is left to the
> network layer (firewall / fail2ban) / the Panel.

### 1.4 Repo config

| Path | What |
|------|------|
| `.gitignore` | Fork-added `.claude-flow/`, `.hive-mind/`, `CLAUDE.md`. Upstream will never add these — keep on merge. |
| `.github/workflows/**`, `Makefile`, `Dockerfile` | Our build/release pipeline (with the renamed module path). |

---

## 2. Config divergences — confirm whether intentional

These sit on **different** values/fields than upstream; they will re-appear in a generated
`config.yml`. Review on upgrade.

| Path | Fork value / field | Note |
|------|--------------------|------|
| `config/config.go` → `Backups.Format` | `"gzip"` (default) / `"zstd"` | Fork-only field. ⚠️ Currently largely **inert**: only the (unused) system-tar streamer reads it; the active Go archiver ignores it. |
| `server/backup_operations.go` | `maxConcurrentBackups/Restores = 8`; cleanup ticker 5 min / op TTL 8 h; backup 6 h / restore 4 h timeouts | Fork-chosen capacity/timeouts. |
| `server/backup_progress.go` | 250 ms throttle; S3 80/20 split; 1 MB chunking | Determines WS emission rate / S3 percentage curve. |
| `server/backup/backup_s3.go` | per-part upload `Content-Type: application/octet-stream` (upstream `application/x-gzip`) | Because the fork supports multiple formats. Verify Panel/S3 presigned flow tolerates it. |

---

## 3. Known issues in our own fork code (tech debt)

Every item below was **verified fork-only** (`git grep` against upstream `e771816` returns
zero hits) — upstream wings does **not** do these, so they are **our** code/behavior, not
inherited upstream defaults that can be ignored. These are not "divergences to preserve" so
much as bugs/concerns in our own additions, worth fixing rather than defending on upgrade.

| Area | Concern (all fork-only) |
|------|---------|
| **Committed binaries** | `wings-debug`, `wings-fixed`, `wings-test` (~41 MB each) and `dist/wings_test` (~28 MB) are committed build artifacts (~150 MB total), not gitignored. Repo bloat / accidental commits. |
| **Backup cleanup scope** | `cleanupBackupFiles` (server.go) and `CleanupBackupFilesForServer` (backup_local.go) match backup files by **filename pattern only** and do **not** filter by the server's ID. Since the backup directory is shared, deleting one server can remove **other** servers' local backups. |
| **`checksum_type` label** | `server/backup.go` emits `"sha256"` in most events but still `"sha1"` in the panel-notify-failure success branch. Reconcile the labels (actual algorithm is sha256). |
| **`validateBackupContent`** | Fails a backup on any server-vs-archive file/dir count mismatch (can race a live server writing files), and computes a full SHA-256 over the **entire server tree and backup file** purely for a debug log line (perf cost on large servers). |
| **ZSTD restore disabled** | `archive_restore.go` `CreateDecompressor` returns `"ZSTD compression is no longer supported"`; any older zstd backups hard-fail to restore despite zstd being a supported *backup* format elsewhere. Reconcile. |
| **Dead code** | `server/filesystem/archive_system.go` (system `tar`/`zstd` shell-out helpers) has **zero callers**. Abandoned experiment; needs host `tar`/`zstd` + uses a hardcoded `/tmp` exclude file. |

---

## 4. NOT fork divergences — adopted from upstream (do **not** re-apply)

These show up around our customizations but are **upstream v1.13.1** code. Treating them as
fork changes risks duplicating or mis-merging them on the next upgrade.

| Path | Reality |
|------|---------|
| `router/router_server_backup.go` SSRF cluster — `backupRestoreHttpClient`, `validateBackupDownloadUrl`, `parseBackupUuid`, `isBlockedBackupRestoreIP`, `isExplicitlyBlockedBackupRestoreIP`, `isAllowedBackupRestoreDestination`, `isSupportedBackupRestoreContentType`, `blockedBackupRestorePrefixes`, `backupDownloadError` | **Upstream v1.13.1** backup-restore SSRF hardening. The **only** fork edit in this cluster: the restore handler calls `backup.IsValidBackupContentType` instead of `isSupportedBackupRestoreContentType` (the latter is retained only for upstream parity + its test). |
| `config/config.go` → `Backups.RestoreHostAllowlist` | **Upstream v1.13.1.** Pairs with the SSRF allowlist above. Not a fork field. |
| `server/backup/backup.go` → `validateIdentifier()` / `normalizedIdentifier()` (+ `Path()` `path.Base` fallback) | **Upstream v1.13.1** UUID hardening. The fork uses them unchanged. |
| `system/const.go` | Byte-identical to upstream (`Version = "develop"`). |
| `.github/FUNDING.yaml` (`github: [pterodactyl]`) | **Upstream default, unchanged** (`git diff e771816 HEAD` is empty). Stale for a fork (sponsorship points at upstream) but NOT our change — clean it up if desired, don't track it as a divergence. |
| `.github/workflows/release.yaml` release-bot identity (`ci@pterodactyl.io` / `Pterodactyl CI`) | **Upstream default, unchanged.** Upstream's release.yaml already sets this identity. Not our divergence. |
