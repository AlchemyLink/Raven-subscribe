# Raven Subscribe — Installation Guide

## Prerequisites

- **Xray-core** installed and configured (inbound configs in `/etc/xray/config.d`)
- **Debian/Ubuntu** or similar Linux (instructions are for systemd)

---

## 1. Install the binary

### Option A: Download release

```bash
# AMD64 (most servers)
curl -Lo xray-subscription https://github.com/AlchemyLink/Raven-subscribe/releases/latest/download/xray-subscription-linux-amd64

# ARM64 (e.g. Raspberry Pi)
# curl -Lo xray-subscription https://github.com/AlchemyLink/Raven-subscribe/releases/latest/download/xray-subscription-linux-arm64

chmod +x xray-subscription
sudo mv xray-subscription /usr/local/bin/
```

### Option B: Build from source

```bash
git clone https://github.com/AlchemyLink/Raven-subscribe.git
cd Raven-subscribe
make build
sudo cp build/xray-subscription /usr/local/bin/
```

### Verify

```bash
/usr/local/bin/xray-subscription -version
```

---

## 2. Create user and directories

```bash
# Create xray user if missing (Xray package usually creates it)
sudo useradd -r -s /usr/sbin/nologin xray 2>/dev/null || true

# Config directory
sudo mkdir -p /etc/xray-subscription

# Data directory (database)
sudo mkdir -p /var/lib/xray-subscription
sudo chown xray:xray /var/lib/xray-subscription

# If using file-based sync (api_user_inbound_tag without xray_api_addr):
# Raven writes to config_dir — xray must own it
sudo chown -R xray:xray /etc/xray/config.d
```

### Xray `config_dir`: owner `xrayuser` and mode `-rwxr-xr-x` (0755)

If Xray runs as **`xrayuser`** (or any non-`xray` account), use the **same** `User=` in `xray-subscription.service` and align ownership and permissions:

```bash
# One-time: owner for all inbound JSON under config_dir
sudo chown -R xrayuser:xrayuser /etc/xray/config.d

# Files: -rwxr-xr-x (0755); directories: rwxr-xr-x
sudo find /etc/xray/config.d -type f -exec chmod 755 {} \;
sudo find /etc/xray/config.d -type d -exec chmod 755 {} \;
```

In **`/etc/xray-subscription/config.json`** set so Raven keeps this mode when it rewrites JSON:

```json
"xray_config_file_mode": "0755"
```

Without this, new writes default to **0600** (`-rw-------`). **Note:** 0755 makes configs world-readable; use only if your threat model allows it.

---

## 3. Configure

```bash
sudo cp config.json.example /etc/xray-subscription/config.json
sudo nano /etc/xray-subscription/config.json
```

**Minimum required:**

```json
{
  "server_host": "your-server.com",
  "admin_token": "your-secret-token",
  "base_url": "http://your-server.com:8080"
}
```

**With API user management** (Raven adds/removes users in Xray):

```json
{
  "server_host": "your-server.com",
  "admin_token": "your-secret-token",
  "base_url": "http://your-server.com:8080",
  "api_user_inbound_tag": "vless-reality",
  "api_user_inbound_protocol": "vless",
  "api_user_inbound_port": 443
}
```

**With VLESS Encryption** (post-quantum ML-KEM-768, Xray ≥ 25.x):

Generate the key pair with `xray vlessenc`, put the **server** string into the inbound `decryption` field in Xray config, and put the **client** string (public keys only) into Raven config:

```bash
# Generate key pair:
xray vlessenc
# Output — two lines:
#   Server (decryption): mlkem768x25519plus.native/xorpub/random.600s.Pad.PrivKey.Seed
#   Client (encryption): mlkem768x25519plus.native/xorpub/random.0rtt.Pad.PubKey.Client
```

`/etc/xray-subscription/config.json`:

