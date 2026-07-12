# Config ↔ API Reference

Which `config.json` fields each API and subscription endpoint reads, and the
edge cases worth knowing. For the full parameter list and defaults, see the
[Configuration section of the README](../README.md#configuration).

## Config fields used by the API

| Config field | Used by | Default | Notes |
|---|---|---|---|
| `admin_token` | all `/api/*` | `""` | Empty = no auth. Send as header `X-Admin-Token` or query `admin_token`. |
| `rate_limit_sub_per_min` | `/sub/*`, `/c/*` | `0` | 0 = disabled. |
| `rate_limit_admin_per_min` | `/api/*` | `0` | 0 = disabled. |
| `server_host` | subscription, links | — | **Required** when a config file is used. |
| `base_url` | `sub_url` in responses | `http://localhost:8080` | Used for the `sub_url` field in user responses. |
| `config_dir` | add/remove users, sync | `/etc/xray/config.d` | Xray config files location. |
| `socks_inbound_port` | generated client config | `0` → 2080 | 0 = generator uses the default. |
| `http_inbound_port` | generated client config | `0` → 1081 | 0 = generator uses the default. |
| `balancer_strategy` | subscription config | `leastPing` | `random`, `roundRobin`, `leastPing`, `leastLoad`. |
| `balancer_probe_url` | subscription config | `https://www.gstatic.com/generate_204` | For `leastPing`/`leastLoad`. |
| `balancer_probe_interval` | subscription config | `30s` | |
| `api_user_inbound_tag` | create user, add client | `""` | When set, new users go to this inbound. |
| `api_user_inbound_protocol` | create user, add client | `""` | Fallback when the tag is not in `config_dir`. |
| `api_user_inbound_port` | create inbound fallback | `0` → 443 | Used when creating an inbound from the protocol. |
| `xray_api_addr` | add/remove users | `""` | Use the Xray gRPC API instead of config files. |
| `xray_config_file_mode` | writes to `config_dir` JSON | `0600` | Octal string, e.g. `0644` for group/other read (testing). |

## Endpoints and the config they read

### Subscription (public)

| Endpoint | Config read |
|---|---|
| `/sub/{token}`, `/c/{token}` | `server_host`, `socks_inbound_port`, `http_inbound_port`, `balancer_*` (0 ports fall back to 2080/1081) |
| `/sub/{token}/links`, `/c/{token}/links.*` | `base_url`, `server_host`, `socks_inbound_port`, `http_inbound_port` |
| `/sub/{token}/vless\|vmess\|trojan\|ss/*` | same as above |

### Admin API (`X-Admin-Token`)

| Endpoint | Config read | Notes |
|---|---|---|
| `GET/POST /api/users` | `admin_token`, `base_url`, `api_user_inbound_tag`, `api_user_inbound_protocol`, `config_dir`, `xray_api_addr` | The JSON `user` has no `email` field (Xray uses the username as the client email internally). |
| `GET/DELETE /api/users/{id}` | `admin_token`, `base_url`, `config_dir`, `xray_api_addr` | `{id}` is the numeric DB user id. |
| `PUT /api/users/{id}/enable\|disable` | `admin_token`, `config_dir`, `xray_api_addr` | |
| `POST /api/users/{id}/token` | `admin_token`, `base_url` | |
| `GET/POST/PUT /api/users/{id}/routes` | `admin_token` | |
| `GET/POST /api/users/{id}/clients` | `admin_token`, `config_dir`, `xray_api_addr`, `api_user_inbound_protocol` | |
| `PUT /api/users/{id}/clients/{inboundId}/enable\|disable` | `admin_token`, `config_dir`, `xray_api_addr` | Syncs the per-client state to Xray. |
| `GET /api/inbounds` | — | |
| `GET/PUT/POST/DELETE /api/routes/global` | `admin_token` | |
| `GET/PUT /api/config/balancer` | `admin_token`, `balancer_*` | |
| `POST /api/sync` | `admin_token`, `config_dir` | |

## Edge cases

1. **`config.Load("")`** — returns defaults without validation; `server_host` may be empty. For tests only — production must pass a config file path.
2. **Empty `config_dir`** — file-based add/remove of users fails. With `api_user_inbound_protocol` set, the inbound can be created in the DB, but the Xray config must exist elsewhere.
3. **`xray_api_addr` without `api_user_inbound_tag`** — restore-on-startup is skipped; create users with explicit inbounds in the request body.
4. **`socks_inbound_port` / `http_inbound_port` = 0** — the generator uses 2080 and 1081. No action needed.
