# EmeraldHost Wings — Divergences from Upstream Pterodactyl Wings

This file tracks **which changes are our own** (EmeraldHost-specific) versus upstream
[`pterodactyl/wings`](https://github.com/pterodactyl/wings). Use it during upgrades so
our customizations are **not accidentally reverted** when pulling in upstream changes.

- **Baseline for this comparison:** upstream tag **`v1.13.4`** (`747bc43`)
- **Last reviewed:** 2026-10-14
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
> operation registry/queue, retry, WebSocket progress and a
> heavily customized restore path. Upstream merges in `server/backup*`, `router/router_server_backup.go`,
> `server/server.go` and `sftp/server.go` will almost always conflict — resolve by **keeping ours**
> and grafting upstream's functional/security changes on top (that is exactly how v1.13.1 was merged).
>
> v1.13.2 and v1.13.3 were exceptions: both stayed clear of the backup subsystem and merged
> (nearly) conflict-free — see §4. The v1.13.3 conflicts were only our rewritten
> `release.yaml` (keep ours, see §1.4) and the module-renamed import block of `server/install.go`.
>
> v1.13.4 was the rule again: a broad hardening release (153 files) with 24 conflicted files,
> including the whole backup/restore/archive path. It was merged the v1.13.1 way — keep ours,
> graft upstream. Rows marked *(v1.13.4)* in §1 record where fork and upstream code now
> interlock; §4 lists what v1.13.4 brought in as plain upstream code.

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
> The fork replaced it with a queued, cancellable, progress-reporting pipeline.

**Operation registry, queue, retry (server package)**

| Path | What |
|------|------|
| `server/backup_operations.go` | **Fork-new.** `BackupOperationRegistry` + global `GetBackupOperationRegistry()`: concurrency-limited (`maxConcurrentBackups/Restores = 8`), queueing, cancellation. `Register()` returns the 5-tuple `(*op, ctx, cancel, err, wasQueued)` consumed by the router. `Cancel/Complete/CleanupStaleOperations` block-receive the semaphore token to release slots. Background `StartBackupOperationCleanup`. |
| `server/backup.go` | `Backup()`/`RestoreBackup()` are now compat wrappers over `BackupWithContext`/`RestoreBackupWithContext` (context + 6h/4h timeouts, atomic state transitions). `BackupWithRetry(ctx,b,2)` with exponential backoff. Restore does compression auto-detection, progress, restore-stats and per-dir `MkdirAll`+`Chown`. On panel-notify failure for a **successful** backup it **does not** delete the archive (upstream does). |
| `server/server.go` | `Server.backingUp *AtomicBool` (+ init), `AtomicStateTransition` + `ApplyAtomicStateTransition()`, `CleanupForDestroy()` cancels in-flight ops + cleans orphaned backup files, `PublishActivity()`. |
| `server/install.go` | `IsBackingUp()` / `SetBackingUp()`. (Note: `IsInProtectedState()` was deliberately **not** extended with `backingUp`.) |
| `server/power.go`, `server/errors.go` | `HandlePowerAction()` also blocks while `IsBackingUp()`, returning new sentinel `ErrServerIsBackingUp`. |
| `environment/environment.go` (+ docker state whitelist) | New states `backup`, `restore`, `backup_queued`, `restore_queued`. The `switch` in `environment/docker/environment.go` `SetState()` must list **all four** (guarded by `environment/docker/state_test.go`); the queued states were only added to it in the v1.13.4 merge (see §3). |
| `router/router_server_backup.go` goroutines | The registry cleanup `defer` (`registry.Complete` + `cancel`) is registered **directly after** a successful `Register()`, before the `wasQueued` `SetState`; both restore goroutines also have a top-level `recover`. Keep that order on merge, otherwise a panic leaks a registry slot until the 8 h stale cleanup. |
| `server/backup.go` `DownloadSizeContextKey` | Typed context key used by the S3 restore path to pass the download size to `RestoreBackupWithContext` (replaced the old `"download_size"` string key). `RestoreBackupWithContext` asks `Environment.IsRunning()` instead of stopping the container when the state is `restore_queued`. |
| `server/backup.go` restore *(v1.13.4)* | `RestoreBackup(b, reader, truncate)` / `RestoreBackupWithContext(ctx, b, reader, truncate)` carry upstream's new `truncate` flag: `TruncateRootDirectory` runs **inside** the restore, after the server has stopped and after compression detection (an undetectable S3 stream no longer wipes files first); the router no longer truncates before locating the backup. Upstream's suspend/unsuspend toggling was dropped by upstream itself (it clobbered Panel suspensions); start protection is `SetRestoring(true)` **before** `WaitForStop` (also cancels SFTP sessions). `restoreEntry` merges both sides: fork's mode-preserving `MkdirAll`+`Chown`+`Chtimes` for directories, upstream's symlink recreation and skipping of hard links/devices/special files. |
| `server/backup/backup.go` *(v1.13.4)* | Fork patch on upstream code: `validateIdentifier()` only writes `b.Uuid` when it changes, and `Details()` validates once before spawning its goroutines — a `-race` failure surfaced by upstream's new restore test, because the fork's restore calls `Details()`. |
| `server/backup/backup_s3.go` *(v1.13.4)* | Upstream's unconditional `defer s.Remove()` was **not** taken (it would delete failed uploads the fork keeps for retry); upstream's error-checked removal is grafted into the success branch only. |
| `server/backup_restore_test.go` *(v1.13.4)* | Upstream suite; only its `restoreTestEnvironment` double gained no-op `SetState()` / `IsRunning()` because the fork's restore drives environment state. Assertions unchanged. |

**Progress & activity over WebSocket**

| Path | What |
|------|------|
| `server/backup_progress.go` | **Fork-new.** `BackupProgressUpdate` WS payload `{backup_id,type,percentage,bytes_written,bytes_total}`, throttled tracker, S3 80/20 archive-vs-upload split. |
| `server/backup/download_progress.go` | **Fork-new.** `DownloadProgressReader` / `NewDownloadProgressReader` — S3 restore download progress. |
| `server/events.go` | New `BackupProgressEvent`, `DownloadProgressEvent`, `ActivityEvent`. **Frontend contract.** |
| `server/activity.go` | New `ActivityFile{Downloaded,Compressed,Decompressed,Chmod}`; `SaveActivity` also publishes `ActivityEvent` over WS. |

**Compression & checksums**

| Path | What |
|------|------|
| `server/backup/backup.go` | **SHA-256** checksums + `ChecksumType: "sha256"` (upstream uses **sha1**). ⚠️ **Protocol-facing** — a careless merge reverts to sha1 and breaks checksum compatibility with our Panel. `PathForLocalBackup()` helper. |
| `server/backup/compression.go` | **Fork-new.** `CompressionRegistry` (gzip/tar/none) + `IsValidBackupContentType()` — used by the router content-type gate; without it the router won't compile. |
| `server/backup/backup_local.go` | `foundPath` + extension-probing `LocateLocal` (`.tar.gz/.tar`), auto-detecting `Restore`, `CleanupBackupFilesForServer`. |
| `server/backup/backup_s3.go` | Two-phase backup reuse, success-flag cleanup (failed uploads kept for retry), orphaned-part logging, upload progress (`ProgressReader`/`ProgressTracker`), custom HTTP/1.1 transport, part-retry with 100MB memory-buffer threshold / 5GB cap, `Restore()` expects an **already-decompressed** tar stream. |
| `server/filesystem/archive.go` | Archiver no longer skips directory entries → **empty directories are preserved** in archives. `createCompressor()` refactor. *(v1.13.4)* Upstream's `compileIgnore()` + `ignoreMatchBudget`, `Readlinkat`-based symlink reading and `*[]byte` buffer pool are taken; the ignore compilation runs **before** `createCompressor()` so an invalid ignore list fails before anything is written. |
| `server/filesystem/archive_restore.go` | **Fork-new.** `DetectCompressionFormat` (magic bytes) + `CreateDecompressor`, wired into restore. |
| `server/filesystem/compress_binary_test.go`, `server/backup/*_test.go`, `router/content_type_test.go`, `server/backup_*_test.go` | Fork-new regression suites for the above. Keep them passing. |
| `server/filesystem/archive_test.go` + `archive_stream_test.go` | **File layout divergence, not new behavior.** Our `archive_test.go` was replaced wholesale with tests for the fork-only `archive_restore.go`; upstream's `TestArchive_Stream` lives in `archive_stream_test.go` instead. Upstream edits to `archive_test.go` therefore land in the *wrong* file on merge — port them into `archive_stream_test.go` by hand. *(v1.13.4: upstream's two new ignore-pattern cases were ported there; `getFiles` now `continue`s on directory entries instead of returning, because of the empty-dir preservation above.)* |

