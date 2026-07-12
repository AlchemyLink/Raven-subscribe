# Multi-node — Usage Guide

Run one Raven control plane in front of several Xray nodes, behind a single set
of subscription URLs. Users are provisioned across the nodes automatically, and
each subscription lets the client spread traffic and fail over between them.

This guide is for **operators** running Raven-subscribe. For the architecture and
the reasoning behind it, see [multi-node-design.md](multi-node-design.md).

> **Single-node is unaffected.** With no `nodes` section in your config, none of
> this applies and behaviour is byte-for-byte identical to before. Reach for
> multi-node only when you actually want several entry servers.

---

## What it gives you

- **A wider entry surface.** Each user's subscription carries one outbound per
  node, under a balancer. The client spreads across nodes, which diversifies the
  entry IPs and ASNs a single token exposes — the property that matters most
  against censorship that blocks by destination IP.
- **Automatic failover.** The client picks the best node (least-ping /
  least-load) and moves off a dead one on its own. No server-side switchover.
- **Scale-out without duplication.** Add capacity or locations without standing
  up a second Raven and a second user database.

## Concepts

| Term | Meaning |
|---|---|
| **Control plane** | Your Raven-subscribe instance — the single source of truth for users. |
| **Node** | A remote Xray server, provisioned identically to the others. |
| **Homogeneous** | Every node shares the same inbounds, REALITY keys and SNI. They differ only in where clients connect (`public_host:public_port`) and where Raven reaches them (`api_addr`). Because of this, one client config works on **any** node. |
| **Placement** | Which nodes a given user is served from. |

## Before you start

Each node must be provisioned **before** Raven can use it:

