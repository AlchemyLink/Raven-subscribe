# xray-subscription

Languages: **English** | [Русский](README.ru.md)

A subscription server for [Xray-core](https://github.com/XTLS/Xray-core) that:

- Generates **per-user xray client JSON configs** from server-side inbound configs
- Exposes a **unique subscription URL** per user for automatic updates in clients
- Exposes **filtered subscription URLs** per protocol / inbound tag
- **Auto-syncs** users from `/etc/xray/config.d` on startup and on file changes
- Supports **all major Xray protocols**: VLESS, VMess, Trojan, Shadowsocks, SOCKS
- Supports **all transport layers**: TCP, WebSocket, gRPC, HTTP/2, KCP, QUIC, HTTPUpgrade, XHTTP
- Supports **REALITY** and **TLS** security layers (auto-derives public key from private key)
- Stores users and mappings in **SQLite**
- Admin REST API for user and inbound management
- Admin REST API for **user/global routing rules** (`direct` / `proxy` / `block`)

---

## Quick Start

### 1. Build

```bash
go mod tidy
make build
# Binary: ./build/xray-subscription
```

### 2. Configure

```bash
cp config.json.example /etc/xray-subscription/config.json
$EDITOR /etc/xray-subscription/config.json
```

```json
{
  "listen_addr": ":8080",
  "server_host": "YOUR_SERVER_IP_OR_DOMAIN",
  "config_dir": "/etc/xray/config.d",
  "db_path": "/var/lib/xray-subscription/db.sqlite",
  "sync_interval_seconds": 60,
  "base_url": "http://YOUR_SERVER_IP:8080",
  "admin_token": "choose-a-strong-secret-token",
  "balancer_strategy": "leastPing",
  "balancer_probe_url": "https://www.gstatic.com/generate_204",
  "balancer_probe_interval": "30s"
}
```

### 3. Run

```bash
# Direct
./build/xray-subscription -config /etc/xray-subscription/config.json

# Or install as systemd service
make install
systemctl enable --now xray-subscription
```

### 4. Get subscription URL

```bash
# List users (auto-synced from config.d)
curl -H "X-Admin-Token: your-token" http://localhost:8080/api/users

# Response includes sub_url per user:
# "sub_url": "http://YOUR_IP:8080/sub/abc123..."
```

Add this URL to your Xray/v2rayN/Nekoray/Sing-box client to auto-update.

---

## Docker Test Environment (Real Xray)

The repository includes a Docker test stack with a **real Xray container** and
the `xray-subscription` API container.

### Start test stack

```bash
make docker-test-up
```

or:

```bash
make build
docker compose -f docker-compose.test.yml up -d
```

Services:

- `xray` on `127.0.0.1:18443` (from `docker/xray/config.d/01_vless.json`)
- `xray-subscription` API on `127.0.0.1:18080`
- admin token: `test-admin-token` (from `docker/xray-subscription/config.json`)

`docker-compose.test.yml` uses the local binary `build/xray-subscription`.
`Dockerfile` is still useful for production/CI image builds.

Quick API checks:

```bash
curl http://127.0.0.1:18080/health
curl -H "X-Admin-Token: test-admin-token" http://127.0.0.1:18080/api/users
```

Quick subscription check (extract Alice token and request links):

```bash
TOKEN=$(curl -s -H "X-Admin-Token: test-admin-token" http://127.0.0.1:18080/api/users | jq -r '.[] | select(.user.username=="alice@example.com") | .user.token')
curl "http://127.0.0.1:18080/sub/$TOKEN/links.txt"
```

### Run Docker E2E test

```bash
make docker-test-e2e
```

This test (`integration/e2e_docker_test.go`) launches real Xray + app containers
and verifies:

- sync from `config.d` to SQLite
- admin API (`/api/users`)
- subscription links (`/sub/{token}/links.txt` and `/sub/{token}/links.b64`)

Integration coverage is also extended by `integration/docker_compose_api_test.go`,
which validates admin/user/route/balancer/subscription flows using `docker compose`.
Both Docker tests are split into subtests to simplify debugging.

Optional image override (if you want another Xray tag):

```bash
XRAY_IMAGE=ghcr.io/xtls/xray-core:latest E2E_DOCKER=1 go test ./integration -v
```

Optional compose overrides:

```bash
APP_HOST_PORT=28080 XRAY_HOST_PORT=28443 make docker-test-up
```

Supported override variables in `docker-compose.test.yml`:

- `XRAY_IMAGE`
- `XRAY_CONFIG_DIR`
- `APP_CONFIG_PATH`
- `APP_BIN_PATH`
- `APP_HOST_PORT`
- `XRAY_HOST_PORT`

### Logs and teardown

```bash
make docker-test-logs
make docker-test-down
```

---

## Run on Server (systemd)

This is a production-oriented setup checklist.

### 1) Directories

```bash
sudo mkdir -p /etc/xray-subscription
sudo mkdir -p /var/lib/xray-subscription
sudo mkdir -p /etc/xray/config.d
```

### 2) Binary install

Copy built binary to the server and install:

```bash
sudo install -m 0755 /home/$USER/xray-subscription /usr/local/bin/xray-subscription
```

### 3) Config file

Use the same config schema from **Quick Start → Configure** (or copy `config.json.example`)
and save it as `/etc/xray-subscription/config.json`.

Notes:
- Use `:8080` or `0.0.0.0:8080` for `listen_addr` (do not use domain name there).
- `config_dir` must contain your Xray inbound configs.
- `balancer_strategy`: `random`, `leastPing`, or `leastLoad` (default: `leastPing`).
- `balancer_probe_*` is used by `leastPing`/`leastLoad` observatory checks.

### 4) Manual start test

```bash
/usr/local/bin/xray-subscription -config /etc/xray-subscription/config.json
```

### 5) systemd unit

Create `/etc/systemd/system/xray-subscription.service`:

```ini
[Unit]
Description=Xray Subscription Server
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/xray-subscription -config /etc/xray-subscription/config.json
Restart=always
RestartSec=3
User=root
WorkingDirectory=/
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
```

Enable and start:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now xray-subscription
sudo systemctl status xray-subscription
```

Logs:

```bash
journalctl -u xray-subscription -f
```

### 6) Health and API check

```bash
curl http://127.0.0.1:8080/health
curl -H "X-Admin-Token: CHANGE_ME_TO_STRONG_TOKEN" http://127.0.0.1:8080/api/users
```

### 7) Binary update flow

After uploading a new binary:

```bash
sudo systemctl restart xray-subscription
curl -f http://127.0.0.1:8080/health
```

---

## How It Works

```
/etc/xray/config.d/
    ├── 01_vless.json        ← server inbounds
    ├── 02_vmess.json
    └── 03_trojan.json
          │
          ▼ (auto-sync on start + file change)
    SQLite DB
    ├── users (alice, bob, ...)
    ├── inbounds (vless-reality, vmess-ws, ...)
    └── user_clients (alice↔vless, alice↔vmess, ...)
          │
          ▼ GET /sub/{token}
    Full xray client JSON config
    (outbounds for each enabled inbound + routing + DNS)
```

---

## API Reference

All admin endpoints require `X-Admin-Token: <your-token>` header.

### Subscription

| Method | URL | Description |
|--------|-----|-------------|
| `GET` | `/sub/{token}` | Download xray client config JSON |
| `GET` | `/sub/{token}/protocol/{protocol}` | Download config JSON for one protocol |
| `GET` | `/sub/{token}/inbound/{inboundTag}` | Download config JSON for one inbound tag |
| `GET` | `/sub/{token}/links` | List helper links: all, by protocol, by inbound tag |
| `GET` | `/sub/{token}/links.txt` | Proxy subscription links (plain text, one link per line) |
| `GET` | `/sub/{token}/links.b64` | Proxy subscription links in base64 (V2Box-friendly) |
| `GET` | `/sub/{token}/vless` | VLESS-only links (plain text, one per line) |
| `GET` | `/sub/{token}/vless.b64` | VLESS-only links (base64) |
| `GET` | `/sub/{token}/vless/list` | List VLESS configs (tags + direct URLs) |
| `GET` | `/sub/{token}/vless/{vlessTag}` | Single VLESS link by tag |
| `GET` | `/sub/{token}/vless/{vlessTag}/b64` | Single VLESS link by tag (base64) |
| `GET` | `/sub/{token}/vmess` | VMess-only links (plain text) |
| `GET` | `/sub/{token}/vmess.b64` | VMess-only links (base64) |
| `GET` | `/sub/{token}/trojan` | Trojan-only links (plain text) |
| `GET` | `/sub/{token}/trojan.b64` | Trojan-only links (base64) |
| `GET` | `/sub/{token}/ss` | Shadowsocks-only links (plain text) |
| `GET` | `/sub/{token}/ss.b64` | Shadowsocks-only links (base64) |
| `GET` | `/sub/{token}/protocol/{protocol}/links.txt` | Protocol-scoped proxy links (plain text) |
| `GET` | `/sub/{token}/protocol/{protocol}/links.b64` | Protocol-scoped proxy links (base64) |
| `GET` | `/sub/{token}/inbound/{inboundTag}/links.txt` | Inbound-tag-scoped proxy links (plain text) |
| `GET` | `/sub/{token}/inbound/{inboundTag}/links.b64` | Inbound-tag-scoped proxy links (base64) |
| `GET` | `/health` | Health check |

`/sub/{token}` supports optional filters:

- `?protocol=vless` (or `vmess`, `trojan`, `shadowsocks`, `socks`)
- `?inbound_tag=vless-xhttp-in-1`
- `?format=v2box` or `?format=links` (plain-text proxy links instead of full JSON)
- `?format=b64` (base64-encoded proxy links)
- `?profile=mobile` (strip `geosite:` / `geoip:` selectors from routing rules)
- `?mobile=1` (same as `profile=mobile`)

Examples:

```bash
# Full client JSON (Xray/v2rayN style)
curl "http://HOST:8080/sub/<token>"

# V2Box-friendly links (plain text, one per line)
curl "http://HOST:8080/sub/<token>/links.txt"

# V2Box-friendly links (base64)
curl "http://HOST:8080/sub/<token>/links.b64"

# VLESS-only links
curl "http://HOST:8080/sub/<token>/vless"
curl "http://HOST:8080/sub/<token>/vless.b64"
curl "http://HOST:8080/sub/<token>/vmess"
curl "http://HOST:8080/sub/<token>/trojan.b64"
curl "http://HOST:8080/sub/<token>/ss"

# List all VLESS configs with tags and direct URLs
curl "http://HOST:8080/sub/<token>/vless/list"

# Single VLESS config by tag
curl "http://HOST:8080/sub/<token>/vless/vless-xhttp-in-1"
curl "http://HOST:8080/sub/<token>/vless/vless-xhttp-in-1/b64"

# Same as links.txt, via format query
curl "http://HOST:8080/sub/<token>?format=v2box"

# Protocol-separated JSON subscription (example: vless)
curl "http://HOST:8080/sub/<token>/protocol/vless"

# Protocol-separated links for V2Box (example: vmess)
curl "http://HOST:8080/sub/<token>/protocol/vmess/links.b64"

# Inbound-tag-separated JSON subscription
curl "http://HOST:8080/sub/<token>/inbound/vless-xhttp-in-1"

# Inbound-tag-separated links for V2Box
curl "http://HOST:8080/sub/<token>/inbound/vless-xhttp-in-1/links.b64"

# Mobile-friendly JSON profile (no geosite/geoip selectors in routing rules)
curl "http://HOST:8080/sub/<token>?profile=mobile"
```

### Users

| Method | URL | Description |
|--------|-----|-------------|
| `GET` | `/api/users` | List all users with sub URLs |
| `POST` | `/api/users` | Create user `{"username":"alice"}` |
| `GET` | `/api/users/{id}` | Get user |
| `DELETE` | `/api/users/{id}` | Delete user |
| `PUT` | `/api/users/{id}/enable` | Enable user |
| `PUT` | `/api/users/{id}/disable` | Disable user |
| `POST` | `/api/users/{id}/token` | Regenerate subscription token |
| `GET` | `/api/users/{id}/routes` | Get user routing rules |
| `POST` | `/api/users/{id}/routes` | Add one user routing rule |
| `PUT` | `/api/users/{id}/routes` | Replace all user routing rules |
| `PUT` | `/api/users/{id}/routes/{index}` | Update one user routing rule by index |
| `DELETE` | `/api/users/{id}/routes/{index}` | Delete one user routing rule by index |
| `PUT` | `/api/users/{id}/routes/id/{routeId}` | Update one user routing rule by stable id |
| `DELETE` | `/api/users/{id}/routes/id/{routeId}` | Delete one user routing rule by stable id |
| `GET` | `/api/users/{id}/clients` | List user's inbound mappings |
| `PUT` | `/api/users/{userId}/clients/{inboundId}/enable` | Enable specific inbound for user |
| `PUT` | `/api/users/{userId}/clients/{inboundId}/disable` | Disable specific inbound for user |

### Inbounds, Global Routes & Sync

| Method | URL | Description |
|--------|-----|-------------|
| `GET` | `/api/inbounds` | List all detected inbounds |
| `GET` | `/api/routes/global` | Get global routing rules |
| `POST` | `/api/routes/global` | Add one global routing rule |
| `PUT` | `/api/routes/global` | Replace all global routing rules |
| `DELETE` | `/api/routes/global` | Clear all global routing rules |
| `GET` | `/api/config/balancer` | Get effective balancer config |
| `PUT` | `/api/config/balancer` | Set/reset runtime balancer override |
| `POST` | `/api/sync` | Trigger manual sync from config.d |

Routing rule constraints for both user/global routes:

- `outboundTag` must be one of: `direct`, `proxy`, `block`
- rule must contain at least one effective field: `domain`, `ip`, `network`, `port`, `protocol`, or `inboundTag`
- `type` must be `field` (or empty)

Route payload examples:

```bash
# Replace all user routes
curl -X PUT "http://HOST:8080/api/users/1/routes" \
  -H "X-Admin-Token: TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "rules": [
      {
        "id": "allow-okko",
        "outboundTag": "direct",
        "domain": ["okko.tv", "okko.sport"]
      },
      {
        "outboundTag": "block",
        "domain": ["geosite:category-ads-all"]
      }
    ]
  }'