**Router API (fork-only endpoints + customized handlers)**

| Path | What |
|------|------|
| `router/router.go` | Fork-only routes `GET /backup/operations` and `DELETE /backup/:backup/cancel`. |
| `router/router_server_backup.go` | `cancelServerBackup` + `getServerBackupOperations` (fork-only). `postServerBackup` / `postServerRestoreBackup` rewritten: 409 concurrency guards, registry queueing, timeouts, panic recovery, retry, S3 download progress, and content-type via `backup.IsValidBackupContentType` (gzip+tar) instead of upstream's gzip-only check. |
| `router/router_server_transfer.go` *(v1.13.4)* | Fork 409 guards (`IsBackingUp()` / `IsRestoring()`) run **before** upstream's new atomic `StartTransferring()` claim. Order matters: after it, a rejected request would leave `transferring=true` stuck. (Gap, pre-existing: `StartTransferring()` itself does not check `IsBackingUp()`.) |
| `router/websocket/websocket.go` `SendJson` | Fork-wider `backup.read` gate: `BackupCompletedEvent`, `BackupRestoreCompletedEvent` (prefix match) **and** `BackupProgressEvent`. Rewritten in v1.13.4 onto upstream's single `validJwt()` token; everything else (write lock + deadline, `SendThrottled`, `SendClose`, `OnAuthenticated`) is upstream. `DownloadProgressEvent` / `ActivityEvent` are forwarded without a permission check (fork behaviour, unchanged). |
| `router/downloader/downloader.go` | Fork redirect-following loop (max 10 hops, each through the SSRF-checked dialer), `maxDownloadSize` (15 GB) / `ErrFileTooLarge`, strict `sanitizeFilename` allowlist on header/user/URL filenames (stricter than upstream: rejects spaces and dotfiles), WebSocket progress events. *(v1.13.4)* Upstream's dialer/`RemoteDownloadAllowlist` SSRF hardening is taken; its `%w` error wrapping is grafted into the fork loop and the `Content-Disposition` name is reduced with `filepath.Base(filepath.Clean("/"+v))` before `sanitizeFilename`. |

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
| `Makefile`, `Dockerfile` | Our build settings (with the renamed module path). |
| `.github/workflows/{release,binary,docker}.yaml` | **Fork-specific release pipeline — always keep ours.** Upstream releases by hand: a human pushes a `v*` tag, `release.yaml` cuts a draft, a human publishes it. We release automatically from `develop` instead, and the version is derived from the newest **upstream** tag that is an ancestor of `develop` — so our releases always carry the upstream version number. Fork-only releases between upstream versions append a fourth component (`v1.13.3.1`), cut by dispatching `release.yaml` with `fork_revision`; the automatic path never does this. Upstream's `release.yaml` has diverged beyond recognition; do not merge it (v1.13.3's `c57c519` CDN-manifest notification was deliberately dropped — it notifies pterodactyl's own CDN repo). See the header comment in `release.yaml` for the full flow and recovery steps. Upstream action/toolchain bumps are still grafted by hand where they apply to our jobs (v1.13.4: Go 1.27.2 + `setup-go` v7 in `binary.yaml` — required since `go.mod` now says `go 1.26` — `docker/login-action` v4 in `release.yaml`, `docker/build-push-action` v7 in `docker.yaml`). |

