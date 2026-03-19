# API ↔ Config Compatibility Report

## Summary

All API endpoints correctly use configuration fields. Defaults and fallbacks are applied where needed. One gap was fixed: enable/disable of a specific client (`PUT /api/users/{id}/clients/{inboundId}/enable|disable`) now syncs to Xray, matching the behavior of user-level enable/disable.

---

## Config Fields Used by API

| Config Field | Used By | Default | Notes |
|--------------|---------|---------|-------|
| `admin_token` | All `/api/*` | `""` | Empty = no auth. Header `X-Admin-Token` or query `admin_token`. |
| `rate_limit_sub_per_min` | `/sub/*`, `/c/*` | `0` | 0 = disabled. |
| `rate_limit_admin_per_min` | `/api/*` | `0` | 0 = disabled. |
| `server_host` | Subscription, links | — | **Required** when config file is used. |
| `base_url` | SubURL in responses | `http://localhost:8080` | Used for `sub_url` in user responses. |
| `config_dir` | Add/remove users, sync | `/etc/xray/config.d` | Xray config files location. |
| `socks_inbound_port` | Generated client config | `0` → 2080 | 0 = use default in generator. |
| `http_inbound_port` | Generated client config | `0` → 1081 | 0 = use default in generator. |
| `balancer_strategy` | Subscription config | `leastPing` | random, roundRobin, leastPing, leastLoad. |
| `balancer_probe_url` | Subscription config | `https://www.gstatic.com/generate_204` | For leastPing/leastLoad. |
| `balancer_probe_interval` | Subscription config | `30s` | JSON: `balancer_probe_interval`. |
| `api_user_inbound_tag` | Create user, add client | `""` | When set, new users go to this inbound. |
| `api_user_inbound_protocol` | Create user, add client | `""` | Fallback when tag not in config_dir. |
| `api_user_inbound_port` | Create inbound fallback | `0` → 443 | Used when creating inbound from protocol. |
| `xray_api_addr` | Add/remove users | `""` | Use Xray gRPC API instead of config files. |

---

## Endpoint ↔ Config Mapping

### Subscription (public)

| Endpoint | Config Used | Compatibility |
|----------|-------------|---------------|
| `/sub/{token}`, `/c/{token}` | ServerHost, SocksInboundPort, HTTPInboundPort, balancer* | OK — 0 ports fall back to 2080/1081 |
| `/sub/{token}/links`, `/c/{token}/links.*` | BaseURL, ServerHost, SocksInboundPort, HTTPInboundPort | OK |
| `/sub/{token}/vless|vmess|trojan|ss/*` | Same as above | OK |

### Admin API

| Endpoint | Config Used | Compatibility |
|----------|-------------|---------------|
| `GET/POST /api/users` | AdminToken, BaseURL, APIUserInboundTag, APIUserInboundProtocol, ConfigDir, XrayAPIAddr | OK |
| `GET/DELETE /api/users/{id}` | AdminToken, BaseURL, ConfigDir, XrayAPIAddr | OK |
| `PUT /api/users/{id}/enable|disable` | AdminToken, ConfigDir, XrayAPIAddr | OK |
| `POST /api/users/{id}/token` | AdminToken, BaseURL | OK |
| `GET/POST/PUT /api/users/{id}/routes` | AdminToken | OK |
| `GET/POST /api/users/{id}/clients` | AdminToken, ConfigDir, XrayAPIAddr, APIUserInboundProtocol | OK |
| `PUT /api/users/{id}/clients/{inboundId}/enable|disable` | AdminToken, ConfigDir, XrayAPIAddr | OK — **fixed**: now syncs to Xray |
| `GET /api/inbounds` | — | OK |
| `GET/PUT/POST/DELETE /api/routes/global` | AdminToken | OK |
| `GET/PUT /api/config/balancer` | AdminToken, BalancerStrategy, BalancerProbeURL, BalancerProbeFreq | OK |
| `POST /api/sync` | AdminToken, ConfigDir | OK |

---

## Edge Cases

1. **`config.Load("")`** — Returns defaults without validation. `server_host` can be empty. Use only for tests; production should use a config file path.

2. **Empty `config_dir`** — Add/remove user will fail when using file-based sync. With `api_user_inbound_protocol` set, inbound can be created in DB; Xray config must exist elsewhere.

3. **`xray_api_addr` without `api_user_inbound_tag`** — Restore on startup skips. Create user with explicit inbounds in body.

4. **`socks_inbound_port` / `http_inbound_port` = 0** — Generator uses 2080 and 1081. No change needed.

---

## Changes Made

- **`setClientEnabled`** — Enable/disable of a specific client now syncs to Xray (config or API), consistent with user-level enable/disable.
- **`GetUserClientByUserAndInbound`** — New DB method to fetch tag and client config for sync.