# Add one global route
curl -X POST "http://HOST:8080/api/routes/global" \
  -H "X-Admin-Token: TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "outboundTag": "proxy",
    "domain": ["geosite:ru-blocked"]
  }'
```

`/api/inbounds` response notes:

- `raw_config` is returned as parsed JSON object/array when valid.
- If stored raw config is malformed JSON, `raw_config` is returned as string (fallback).

Balancer runtime override examples:

```bash
# Set runtime balancer strategy (takes effect immediately for new generated configs)
curl -X PUT "http://HOST:8080/api/config/balancer" \
  -H "X-Admin-Token: TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "strategy": "leastPing",
    "probe_url": "https://www.gstatic.com/generate_204",
    "probe_interval": "30s"
  }'

# Reset runtime override and return to config.json values
curl -X PUT "http://HOST:8080/api/config/balancer" \
  -H "X-Admin-Token: TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"reset": true}'
```

---

## Supported Protocols

| Protocol | Client Credentials | Notes |
|----------|-------------------|-------|
| **VLESS** | UUID + Flow | XTLS Vision, REALITY |
| **VMess** | UUID + AlterId | |
| **Trojan** | Password | |
| **Shadowsocks** | Password + Method | Single and multi-user (2022 ciphers) |
| **SOCKS** | User + Password | |

## Supported Transports

TCP, WebSocket (WS), gRPC, HTTP/2 (h2), mKCP, QUIC, HTTPUpgrade, XHTTP (SplitHTTP)

## Supported Security

TLS (with fingerprint), REALITY (auto-derives public key from private key via X25519)

REALITY extras supported in generated client config:

- `mldsa65Verify` (derived from server `mldsa65Seed` if needed)
- `publicKey` (derived from `privateKey` if not provided)

---

## Server Config Format

Your `/etc/xray/config.d/*.json` files can use the standard inbounds array format:

```json
[
  {
    "tag": "vless-reality",
    "port": 443,
    "protocol": "vless",
    "settings": {
      "clients": [
        {"id": "UUID", "flow": "xtls-rprx-vision", "email": "alice"}
      ],
      "decryption": "none"
    },
    "streamSettings": {
      "network": "tcp",
      "security": "reality",
      "realitySettings": {
        "serverNames": ["www.microsoft.com"],
        "privateKey": "YOUR_PRIVATE_KEY",
        "shortIds": ["abc123"]
      }
    }
  }
]
```

Or the full xray config format with top-level `inbounds` key — both are supported.

### REALITY Public Key

If `publicKey` is not present in `realitySettings`, the app **automatically derives it**
from `privateKey` using X25519 curve math. You can also generate it manually:

```bash
xray x25519
# Private key: abc...
# Public key:  xyz...
```

---

## Client Config Output Example

```json
{
  "log": {"loglevel": "warning"},
  "dns": {"servers": ["8.8.8.8", "1.1.1.1", ...]},
  "inbounds": [
    {"tag": "socks", "port": 2080, "protocol": "socks"},
    {"tag": "http",  "port": 1081, "protocol": "http"}
  ],
  "outbounds": [
    {
      "tag": "vless-reality-0",
      "protocol": "vless",
      "settings": {
        "vnext": [{"address": "1.2.3.4", "port": 443, "users": [...]}]
      },
      "streamSettings": {
        "network": "tcp",
        "security": "reality",
        "realitySettings": {
          "serverName": "www.microsoft.com",
          "fingerprint": "chrome",
          "publicKey": "xyz...",
          "shortId": "abc123"
        }
      }
    },
    {"tag": "direct", "protocol": "freedom"},
    {"tag": "block",  "protocol": "blackhole"}
  ],
  "routing": {
    "domainStrategy": "IPOnDemand",
    "rules": [
      {"type": "field", "outboundTag": "proxy",  "domain": ["geosite:ru-blocked"]},
      {"type": "field", "outboundTag": "proxy",  "ip":     ["geoip:ru-blocked"]},
      {"type": "field", "outboundTag": "block",  "domain": ["geosite:category-ads-all", "geosite:category-ads", "geosite:category-public-tracker"]},
      {"type": "field", "outboundTag": "direct", "ip":     ["geoip:private", "geoip:ru"]},
      {"type": "field", "outboundTag": "direct", "domain": ["geosite:private"]},
      {"type": "field", "outboundTag": "<resolved-proxy-or-balancer>", "port": "0-65535"}
    ]
  }
}
```

Notes:

- `outboundTag: "proxy"` in custom/default rules is a **logical target**. Generator resolves it to:
  - concrete outbound tag when there is one proxy outbound
  - routing balancer (`balancerTag`) when there are multiple proxy outbounds
- Rule priority is: **user rules > global rules > defaults**

---

## Security Notes

- The `admin_token` protects all `/api/*` endpoints; keep it secret.
- Subscription URLs (`/sub/{token}`) are public by design — share only with the user.
- Use `POST /api/users/{id}/token` to rotate a compromised token.
- Run behind a reverse proxy (nginx/caddy) with HTTPS in production.
