# Raven Subscribe

Languages: **English** | [Русский](README.ru.md)

[![Built for Xray-core](https://img.shields.io/badge/Built%20for-Xray--core-blue?logo=github)](https://github.com/XTLS/Xray-core)
[![Test](https://github.com/AlchemyLink/Raven-subscribe/actions/workflows/test.yml/badge.svg)](https://github.com/AlchemyLink/Raven-subscribe/actions/workflows/test.yml)
[![Security Scan](https://github.com/AlchemyLink/Raven-subscribe/actions/workflows/security.yml/badge.svg)](https://github.com/AlchemyLink/Raven-subscribe/actions/workflows/security.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/alchemylink/raven-subscribe)](https://goreportcard.com/report/github.com/alchemylink/raven-subscribe)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Status](https://img.shields.io/badge/Status-Alpha%20Testing-orange)](https://github.com/AlchemyLink/Raven-subscribe)
[![Stars](https://img.shields.io/github/stars/AlchemyLink/Raven-subscribe?style=flat)](https://github.com/AlchemyLink/Raven-subscribe/stargazers)
[![Forks](https://img.shields.io/github/forks/AlchemyLink/Raven-subscribe?style=flat)](https://github.com/AlchemyLink/Raven-subscribe/network/members)
[![Hits](https://hits.dwyl.com/AlchemyLink/Raven-subscribe.svg?style=flat)](https://hits.dwyl.com/AlchemyLink/Raven-subscribe)

**Self-hosted subscription server for [XTLS/Xray-core](https://github.com/XTLS/Xray-core) and [sing-box](https://github.com/SagerNet/sing-box).** Auto-discovers users from your Xray server configs and gives each one a personal subscription URL — so V2RayNG, NekoBox, Hiddify, and other VPN clients always fetch the latest connection settings automatically.

Supports **VLESS, VMess, Trojan, Shadowsocks, Hysteria2**, transports **XHTTP/SplitHTTP, WebSocket, gRPC, REALITY**, and serves configs in Xray JSON, sing-box JSON, and share-link formats.

> [!WARNING]
> **Alpha Testing** — This project is under active development. APIs and config fields may change between versions. Please [report issues](https://github.com/AlchemyLink/Raven-subscribe/issues).

---

## Table of Contents

- [What it does](#what-it-does)
- [Features](#features)
- [How it works](#how-it-works)
- [Quick Start](#quick-start)
- [Configuration](#configuration)
- [Subscription URLs](#subscription-urls)
- [Admin API](#admin-api)
- [Routing Rules](#routing-rules)
- [Emergency Config Rotation](#emergency-config-rotation)
- [Supported Protocols & Transports](#supported-protocols--transports)
- [sing-box / Hysteria2](#sing-box--hysteria2)
- [Docker](#docker)
- [Contributing](#contributing)

---

## What it does

When you run an Xray proxy server, each user needs a client config file with the correct server address, port, UUID, and transport settings. Keeping those configs up to date manually is tedious.

**Raven Subscribe** solves this:

1. It reads your Xray server configs from `/etc/xray/config.d`
2. Discovers all users (clients) defined in those configs automatically
3. Generates a ready-to-use Xray client config for each user
4. Serves it via a unique subscription URL

Users just add their subscription URL to any Xray-compatible client (V2RayNG, NekoBox, V2Box, Hiddify, etc.) — and the client fetches fresh settings automatically.

---

## Features

### For users
- **Personal subscription URL** — one link that always returns the latest config
- **Multiple formats** — full Xray JSON, sing-box JSON, plain share links, Base64-encoded links
- **Protocol-specific links** — get only VLESS, VMess, Trojan, Shadowsocks, or Hysteria2 links
- **Mobile-optimized configs** — auto-detected from User-Agent (Android, iPhone, NekoBox, V2RayNG) or via `?profile=mobile`
- **Custom routing rules** — per-user rules to route specific sites directly, through proxy, or block them

### For administrators
- **Database as source of truth** — when `api_user_inbound_tag` is set, add/remove/enable/disable users via API and they sync to Xray immediately
- **Zero-touch user sync** — users can also be discovered from Xray config `email` fields
- **File watcher** — detects config changes instantly (fsnotify + periodic polling)
- **Full REST API** — manage users, tokens, routing rules, and balancer settings
- **Per-user client control** — enable/disable a user's access to specific inbounds
- **Token rotation** — regenerate any user's subscription token without downtime
- **Balancer support** — automatic load balancing across multiple outbounds (leastPing, leastLoad, random)
- **Global routing rules** — apply routing rules to all users at once

### Protocols & transports
- **VLESS**, **VMess**, **Trojan**, **Shadowsocks**, **SOCKS** (via Xray-core)
- **Hysteria2** (via sing-box) — QUIC-based protocol with Salamander obfuscation
- **TCP**, **WebSocket**, **gRPC**, **HTTP/2**, **KCP**, **QUIC**, **HTTPUpgrade**, **XHTTP (SplitHTTP)**
- **TLS** and **REALITY** security layers with automatic key derivation

---

## How it works

```
/etc/xray/config.d/          /etc/sing-box/config.json
    ├── vless-reality.json        └── (hysteria2 inbound)
    ├── vmess-ws.json
    └── trojan-tls.json
           │                              │
           └──────────────┬───────────────┘
                          ▼
                   Raven Subscribe
                   (watches for changes)
                          │
                          ├─ Parses inbounds, discovers users
                          ├─ Stores in SQLite
                          ├─ Serves subscription URLs
                          └─ API-created users → Xray (config files or gRPC API)
                                     │
                                     ▼
                   https://your-server.com/sub/{token}         ← Xray JSON
                   https://your-server.com/sub/{token}/singbox  ← sing-box JSON
                   https://your-server.com/sub/{token}/hysteria2 ← share links
                                     │
                                     ▼
                   V2RayNG / NekoBox / Hiddify / V2Box / Hysteria2 app
                   (fetches config automatically)
```

Each user gets a unique token. When their client fetches the subscription URL, Raven Subscribe builds a complete Xray client config on the fly — with all their enabled inbounds, routing rules, DNS settings, and balancer config.

---

## Quick Start

### 1. Install

**From binary:**
```bash
curl -Lo xray-subscription https://github.com/AlchemyLink/Raven-subscribe/releases/latest/download/xray-subscription-linux-amd64
chmod +x xray-subscription
sudo mv xray-subscription /usr/local/bin/
```

**From source:**
```bash
git clone https://github.com/AlchemyLink/Raven-subscribe.git
cd Raven-subscribe
make build
sudo cp build/xray-subscription /usr/local/bin/
```

### 2. Configure

```bash
sudo mkdir -p /etc/xray-subscription
sudo cp config.json.example /etc/xray-subscription/config.json
sudo nano /etc/xray-subscription/config.json
```

Minimum required settings:

```json
{
  "server_host": "your-server-ip-or-domain",
  "admin_token": "your-secret-admin-token",
  "base_url": "http://your-server-ip-or-domain:8080"
}
```

### 3. Run

```bash
xray-subscription -config /etc/xray-subscription/config.json
```

**As a systemd service:**
```bash
sudo cp xray-subscription.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now xray-subscription
```

The service runs as `User=xray` so Raven and Xray share ownership of config files. When `api_user_inbound_tag` is set, Raven writes to `config_dir`; Xray must read those files. Ensure:

```bash
# Create xray user/group if missing (Xray package usually does this)
sudo useradd -r -s /usr/sbin/nologin xray 2>/dev/null || true

# Let xray own config_dir and data dir when using file-based sync
sudo chown -R xray:xray /etc/xray/config.d /var/lib/xray-subscription
```

### 4. Get user subscription URLs

```bash
# List all users and their subscription URLs
curl -H "X-Admin-Token: your-secret-admin-token" http://localhost:8080/api/users
```

Response:
```json
[
  {
    "user": {
      "id": 1,
      "username": "alice",
      "token": "a3f8c2...",
      "enabled": true
    },
    "sub_url": "http://your-server:8080/sub/a3f8c2...",
    "sub_urls": {
      "full":        "http://your-server:8080/sub/a3f8c2...",
      "links_txt":   "http://your-server:8080/sub/a3f8c2.../links.txt",
      "links_b64":   "http://your-server:8080/sub/a3f8c2.../links.b64",
      "compact":     "http://your-server:8080/c/a3f8c2...",
      "compact_txt": "http://your-server:8080/c/a3f8c2.../links.txt",
      "compact_b64": "http://your-server:8080/c/a3f8c2.../links.b64",
      "singbox":     "http://your-server:8080/sub/a3f8c2.../singbox",
      "hysteria2":   "http://your-server:8080/sub/a3f8c2.../hysteria2"
    }
  }
]
```

Give each user their `sub_urls.compact` URL — they add it to their VPN client and are ready to go. For Hysteria2 clients, use `sub_urls.singbox` or `sub_urls.hysteria2`.

---

## Configuration

Configuration is loaded from a JSON file (default: `config.json` in the current directory). Use `-config` flag to specify a path:

```bash
xray-subscription -config /etc/xray-subscription/config.json
```

### Full config reference

```json
{
  "listen_addr": ":8080",
  "server_host": "your-server.com",
  "config_dir": "/etc/xray/config.d",
  "db_path": "/var/lib/xray-subscription/db.sqlite",
  "sync_interval_seconds": 60,
  "base_url": "http://your-server.com:8080",
  "admin_token": "your-secret-token",
  "balancer_strategy": "leastPing",
  "balancer_probe_url": "https://www.gstatic.com/generate_204",
  "balancer_probe_interval": "30s",
  "socks_inbound_port": 2080,
  "http_inbound_port": 1081,
  "rate_limit_sub_per_min": 60,
  "rate_limit_admin_per_min": 30,
  "api_user_inbound_tag": "vless-reality",
  "xray_api_addr": "",
  "xray_enabled": true,
  "singbox_config": "/etc/sing-box/config.json",
  "singbox_enabled": true,
  "inbound_hosts": {
    "vless-reality-in": "media.example.com"
  },
  "inbound_ports": {
    "vless-reality-in": 8445
  }
}
```

> **Note:** `vless_client_encryption` is intentionally omitted from the example above — only add it when using VLESS Encryption (non-`"none"` decryption on the server inbound). Setting it to `false`, `"none"`, or `""` causes a parse error.

### Parameter reference

#### Server

| Field | Default | Description |
|-------|---------|-------------|
| `listen_addr` | `:8080` | Address and port to bind. Use `:8080` for all interfaces, or `127.0.0.1:8080` for localhost only (e.g. behind nginx). |
| `server_host` | — | **Required.** Your server IP or domain. Used as the outbound address in generated client configs. Must match what clients will connect to. |
| `base_url` | `http://localhost:8080` | Full base URL for subscription links. Shown to users in API responses. Use `https://` if behind TLS reverse proxy. |

#### Storage & sync

| Field | Default | Description |
|-------|---------|-------------|
| `config_dir` | `/etc/xray/config.d` | Directory containing Xray server inbound JSON configs. Raven watches this for changes (fsnotify + periodic scan). |
| `db_path` | `/var/lib/xray-subscription/db.sqlite` | SQLite database path. Stores users, tokens, routing rules, and synced client data. |
| `sync_interval_seconds` | `60` | Interval (seconds) for re-scanning `config_dir`. Also triggered on file changes. |

#### Admin API

| Field | Default | Description |
|-------|---------|-------------|
| `admin_token` | — | **Required.** Secret token for Admin API. Pass in `X-Admin-Token` header. Use a long random string; generate with `openssl rand -hex 32`. |

#### Load balancer

Used when your Xray config has multiple outbounds (e.g. several proxy nodes). Controls how the client chooses between them.

| Field | Default | Description |
|-------|---------|-------------|
| `balancer_strategy` | `leastPing` | Strategy: `leastPing` (lowest latency), `leastLoad` (least connections), `random`, `roundRobin`. |
| `balancer_probe_url` | `https://www.gstatic.com/generate_204` | URL for latency probes (used by `leastPing`). Must be reachable from the server. |
| `balancer_probe_interval` | `30s` | How often to probe outbounds. Go duration: `30s`, `1m`, etc. |

#### Client config generation

| Field | Default | Description |
|-------|---------|-------------|
| `socks_inbound_port` | `2080` | Local SOCKS5 proxy port in generated client configs. Clients use this for system/app proxy. |
| `http_inbound_port` | `1081` | Local HTTP proxy port in generated client configs. |

#### Rate limiting

Limits requests per IP per minute. `0` = disabled. Helps prevent abuse.

| Field | Default | Description |
|-------|---------|-------------|
| `rate_limit_sub_per_min` | `0` | Max requests/min per IP for `/sub/*` and `/c/*`. Recommended: 60 for production. |
| `rate_limit_admin_per_min` | `0` | Max requests/min per IP for `/api/*`. Recommended: 30. |
| `api_user_inbound_tag` | `""` | When set, the database is the source of truth: users created via API are added to this Xray inbound; deleted users are removed; enable/disable syncs to Xray. Uses `config_dir` file write or Xray API (if `xray_api_addr` is set). |
| `xray_api_addr` | `""` | When set, users are synced via Xray gRPC API instead of config files. E.g. `127.0.0.1:8080`. Requires `api_user_inbound_tag`. Xray must have API enabled with HandlerService. |
| `api_user_inbound_protocol` | `""` | Fallback when `config_dir` has no inbounds: protocol (`vless`, `vmess`, `trojan`, `shadowsocks`) to create the inbound in DB. Use when Xray configs are elsewhere. |
| `api_user_inbound_port` | `443` | Port for the inbound when using `api_user_inbound_protocol` fallback. |
| `xray_config_file_mode` | *(omit)* | Octal mode for JSON files Raven writes under `config_dir` (e.g. `"0644"` so another local user can read configs for testing). Default **`0600`**. Only permission bits `0`–`7` (max `0777`). |
| `vless_client_encryption` | *(omit)* | Map of inbound tag → client-side VLESS Encryption string (Xray-core ≥ v26.2.6, PR #5067). Only needed when the inbound uses VLESS Encryption (`decryption` ≠ `"none"`). Generate both strings with `xray vlessenc`. Example: `{"vless-reality-in": "mlkem768x25519plus..."}`. When set, flow is forced to `xtls-rprx-vision` and Mux is disabled. Omit or remove entirely when not using VLESS Encryption. |
| `xray_enabled` | `true` | Set to `false` to disable Xray config sync (suppress warnings if Xray is not installed). |
| `singbox_config` | `""` | Path to sing-box server config file (e.g. `/etc/sing-box/config.json`). When set, Raven also syncs Hysteria2 inbounds from it. |
| `singbox_enabled` | auto | Controls sing-box sync. Defaults to `true` when `singbox_config` is set. Set to `false` to temporarily disable without removing the path. |
| `inbound_hosts` | `{}` | Per-inbound host overrides. Key: inbound tag, value: host/domain. Overrides `server_host` for matching inbounds in generated client configs. Falls back to `server_host` when tag is not listed. Example: `{"vless-reality-in": "relay.example.com"}` |
| `inbound_ports` | `{}` | Per-inbound port overrides. Key: inbound tag, value: port number. Overrides the inbound's own port in generated client configs. Useful when clients connect through a relay that listens on a different port. Example: `{"vless-reality-in": 8445}` |

**DB ↔ Xray sync (when `api_user_inbound_tag` is set):** The database is the source of truth. All changes propagate to Xray immediately:

| Action | DB | Xray |
|--------|----|------|
| Create user (`POST /api/users`) | Add | Add to inbound |
| Delete user (`DELETE /api/users/{id}`) | Remove | Remove from inbound |
| Disable user (`PUT /api/users/{id}/disable`) | `enabled=false` | Remove from inbound |
| Enable user (`PUT /api/users/{id}/enable`) | `enabled=true` | Add to inbound |

**Xray API mode** (when `xray_api_addr` is set): Users are synced via gRPC instead of config files. Xray must have API enabled with `HandlerService` in `services`.

- **Restore on startup:** Raven restores all users from the database to Xray via API (survives Xray restarts).
- **Periodic DB→config sync:** Raven periodically writes users to config files, so they persist across both Raven and Xray restarts.

### Example: minimal config

```json
{
  "server_host": "vpn.example.com",
  "admin_token": "your-secret-token",
  "base_url": "https://vpn.example.com"
}
```

All other parameters use defaults.

### Example: production with rate limits

```json
{
  "listen_addr": "127.0.0.1:8080",
  "server_host": "vpn.example.com",
  "base_url": "https://vpn.example.com",
  "admin_token": "your-secret-token",
  "rate_limit_sub_per_min": 60,
  "rate_limit_admin_per_min": 30
}
```

Use `127.0.0.1` when running behind nginx/caddy as reverse proxy.

---

## Subscription URLs

Each user has several subscription endpoints:

| Endpoint | Description |
|---|---|
| `/c/{token}` | **Primary.** Lightweight Xray JSON config — `geosite:`/`geoip:` selectors stripped. Works great on all devices. |
| `/sub/{token}` | Full Xray JSON config with all routing rules including geo databases. |
| `/sub/{token}/singbox` | sing-box JSON config with Hysteria2 outbounds. For Hysteria2 clients. |
| `/sub/{token}/hysteria2` | Hysteria2 share links (`hysteria2://…`), plain text. |
| `/sub/{token}/hysteria2.b64` | Hysteria2 share links, Base64-encoded. |
| `/sub/{token}/links` | JSON map of all share-link URLs grouped by protocol and inbound tag. |
| `/sub/{token}/protocol/{protocol}` | Share links filtered by protocol name (e.g. `vless`, `vmess`, `trojan`, `ss`, `hysteria2`). |

### `/c/{token}` — primary endpoint (recommended)

The compact endpoint is the recommended URL to give to users. It returns a complete Xray client config with routing rules optimized for lower memory usage — `geosite:` and `geoip:` selectors are stripped, keeping only explicit domain and IP rules.

Works on all clients: V2RayNG, NekoBox, V2Box, Hiddify, and desktop clients.

| What you want | URL |
|---|---|
| Full Xray JSON config | `/c/{token}` |
| All share links (plain text) | `/c/{token}/links.txt` |
| All share links (Base64) | `/c/{token}/links.b64` |

### `/sub/{token}` — full endpoint

Returns the complete config including `geosite:` and `geoip:` routing rules. Use this if your client supports geo databases and you want full routing control.

| What you want | URL |
|---|---|
| Full Xray JSON config | `/sub/{token}` |
| All share links (plain text) | `/sub/{token}/links.txt` |
| All share links (Base64) | `/sub/{token}/links.b64` |
| VLESS links only | `/sub/{token}/vless` |
| VMess links only | `/sub/{token}/vmess` |
| Trojan links only | `/sub/{token}/trojan` |
| Shadowsocks links only | `/sub/{token}/ss` |
| Hysteria2 share links | `/sub/{token}/hysteria2` |
| Hysteria2 share links (Base64) | `/sub/{token}/hysteria2.b64` |
| sing-box JSON (Hysteria2) | `/sub/{token}/singbox` |
| Specific inbound only | `/sub/{token}/inbound/{tag}` |
| Lightweight config (explicit) | `/sub/{token}?profile=mobile` |

### Example: import into V2RayNG

1. Open V2RayNG → tap **+** → **Import config from URL**
2. Paste: `http://your-server:8080/c/YOUR_TOKEN`
3. Tap **OK** — done. The app fetches and imports all your connections.

### Example: import into NekoBox / Hiddify

Use the same `/c/{token}` URL. These clients support Xray JSON format natively.

### Example: get plain share links

```bash
curl http://your-server:8080/c/YOUR_TOKEN/links.txt
```

Output:
```
vless://uuid@your-server:443?type=ws&security=tls&...#vless-ws-tls
vmess://eyJ2IjoiMiIsInBzIjoidm1lc3MtdGNwIiwiYWRkIjoieW91ci1zZXJ2ZXIiLCJwb3J0IjoiODA4MCIsImlkIjoiLi4uIn0=
trojan://password@your-server:443?security=tls&...#trojan-tls
```

### Auto-detection

When a mobile client fetches `/sub/{token}`, Raven Subscribe automatically detects it from the `User-Agent` header (Android, iPhone, iPad, V2RayNG, NekoBox, V2Box) and applies the lightweight profile automatically. The `/c/{token}` endpoint always uses the lightweight profile regardless of User-Agent.

---

## Admin API

All admin endpoints require authentication. Pass your `admin_token` as a header or query parameter:

```bash
# As header (recommended)
curl -H "X-Admin-Token: your-secret-token" http://localhost:8080/api/users

# As query parameter
curl "http://localhost:8080/api/users?admin_token=your-secret-token"
```

### Users

#### List all users
```bash
GET /api/users
```
```bash
curl -H "X-Admin-Token: secret" http://localhost:8080/api/users
```
```json
[
  {
    "user": {"id": 1, "username": "alice@example.com", "token": "abc123", "enabled": true},
    "sub_url": "http://your-server:8080/sub/abc123",
    "sub_urls": {
      "full":        "http://your-server:8080/sub/abc123",
      "links_txt":   "http://your-server:8080/sub/abc123/links.txt",
      "links_b64":   "http://your-server:8080/sub/abc123/links.b64",
      "compact":     "http://your-server:8080/c/abc123",
      "compact_txt": "http://your-server:8080/c/abc123/links.txt",
      "compact_b64": "http://your-server:8080/c/abc123/links.b64",
      "singbox":     "http://your-server:8080/sub/abc123/singbox",
      "hysteria2":   "http://your-server:8080/sub/abc123/hysteria2"
    }
  }
]
```

#### Create a user
```bash
POST /api/users
Content-Type: application/json

{"username": "bob"}
```
On create, `email` is not accepted; internally it matches `username` for Xray. API JSON does **not** include `email` (use `username`).

```bash
curl -X POST -H "X-Admin-Token: secret" -H "Content-Type: application/json" \
  -d '{"username":"bob"}' http://localhost:8080/api/users
```
```json
{
  "user": {"id": 2, "username": "bob", "token": "xyz789", "enabled": true},
  "sub_url": "http://your-server:8080/sub/xyz789",
  "sub_urls": {
    "full":        "http://your-server:8080/sub/xyz789",
    "links_txt":   "http://your-server:8080/sub/xyz789/links.txt",
    "links_b64":   "http://your-server:8080/sub/xyz789/links.b64",
    "compact":     "http://your-server:8080/c/xyz789",
    "compact_txt": "http://your-server:8080/c/xyz789/links.txt",
    "compact_b64": "http://your-server:8080/c/xyz789/links.b64",
    "singbox":     "http://your-server:8080/sub/xyz789/singbox",
    "hysteria2":   "http://your-server:8080/sub/xyz789/hysteria2"
  }
}
```
When `api_user_inbound_tag` is set, the user is also added to Xray (config file or API).

#### Get a user
```bash
GET /api/users/{id}
```

#### Delete a user
```bash
DELETE /api/users/{id}
```
`{id}` accepts a **numeric id** or a **username** (including email format like `alice@example.com`). Applies to `GET`, `DELETE`, `enable`, `disable`, `token`, `routes`, and `clients` routes.

When `api_user_inbound_tag` is set, the user is also removed from Xray.

#### Example: create and delete (bash)

```bash
HOST="http://localhost:8080"
ADMIN="your-secret-admin-token"

# 1) Create user
CREATE_JSON=$(curl -sS -X POST "$HOST/api/users" \
  -H "X-Admin-Token: $ADMIN" \
  -H "Content-Type: application/json" \
  -d '{"username":"alice@example.com"}')
echo "$CREATE_JSON"

# 2) Delete by username (no jq needed)
curl -sS -X DELETE "$HOST/api/users/alice@example.com" \
  -H "X-Admin-Token: $ADMIN"
# {"status":"deleted"}

# — or by numeric id (needs jq)
USER_ID=$(echo "$CREATE_JSON" | jq -r '.user.id')
curl -sS -X DELETE "$HOST/api/users/$USER_ID" \
  -H "X-Admin-Token: $ADMIN"

# 3) Confirm gone
curl -sS -H "X-Admin-Token: $ADMIN" "$HOST/api/users/alice@example.com"
# {"error":"user not found"}
```

#### Enable / disable a user
```bash
PUT /api/users/{id}/enable
PUT /api/users/{id}/disable
```
When `api_user_inbound_tag` is set, the user is added to or removed from Xray accordingly.

#### Regenerate subscription token
```bash
POST /api/users/{id}/token
```
Returns new `{token, sub_url}`. The old URL stops working immediately.

#### List user's inbound connections
```bash
GET /api/users/{id}/clients
```
Shows which inbounds the user is enrolled in and whether each is enabled.

#### Add one inbound connection for an existing user
```bash
POST /api/users/{id}/clients
Content-Type: application/json

{
  "tag": "vless-xhttp-in",
  "protocol": "vless"
}
```
Example:
```bash
curl -H "X-Admin-Token: <admin-token>" \
  -X POST http://<host>:8080/api/users/16/clients \
  -d '{"tag":"vless-xhttp-in"}'
```
- `tag` is required.
- `protocol` is optional. If omitted, it is resolved by `tag` from synced inbounds, then falls back to `api_user_inbound_protocol`.
- If the user is already enrolled in this inbound, the existing client record is returned (idempotent behavior).

#### Enable / disable a specific connection
```bash
PUT /api/users/{userId}/clients/{inboundId}/enable
PUT /api/users/{userId}/clients/{inboundId}/disable
```
Use this to give a user access to only certain servers/protocols.

### Inbounds

#### List all synced inbounds
```bash
GET /api/inbounds
```
```json
[
  {
    "id": 1,
    "tag": "vless-reality",
    "protocol": "vless",
    "port": 443,
    "config_file": "/etc/xray/config.d/vless-reality.json"
  }
]
```

#### Trigger manual sync
```bash
POST /api/sync
```
Forces an immediate re-scan of `config_dir`. Useful after editing Xray configs.

### Balancer

#### Get balancer config
```bash
GET /api/config/balancer
```

#### Update balancer settings at runtime
```bash
PUT /api/config/balancer
Content-Type: application/json

{
  "strategy": "leastPing",
  "probe_url": "https://www.gstatic.com/generate_204",
  "probe_interval": "30s"
}
```

#### Reset to config file defaults
```bash
PUT /api/config/balancer
Content-Type: application/json

{"reset": true}
```

### Health check
```bash
GET /health
```
```json
{"status": "ok"}
```
No authentication required. Use this for uptime monitoring.

---

## Routing Rules

Raven Subscribe generates Xray client configs with a three-tier routing system:

```
User rules  →  Global rules  →  Built-in defaults
(highest priority)              (lowest priority)
```

### Built-in defaults

Every generated config includes these rules automatically:

- **Direct**: Russian services (Yandex, VK, Lamoda, etc.), private IPs, `geoip:ru`
- **Proxy**: `geosite:ru-blocked`, `geoip:ru-blocked`
- **Block**: Ads and public torrent trackers

### Add a global rule (applies to all users)

```bash
POST /api/routes/global
Content-Type: application/json

{
  "type": "field",
  "outboundTag": "direct",
  "domain": ["example.com", "geosite:cn"]
}
```

### Add a per-user rule

```bash
POST /api/users/{id}/routes
Content-Type: application/json

{
  "type": "field",
  "outboundTag": "block",
  "domain": ["ads.example.com"]
}
```

### Rule schema

```json
{
  "id": "optional-id",
  "type": "field",
  "outboundTag": "direct | proxy | block",
  "domain": ["example.com", "geosite:ru-blocked"],
  "ip": ["1.1.1.1/32", "geoip:ru"],
  "network": "tcp | udp",
  "port": "443",
  "protocol": ["http", "tls"],
  "inboundTag": ["socks"]
}
```

`outboundTag` must be one of: `direct`, `proxy`, `block`.

---

## Emergency Config Rotation

When a VPN blocking event (e.g. DPI detection) hits your main inbounds, emergency mode lets you instantly redirect **all subscriptions** to a pre-configured set of fallback inbounds — with a single API call. Clients that re-fetch their subscription URL automatically receive the fallback config.

When emergency mode is active, subscription responses include an `X-Emergency-Mode: active` header. If a user is not enrolled in any of the profile's inbounds, their normal subscription is served as a fallback.

### Workflow

1. Create an emergency profile with the inbound tags of your fallback server
2. When a block occurs: activate the profile
3. Clients that refresh their subscription automatically switch to fallbacks
4. When the block is resolved: deactivate — all clients revert to normal configs

### Create an emergency profile

```bash
POST /api/emergency/profiles
Content-Type: application/json

{
  "name": "CDN fallback",
  "description": "XHTTP over CDN, active when main Reality is blocked",
  "inbound_tags": ["vless-cdn-in", "vless-xhttp-v2-in"]
}
```

```bash
curl -X POST -H "X-Admin-Token: secret" -H "Content-Type: application/json" \
  -d '{"name":"CDN fallback","description":"fallback via CDN","inbound_tags":["vless-cdn-in"]}' \
  http://localhost:8080/api/emergency/profiles
```

Response:
```json
{"id": 1, "name": "CDN fallback", "description": "fallback via CDN", "inbound_tags": ["vless-cdn-in"]}
```

### Activate emergency mode

```bash
POST /api/emergency/activate
Content-Type: application/json

{"profile_id": 1}
```

```bash
curl -X POST -H "X-Admin-Token: secret" -H "Content-Type: application/json" \
  -d '{"profile_id":1}' http://localhost:8080/api/emergency/activate
```

Response:
```json
{
  "active": true,
  "profile_id": 1,
  "profile": {"id": 1, "name": "CDN fallback", "inbound_tags": ["vless-cdn-in"]},
  "activated_at": "2025-01-15T12:00:00Z"
}
```

### Deactivate emergency mode

```bash
POST /api/emergency/deactivate
```

```bash
curl -X POST -H "X-Admin-Token: secret" http://localhost:8080/api/emergency/deactivate
```

Response: `{"active": false}`

### Check emergency status

```bash
GET /api/emergency/status
```

### Emergency profile management

| Method | Endpoint | Description |
|--------|----------|-------------|
| `GET` | `/api/emergency/profiles` | List all profiles |
| `POST` | `/api/emergency/profiles` | Create profile |
| `GET` | `/api/emergency/profiles/{id}` | Get profile by ID |
| `PUT` | `/api/emergency/profiles/{id}` | Update profile |
| `DELETE` | `/api/emergency/profiles/{id}` | Delete profile (not allowed if active) |

> **Note:** You cannot delete a profile while it is the active emergency profile. Deactivate first.

---

## Supported Protocols & Transports

### Protocols

| Protocol | Core | Share link format | Notes |
|---|---|---|---|
| VLESS | Xray | `vless://uuid@host:port?...#tag` | Supports REALITY, TLS, plain |
| VMess | Xray | `vmess://base64(json)` | |
| Trojan | Xray | `trojan://password@host:port?...#tag` | |
| Shadowsocks | Xray | `ss://base64(method:pass)@host:port#tag` | Single and multi-user |
| SOCKS | Xray | — | No share link format |
| Hysteria2 | sing-box | `hysteria2://password@host:port?...#tag` | QUIC-based, Salamander obfuscation |

### Transport layers

| Transport | Description |
|---|---|
| TCP | Raw TCP, with optional HTTP header obfuscation |
| WebSocket | WS with path and host headers |
| gRPC | gRPC with serviceName |
| HTTP/2 | H2 with host and path |
| mKCP | UDP-based, with header types |
| QUIC | QUIC transport |
| HTTPUpgrade | HTTP upgrade handshake |
| XHTTP / SplitHTTP | Split HTTP for CDN-friendly connections |

### Security layers

| Security | Notes |
|---|---|
| TLS | Strips server certificates, sets `fingerprint: chrome` by default |
| REALITY | Auto-derives `publicKey` from server's `privateKey`, picks first `serverName` and `shortId` |

---

## sing-box / Hysteria2

Raven Subscribe can run alongside [sing-box](https://github.com/SagerNet/sing-box) and serve Hysteria2 subscriptions from the same service.

### How it works

When `singbox_config` is set, Raven parses the sing-box server config, discovers Hysteria2 inbounds and their users, and stores them in the same SQLite database alongside Xray users. Each user's subscription then includes Hysteria2 endpoints in `sub_urls`.

Xray sync and sing-box sync are fully independent — if one core is not installed, the other still works.

**Important:** Hysteria2 users are excluded from Xray JSON subscriptions (`/sub/{token}`, `/c/{token}`). They are served exclusively via the dedicated Hysteria2 endpoints below.

### Configuration

```json
{
  "server_host": "vpn.example.com",
  "admin_token": "your-secret-token",
  "base_url": "https://vpn.example.com",
  "singbox_config": "/etc/sing-box/config.json",
  "singbox_enabled": true,
  "xray_enabled": true
}
```

| Parameter | Default | Description |
|---|---|---|
| `singbox_config` | `""` | Path to sing-box server config file. When set, Raven syncs Hysteria2 inbounds from it. |
| `singbox_enabled` | auto | `true` when `singbox_config` is set. Set to `false` to temporarily disable without removing the path. |
| `xray_enabled` | `true` | Set to `false` to disable Xray sync (e.g. when running sing-box only). |

### Subscription endpoints for Hysteria2

| Endpoint | Format | Use with |
|---|---|---|
| `/sub/{token}/singbox` | sing-box JSON | sing-box client, NekoBox (sing-box mode) |
| `/sub/{token}/hysteria2` | `hysteria2://` share links | Hysteria2 app, Hiddify |
| `/sub/{token}/hysteria2.b64` | Base64-encoded links | Clients that require encoded input |

### Generated sing-box client config

`/sub/{token}/singbox` returns a ready-to-use sing-box client config:

```json
{
  "log": {"level": "warn", "timestamp": true},
  "inbounds": [
    {"type": "mixed", "tag": "mixed-in", "listen": "127.0.0.1", "listen_port": 2080}
  ],
  "outbounds": [
    {
      "type": "hysteria2",
      "tag": "hysteria2-in-0",
      "server": "vpn.example.com",
      "server_port": 443,
      "password": "<user-password>",
      "tls": {"enabled": true, "server_name": "vpn.example.com"}
    },
    {"type": "direct", "tag": "direct"},
    {"type": "block",  "tag": "block"}
  ],
  "route": {
    "auto_detect_interface": true,
    "final": "hysteria2-in-0"
  }
}
```

The `mixed` inbound listens on `127.0.0.1:2080` and accepts both SOCKS5 and HTTP proxy connections. All traffic is routed through the first Hysteria2 outbound by default.

### Salamander obfuscation

If your sing-box inbound has `obfs` configured, Raven automatically includes it in all generated links and configs:

```json
{
  "type": "hysteria2",
  "tag": "hysteria2-in",
  "listen_port": 443,
  "obfs": {
    "type": "salamander",
    "password": "your-obfs-password"
  },
  "users": [{"name": "alice@example.com", "password": "user-password"}],
  "tls": {"enabled": true, "server_name": "vpn.example.com"}
}
```

The generated `hysteria2://` share link will contain `?obfs=salamander&obfs-password=...` automatically, and the sing-box JSON config will include the `obfs` block in the outbound.

---

## Docker

### Run with Docker Compose

```yaml
# docker-compose.yml
services:
  raven-subscribe:
    image: ghcr.io/alchemylink/raven-subscribe:latest
    ports:
      - "8080:8080"
    volumes:
      - ./config.json:/etc/xray-subscription/config.json:ro
      - /etc/xray/config.d:/etc/xray/config.d:ro
      - raven-data:/var/lib/xray-subscription
    restart: unless-stopped

volumes:
  raven-data:
```

```bash
docker compose up -d
```

### Build from source

```bash
docker build -t raven-subscribe .
docker run -p 8080:8080 \
  -v ./config.json:/etc/xray-subscription/config.json:ro \
  -v /etc/xray/config.d:/etc/xray/config.d:ro \
  raven-subscribe
```

---

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

### Before submitting a PR

```bash
go test ./... -race
golangci-lint run --timeout=5m
```

### Release

```bash
make release VERSION=v1.2.3
```

---

## License

[MIT](LICENSE) © AlchemyLink
