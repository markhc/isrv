# Running isrv as a Tor onion service

isrv is well suited to anonymity-focused deployments: the web UI makes no
third-party requests (no CDNs, web fonts, or analytics), it runs as a single
static binary with no external dependencies, and it ships a minimal-logging
mode that strips anything identifying from its logs. This guide runs isrv as a
v3 onion service so users reach it entirely over the Tor network.

## How it fits together

Tor runs the onion service and forwards the virtual onion port to isrv
listening on loopback. isrv itself never listens on the public internet.

```
Tor client ─> Tor network ─> onion service ─> 127.0.0.1:8080 (isrv)
```

## 1. isrv configuration

Four settings matter. In `config.yaml`:

```yaml
serverHost: 127.0.0.1              # bind to loopback ONLY - never expose on the clearnet
serverPort: 8080                   # must match the HiddenServicePort target below
serverUrl: http://youraddress.onion # share links are generated from this (fill in after step 2)

logging:
  anonymize: true                  # minimal, no-logs posture (see below)
  logToFile: false                 # disable isrv's log file (see disk note below)
```

Or via environment variables:

```bash
ISRV_SERVER_HOST=127.0.0.1
ISRV_SERVER_PORT=8080
ISRV_SERVER_URL=http://youraddress.onion
ISRV_LOGGING_ANONYMIZE=true
ISRV_LOGGING_LOG_TO_FILE=false
```

Binding `serverHost` to `127.0.0.1` is the important one: it ensures the only
way to reach isrv is through the onion service, so the same instance cannot be
correlated with a clearnet IP.

## 2. Tor configuration

Add an onion service to your `torrc` (typically `/etc/tor/torrc`):

```
HiddenServiceDir /var/lib/tor/isrv/
HiddenServicePort 80 127.0.0.1:8080
```

`HiddenServicePort 80 127.0.0.1:8080` maps port 80 on the onion address to
isrv's `serverHost:serverPort`. Version 3 onion services are the default; no
`HiddenServiceVersion` line is needed on modern Tor.

Restart Tor and read the generated address:

```bash
sudo systemctl restart tor
sudo cat /var/lib/tor/isrv/hostname
# -> youraddress.onion
```

## 3. Finish and start

Put the `.onion` hostname from step 2 into `serverUrl` (share links are built
from it, not from request headers), then start isrv:

```bash
isrv --config config.yaml
```

## 4. Verify

- Open `http://youraddress.onion` in Tor Browser and upload a file.
- Or from the command line via `torsocks`:

  ```bash
  torsocks curl -F 'file=@example.txt' http://youraddress.onion
  ```

Confirm the returned link uses the `.onion` host. If it points at
`localhost`, `serverUrl` was not updated in step 3.

## No logs (anonymize mode)

`logging.anonymize: true` makes a no-logs instance. When enabled it:

- **drops successful request logs entirely** - only warnings and errors are written;
- **omits every identifying field** from the records that remain: client IP,
  user agent, request path (which contains the file ID), filename, and host.

It supersedes `logIps`, so you do not need to set that separately. Setting
`logToFile: false` disables isrv's own log file; the remaining warning/error
records still go to stdout/stderr. Whether that console output reaches disk is
deployment-dependent - container runtimes and init systems often persist it
(for example Docker's `json-file` driver or the systemd journal). If you need a
guarantee that nothing lands on disk, route isrv's console output to volatile
storage (or discard it) at the deployment layer.

Server-side error detail (which file failed to decrypt, a storage error, and
so on) is deliberately kept, because it is about the server rather than any
user.

## Rate limiting and client IPs

Every request from the Tor daemon arrives at isrv from `127.0.0.1`, so isrv
sees a single client IP for all users. Two consequences:

- **Leave `security.trustedProxies` empty.** Tor does not send an
  `X-Forwarded-For` header, and there is no real client IP at this layer to
  recover - that is the whole point. Adding `127.0.0.1` as a trusted proxy
  would not reveal anything useful.
- **Per-IP rate limiting becomes global.** Because all traffic shares one
  apparent IP, the rate limiter throttles all users together and a single
  heavy user can exhaust the budget for everyone. Tune
  `security.rateLimit.requestsPerMinute` accordingly, or treat it purely as a
  global request cap. (In `anonymize` mode the rate-limit events are not
  logged, since they carry no useful IP.)

## What this protects, and what it does not

**Protected:**

- User network anonymity, provided by Tor.
- No side-channel deanonymization from the web UI - it loads no third-party
  resources, so opening a share page never causes the browser to contact
  another host.

**Still your responsibility:**

- **Files at rest on the server.** Uploads are stored on the operator's disk.
  Enable encryption at rest (`encryption.enabled: true` with an age identity)
  so a seized or copied disk does not expose file contents.

## Running under Docker

If isrv runs in a container while Tor runs on the host, a `127.0.0.1` bind
*inside the container* is not reachable by the host's Tor daemon. Either:

- run Tor in the same Docker network (or the same compose file) and point
  `HiddenServicePort` at isrv's container IP (see the caveat below), or
- publish isrv's port only on the host loopback
  (`ports: ["127.0.0.1:8080:8080"]`) and keep `HiddenServicePort 80
  127.0.0.1:8080`.

In both cases make sure the port is never published on a public interface.