---

## 2. Config divergences — confirm whether intentional

These sit on **different** values/fields than upstream; they will re-appear in a generated
`config.yml`. Review on upgrade.

| Path | Fork value / field | Note |
|------|--------------------|------|
| `server/backup_operations.go` | `maxConcurrentBackups/Restores = 8`; cleanup ticker 5 min / op TTL 8 h; backup 6 h / restore 4 h timeouts | Fork-chosen capacity/timeouts. |
| `server/backup_progress.go` | 250 ms throttle; S3 80/20 split; 1 MB chunking | Determines WS emission rate / S3 percentage curve. |
| `server/backup/backup_s3.go` | per-part upload `Content-Type: application/octet-stream` (upstream `application/x-gzip`) | Fork choice. Verify the Panel/S3 presigned flow tolerates it. |

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
| **Queued states crashed the router (fixed in the v1.13.4 merge)** | Up to v1.13.3 the docker `SetState()` whitelist did not contain `backup_queued`/`restore_queued`, so every backup/restore that had to wait for a registry slot panicked in the router goroutine (latent crash; restores had no `recover` at all, so a queued restore crashed Wings) and leaked its registry slot. Fixed during the v1.13.4 merge: whitelist extended, cleanup `defer` moved before the queued `SetState`, top-level `recover` added to both restore goroutines, and the restore stop-wait no longer tries to stop an offline container just because the state is `restore_queued`. Kept here as a reminder to re-check after merges that touch these files. |
| **`validateBackupContent`** | Fails a backup on any server-vs-archive file/dir count mismatch (can race a live server writing files), and computes a full SHA-256 over the **entire server tree and backup file** purely for a debug log line (perf cost on large servers). |

