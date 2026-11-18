---
layout: default
title: Privacy and security
nav_order: 7
---

# Privacy and security

ClashPulse handles subscription URLs, proxy credentials, and a Mihomo
controller secret. This page describes what the code does with them.

## Mihomo controller

- Mihomo runs as an explicit executable with an argument list; no shell is
  involved.
- On Linux and macOS the controller listens on a Unix socket in a 0700
  `ctl` directory inside the private state directory, so it opens no TCP
  port. Mihomo creates that socket world-accessible and does not check the
  secret on it; the 0700 directory is the access control.
- On Windows, where Mihomo has no Unix-socket controller, it listens on a
  random loopback port.
- Each service start generates a random 32-byte controller secret; the
  generated config also sets `allow-lan: false` and binds to `127.0.0.1`. A controller address
  that is not loopback is refused.
- The secret never crosses IPC: snapshots carry no controller fields.

## Local IPC

The GUI, TUI, and CLI reach the service over:

- **Linux and macOS**: a Unix socket, `$XDG_RUNTIME_DIR/clashpulse.sock` or
  `$TMPDIR/clashpulse-<uid>/ipc.sock`. The service refuses to listen unless
  the socket's directory is exactly 0700. Each connection is checked against
  the peer's user ID (`SO_PEERCRED` on Linux, peer credentials on macOS) and
  rejected when it is not the current user.
- **Windows**: a named pipe whose security descriptor grants access only to
  the current user's SID; the pipe's owner process is checked as well.

There is no TCP transport. The first frame negotiates the protocol
version, and a client with a different version is rejected.

Snapshots carry each subscription's and resource's source host, never its
URL. A URL crosses IPC only inside the edit command that sets it.

## Private state

Downloaded snapshots, the generated Mihomo configuration, cached resources,
the controller socket, and lock files live in private state, kept apart from
your source profiles: directories are created 0700 and files 0600. Config
files written by ClashPulse (seeded or edited from the GUI or TUI) are 0600
in a 0700 directory. Windows has no POSIX mode bits; there the user
profile's ACL keeps configuration private.

Subscription URLs are secrets. Keep `subscriptions.toml` 0600 and its
directory 0700.

## Transactional configuration

A candidate configuration is written to a private 0600 temporary file and
checked with the selected Mihomo (`-t`) before it replaces the running one.
If validation or the restart fails, the prior generated config, the prior
selections, and the running process are restored, and the error is reported.
A Mihomo binary change is held to the same rule: a binary that fails its
checks leaves the previous binary, the config file, and the running Mihomo
in place.

## System proxy

ClashPulse points the OS proxy only at a loopback listener that is already
accepting connections, and a failed apply rolls back its partial writes.
Turning it off differs by platform:

- **Linux (GNOME settings)**: resets the proxy keys ClashPulse wrote to their
  schema defaults. Earlier values are not preserved.
- **macOS and Windows**: restores the HTTP and HTTPS settings captured before
  ClashPulse applied its own.

## Node names

A provider's node name can carry the provider's domain. Group and node
labels sent to the GUI, TUI, and tray replace any hostname-shaped text with
`[redacted]`. A name containing `@` (a possible `user:password@host`), a
non-printable character, or more than 80 characters is shown as a
positional label such as `Proxy 2`.

## What is shown

- The service writes no log file, and Mihomo's console output is discarded.
- The activity view (GUI Overview > View activity, TUI `~`) holds the
  current session's diagnostics: fixed messages tied to a stable ID. The IPC
  layer rejects a diagnostic that contains `://`, so a URL cannot reach it.
- Subscription and resource failures are reported as fixed labels such as
  `subscription HTTP 403`, never the raw error text.
- `clashpulse activate` errors never include the profile path, and its
  private generated config is removed on shutdown.
- HTTP response bodies stay in the service. `--show-response` prints up to
  4 KiB of printable response text to local stderr and is refused while a
  service is running.
- **Generated config changes** names fields and reasons only: no source or
  generated values, credentials, endpoints, or URLs.

## Network access

ClashPulse has no telemetry or crash reporting. The only HTTP requests it
makes itself are:

- subscription and resource downloads, to the URLs you configure;
- controller calls to Mihomo over the private socket or loopback; latency
  probes go through Mihomo's delay endpoint to `test_url`;
- Steady stream downloads of `stream_url` through Mihomo's private loopback
  listener.

The default `test_url` is plain HTTP and can be intercepted. Latency is a
routing signal, never an integrity check; use an HTTPS `test_url` if that
matters.
