---
layout: default
title: Privacy and security
nav_order: 7
---

# Privacy and security

## Mihomo controller

Mihomo runs from an argument list, never through a shell. On Linux and macOS
its controller is a Unix socket in a 0700 `ctl` directory, so no TCP port
opens. Mihomo makes that socket world-accessible and does not check the
secret on it; the directory is the access control. On Windows it listens on a
random loopback port.

Each start generates a random 32-byte secret, and the generated config sets
`allow-lan: false` and `127.0.0.1`. A non-loopback controller is refused. The
secret never crosses IPC.

## Local IPC

Linux and macOS use `$XDG_RUNTIME_DIR/clashpulse.sock` or
`$TMPDIR/clashpulse-<uid>/ipc.sock`, only if its directory is exactly 0700.
Connections from other users are rejected (`SO_PEERCRED` on Linux, peer
credentials on macOS). Windows uses a named pipe only the current user's SID
can open, and checks the pipe's owner process.

There is no TCP transport, and a client with another protocol version is
rejected. Snapshots carry a source's host, never its URL.

## Files

Snapshots, the generated config, cached resources, the controller socket, and
locks live in private state: 0700 directories, 0600 files. Config files
ClashPulse writes are 0600 in a 0700 directory. On Windows the user profile's
ACL does this. Keep `subscriptions.toml` 0600; its URLs are secrets.

A new config is written to a private temp file and checked with `mihomo -t`
first. If validation or restart fails, the previous config, selections, and
process come back. Changing the Mihomo binary follows the same rule.

## System proxy

The OS proxy only ever points at a loopback listener that already accepts
connections, and a failed apply rolls back. Turning it off on Linux (GNOME)
resets the keys ClashPulse wrote to their defaults, losing earlier values. On
macOS and Windows it restores the HTTP and HTTPS settings saved before.

## What is shown

- Node names have hostname-shaped text replaced with `[redacted]`. Names with
  `@`, non-printable characters, or over 80 characters become `Proxy 2` and
  so on.
- There is no log file, and Mihomo's console output is discarded.
- The activity view holds this session's fixed messages; IPC rejects any
  containing `://`.
- Download failures show fixed labels such as `subscription HTTP 403`.
- `clashpulse activate` errors never show the profile path.
- Response bodies stay in the service; `--show-response` prints up to 4 KiB
  locally and is refused while a service runs.
- Generated config changes shows field names and reasons only.

## Network access

No telemetry or crash reporting. ClashPulse itself only requests:

- the subscription and resource URLs you configure;
- the Mihomo controller, including latency probes to `test_url`;
- `stream_url` through Mihomo's private loopback listener (Steady stream).

The default `test_url` is plain HTTP and can be intercepted; use HTTPS if
probe integrity matters.