---

## 4. NOT fork divergences — adopted from upstream (do **not** re-apply)

These show up around our customizations but are **upstream** code. Treating them as
fork changes risks duplicating or mis-merging them on the next upgrade.

| Path | Reality |
|------|---------|
| `config/config.go` → `ResolveToken(remote bool)`; `remote/http.go` → `Client.SetCredentials()` + mutex-guarded credentials; `router/router_system.go` → token re-resolve/empty-token guard/credential rotation in `postUpdateConfiguration` | **Upstream v1.13.3** master-key-rotation cluster (`07ce5fe`, `392e52c`, `3e6c2c9`): Panel-sent master key resets now propagate to the running daemon, with remote token values barred from `file://`/`$VAR` indirection and checked against `WINGS_TOKEN_ID`/`WINGS_TOKEN` overrides. Fork edit: module rename only. |
| `sftp/handler.go` → `setstatMode()` + `sftpAttributeExtended` rejection | **Upstream v1.13.3** (`da1a216`) hardening against unbounded allocations from crafted setstat packets. The fork's `publisher` wiring in `NewHandler` sits in the same file — both must survive a merge. |
| `environment/docker/cgroup_burst.go` (+ test), `applyCpuBurst`/`clearCpuBurst`/`SetCpuBurst` call sites in `container.go`/`power.go`/`server/install.go`, `config_docker.go` → `CpuPeriod`/`CpuBurst`/`CpuShares`, `environment/settings.go` quota math | **Upstream v1.13.3** CPU allocation work (`2cc8a10`, `6987d5e`). Not fork code — only the imports in the new files were renamed to `Rene-Roscher` (they arrive `pterodactyl` on every upstream merge; grep for leaks). |
| `config/config_token_test.go`, `remote/http_test.go`, `router/router_system_test.go`, `environment/docker/cgroup_burst_test.go`, `config/config_docker_test.go`, upstream additions in `sftp/handler_test.go` | **Upstream v1.13.3** suites, module rename only. Not fork suites. |
| `router/tokens/websocket.go` → `isDenylisted()`, and `Denylisted()` on `FilePayload` / `BackupPayload` / `UploadPayload` (+ their new `user_uuid` claim) | **Upstream v1.13.2** (`28af6dd`, "update token validation"). Revocation checking was extracted out of `WebsocketPayload.Denylisted()` into a shared `isDenylisted()` and applied to the backup-download, file-download and file-upload one-time tokens, which previously only checked `IsUniqueRequest()`/scope. Also tightened `Before(t)` → `!After(t)`, so a token issued in the same second as the revocation is now denied. All four files are byte-identical to upstream — **keep them that way**. |
| `router/tokens/denylist_test.go` | **Upstream v1.13.2**, unmodified. Covers the four payload types above. Not a fork suite. |
| `router/router_download.go`, `router/router_server_files.go` → the `token.Denylisted() \|\|` guards | **Upstream v1.13.2** call sites. The surrounding files *are* fork-modified (module rename + activity logging), so these three one-liners are easy to lose in a conflict resolution — check they survive. |
| `router/router_server_backup.go` SSRF cluster — `backupRestoreHttpClient`, `validateBackupDownloadUrl`, `parseBackupUuid`, `isBlockedBackupRestoreIP`, `isAllowedBackupRestoreDestination`, `isSupportedBackupRestoreContentType`, `backupDownloadError` | **Upstream v1.13.1** backup-restore SSRF hardening; since **v1.13.4** the IP checks delegate to `internal/network` (`IsInternal` / `IsAllowed`) and `blockedBackupRestorePrefixes` / `isExplicitlyBlockedBackupRestoreIP` are gone upstream — do not resurrect them. The **only** fork edit in this cluster: the restore handler calls `backup.IsValidBackupContentType` instead of `isSupportedBackupRestoreContentType` (the latter is retained only for upstream parity + its test). |
| `config/config.go` → `Backups.RestoreHostAllowlist` | **Upstream v1.13.1.** Pairs with the SSRF allowlist above. Not a fork field. |
| `server/backup/backup.go` → `validateIdentifier()` / `normalizedIdentifier()` (+ `Path()` `path.Base` fallback) | **Upstream v1.13.1** UUID hardening. The fork uses them unchanged. |
| `system/const.go` | Byte-identical to upstream (`Version = "develop"`). |
| `.github/FUNDING.yaml` (`github: [pterodactyl]`) | **Upstream default, unchanged** (`git diff 28af6dd HEAD` is empty). Stale for a fork (sponsorship points at upstream) but NOT our change — clean it up if desired, don't track it as a divergence. |
| `.github/workflows/release.yaml` release-bot identity (`ci@pterodactyl.io` / `Pterodactyl CI`) | **Upstream default, unchanged.** Upstream's release.yaml already sets this identity. Not our divergence. |
| `internal/network/*` (`LimitListener`, address/allowlist helpers), SFTP connection limits in `sftp/server.go`, websocket write lock/deadline/throttling, `server/filesystem/ignore.go` (`ValidateIgnore`, ignore limits), `server/mounts.go` mount validation, `system/locker.go` `ErrLockerDestroyed`, `internal/cron/delivery.go`, `internal/ufs` link-containment work, `server/filesystem/compress.go` rewrite (and removal of `archiverext/`), `server/listeners.go`, `router/websocket/listeners.go` | **Upstream v1.13.4** hardening, module rename only. `sftp/server.go`, `server/listeners.go`, `router/websocket/listeners.go`, `server/filesystem/{compress,errors}.go` are byte-identical to upstream apart from imports — keep them that way. The ~60 new `*_test.go` files of v1.13.4 are upstream suites, not fork suites. |
| `wings.go` | Upstream v1.13.4 dropped the deprecated `rand.Seed`; the file is upstream apart from the import path. |
| `.github/workflows/lint.yaml`, `.golangci.yml` | **Upstream v1.13.4**, adopted unchanged: golangci-lint v2 (standard linters + `gosec`, `gofmt` formatter) on push/PR to `develop`. The fork adds **no** `exclusions.rules`; fork findings were fixed in code, and the few deliberate ones carry a targeted `//nolint:gosec // <reason>` comment (`server/backup.go` `os.Open` of wings-built backup paths, `server/backup_progress.go` `context.WithCancel` whose cancel is stored on the tracker). Keep it that way: no disabling linters or the formatter for fork code. |

