<p align="center">
  <a href="https://pterodactyl.io">
    <picture>
      <source media="(prefers-color-scheme: dark)" srcset=".github/assets/logo-dark.svg">
      <img alt="Pterodactyl" src=".github/assets/logo-light.svg" height="72">
    </picture>
  </a>
</p>

![Discord](https://img.shields.io/discord/122900397965705216?label=Discord&logo=Discord&logoColor=white)
![GitHub Releases](https://img.shields.io/github/downloads/pterodactyl/wings/latest/total)
[![Go Report Card](https://goreportcard.com/badge/github.com/pterodactyl/wings)](https://goreportcard.com/report/github.com/pterodactyl/wings)

# Pterodactyl Wings

Wings is Pterodactyl's server control plane, built for the rapidly changing gaming industry and designed to be
highly performant and secure. Wings provides an HTTP API allowing you to interface directly with running server
instances, fetch server logs, generate backups, and control all aspects of the server lifecycle.

In addition, Wings ships with a built-in SFTP server allowing your system to remain free of Pterodactyl specific
dependencies, and allowing users to authenticate with the same credentials they would normally use to access the Panel.

## Fork Features (emeraldhost/wings)

This fork extends Pterodactyl Wings with a queued, cancellable backup/restore pipeline, live progress over the
WebSocket and activity streaming. The full list of divergences from upstream is kept in
[`FORK_CHANGES.md`](FORK_CHANGES.md); the WebSocket payloads are documented in
[`WEBSOCKET_EVENTS.md`](WEBSOCKET_EVENTS.md) and [`BACKUP_PROGRESS_EVENTS.md`](BACKUP_PROGRESS_EVENTS.md).

| Feature | Upstream Wings | This fork |
|---------|----------------|-----------|
| **Backup/restore concurrency** | Unbounded goroutines | Operation registry: max 8 backups + 8 restores per node, the rest queued; cancellable (`DELETE /api/servers/:server/backup/:backup/cancel`, `GET /api/servers/:server/backup/operations`) |
| **Backup progress** | None | `backup progress` events (throttled to 250 ms); S3 backups report 0–80 % for the archive and 80–100 % for the upload |
| **Restore progress** | None | `backup progress` events for the restore, plus `type: "download"` progress while an S3 backup is downloaded |
| **Server states** | `offline`, `starting`, `running`, `stopping` | Adds `backup`, `restore`, `backup_queued`, `restore_queued`; power actions are blocked while a backup runs |
| **Backup checksums** | SHA-1 | SHA-256 (`checksum_type: "sha256"`) |
| **Backup formats** | gzip only | gzip and plain tar, detected by magic bytes on restore; empty directories are preserved |
| **Failed backups** | No retry | Up to two automatic retries (30 s, 120 s backoff); failed S3 uploads keep the local archive for the retry |
| **Remote file pulls** | No redirects, no progress | Follows up to 10 redirects (each SSRF-checked), 15 GB cap, `download progress` events |
| **Activity** | Stored for the Panel only | Additionally pushed live as `activity` WebSocket events (SFTP and HTTP file actions) |

### WebSocket events added by this fork
- `backup progress`: `{backup_id, type, percentage, bytes_written, bytes_total}` with `type` = `create` | `restore` | `download`
- `download progress`: progress of a remote file pull
- `activity`: file/SFTP activity (`{event, user, metadata}`)
- `status`: now also carries `backup`, `restore`, `backup_queued` and `restore_queued`

Upstream's `backup completed` and `backup restore completed` events are still sent when an operation finishes.

## Installation

This fork is a drop-in replacement for the original Wings. Simply replace your Wings binary with this version:

```bash
# Download the pre-built binary (linux amd64 / arm64)
curl -L -o /usr/local/bin/wings "https://github.com/emeraldhost/wings/releases/latest/download/wings_linux_$([[ "$(uname -m)" == "x86_64" ]] && echo "amd64" || echo "arm64")"
chmod u+x /usr/local/bin/wings

# Or build from source
go build -o wings .
```

## Pterodactyl v2 is coming soon

Wings is stable and remains the daemon for Pterodactyl: the same Wings is used with the upcoming Panel v2.

[![Watch: Introducing Pterodactyl v2](.github/assets/v2-video.jpg)](https://www.youtube.com/watch?v=1ER4kv0jVEU)

## Sponsors

I would like to extend my sincere thanks to the following sponsors for helping fund Pterodactyl's development.
[Interested in becoming a sponsor?](https://github.com/sponsors/pterodactyl)

| Company                                                                           | About                                                                                                                                                                                                                                           |
|-----------------------------------------------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| [**Buildurly**](https://buildurly.com/)                                           | Buildurly is a hardware procurement company. They deliver tailored, enterprise-grade hardware solutions designed around your unique needs. From sourcing to delivery, Buildurly's white-glove service ensures a seamless, worry-free, professional experience.                                                                                                                                          |
| [**Hosturly**](https://hosturly.com/)                                             | Hosturly is an enterprise hosting provider. They provide cost-effective, high-performance, and reliable services, including VPS, Web, Dedicated, and Colocation.                                                                                |
| [**indifferent broccoli**](https://indifferentbroccoli.com/)                      | indifferent broccoli is a game server hosting and rental company. With them, you get top-notch computer power for your gaming sessions. They destroy lag, latency, and complexity--letting you focus on the fun stuff.                         |
| [**Infraly, LLC**](https://infraly.co/)                                           | Infraly is an infrastructure company powering the next generation of online services. Through their brands, Infraly delivers cutting-edge solutions across multiple markets. Their vertically integrated approach provides unmatched performance, scalability, and reliability, giving our customers full control.                                                                                     |
| [**MineStrator**](https://minestrator.com/)                                       | MineStrator is a game server hosting provider. Looking for the most high-end French hosting company for your Minecraft server? More than 24,000 members on our Discord trust us. Give us a try!                                                |
| [**Physgun**](https://physgun.com/)                                               | Physgun is a game server hosting provider. Most providers rent rack space and rebrand a panel. At Physgun, they engineer the performance, write the features, and staff the support. Physgun truly is game hosting perfected!                   |
| [**WISP**](https://wisp.gg/)                                                      | WISP is an industry-leading SaaS platform for game server management, designed for hosting companies, gaming organizations, and enthusiasts. WISP combines modern, intuitive interfaces with powerful tools, making server deployment and administration seamless, scalable, and efficient.     

## Documentation

* [Panel Documentation](https://docs.pterodactyl.io/v1/panel/getting-started)
* [Wings Documentation](https://docs.pterodactyl.io/v1/wings/)
* [Community Guides](https://docs.pterodactyl.io/v1/guides/panel-installation/debian)
* Or, get additional help [via Discord](https://discord.gg/pterodactyl)

## Reporting Issues

Please use the [pterodactyl/panel](https://github.com/pterodactyl/panel) repository to report any issues or make
feature requests for Wings. In addition, the [security policy](https://github.com/pterodactyl/panel/security/policy) listed
within that repository also applies to Wings.
