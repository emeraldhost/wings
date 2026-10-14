# Wings WebSocket Events Reference

This document provides a complete reference for all WebSocket events emitted by Pterodactyl Wings.

## Event Overview

All events are sent via WebSocket in the following format:
```json
{
  "event": "event_name", 
  "args": [payload]
}
```

## Server Events

### 🏃 **Runtime & Process Events**

#### `status`
Server status changes (starting, running, stopping, offline).
```json
{
  "event": "status",
  "args": ["running"]
}
```
**Values**: `offline`, `starting`, `running`, `stopping`

#### `stats` 
Server resource usage statistics.
```json
{
  "event": "stats",
  "args": [{
    "memory_bytes": 1073741824,
    "memory_limit_bytes": 2147483648,
    "cpu_absolute": 45.5,
    "network": {
      "rx_bytes": 1024,
      "tx_bytes": 2048
    },
    "uptime": 3600000,
    "state": "running"
  }]
}
```

#### `console output`
Real-time console output from the server process.
```json
{
  "event": "console output",
  "args": ["[10:30:15] [Server thread/INFO]: Player joined the game"]
}
```

#### `daemon message`
System messages from Wings daemon.
```json
{
  "event": "daemon message", 
  "args": ["Server marked as starting..."]
}
```

### 📦 **Installation Events**

#### `install started`
Server installation process has begun.
```json
{
  "event": "install started",
  "args": [true]
}
```

#### `install output`
Real-time output from installation process.
```json
{
  "event": "install output",
  "args": ["Downloading server files..."]
}
```

#### `install completed`
Installation process finished.
```json
{
  "event": "install completed",
  "args": [true]
}
```

### 💾 **Backup Events**

#### `backup progress` ⭐ *New*
Real-time backup creation/restoration progress.
```json
{
  "event": "backup progress",
  "args": [{
    "backup_id": "47363ce7-d70a-430e-8e75-6dc87c8d016d",
    "type": "create",
    "percentage": 45,
    "bytes_written": 471859200,
    "bytes_total": 1048576000
  }]
}
```

**Fields:**
- `backup_id`: UUID of the backup
- `type`: `"create"`, `"restore"`, or `"download"` (S3 backup being downloaded for a restore)
- `percentage`: 0-100, or -1 for error
- `bytes_written`: Bytes processed (omitted when 0)
- `bytes_total`: Total bytes (omitted when unknown)

Requires the `backup.read` permission (same gate as `backup completed` / `backup restore completed`).

#### `download progress` ⭐ *New*
Progress of a remote file pull (`POST /api/servers/:server/files/pull` with `foreground: false`).
```json
{
  "event": "download progress",
  "args": [{
    "identifier": "download-uuid",
    "filename": "world.zip",
    "directory": "/",
    "url": "https://example.com/world.zip",
    "percentage": 45,
    "bytes_written": 471859200,
    "bytes_total": 1048576000,
    "status": "downloading"
  }]
}
```

- `status`: `"downloading"`, `"completed"` (percentage 100) or `"failed"` (percentage -1); a final event is always sent
- Throttled to 250 ms; `bytes_total` is 0 when the size is unknown
- Requires the `file.read` permission

#### `backup completed`
Backup creation finished.
```json
{
  "event": "backup completed",
  "args": [{
    "uuid": "backup-uuid",
    "is_successful": true,
    "checksum": "sha256-hash",
    "checksum_type": "sha256",
    "file_size": 1048576000
  }]
}
```

#### `backup restore completed`
Backup restoration finished.
```json
{
  "event": "backup restore completed",
  "args": [true]
}
```

### 📁 **Activity Events** ⭐ *Enhanced*

#### `activity`
File operations and server activities, pushed live in addition to being stored for the Panel. Requires the `activity.read` permission.
```json
{
  "event": "activity",
  "args": [{
    "event": "server:sftp.write",
    "user": "user-uuid",
    "metadata": {
      "file": "config/server.properties"
    }
  }]
}
```

**Common Activity Types:**
- `server:sftp.create` / `server:sftp.write` - File created / modified (SFTP or the file API)
- `server:sftp.delete` - File deleted
- `server:sftp.rename` - File renamed
- `server:sftp.create-directory` - Directory created
- `server:file.downloaded` - Remote file pulled
- `server:file.compressed` / `server:file.decompressed` - Archive created / extracted
- `server:file.chmod` - Permissions changed
- `server:console.command` - Console command executed
- `server:power.start` / `server:power.stop` - Power actions
- `backup_failed` - All backup retries failed (different payload: `{event, timestamp, backup_id, server_id, error, total_attempts, message}`)

### 🔄 **Transfer Events**

#### `transfer logs`
Server transfer process logs.
```json
{
  "event": "transfer logs",
  "args": ["Transferring server files..."]
}
```

#### `transfer status`
Transfer status updates.
```json
{
  "event": "transfer status", 
  "args": ["processing"]
}
```

### ❌ **Deletion Events**

#### `deleted`
Server has been deleted from the system.
```json
{
  "event": "deleted",
  "args": [null]
}
```

## Event Frequency & Performance

| Event Type | Frequency | Throttling |
|------------|-----------|------------|
| `status` | On state change | None |
| `stats` | Every 1-2 seconds | Built-in |
| `console output` | Real-time | None |
| `backup progress` | Max 4/second | 250 ms throttling (`download` type unthrottled) |
| `download progress` | Max 4/second | 250 ms throttling |
| `activity` | On action | None |
| `install output` | Real-time | None |
| `daemon message` | As needed | None |

## Authentication

WebSocket connections require JWT authentication:
```
wss://wings-host/api/servers/{server-id}/ws?token=jwt-token
```

## Error Handling

All events are sent asynchronously. If event publishing fails, server operations continue normally. Events are not queued or retried.

## Backwards Compatibility  

All events maintain backwards compatibility. New fields may be added but existing fields will not be removed or changed in structure.