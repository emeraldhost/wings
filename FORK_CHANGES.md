# EmeraldHost Wings — Divergences from Upstream Pterodactyl Wings

This file tracks **which changes are our own** (EmeraldHost-specific) versus upstream
[`pterodactyl/wings`](https://github.com/pterodactyl/wings). Use it during upgrades so
our customizations are **not accidentally reverted** when pulling in upstream changes.

- **Baseline for this comparison:** upstream tag **`v1.13.4`** (`747bc43`)
- **Last reviewed:** 2026-10-14
- **Repo vs. module path:** the repo is `github.com/emeraldhost/wings`; the Go module path is
  `github.com/Rene-Roscher/wings` (upstream is `github.com/pterodactyl/wings`). Version is injected at
  build time via ldflags (`-X .../system.Version=<tag>`); `system/const.go` stays `develop` and is **not** a divergence.
- **How to regenerate the picture:**
  ```bash
  git fetch upstream --tags
  git diff --name-status <upstream-tag> HEAD        # what differs
  git diff <upstream-tag> HEAD -- <path>            # inspect a single file
  ```
  Diff direction: in `git diff <upstream-tag> HEAD`, a `+` line is **in our fork**,
  a `-` line is **upstream**.

> ⚠️ The biggest divergence by far is the **backup subsystem** (~6 000 lines added vs v1.13.4: about
> 3 300 production code and 2 700 fork-only tests): an operation registry/queue, retry, WebSocket
> progress and a heavily customized restore path. Upstream merges in `server/backup*`,
> `router/router_server_backup.go`, `server/server.go` and `server/filesystem/archive.go` will almost
> always conflict — resolve by **keeping ours** and grafting upstream's functional/security changes on
> top (that is exactly how v1.13.1 and v1.13.4 were merged).
> `sftp/server.go` is **no longer fork code** (see §1.3/§4): on a conflict there, take upstream's version
> and re-apply only the module rename.
>
> v1.13.2 and v1.13.3 stayed clear of the backup subsystem and merged (nearly) conflict-free.
> v1.13.4 was a broad hardening release (153 files) with 24 conflicted files, including the whole
> backup/restore/archive path. Rows marked *(v1.13.4)* in §1 record where fork and upstream code now
> interlock; §4 lists what v1.13.4 brought in as plain upstream code.

---

## 1. Our own changes — PRESERVE on every upgrade

### 1.1 Module rename and repo links (mechanical, but must stay)

| Path | What | Note |
|------|------|------|
| `go.mod` | `module github.com/pterodactyl/wings` → `github.com/Rene-Roscher/wings` | Root of the rename; every Go import of the module changes accordingly. |
| `Dockerfile`, `Makefile`, `.github/workflows/push.yaml` | ldflags `-X …/system.Version` / `SRC_PATH` use the `Rene-Roscher` module. Otherwise identical to upstream v1.13.4: on merge, take upstream and re-apply the path. | Required so `system.Version` is injected and builds resolve. The fork-only `binary.yaml` sets the same ldflags path. `release.yaml`, `binary.yaml`, `docker.yaml` are full fork rewrites, see §1.4. |
| `README.md` ("Installation" section), `cmd/root.go` (startup banner Source/License) | Point at the **repo** `github.com/emeraldhost/wings`, not the module path. Release assets are `wings_linux_{amd64,arm64}` + `SHA256SUMS` (no plain `wings` asset). | Repo URL ≠ module path: only `go.mod`/imports/ldflags use `Rene-Roscher`. |
| ~every `*.go` file | `github.com/pterodactyl/wings/...` → `github.com/Rene-Roscher/wings/...` | **Incidental noise** — appears as conflicts on most merges but carries no behavior. Always "keep ours (Rene-Roscher)". E.g. `wings.go` differs only by its `cmd` import (upstream v1.13.4 dropped `rand.Seed` there — do not re-add it). After the rename, re-run `gofmt` (§5). |

### 1.2 Backup subsystem — the largest divergence

> Upstream's backup path is small (`s.Backup(b)` / `s.RestoreBackup(b, reader, truncate)` in a bare goroutine).
> The fork replaced it with a queued, cancellable, progress-reporting pipeline.

**Operation registry, queue, retry (server package)**

| Path | What |
|------|------|
| `server/backup_operations.go` | **Fork-new.** `BackupOperationRegistry` + global `GetBackupOperationRegistry()`: concurrency-limited (`maxConcurrentBackups/Restores = 8`), queueing, cancellation. `Register()` returns the 5-tuple `(*op, ctx, cancel, err, wasQueued)` consumed by the router. `Cancel/Complete/CleanupStaleOperations/CancelAllForServer` block-receive the semaphore token to release slots. Background `StartBackupOperationCleanup` (5 min ticker, drops operations older than 8 h) is launched from `cmd/root.go`. |
| `server/backup.go` | `Backup()` (own 6 h timeout) and `RestoreBackup()` (server context, no timeout) are compat wrappers over `BackupWithContext` (adds a 6 h timeout only if the ctx has no deadline) and `RestoreBackupWithContext`. The router sets the 6 h backup / 4 h restore timeouts. State transitions are atomic (`ApplyAtomicStateTransition`). `BackupWithRetry(ctx, b, 2)` (called by the router; 3 attempts) gives each attempt its own 6 h timeout and backs off quadratically (30 s·n²: 30 s, 120 s); no retry on cancel/deadline. Restore does compression auto-detection, progress and restore-stats. The S3 download size reaches the restore via the typed context key `DownloadSizeContextKey` (replaced the old `"download_size"` string key). In state `restore_queued` the restore asks `Environment.IsRunning()` instead of stopping the container. On panel-notify failure for a **successful** backup it **does not** delete the archive (upstream does). |
| `server/backup.go` restore *(v1.13.4)* | `RestoreBackup(b, reader, truncate)` / `RestoreBackupWithContext(ctx, b, reader, truncate)` carry upstream's new `truncate` flag: `TruncateRootDirectory` runs **inside** the restore, after the server has stopped and after compression detection (an undetectable S3 stream no longer wipes files first); the router no longer truncates before locating the backup. Upstream dropped the suspend/unsuspend toggling (it clobbered Panel suspensions); start protection is `SetRestoring(true)` **before** `WaitForStop` (also cancels SFTP sessions). `restoreEntry` merges both sides: fork's mode-preserving `MkdirAll`+`Chown`+`Chtimes` for directories, upstream's symlink recreation and skipping of hard links/devices/special files. |
| `server/server.go` | `Server.backingUp *AtomicBool` (+ init), `AtomicStateTransition` + `ApplyAtomicStateTransition()`, `PublishActivity()` (see §1.3). `CleanupForDestroy()` is **upstream again** since the v1.13.4 merge — no backup-op cancellation and no backup-file deletion there (see §3 resolved). |
| `router/router_server.go` `deleteServer` | Calls `GetBackupOperationRegistry().CancelAllForServer()` for the real server being deleted (moved here from `CleanupForDestroy` in the v1.13.4 merge, so throwaway duplicate-create/transfer instances with the same UUID no longer touch the live server's operations). |
| `server/install.go` | `IsBackingUp()` / `SetBackingUp()`. (Note: `IsInProtectedState()` was deliberately **not** extended with `backingUp`.) |
| `server/power.go`, `server/errors.go` | `HandlePowerAction()` also blocks while `IsBackingUp()`, returning new sentinel `ErrServerIsBackingUp`. |
| `environment/environment.go` (+ docker state whitelist) | New states `backup`, `restore`, `backup_queued`, `restore_queued`. The `switch` in `environment/docker/environment.go` `SetState()` must list **all four** (guarded by `environment/docker/state_test.go`). The queued states were only added to it in the v1.13.4 merge; before that, every backup/restore that had to wait for a registry slot panicked in `SetState()` and leaked its slot, and a queued restore crashed Wings. |
| `router/router_server_backup.go` goroutines | The registry cleanup `defer` (`registry.Complete` + `cancel`) is registered **directly after** a successful `Register()`, before the `wasQueued` `SetState`; both restore goroutines also have a top-level `recover`. Keep that order on merge, otherwise a panic leaks a registry slot until the 8 h stale cleanup. |
| `server/backup_restore_test.go` *(v1.13.4)* | Upstream suite; only its `restoreTestEnvironment` double gained no-op `SetState()` / `IsRunning()` because the fork's restore drives environment state. Assertions unchanged. |

**Progress & activity over WebSocket**

| Path | What |
|------|------|
| `server/backup_progress.go` | **Fork-new.** `BackupProgressUpdate` WS payload `{backup_id,type,percentage,bytes_written?,bytes_total?}` (byte fields `omitempty`). `type` is `create` / `restore` (250 ms-throttled `SimpleProgressTracker`) or `download` (S3 restore download, published unthrottled from `router/router_server_backup.go`). Also the S3 80/20 archive-vs-upload split. |
| `server/backup/download_progress.go` | **Fork-new.** `DownloadProgressReader` / `NewDownloadProgressReader`: wraps the S3 restore download body; its callback emits `BackupProgressEvent` with `type: "download"`, **not** `DownloadProgressEvent`. |
| `internal/progress/progress.go` | Fork hook on upstream's `Progress` writer: optional `ProgressCallback func()` field (called after every `Write()`), new `AddWritten(bytes uint64)` (atomic add + callback). Wired in `server/backup.go` (`ProgressCallback = progressTracker.CheckProgress`, `restoreProgress.AddWritten`) and required by the `ProgressTracker` interface in `server/backup/backup_s3.go`. **Keep on merge** — with upstream's file, `server/backup*.go` does not compile. Caveat: the `recover` in `Write()` covers the whole function, so a panicking callback makes `Write` return `(0, nil)` without forwarding the chunk. |
| `server/events.go` | New `BackupProgressEvent` (`"backup progress"`), `DownloadProgressEvent` (`"download progress"`: remote file pull in `router/downloader`, payload `DownloadProgressUpdate {identifier,filename,directory,url,percentage,bytes_written,bytes_total,status}`), `ActivityEvent` (`"activity"`). **Frontend contract** — see `WEBSOCKET_EVENTS.md` / `BACKUP_PROGRESS_EVENTS.md`. |

**Compression & checksums**

| Path | What |
|------|------|
| `server/backup/backup.go` | **SHA-256** checksums + `ChecksumType: "sha256"` (upstream uses **sha1**). ⚠️ **Protocol-facing** — a careless merge reverts to sha1 and breaks checksum compatibility with our Panel. Upstream's package-level gzip-only `format` is removed: restore detects the compression via `filesystem.DetectCompressionFormat` + `CreateDecompressor` (local) or gets an already-decompressed stream (S3), then extracts with plain `archives.Tar{}`. *(v1.13.4)* Fork patch on upstream's identifier code: `validateIdentifier()` only writes `b.Uuid` when it changes, and `Details()` validates once before spawning its goroutines — a `-race` failure surfaced by upstream's new restore test (the fork's restore calls `Details()`). |
| `server/backup/compression.go` | **Fork-new.** `CompressionRegistry` (gzip/tar/none) + `IsValidBackupContentType()` — used by the router content-type gate; without it the router won't compile. |
| `server/backup/backup_local.go` (+ `foundPath` in `server/backup/backup.go`) | `LocateLocal` fallback: if `<uuid>.tar.gz` is missing it also looks for `<uuid>.tar` and stores it in the fork-added `Backup.foundPath`, which `Backup.Path()` returns — so Remove/Restore **and** Size/Checksum/Details use the same file. Auto-detecting `Restore`. |
| `server/backup/backup_s3.go` | Two-phase backup reuse; the local archive is removed only after a successful upload (failed uploads kept for retry — *(v1.13.4)* upstream's unconditional `defer s.Remove()` was **not** taken, its error-checked removal is grafted into the success branch only); orphaned-part logging; upload progress (`ProgressReader`/`ProgressTracker`); custom `http.Transport` for part uploads (see §3); part-retry with 100 MB memory-buffer threshold / 5 GB cap; `Restore()` expects an **already-decompressed** tar stream. |
| `server/filesystem/archive.go` | Archiver no longer skips directory entries → **empty directories are preserved** in archives. `createCompressor()` refactor. *(v1.13.4)* Upstream's `compileIgnore()` + `ignoreMatchBudget`, `Readlinkat`-based symlink reading and `*[]byte` buffer pool are taken; the ignore compilation runs **before** `createCompressor()` so an invalid ignore list fails before anything is written. |
| `server/filesystem/archive_restore.go` | **Fork-new.** `DetectCompressionFormat` (magic bytes) + `CreateDecompressor`, wired into restore. |
| `server/filesystem/compress_binary_test.go`, `server/backup/{content_validation,restore,restore_edge_case,s3_simple}_test.go`, `router/content_type_test.go`, `server/backup_{cleanup,progress_validation,queue,retry}_test.go`, `environment/docker/state_test.go` | Fork-new regression suites for the above. Keep them passing. (`server/backup/backup_test.go` and `server/backup_restore_test.go` are upstream suites.) |
| `server/filesystem/archive_test.go` + `archive_stream_test.go` | **File layout divergence, not new behavior.** Our `archive_test.go` holds tests for the fork-only `archive_restore.go`; upstream's `TestArchive_Stream` lives in `archive_stream_test.go` instead. Upstream edits to `archive_test.go` therefore land in the *wrong* file on merge — port them into `archive_stream_test.go` by hand. *(v1.13.4: upstream's two new ignore-pattern cases were ported there; `getFiles` `continue`s on directory entries instead of returning, because of the empty-dir preservation above.)* |

**Router API (fork-only endpoints + customized handlers)**

| Path | What |
|------|------|
| `router/router.go` | Fork-only routes `GET /api/servers/:server/backup/operations` and `DELETE /api/servers/:server/backup/:backup/cancel`. |
| `router/router_server_backup.go` | Fork-only `cancelServerBackup` (404 when unknown/done, 403 when the op belongs to another server) and `getServerBackupOperations` (`{operations:[{id,backup_id,type,start_time}],count}`). `postServerBackup` / `postServerRestoreBackup` rewritten: 409 guards (backing up / restoring / transferring), registry queueing (`backup_queued` / `restore_queued`), 6 h / 4 h timeouts, panic recovery, `BackupWithRetry(ctx, b, 2)`. `SetRestoring` and `BackupRestoreCompletedEvent` live inside `RestoreBackupWithContext`. S3 restores wrap `res.Body` in `backup.NewDownloadProgressReader` and pass the size via `server.DownloadSizeContextKey`; only the download **host** is logged, never the presigned URL. Content-type gate is `backup.IsValidBackupContentType` (gzip family, tar **and** `application/octet-stream` via the `none` adapter; an empty `Content-Type` is accepted) instead of upstream's gzip-only check — the real format is sniffed afterwards. `deleteServerBackup` falls back to removing a leftover local S3 archive (`NewS3(client, id, "").Path()`) when `LocateLocal` misses. |
| `router/router_server_files.go` | Fork activity logging on the Panel-API file handlers: rename/delete/write/create-directory log upstream's `server:sftp.*` events (write picks `create` vs `write` from a `Stat` before the write); pull/compress/decompress/chmod log the fork-new `server:file.{downloaded,compressed,decompressed,chmod}`. All via `NewRequestActivity("", c.ClientIP())` (empty user). `postServerPullRemoteFile` calls `dl.EnableEvents()` for non-foreground pulls, and its 202 adds `filename`, `directory`, `url`. Upstream's v1.13.2 `token.Denylisted()` upload guard sits in the same file (§4). |
| `router/router_server_transfer.go` *(v1.13.4)* | Fork 409 guards (`IsBackingUp()` / `IsRestoring()`) run **before** upstream's new atomic `StartTransferring()` claim. Order matters: after it, a rejected request would leave `transferring=true` stuck. Fork patch in `server/install.go`: `StartTransferring()` also refuses while `IsBackingUp()`, so both router guards only add more specific messages. (Queued backups: see §3.) |
| `router/websocket/websocket.go` `SendJson` | Fork permission gates: `BackupCompletedEvent`, `BackupRestoreCompletedEvent` (prefix match) **and** `BackupProgressEvent` need `backup.read`; the fork-only `activity` event needs `activity.read` and `download progress` needs `file.read` (added in the v1.13.4 merge — before that they reached every websocket subscriber). Rewritten in v1.13.4 onto upstream's single `validJwt()` token; everything else (write lock + deadline, `SendThrottled`, `SendClose`, `OnAuthenticated`) is upstream. |
| `router/downloader/downloader.go` | Fork redirect-following loop (upstream follows **no** redirects; the fork follows up to 10 hops, each through the SSRF-checked dialer). A missing `Content-Length` (chunked) is allowed where upstream rejects it; `maxDownloadSize` (15 GB) / `ErrFileTooLarge` is enforced for known sizes up front and for unknown sizes through `maxSizeReader`; unknown-size bodies are written with upstream's `Filesystem().WriteFrom` (quota per write, file removed on failure) because `Write` copies nothing for a size of -1. Strict `sanitizeFilename` allowlist on header/user/URL filenames (stricter than upstream: rejects spaces and dotfiles). `download progress` events are opt-in via `EnableEvents()` (background pulls only), throttled to 250 ms, with a final `completed`/`failed` event. *(v1.13.4)* Upstream's dialer/`RemoteDownloadAllowlist` SSRF hardening is taken; its `%w` error wrapping is grafted into the fork loop and the `Content-Disposition` name is reduced with `filepath.Base(filepath.Clean("/"+v))` before `sanitizeFilename`. |

### 1.3 Activity streaming over WebSocket (SFTP + HTTP actions)

| Path | What |
|------|------|
| `server/activity.go` | New events `server:file.{downloaded,compressed,decompressed,chmod}` (`ActivityFile*`, logged by `router/router_server_files.go`). `SaveActivity` also publishes `ActivityEvent` (`{event,user,metadata}`) in a goroutine with `recover`, so every activity logged through it reaches WS clients. |
| `sftp/event.go`, `sftp/handler.go`, `server/server.go` `PublishActivity()` | `EventPublisher` interface + `publisher` field on the SFTP event handler (`publisher: srv` in `NewHandler`). After the DB insert, `Log()` publishes `{event,user,metadata}` asynchronously (goroutine + `recover`) via `Server.PublishActivity` → `ActivityEvent` on the server event bus → WebSocket clients. The Panel itself still receives activity only through the DB/cron path. `PublishActivity` ignores its `event` argument. `handler.go` also carries upstream's v1.13.3 setstat hardening (§4). |
| `server/backup.go` `BackupWithRetry` | Once every retry has failed, publishes an `ActivityEvent` with a **different** payload: `{event:"backup_failed",timestamp,backup_id,server_id,error,total_attempts,message}`. |

> The fork's previous `SmartSecurityProtector` SFTP brute-force/IP-reputation system
> (and its `sftp.security.*` config) was **removed** — `sftp/server.go` now matches upstream
> (vanilla SFTP auth plus upstream v1.13.4's connection limits) apart from the module rename.

### 1.4 Repo config & docs

| Path | What |
|------|------|
| `.gitignore` | Fork-added `.claude-flow/`, `.hive-mind/`, `CLAUDE.md` (local AI-tooling files; never committed) and `/wings-*`, `/dist/` (local build artifacts). Upstream will never add these — keep on merge. |
| `.github/workflows/{release,binary,docker}.yaml` | **Fork-specific release pipeline — always keep ours.** Upstream releases by hand: a human pushes a `v*` tag, `release.yaml` cuts a draft, a human publishes it. We release automatically from `develop` instead, and the version is derived from the newest **upstream** tag that is an ancestor of `develop` — so our releases always carry the upstream version number. Fork-only releases between upstream versions append a fourth component (`v1.13.3.1`), cut by dispatching `release.yaml` with `fork_revision`; the automatic path never does this. Upstream's `release.yaml` has diverged beyond recognition; do not merge it (v1.13.3's `c57c519` CDN-manifest notification was deliberately dropped — it notifies pterodactyl's own CDN repo; upstream's `git config` bot identity is gone too, our pipeline makes no git commits). See the header comment in `release.yaml` for the full flow and recovery steps. Upstream action/toolchain bumps are still grafted by hand where they apply to our jobs (v1.13.4: Go 1.27.2 + `setup-go` v7 in `binary.yaml` — required since `go.mod` says `go 1.26` — `docker/login-action` v4 in `release.yaml`, `docker/build-push-action` v7 in `docker.yaml`). |
| `CHANGELOG.md` | **Fork-maintained.** Upstream's file stops at `v1.12.3`; all `v1.13.x` entries (incl. fork-only `v1.13.3.1`) are written by us and double as release notes (the release gate refuses to publish without the section). Write each new version's entry by hand from upstream's commits; on a conflict keep ours. |
| `README.md` ("Fork Features" block) | Fork-only summary of the backup/restore pipeline and WS events, between upstream's intro and "Pterodactyl v2 is coming soon". On merge keep our block plus the Installation section, take upstream's text elsewhere. |
| `BACKUP_PROGRESS_EVENTS.md`, `WEBSOCKET_EVENTS.md` | **Fork-only** frontend-contract docs for the WS events. Upstream has no such files. Update them whenever an event payload, permission or throttle changes. |

---

## 2. Hardcoded value divergences — confirm whether intentional

These are fork-chosen **hardcoded constants/literals in Go code** (not `config.yml` options — they never
appear in a generated `config.yml`); changing them per node needs a code change. Review on upgrade.

| Path | Fork value | Note |
|------|------------|------|
| `server/backup_operations.go` | `maxConcurrentBackups/Restores = 8`; stale-op cleanup ticker 5 min, op TTL 8 h | Fork-chosen capacity. |
| `router/router_server_backup.go`, `server/backup.go` | Backup timeout 6 h (router; `BackupWithContext` only without a deadline; per `BackupWithRetry` attempt; `Backup()`). Restore timeout 4 h, set only in the router. Retries: 2 (30 s, 120 s backoff). Upstream has none of these. | Fork-chosen timeouts. Retries cannot exceed the outer 6 h deadline. |
| `server/backup_progress.go` (+ `server/backup/backup_s3.go`, `router/downloader/downloader.go`) | 250 ms WS throttle; S3 80/20 archive/upload split; byte mode (total unknown) counts whole MB; S3 upload reader caps reads at 1 MB (`maxChunk`) | Sets the WS event rate / S3 percentage curve. The 250 ms value is duplicated in three files — change them together. |
| `server/backup/backup_s3.go` | per-part upload `Content-Type: application/octet-stream` (upstream `application/x-gzip`) | Fork choice. Verify the Panel/S3 presigned flow tolerates it. |

---

## 3. Known issues in our own fork code (tech debt)

Every open item below was **verified fork-only** against upstream **`v1.13.4`** — the code involved does
not exist upstream. These are bugs/concerns in our own additions; fix them rather than defend them on upgrade.

| Area | Concern (all fork-only) |
|------|---------|
| **Queued backup/restore guards** | `backingUp`/`restoring` are set only **after** the registry slot is acquired, and `Register()` blocks until then without making the op visible anywhere. So a backup/restore that waits in the queue (more than 8 concurrent on the node) passes every 409 guard (backup, restore, transfer, power) until it actually starts. Fix: a per-server pending counter set synchronously in the handler before the 202, released when the op ends. |

**Resolved in the v1.13.4 merge** (kept briefly as a reminder to re-check after merges that touch these files):

- **Node-wide backup wipe:** `CleanupForDestroy()` ran `cleanupBackupFiles()`, which deleted **every** `<36-char-id>.tar.gz/.tar/.gz` in the shared backup directory — all local backups of all servers on the node — on every server deletion, 409 duplicate create, duplicate/failed incoming transfer and boot-time duplicate. `cleanupBackupFiles` and the dead `CleanupBackupFilesForServer` are removed; local backups are only deleted by the Panel's per-backup delete, like upstream.
- **Duplicate instances cancelled the live server's operations:** `CancelAllForServer` moved out of `CleanupForDestroy()` into `router/router_server.go` `deleteServer`, so throwaway duplicate-create/transfer instances with the same UUID no longer cancel the live server's backups/restores.
- **Uncapped chunked downloads:** responses without `Content-Length` are now capped at `maxDownloadSize` (15 GB) via `maxSizeReader` (`ErrFileTooLarge`). They also used to land as an **empty file**: `Filesystem().Write` copies nothing for a size of -1, so they now go through `WriteFrom`. A failed write now also sets the outer `err`, so the final `download progress` event reports `failed` instead of `completed`.
- **Presigned URL in logs:** the backup restore logged the full presigned `download_url` (incl. S3 signature) at Info level; now only the host is logged.
- **Ungated WS events:** `activity` and `download progress` reached every websocket subscriber (file paths, pull URLs, `backup_failed` errors); `activity` now requires `activity.read`, `download progress` requires `file.read`.
- **Dead validation code:** `validateBackupContent` / `validateBackupIntegrity` (+ their count/hash helpers) were unreachable and are removed.
- **Committed binaries:** `wings-debug`, `wings-fixed`, `wings-test` and `dist/wings_test` (~153 MB) are untracked; `.gitignore` now has `/wings-*` and `/dist/`, and `binary.yaml` runs `mkdir -p dist` (the directory used to exist only because of the committed binary). The blobs stay in history until a rewrite.
- **`checksum_type` label:** the kept-backup/panel-notify-failure event now says `"sha256"` like all others.
- **Transfer during a running backup:** `StartTransferring()` now also refuses while `IsBackingUp()`, so the check is part of the atomic claim and not only the router guard.
- **S3 upload transport:** `newS3FileUploader` really forces HTTP/1.1 now (empty non-nil `TLSNextProto`; `ForceAttemptHTTP2: false` alone did not) and honours `Proxy: http.ProxyFromEnvironment` again.
- **`LocateLocal` `.tar` fallback:** `foundPath` moved from `LocalBackup` into `Backup`, so `Size()/Checksum()/Details()` read the found `<uuid>.tar` too (they used the `.tar.gz` path and failed). `PathForLocalBackup()` (no callers) removed. Covered by `server/backup/locate_local_test.go`.
- **Queued states crashed the router:** see the `environment/environment.go` row in §1.2.

---

## 4. NOT fork divergences — adopted from upstream (do **not** re-apply)

These show up around our customizations but are **upstream** code. Treating them as
fork changes risks duplicating or mis-merging them on the next upgrade.

| Path | Reality |
|------|---------|
| `internal/network/*` (`LimitListener`, address/allowlist helpers), SFTP connection limits in `sftp/server.go`, websocket write lock/deadline/throttling, `server/filesystem/ignore.go` (`ValidateIgnore`, ignore limits), `server/mounts.go` mount validation, `system/locker.go` `ErrLockerDestroyed`, `internal/cron/delivery.go`, `internal/ufs` link-containment work, `server/filesystem/compress.go` rewrite (and removal of `archiverext/`), `server/listeners.go`, `router/websocket/listeners.go`, `config/config.go` `PanelConfiguration` | **Upstream v1.13.4** hardening, module rename only. `sftp/server.go`, `server/listeners.go`, `router/websocket/listeners.go`, `server/filesystem/{compress,errors}.go` are byte-identical to upstream apart from imports — keep them that way. The ~60 new `*_test.go` files of v1.13.4 are upstream suites. (`internal/progress/progress.go` is the exception under `internal/` — fork-modified, see §1.2.) |
| `.github/workflows/lint.yaml`, `.golangci.yml` | **Upstream v1.13.4**, adopted unchanged: golangci-lint v2 (standard linters + `gosec`, `gofmt` formatter) on push/PR to `develop`. The fork adds **no** `exclusions.rules`; fork findings are fixed in code, and deliberate exceptions carry a targeted `//nolint:<linter> // <reason>` comment (diff `git grep -n nolint` against upstream to list them). Keep it that way: no disabling linters or the formatter for fork code. |
| `config/config.go` → `ResolveToken(remote bool)`; `remote/http.go` → `Client.SetCredentials()` + mutex-guarded credentials; `router/router_system.go` → token re-resolve/empty-token guard/credential rotation in `postUpdateConfiguration` | **Upstream v1.13.3** master-key-rotation cluster (`07ce5fe`, `392e52c`, `3e6c2c9`). Fork edit: module rename only. |
| `sftp/handler.go` → `setstatMode()` + `sftpAttributeExtended` rejection | **Upstream v1.13.3** (`da1a216`) hardening. The fork's `publisher` wiring in `NewHandler` sits in the same file — both must survive a merge. |
| `environment/docker/cgroup_burst.go` (+ test), `applyCpuBurst`/`clearCpuBurst`/`SetCpuBurst` call sites in `container.go`/`power.go`/`server/install.go`, `config_docker.go` → `CpuPeriod`/`CpuBurst`/`CpuShares`, `environment/settings.go` quota math | **Upstream v1.13.3** CPU allocation work (`2cc8a10`, `6987d5e`). Module rename only. |
| `config/config_token_test.go`, `remote/http_test.go`, `router/router_system_test.go`, `environment/docker/cgroup_burst_test.go`, `config/config_docker_test.go`, upstream additions in `sftp/handler_test.go` | **Upstream v1.13.3** suites, module rename only. |
| `router/tokens/websocket.go` → `isDenylisted()`, and `Denylisted()` on `FilePayload` / `BackupPayload` / `UploadPayload` (+ their `user_uuid` claim) | **Upstream v1.13.2** (`28af6dd`). Revocation checking applied to the backup-download, file-download and file-upload one-time tokens; `Before(t)` → `!After(t)`. All four files are byte-identical to upstream — **keep them that way**. |
| `router/tokens/denylist_test.go` | **Upstream v1.13.2**, unmodified. |
| `router/router_download.go`, `router/router_server_files.go` → the `token.Denylisted() \|\|` guards | **Upstream v1.13.2** call sites: 2× `router_download.go` (backup + file download; the file is upstream apart from the module rename) and 1× `router_server_files.go` `postServerUploadFiles` (that file is fork-modified, §1.2 — the guard most likely to get lost in a conflict). Check all three survive. |
| `router/router_server_backup.go` SSRF cluster — `backupRestoreHttpClient`, `validateBackupDownloadUrl`, `parseBackupUuid`, `isBlockedBackupRestoreIP`, `isAllowedBackupRestoreDestination`, `isSupportedBackupRestoreContentType`, `backupDownloadError` | **Upstream v1.13.1** backup-restore SSRF hardening; since **v1.13.4** the IP checks delegate to `internal/network` (`IsInternal` / `IsAllowed`) and `blockedBackupRestorePrefixes` / `isExplicitlyBlockedBackupRestoreIP` are gone upstream — do not resurrect them. The **only** fork edit in this cluster: the restore handler calls `backup.IsValidBackupContentType` instead of `isSupportedBackupRestoreContentType` (retained for upstream parity + its test). |
| `config/config.go` → `Backups.RestoreHostAllowlist` | **Upstream v1.13.1.** Not a fork field. |
| `server/backup/backup.go` → `validateIdentifier()` / `normalizedIdentifier()` (+ `Path()` `path.Base` fallback) | **Upstream v1.13.1** UUID hardening — not fork code. `normalizedIdentifier()` and the `Path()` fallback are unchanged; `validateIdentifier()` carries the small fork race fix documented in §1.2 *(v1.13.4)* — keep it on merge. |
| `system/const.go` | Byte-identical to upstream (`Version = "develop"`). |
| `.github/FUNDING.yaml` (`github: [pterodactyl]`) | **Upstream default, unchanged** (`git diff v1.13.4 HEAD -- .github/FUNDING.yaml` is empty). Stale for a fork but NOT our change. |

> ⚠️ **Panel coupling introduced by v1.13.2.** `isDenylisted()` **fails closed**: a token with no
> `iat`, no `server_uuid` or no `user_uuid` is rejected outright. Backup downloads, file downloads and
> file uploads only work against a Panel that puts `user_uuid` into those JWTs; against an older Panel
> they return `404`. Deploy Panel **before** Wings.
>
> ⚠️ **Panel coupling introduced by v1.13.4.** `postUpdateConfiguration` only applies the
> `PanelConfiguration` fields (debug, uuid, token id/token, API host/port/SSL/upload limit, SFTP port);
> anything else the Panel sends is ignored and stays under the node operator's control.

---

## 5. Upgrade merge checklist

1. `git fetch upstream --tags`, then `git merge <upstream-tag>` on an `upgrade-<version>` branch; resolve per §1 (backup/archiver areas: keep ours, graft upstream fixes).
2. Rename leaked imports: `github.com/pterodactyl/wings` → `github.com/Rene-Roscher/wings` in every `.go` file (new upstream files arrive with the old path). Verify with `git grep -n 'github.com/pterodactyl/wings' -- '*.go' go.mod` — must print nothing. (A looser `pterodactyl/wings` grep also hits the log path `/var/log/pterodactyl/wings.log` in `cmd/diagnostics.go`; that stays.)
3. **Run `gofmt -w` (or `goimports -w`) on the renamed files.** The rename changes import sort order (`github.com/Rene-Roscher/...` sorts before `github.com/apex`/`gin-gonic`/...). On Windows, check against an LF export of the index (`git -c core.autocrlf=false checkout-index -a --prefix=<tmp>/` then `gofmt -l <tmp>`), since the CRLF working tree reports every file.
4. `GOOS=linux go build ./...` and `GOOS=linux go vet ./...` (both work from Windows); `go test -race ./...` must run on Linux — a Linux host, WSL, or a `golang:<version>` Docker container with the repo mounted.
5. If the merge touched `environment/docker/environment.go`, `environment/environment.go` or `router/router_server_backup.go`: run `go test ./environment/docker -run SetState` and check by eye that in all three `Register()` call sites the `registry.Complete`/`cancel` `defer` comes before `if wasQueued { … SetState … }`, and that both restore goroutines open with a top-level `recover`.
6. `make lint` (= `GOOS=linux golangci-lint run ./...`) with the version pinned in `lint.yaml` (currently v2.11.4) must report 0 issues **before** opening the upgrade PR. Always lint with `GOOS=linux`: a bare run on Windows/macOS skips the `_linux`/`_unix` files CI checks.
7. Update this file (baseline tag; §4 for adopted upstream code; remove fixed items from §3, moving any lasting invariant into §1) and write the `CHANGELOG.md` section. Re-verify every "byte-identical" / "unchanged" / "module rename only" claim in §4 against the new tag — for each path this must print nothing:
   `git diff --ignore-cr-at-eol <upstream-tag> HEAD -- <path> | grep -E '^[+-]' | grep -vE '^(\+\+\+|---) ' | grep -vE 'Rene-Roscher|pterodactyl/wings'`
