# `multi-node` — Design

Status: implemented, shipped in v0.4.0.

> **This is the design / architecture document, written for contributors.** To
> **use** multi-node (configure nodes, place users, operate a fleet), read the
> [Multi-node Usage Guide](multi-node.md) instead.

> This document is maintained in English. A Russian translation is kept at [multi-node-design.ru.md](multi-node-design.ru.md) (may lag behind).

Scope: Raven-subscribe. Touches `internal/{config,database,api,syncer,xray,core}`.
It does **not** change the subscription output for single-node installs (see §10).
Issue: [#100 multi node support](https://github.com/AlchemyLink/Raven-subscribe/issues/100).
Related: multi-node builds on the `core.AdminAPI` write-path seam in `internal/core`.

## 1. Why

Normally one Raven manages exactly one Xray: it writes to the local `config.d`
and talks to a single `xray_api_addr`. To run several Xray servers this way you
would have to run a separate Raven — with its own SQLite database — next to each
one. That means duplicated user stores, tokens drifting out of sync, and several
places to administer.

The goal (#100): **one Raven owns all users and hands them out to several remote
Xray servers**, with no extra Raven per server. For small private deployments
this removes the main operational barrier to scaling out.

This turned out to be a small change, because the gRPC client
(`internal/xray/apiclient.go`) is **already stateless and multi-target**: every
function takes an `apiAddr` argument and dials fresh. The "one server" assumption
lives in just a handful of places (see §3), not in the wire layer.

## 2. What this is not (scope)

- **Not a second engine.** Every node is Xray. sing-box stays out of scope.
  Multi-node is independent of the engine abstraction.
- **Not heterogeneous nodes.** Nodes are **homogeneous**: identical inbounds,
  REALITY keys, SNI and VLESS encryption. They differ only in where clients
  connect (`public_host:public_port`) and where Raven reaches them (`api_addr`).
  This is the same "mirror config" already used for the RU bridge. Nodes with
  different protocols or keys are a separate project.
- **Not a deployment tool.** Raven does not install or configure Xray on a node —
  that is Ansible's job. Raven only distributes users to nodes that are already
  provisioned and identically configured.
- **Not a change to the single-node path.** With no `nodes` section in the
  config, behaviour is byte-for-byte identical to before (§10).
- **Not plaintext gRPC over the internet.** See §7 — this was a hard requirement,
  not a nice-to-have.

## 3. Where "one node" was baked in

Before multi-node, the single-server assumption lived in four layers:

- **config** — `XrayAPIAddr` (one gRPC address) and `APIUserInboundTag` (one
  target inbound).
- **DB model** — no concept of a node; users mapped directly to inbounds.
- **api CRUD** — a single `if apiAddr != "" { …gRPC } else { …file }` backend
  choice per operation.
- **syncer** — drift detection read the "have" set from the local `config.d`
  files, and health was one global snapshot.

The gRPC client (`xray/apiclient.go`) was the one layer that already worked with
any number of targets. Multi-node extends exactly those four layers and leaves
the gRPC layer alone.

## 4. Node model

```jsonc
// config.json — a new optional section. Absent → single-node, exactly as before.
{
  "server_host": "eu.example.com",        // legacy: becomes the implicit local node
  "xray_api_addr": "127.0.0.1:10085",     // legacy
  "api_user_inbound_tag": "vless-reality-in",

  "nodes": [
    {
      "name": "eu-1",                      // stable node id (used as the DB key)
      "api_addr": "10.7.0.1:10085",        // Xray gRPC API — on the WireGuard address (§7)
      "inbound_tag": "vless-reality-in",   // the inbound users are added to
      "public_host": "eu.example.com",     // what goes into the client's outbound
      "public_port": 443,
      "enabled": true,                     // node is in rotation (default true)
      "deploy": { "mode": "grpc" }         // optional; "grpc" (default) | "ssh_rsync" (reserved)
    },
    { "name": "eu-2", "api_addr": "10.7.0.2:10085",
      "inbound_tag": "vless-reality-in", "public_host": "eu2.example.com",
      "public_port": 443 },

    { "name": "eu-3",                      // a node WITHOUT WireGuard → gRPC over a public address, guarded by mTLS
      "api_addr": "203.0.113.7:10085",     // public address is allowed because a tls block is present (§7)
      "inbound_tag": "vless-reality-in", "public_host": "eu3.example.com",
      "public_port": 443,
      "tls": {                             // optional; mutual TLS on the gRPC dial to this node
        "ca_cert":     "/etc/raven/pki/node-ca.pem",       // CA that signed the node's server cert
        "client_cert": "/etc/raven/pki/raven-client.pem",
        "client_key":  "/etc/raven/pki/raven-client.key",
        "server_name": "eu-3.internal"     // optional; defaults to the api_addr host
      }
    }
  ]
}
```

Rules applied in `config.Load`:

- **No `nodes`** → Raven synthesizes one implicit node called `local` from the
  legacy fields. All downstream code always works with a list of nodes;
  single-node is simply the N=1 case.
- **`nodes` present** → the legacy fields are ignored for provisioning
  (`server_host` still serves as a fallback host for the balancer).
- **Validation** — each `name` is unique and non-empty; `api_addr` is non-empty;
  for a `grpc` node the address must be private, OR carry a `tls` block, OR set
  `allow_public_grpc: true` (see the anti-footgun guard in §7). A `tls` block, if
  present, requires `ca_cert`, `client_cert` and `client_key`.

## 5. DB model

Two additive tables. Raven-subscribe applies schema changes with its own
idempotent `migrate()` (plain `CREATE TABLE IF NOT EXISTS`) — it does **not** use
goose (goose is only in the dashboard). Existing single-node databases are
upgraded transparently.

```sql
CREATE TABLE IF NOT EXISTS nodes (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL UNIQUE,
    api_addr    TEXT NOT NULL,
    inbound_tag TEXT NOT NULL,
    public_host TEXT NOT NULL,
    public_port INTEGER NOT NULL,
    enabled     INTEGER NOT NULL DEFAULT 1,
    created_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- which users are placed on which node. No row = the user is not on that node.
CREATE TABLE IF NOT EXISTS user_nodes (
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    node_id INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, node_id)
);
CREATE INDEX IF NOT EXISTS idx_user_nodes_node ON user_nodes(node_id);
```

- **`nodes` is reconciled from the config at startup**: rows are upserted by
  `name`, and a node that disappears from the config is marked `enabled=0` rather
  than deleted, so its `user_nodes` rows survive.
- **Placement backfill** — a user with no placement rows is placed on all
  `enabled` nodes. On a single-node upgrade this puts every existing user on
  `local`, so behaviour does not change. The backfill is idempotent.

Key point: the credential blob (`user_clients`) stays **per-inbound**, not
per-node. Because nodes are homogeneous, one credential is valid on every node
that serves that `inbound_tag`. `user_nodes` controls only *placement*, never the
credentials — which keeps the schema small.

## 6. Changes by layer

### 6.1 `internal/core` — fan-out as `AdminAPI`

No new interface is needed. Multi-node is a **composite implementation** of the
existing `core.AdminAPI`:

```go
// internal/xray/fanout.go
type fanoutAdmin struct {
    targets []namedAdmin // one grpc admin per node
}

func NewFanoutAdmin(nodes []core.NodeTarget) core.AdminAPI { … }

// AddClient on the fan-out = AddClient on every target node.
// A partial success is reported upward but NOT rolled back (§6.4).
func (f *fanoutAdmin) AddClient(inboundTag, identity string, h core.AddClientHint) (string, error)
```

`AddClient` must return **one** credential to store in the DB. Since nodes are
homogeneous the credential is identical everywhere, so the fan-out takes it from
the first node that succeeds and checks that the others match (a mismatch means
the nodes have drifted apart and is logged).

The `core.AdminAPI` method signatures do **not** change — the fan-out hides
behind the same interface, so `api.Server` cannot tell one node from N.

Note on the core refactor: it is intentionally frozen at Phase 1 — the interfaces
are declared but `internal/api` and `internal/syncer` still import `internal/xray`
directly. Multi-node does not wait for the full refactor (§9); it adopts only the
write-path part of `core.AdminAPI`.

### 6.2 `internal/api` — CRUD and node selection

- `POST /api/users` accepts an optional `nodes: ["eu-1","eu-2"]`. Empty → all
  `enabled` nodes. This populates `user_nodes`.
- All user CRUD goes through the single `s.admin` (the fan-out) — no per-call
  backend branching.
- Admin endpoints: `GET /api/nodes`, and `GET/POST/DELETE /api/users/{id}/nodes`
  to inspect and change a user's placement.

### 6.3 `internal/xray/generator.go` — one outbound per node

For each node a user is placed on, the generator emits **one outbound** pointing
at `node.public_host:node.public_port`, inheriting the protocol, security and
REALITY settings from the `inbound_tag`. All of these outbounds go under the
**existing balancer** (`BalancerStrategy`), so the client picks a live node by
least-ping / least-load on its own.

At N=1 (single-node) the outbound is byte-for-byte identical to before (checked
by a golden test, §10).

### 6.4 `internal/syncer` — per-node drift and status

Xray's `HandlerService` can return an inbound's live user list over gRPC
(`GetInboundUsers`). That gives each remote node a **full reconcile over the
network**: read the "have" set from the node, take the "want" set from the DB,
and apply the difference with `AddUser` / `RemoveUser`. It is the same idea as the
local `config.d` drift check, but without reading files on disk.

The reconcile loop runs once per `SyncInterval` for every `enabled` node:

- `AddUser` on a user that already exists returns `"User X already exists."`, and
  `RemoveUser` on a missing one returns `"User X not found."`. The fan-out treats
  both as harmless (matched by message), so re-applying the same state is
  idempotent and does not spam errors.
- Per-node health = (gRPC reachable) + (drift count after the pass).

**A node restart wipes its gRPC-added users (see §12).** So the reconcile loop is
not only drift insurance — it is a **mandatory recovery mechanism**: after a
remote node restarts, its users are gone until the next pass, so a gRPC-only node
has an outage of at most one `SyncInterval`.

Health is reported per-node. `/api/sync/status` returns a `nodes` map keyed by
node name, each entry carrying reachability, the target vs. present user counts,
and apply errors. For single-node it is one entry, `local`, so the shape extends
additively and existing dashboards keep working.

## 7. Security (a hard requirement)

Xray's `HandlerService` gives **full control** over users and inbounds — and,
since it has no per-RPC access control, over outbounds too. Exposed on the public
internet without authentication or TLS, anyone who reaches the port can
add or remove users on every node. **Multi-node must never run plaintext gRPC
over a public network.**

The transport is chosen **per node, by `api_addr`**, in a single place —
`dialXrayAPI` / `resolveCredentials`: a node configured with mTLS dials over TLS,
every other address (WireGuard or loopback) dials plaintext. Two options, in
order of preference:

1. **gRPC over WireGuard (the default recommendation).** Bind Xray's API to the
   node's WireGuard address; encryption and mutual authentication are handled by
   WireGuard. The gRPC stays plaintext but is unreachable from outside the tunnel.
   This fits the existing EU/RU mesh and needs no certificates. Config: a private
   `api_addr`, no `tls` block.
2. **mTLS on gRPC (for nodes without WireGuard).** An optional per-node `tls`
   block `{ca_cert, client_cert, client_key, server_name?}`. Raven acts as the
   TLS client: it presents its client certificate, verifies the node's server
   certificate against the CA, and forces TLS 1.3. Certificates are read **once
   at startup** and the load is **fail-closed** — a broken certificate is fatal,
   never a silent downgrade to plaintext against a public address. The node's
   Ansible role provisions the matching server certificate and `clientAuth`.

Anti-footgun guard in `config.Load`: a `grpc` node with a public `api_addr` and
no protection refuses to start. The guard is satisfied by **any** of a
private/WG address, a `tls` block (mTLS is itself encryption + authentication),
or an explicit `allow_public_grpc: true` (an escape hatch for operators who
terminate mTLS with an external sidecar).

## 8. Durability across a node restart

gRPC changes are live but **in-memory only** (§12): they take effect instantly
but are lost when the node restarts. Writing users into a node's `config.d` gives
the opposite — durable but only applied on the next reload. The local node
already gets both, because Raven dual-writes to gRPC and to `config.d` and
restores users on startup.

A remote gRPC-only node has no local `config.d` that Raven can write, so its
durability comes from the reconcile loop (§6.4): after a restart its users are
restored within one `SyncInterval`. That is sufficient for small installs, so it
is what ships. For strict durability a future option is to also push into the
node's `config.d` over SSH/rsync (`deploy.mode: "ssh_rsync"` is reserved in the
schema but not yet implemented).

## 9. Implementation status

Delivered in phases, each merged as a separate PR (all green, single-node
behaviour unchanged at every step), and released in **v0.4.0**:

- **Phase 0** — adopt `core.AdminAPI` for the write-path.
- **Phase 1** — config `nodes` section + DB tables, additive and inert for
  single-node.
- **Phase 2** — `fanoutAdmin` fans writes out to all nodes.
- **Phase 3** — per-node outbounds in generated configs, under the balancer.
- **Phase 4** — node/placement API and per-node reconcile + status.
- **Phase 5** — WireGuard-default and optional mTLS transport; README/INSTALL
  docs; the Ansible node role in Raven-server-install.

## 10. Guarantee: the single-node output does not change

The core acceptance criterion. With **no `nodes`** in the config:

- all unit and integration tests pass;
- `/sub/{token}` and `/c/{token}` (every view) return a **byte-identical**
  response before and after, on the golden set;
- `/api/sync/status` contains exactly one entry, `local`.

With homogeneous `nodes=[A,B]`:

- a user placed on both gets a balancer of two outbounds;
- if B is down, the user still works through A and B shows `reachable:false`;
- a public `api_addr` without `tls`/`allow_public_grpc` refuses to start; with a
  `tls` block it starts; a broken cert is fatal (fail-closed).

## 11. Known limitations and follow-ups

- **Placement policy** — new users default to all `enabled` nodes, overridable
  per user. Different REALITY keys per node are out of scope (that would be
  heterogeneous nodes).
- **Emergency rotation** — the killswitch / inbound rotation currently targets
  one address. Fanning it out is nearly free through `fanoutAdmin` (it already
  covers `AddInboundFromJSON` / `RemoveInbound`) but is not wired up yet.
- **Per-node stats** — `xray-stats-exporter` scrapes one Xray. With balancing a
  user's traffic is split across nodes, so per-user totals must be summed across
  nodes. This matters for quotas/billing and belongs to the observability
  roadmap.
- **No HA for Raven itself** — one control plane means one SQLite. A
  highly-available Raven is a separate project.

## 12. Xray runtime facts behind this design (verified against `xray-core v1.260327.0`)

These runtime behaviours of Xray are what make the "one control plane → N nodes
over gRPC" scheme work, and why some parts of the design are the way they are.

- **gRPC user changes are in-memory only.** All user state lives in an in-memory
  validator; nothing is written to disk. So an Xray restart drops every user that
  was added over gRPC alone — only users present in `config.d` at startup
  survive. This is why the reconcile loop is a recovery mechanism, not just drift
  insurance (§6.4, §8).
- **Idempotent error messages.** Adding a user that already exists, or removing
  one that is absent, returns a specific message rather than changing state. The
  fan-out relies on matching those messages so that re-applying the same state is
  a no-op.
- **The live user list is readable.** `GetInboundUsers` returns the node's
  current users (both gRPC- and config-loaded), which is what makes a network
  reconcile possible.
- **Nodes share one trust domain.** Homogeneity requires identical REALITY keys
  on every node, so one client config authenticates across the whole balancer.
  The trade-off: compromising the key on any node compromises the REALITY
  identity of all of them. This is the same model as the RU bridge and should be
  documented as an explicit security property.
- **A node must be pre-provisioned by Ansible** with an `api` inbound exposing
  `HandlerService` on its WireGuard address, and an inbound structure identical to
  the other nodes. Raven does not configure Xray.

## 13. Future direction (not in scope)

Mature multi-node panels (Marzban, Marzneshin, Remnawave) all put a small
**agent** on each node instead of reaching Xray's gRPC directly across the
network. An agent adds durability, autonomy when the control plane is offline,
and a cleaner security boundary — at the cost of an extra component to build and
ship.

We deliberately chose the agentless design: at our scale, with a WireGuard mesh
and Ansible already in place, it is simpler and matches the request in #100. If
AlchemyLink ever grows to many nodes, the natural next step is a thin,
capability-confined `raven-node` agent (exposing only user operations, with
config pulled and signature-verified from git). Because the write-path already
goes through `core.AdminAPI`, that would be a swap of one implementation, not a
rewrite of `api`/`syncer`.