> ⚠️ **Panel coupling introduced by v1.13.2.** `isDenylisted()` **fails closed**: a token with no
> `iat`, no `server_uuid` or no `user_uuid` is rejected outright. The `user_uuid` claim is new in
> v1.13.2, so backup downloads, file downloads and file uploads only work against a Panel that
> puts `user_uuid` into those JWTs. Against an older Panel every such request returns
> `404 "The requested resource was not found on this server."` — deploy Panel **before** Wings,
> and if downloads/uploads start 404-ing after a Wings upgrade, this is the first thing to check.

---

## 5. Upgrade merge checklist

1. `git merge <upstream-tag>` on an `upgrade-<version>` branch; resolve per §1 (backup/archiver areas: keep ours, graft upstream fixes).
2. Rename leaked imports: `github.com/pterodactyl/wings` → `github.com/Rene-Roscher/wings` in every `.go` file (new upstream files arrive with the old path).
3. **Run `gofmt -w` (or `goimports -w`) on the renamed files.** The rename changes import sort order (`github.com/Rene-Roscher/...` sorts before `github.com/apex`/`gin-gonic`/...), so files that upstream left gofmt-clean are no longer clean after the sed. On Windows, check against an LF export of the index (`git -c core.autocrlf=false checkout-index -a --prefix=<tmp>/` then `gofmt -l <tmp>`), since the CRLF working tree reports every file.
4. `go build ./...`, `go vet ./...`, `go test -race ./...` (Linux / `GOOS=linux`).
5. `golangci-lint run ./...` with the version pinned in `lint.yaml` (currently v2.11.4) must report 0 issues **before** opening the upgrade PR, otherwise the Lint workflow fails on the PR and on `develop`.
6. Update this file (baseline tag, §4 for adopted upstream code, §3 for anything fixed) and the changelog.
