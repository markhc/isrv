# Running isrv behind NGINX

isrv does not terminate TLS itself, so a production deployment normally sits
behind a reverse proxy. This page provides a complete NGINX server block and
explains every knob that affects isrv: client IP detection, upload and
download buffering, timeouts, and which endpoints to keep off the public
internet.

Tested against NGINX 1.24+. Directives that depend on your isrv configuration
are called out in [What to tweak](#what-to-tweak).

## Before you start: isrv-side settings

NGINX config is only half of the story. Three isrv settings must match:

1. **`serverUrl`** — set to the public URL (`https://files.example.com`).
   isrv generates share links from this value, not from request headers.
2. **`security.trustedProxies`** — must contain NGINX's address, or isrv
   ignores `X-Forwarded-For` and rate-limits/logs every request as coming
   from the proxy IP:

   ```yaml
   security:
     trustedProxies:
       - "127.0.0.1"        # NGINX on the same host
       # - "172.16.0.0/12"  # NGINX in a Docker network
   ```

   Or: `ISRV_SECURITY_TRUSTED_PROXIES=127.0.0.1`.
3. **`maxFileSizeMb`** — NGINX's `client_max_body_size` below must be at
   least this value (default 512).

## Server block

```nginx
# /etc/nginx/conf.d/isrv.conf

upstream isrv {
    server 127.0.0.1:8080;   # match serverHost/serverPort
    keepalive 32;            # reuse upstream connections
}

# Redirect plain HTTP to HTTPS.
server {
    listen 80;
    listen [::]:80;
    server_name files.example.com;

    # If you use certbot --webroot, keep the challenge path reachable:
    # location /.well-known/acme-challenge/ { root /var/www/certbot; }

    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl;
    listen [::]:443 ssl;
    http2 on;
    server_name files.example.com;

    ssl_certificate     /etc/letsencrypt/live/files.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/files.example.com/privkey.pem;
    ssl_protocols       TLSv1.2 TLSv1.3;
    ssl_session_cache   shared:SSL:10m;
    ssl_session_timeout 1d;
    # Enable once you are sure HTTPS works everywhere:
    # add_header Strict-Transport-Security "max-age=63072000" always;

    # --- Upload size ------------------------------------------------------
    # Must be >= isrv maxFileSizeMb (default 512). A few MB of headroom
    # covers multipart form overhead; isrv enforces the real limit and
    # responds 413 itself.
    client_max_body_size 520m;

    # --- Common proxy headers --------------------------------------------
    proxy_http_version 1.1;
    proxy_set_header   Host              $host;
    proxy_set_header   X-Forwarded-For   $proxy_add_x_forwarded_for;
    proxy_set_header   X-Forwarded-Proto $scheme;
    proxy_set_header   Connection        "";   # required for upstream keepalive

    # --- Timeouts ---------------------------------------------------------
    # Time NGINX waits on isrv, per read/write. Uploads and downloads move
    # between NGINX and isrv at local speed, so these do NOT need to cover
    # a slow client's full transfer time (buffering absorbs that, see below).
    proxy_connect_timeout 5s;
    proxy_send_timeout    120s;
    proxy_read_timeout    120s;

    # --- Uploads and the SPA (POST / is the upload endpoint) --------------
    location / {
        proxy_pass http://isrv;

        # Request buffering is ON (the NGINX default) on purpose: NGINX
        # spools the upload to disk, then replays it to isrv at local speed.
        # isrv currently enforces a fixed 30s server read timeout covering
        # the whole request body, so streaming a slow client's upload
        # straight through would abort large uploads on slow links.
        # See "Streaming profile" below before turning this off.
        #proxy_request_buffering off;

        # Where NGINX spools request bodies. Needs free disk space of
        # roughly client_max_body_size per concurrent upload.
        #client_body_temp_path /var/cache/nginx/client_temp;
    }

    # --- Downloads (/d/<id>) ----------------------------------------------
    location /d/ {
        proxy_pass http://isrv;

        # Response buffering is ON (default) on purpose, mirroring uploads:
        # NGINX slurps the file from isrv at local speed and spoon-feeds the
        # client, freeing the isrv connection and absorbing isrv's fixed 30s
        # write timeout for slow clients.
        # proxy_buffering on;                # default

        # Per-connection spool cap. Once a response outgrows this, NGINX
        # reads from isrv only as fast as the client consumes — putting the
        # 30s isrv write timeout back in play for the remainder. Keep it
        # >= maxFileSizeMb. Default is 1024m.
        proxy_max_temp_file_size 1024m;

        # Do not compress downloads: gzip would interfere with Content-Length
        # and Range (resume/partial) responses that isrv serves on this path.
        gzip off;
    }

    # --- Observability endpoints ------------------------------------------
    # /metrics exposes Prometheus metrics and /healthz//readyz probe
    # internals; they are always enabled in isrv. Do not expose them
    # publicly.
    location = /metrics {
        allow 127.0.0.1;
        # allow 10.0.0.0/8;   # your monitoring network
        deny  all;
        proxy_pass http://isrv;
    }
    location ~ ^/(healthz|readyz)$ {
        allow 127.0.0.1;
        deny  all;
        proxy_pass http://isrv;
    }

    # --- Admin panel (optional hardening) ----------------------------------
    # The panel has its own login + rate limiting; an IP allowlist on top
    # costs nothing if you only ever administer from known networks.
    #location /admin {
    #    allow 203.0.113.7;    # your IP
    #    deny  all;
    #    proxy_pass http://isrv;
    #}

    # --- Compression for the web UI ----------------------------------------
    gzip on;
    gzip_types text/css application/javascript application/json image/svg+xml;
    gzip_min_length 1024;
}
```

## What to tweak

| You changed (isrv) | Change in NGINX |
|---|---|
| `maxFileSizeMb` | `client_max_body_size` (≥ that value) and `proxy_max_temp_file_size` in `location /d/` |
| `serverHost` / `serverPort` | the `upstream isrv` address |
| `security.trustedProxies` | nothing — but it must contain the address NGINX connects *from* |
| Admin panel enabled | consider uncommenting the `/admin` allowlist |
| Prometheus scraping from another host | add its IP/CIDR to the `/metrics` allow list |
| S3 storage with `proxyDownloads: false` | nothing — downloads redirect the client to a pre-signed S3 URL, so file bytes bypass NGINX entirely; only the redirect passes through `/d/` |