- Xray is running the **same** inbound / REALITY configuration as your other
  nodes. The Ansible `role_xray_node` does this — see
  [INSTALL §6](INSTALL.md#6-multi-node-deployment) — or set it up by hand.
- Xray's gRPC API is reachable by the control plane, over WireGuard
  (recommended) or mTLS (step 2).
- Raven does **not** install or configure Xray. It only distributes users to
  nodes that are already up and identically configured.

---

## 1. Declare your nodes

Add a `nodes` array to `config.json`:

```jsonc
{
  "server_host": "vpn.example.com",
  "admin_token": "your-secret-token",
  "base_url": "https://vpn.example.com",

  "nodes": [
    {
      "name": "eu-1",                    // stable node id (used as the DB key)
      "api_addr": "10.7.0.1:10085",      // Xray gRPC API — a WireGuard/private address (step 2)
      "inbound_tag": "vless-reality-in", // the inbound users are added to
      "public_host": "eu1.example.com",  // where clients connect for this node
      "public_port": 443,
      "enabled": true                    // in rotation; omit → true
    },
    {
      "name": "eu-2", "api_addr": "10.7.0.2:10085",
      "inbound_tag": "vless-reality-in",
      "public_host": "eu2.example.com", "public_port": 443
    }
  ]
}
```

| Field | Required | Description |
|---|---|---|
| `name` | yes | Stable, unique node id. |
| `api_addr` | yes | `host:port` of the node's Xray gRPC API. Must be a private/WireGuard address unless guarded by `tls` or `allow_public_grpc`. |
| `inbound_tag` | yes | The inbound tag users are added to on this node. |
| `public_host` / `public_port` | yes | Where clients connect for this node. |
| `enabled` | no | `false` takes the node out of rotation (default `true`). |
| `tls` | no | mTLS on the gRPC dial to this node — see step 2. |
| `allow_public_grpc` | no | Acknowledges a public `api_addr` guarded some other way (e.g. an external mTLS sidecar). |

When `nodes` is present, the legacy single-node fields (`xray_api_addr`,
`api_user_inbound_tag`) are no longer used for provisioning; `server_host` still
serves as the default host for the balancer.

## 2. Secure the control channel

A node's gRPC API is **full control** over that node. It must never be reachable
in plaintext on a public address — Raven refuses to start if it is. Two options:

**WireGuard (recommended).** Put the node's Xray API on a WireGuard address and
list that address as `api_addr`. Encryption and mutual authentication come from
WireGuard; there are no certificates to manage. A private `api_addr` needs no
extra config.

**mTLS (for nodes without WireGuard).** Add a per-node `tls` block. Raven
presents a client certificate and verifies the node against your CA:

```jsonc
{
  "name": "eu-3",
  "api_addr": "203.0.113.7:10085",       // public address is OK — guarded by tls below
  "inbound_tag": "vless-reality-in",
  "public_host": "eu3.example.com", "public_port": 443,
  "tls": {
    "ca_cert":     "/etc/raven/pki/node-ca.pem",     // CA that signed the node's server cert
    "client_cert": "/etc/raven/pki/raven-client.pem",
    "client_key":  "/etc/raven/pki/raven-client.key",
    "server_name": "eu-3.internal"                   // optional; defaults to the api_addr host
  }
}
```

Certificates are read once at startup; a missing or unreadable cert stops Raven
rather than silently falling back to plaintext. The node side (server cert +
`clientAuth`) is provisioned by its Ansible role.

## 3. Place users on nodes

By default a new user is placed on **all enabled nodes**. To pin a user to a
subset, or to change placement later, use the admin API (`X-Admin-Token` header):

```bash
# Create a user on specific nodes (omit "nodes" → all enabled nodes)
curl -H "X-Admin-Token: $TOKEN" -X POST https://vpn.example.com/api/users \
  -d '{"username":"alice@vpn.example.com","nodes":["eu-1","eu-2"]}'

# Inspect / replace / drop a user's placement
curl -H "X-Admin-Token: $TOKEN"          https://vpn.example.com/api/users/alice@vpn.example.com/nodes
curl -H "X-Admin-Token: $TOKEN" -X POST  https://vpn.example.com/api/users/alice@vpn.example.com/nodes -d '{"nodes":["eu-1"]}'
curl -H "X-Admin-Token: $TOKEN" -X DELETE https://vpn.example.com/api/users/alice@vpn.example.com/nodes/eu-2

# List the configured nodes (read-only — nodes are managed in config, not created via API)
curl -H "X-Admin-Token: $TOKEN" https://vpn.example.com/api/nodes
```

The user's subscription then contains one balanced outbound per placed node.

## 4. Check node health

```bash
curl -H "X-Admin-Token: $TOKEN" https://vpn.example.com/api/sync/status
```

The response carries the usual single-node fields plus a `nodes` map:

```jsonc
"nodes": {
  "eu-1": { "reachable": true,  "users_target": 42, "users_present": 42, "last_apply_ok": true },
  "eu-2": { "reachable": false, "users_target": 42, "users_present": 0,  "last_error": "..." }
}
```

- `reachable` — the last gRPC dial to the node succeeded.
- `users_target` vs `users_present` — how many users should be on the node
  (from the DB) vs how many it currently reports.

## 5. Day-2 operations

- **Add a node.** Provision it (step *Before you start*), add it to the `nodes`
  array, and restart Raven. Existing users are backfilled onto the new node on
  the next reconcile.
- **Take a node out of rotation.** Set `"enabled": false` (or remove it from the
  config — it is then marked disabled, not deleted, so its placements survive if
  you bring it back). Clients drop it automatically via the balancer.
- **After a node restart, do nothing.** Xray keeps API-added users in memory, so
  a node restart drops them — but the reconcile loop re-applies each node's users
  every sync interval, so the node self-heals within one interval.

## Troubleshooting

| Symptom | Likely cause / fix |
|---|---|
| Node shows `reachable: false` | Wrong `api_addr`, WireGuard route down, or firewall. For mTLS, check the certs match. The reconcile loop retries every sync interval. |
| `users_present` below `users_target` | Node was recently (re)started or briefly unreachable; the next reconcile pass fills it. Persisting → check the node's Xray logs. |
| Raven won't start: *"api_addr … is not a private/loopback address"* | The node's gRPC is on a public address with no guard. Use a WireGuard address, add a `tls` block, or set `allow_public_grpc: true`. |
| Raven won't start: mTLS cert error | A configured `tls` cert is missing/unreadable. This is fatal by design (no silent plaintext fallback) — fix the path/permissions. |

## Limitations

- **Homogeneous nodes only.** All nodes share the same REALITY keys and inbound
  shape. Different per-node keys/protocols are not supported.
- **Per-node stats are not aggregated for quotas.** A user's traffic is split
  across nodes; summing it per user is on the observability roadmap.
- **No control-plane HA.** One Raven means one SQLite database.

## Learn more

- **Bring up a node (server side):** [INSTALL §6 — Multi-node deployment](INSTALL.md#6-multi-node-deployment).
- **Architecture & rationale:** [multi-node-design.md](multi-node-design.md).
