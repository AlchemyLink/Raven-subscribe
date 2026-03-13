# xray-subscription

A subscription server for [Xray-core](https://github.com/XTLS/Xray-core) that:

- Generates **per-user xray client JSON configs** from server-side inbound configs
- Exposes a **unique subscription URL** per user for automatic updates in clients
- **Auto-syncs** users from `/etc/xray/config.d` on startup and on file changes
- Supports **all major Xray protocols**: VLESS, VMess, Trojan, Shadowsocks, SOCKS
- Supports **all transport layers**: TCP, WebSocket, gRPC, HTTP/2, KCP, QUIC, HTTPUpgrade, XHTTP
- Supports **REALITY** and **TLS** security layers (auto-derives public key from private key)
- Stores users and mappings in **SQLite**
- Admin REST API for user and inbound management

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
  "admin_token": "choose-a-strong-secret-token"
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
| `GET` | `/health` | Health check |

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
| `GET` | `/api/users/{id}/clients` | List user's inbound mappings |
| `PUT` | `/api/users/{userId}/clients/{inboundId}/enable` | Enable specific inbound for user |
| `PUT` | `/api/users/{userId}/clients/{inboundId}/disable` | Disable specific inbound for user |

### Inbounds & Sync

| Method | URL | Description |
|--------|-----|-------------|
| `GET` | `/api/inbounds` | List all detected inbounds |
| `POST` | `/api/sync` | Trigger manual sync from config.d |

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
    {"tag": "socks", "port": 1080, "protocol": "socks"},
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
    "domainStrategy": "IPIfNonMatch",
    "rules": [
      {"type": "field", "outboundTag": "block",  "domain": ["geosite:category-ads-all"]},
      {"type": "field", "outboundTag": "direct", "ip":     ["geoip:private", "geoip:cn"]},
      {"type": "field", "outboundTag": "direct", "domain": ["geosite:cn"]},
      {"type": "field", "outboundTag": "vless-reality-0", "network": "tcp,udp"}
    ]
  }
}
```

---

## Security Notes

- The `admin_token` protects all `/api/*` endpoints; keep it secret.
- Subscription URLs (`/sub/{token}`) are public by design — share only with the user.
- Use `POST /api/users/{id}/token` to rotate a compromised token.
- Run behind a reverse proxy (nginx/caddy) with HTTPS in production.