```json
{
  "listen_addr": ":8080",
  "server_host": "vpn.example.com",
  "config_dir": "/etc/xray/config.d",
  "db_path": "/var/lib/xray-subscription/db.sqlite",
  "sync_interval_seconds": 60,
  "base_url": "https://vpn.example.com:8080",
  "admin_token": "a3f8c2d1e9b047fc82a1d3e6c5f092bb",
  "rate_limit_sub_per_min": 60,
  "rate_limit_admin_per_min": 30,
  "api_user_inbound_tag": "vless-reality-in",
  "xray_api_addr": "127.0.0.1:10085",
  "vless_client_encryption": {
    "vless-reality-in": "mlkem768x25519plus.native/xorpub/random.0rtt/1rtt.3Kh8A.wX9pR2mLqTzYvNcBdEsUoJfGiHkP4aA.CLT7mZ2NpRqLvXyBwJfGiHkP4aAeUsOdTcE",
    "vless-xhttp-in":   "mlkem768x25519plus.native/xorpub/random.0rtt/1rtt.3Kh8A.wX9pR2mLqTzYvNcBdEsUoJfGiHkP4aA.CLT7mZ2NpRqLvXyBwJfGiHkP4aAeUsOdTcE"
  }
}
```

> **Важно**: в `vless_client_encryption` — **только клиентская строка** (публичные ключи, безопасна для хранения в конфиге).
> Серверная строка (`decryption`) содержит приватные ключи и никогда не должна попадать в Raven config.

To add **every** new user to **all** inbounds Raven knows (instead of a single tag), use `"api_user_all_inbounds": true` and omit `api_user_inbound_tag` (or leave it empty).

**Generate a strong admin token:**

```bash
openssl rand -hex 32
```

---

## 4. Install systemd service

```bash
sudo cp xray-subscription.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable xray-subscription
sudo systemctl start xray-subscription
```

---

## 5. Verify

```bash
# Service status
sudo systemctl status xray-subscription

# Health check
curl http://localhost:8080/health

# List users (replace with your admin token)
curl -H "X-Admin-Token: your-secret-token" http://localhost:8080/api/users
```

---

## Troubleshooting

### "No such file or directory" when starting

The binary is not at `/usr/local/bin/xray-subscription`. Install it (step 1) or edit the service file:

```bash
sudo nano /etc/systemd/system/xray-subscription.service
# Change ExecStart= path to your binary location
```

### "unable to open database file (14)"

The `xray` user cannot write to the data directory:

```bash
sudo mkdir -p /var/lib/xray-subscription
sudo chown xray:xray /var/lib/xray-subscription
sudo systemctl restart xray-subscription
```

### Xray cannot read configs after Raven adds users

Raven and Xray must run as the same user. The service uses `User=xray`. Ensure:

```bash
# Xray runs as xray
grep -E "^User=" /etc/systemd/system/xray.service

# config_dir owned by xray
sudo chown -R xray:xray /etc/xray/config.d
```

### Behind reverse proxy (nginx/caddy)

Set `listen_addr` to `127.0.0.1:8080` so Raven only listens on localhost. Use `base_url` with your public domain and `https://`.

### `/sub/{token}` or `/c/{token}` returns "no inbound clients for this user"

`/api/users` lists rows in the `users` table. A **subscription** needs at least one row in **`user_clients`** (user linked to an inbound with credentials).

If **`api_user_inbound_tag`** or **`api_user_all_inbounds`** is set in Raven’s config, the **first** successful subscription request for a user with no `user_clients` will **add** them to the default inbound(s) automatically (same as at user creation).

That happens when:

1. **`api_user_all_inbounds`** is `true` — new users get a client on **every** inbound Raven knows (from the DB / `config_dir` sync).
2. **`api_user_inbound_tag`** is set — new users get a client on that one inbound only (ignored if `api_user_all_inbounds` is true).
3. **`POST /api/users/{id}/clients`** with `{"tag":"your-inbound-tag"}` — add an existing user to an inbound.
4. **Syncer** — users discovered from `config_dir` get `user_clients` when Xray configs are scanned.

If you created users with `POST /api/users` **without** `api_user_all_inbounds`, **without** `api_user_inbound_tag`, and without `inbounds` in the JSON body, they have a token but no subscription until you add clients (option 3) or enable options 1–2 for new users.

### User still listed after `DELETE /api/users/{id}`

If **`xray_api_addr`** is set, Raven used to remove the client only from the **running** Xray process while the **JSON files** in `config_dir` still contained that client. The syncer then re-imported the client and **recreated the user** in SQLite.

Current versions also remove the client from the on-disk inbound JSON when deleting a user. If you still see this on an old build: remove the client manually from `config_dir` or upgrade.

---

## File locations

| Path | Purpose |
|------|---------|
| `/usr/local/bin/xray-subscription` | Binary |
| `/etc/xray-subscription/config.json` | Config |
| `/var/lib/xray-subscription/db.sqlite` | Database |
| `/etc/xray/config.d/` | Xray inbound configs (Raven reads/writes) |
