# `multi-node` — Design Doc

Status: draft, 2026-06-07.

> This document is maintained in English. A Russian translation is kept at [multi-node-design.ru.md](multi-node-design.ru.md) (may lag behind).

Scope: Raven-subscribe. Touches `internal/{config,database,api,syncer,xray,core}`.
Does not change the subscription wire format for single-node installs (verifiable, see §10).
Issue: [#100 multi node support](https://github.com/AlchemyLink/Raven-subscribe/issues/100).
Related doc: [`internal-core-design.md`](internal-core-design.md) — multi-node sits on top of it.

## 1. Why

Today a single Raven manages exactly one Xray (local `config.d` + one
`xray_api_addr`). To run several Xray nodes, you have to stand up a separate
Raven on each node with its own SQLite — duplication of the user store,
token drift, N points of administration.

The request (#100): **one Raven as the authoritative user store → distributes
users to several remote Xray nodes**, without a separate Raven on each. For
small private installs this removes the main ops barrier.

Key research finding: the gRPC layer (`internal/xray/apiclient.go`) **is
already stateless and multi-target** — every function takes `apiAddr` as a
parameter and dials anew. The single-target assumption lives in just one config
field (`XrayAPIAddr`) and in the calling code. And `internal/core` (Phase 1
done) already introduces the `core.AdminAPI` interface with constructors
`NewGRPCAdmin(apiAddr)` / `NewFileAdmin(...)` — this is precisely the multi-node
insertion point.

## 2. Non-goals (explicit)

- **We do not introduce a second engine.** Multi-node ≠ multi-core. All nodes
  are Xray. Sing-box remains out of scope (see `internal-core-design.md` §2).
  Multi-node is orthogonal to the engine abstraction and does not require
  generalizing it.
- **We do not make nodes heterogeneous by protocol/REALITY.** Nodes are
  **homogeneous**: identical inbound structures, REALITY keys, SNI, VLESS
  Encryption. They differ only in `public_host:public_port` (and `api_addr`).
  This is the "mirror config" — the same principle already adopted for the
  bridge (mldsa65 compat: bridge mirrors EU). Heterogeneous nodes are a separate
  project, not now.
- **We do not introduce Xray deploy orchestration.** Raven does not install or
  configure Xray on nodes — that's Ansible's job. Raven only distributes users
  to already-provisioned, identically configured nodes.
- **We do not break the single-node path.** Absent a `nodes` section in the
  config, behavior is byte-identical to today's (CI invariant, §10).
- **We do not ship plaintext gRPC over the internet.** See §7 — this is a
  blocker, not a TODO.

## 3. Current binding to "one node" (ground truth)

Where "one Xray" is baked into the code:

| Point | What it assumes about single-node |
|---|---|
| `config.Config.XrayAPIAddr` (string) | one gRPC address |
| `config.Config.APIUserInboundTag` (string) | one target inbound |
| `config.Config.ServerHost` + `InboundHosts`/`InboundPorts` (map by tag) | one set of endpoints; host/port keyed by **tag**, not by node |
| `api/server.go` CRUD (~575-770) | `if apiAddr != "" { …ViaAPI } else { …file }` — one backend |
| `syncer/syncer.go:229` Mode-2 drift | the "have" set is read from the **local** `config.d` via `GetExistingIdentitiesInInbound` (files on disk) |
| `syncer/status.go` `SyncStatus` | one global health snapshot |
| `xray/apiclient.go` | **already multi-target** — the one place that does NOT need changing |

Logically the single-node assumption lives in 4 places: config, DB model,
CRUD routing in api, and drift/status in syncer. The doc's goal is to extend
exactly these 4, leaving the gRPC layer as-is and not touching the local path.

## 4. Node model

```jsonc
// config.json — new optional section. Absent → single-node, as today.
{
  "server_host": "eu.example.com",        // legacy: stays as the implicit local node
  "xray_api_addr": "127.0.0.1:10085",     // legacy
  "api_user_inbound_tag": "vless-reality-in",

  "nodes": [
    {
      "name": "eu-1",                      // stable node id (FK in the DB)
      "api_addr": "10.7.0.1:10085",        // gRPC HandlerService, ONLY on the WG address (§7)
      "inbound_tag": "vless-reality-in",   // which inbound to pour users into
      "public_host": "eu.example.com",     // what lands in the client config's outbound
      "public_port": 443,
      "enabled": true,                     // node is in the generator/sync rotation
      "deploy": {                          // OPTIONAL, only for file-backend nodes
        "mode": "grpc"                     // "grpc" (default) | "ssh_rsync"
      }
    },
    { "name": "eu-2", "api_addr": "10.7.0.2:10085",
      "inbound_tag": "vless-reality-in", "public_host": "eu2.example.com",
      "public_port": 443, "enabled": true },

    { "name": "eu-3",                      // node WITHOUT WG → gRPC over the public address under mTLS
      "api_addr": "203.0.113.7:10085",     // public address allowed because a tls block is present (§7)
      "inbound_tag": "vless-reality-in", "public_host": "eu3.example.com",
      "public_port": 443, "enabled": true,
      "tls": {                             // OPTIONAL, mTLS on the gRPC dial to this node
        "ca_cert":     "/etc/raven/pki/node-ca.pem",   // CA that signed the node's server-cert
        "client_cert": "/etc/raven/pki/raven-client.pem",
        "client_key":  "/etc/raven/pki/raven-client.key",
        "server_name": "eu-3.internal"     // empty → host from api_addr
      }
    }
  ]
}
```

Backward-compat rules (in `config.Load`):
- `nodes` absent/empty → synthesize **one** implicit node `name="local"` from
  the legacy fields (`xray_api_addr`/`api_user_inbound_tag`/`server_host`). All
  downstream code works only with `[]Node`, single-node being the special case
  N=1.
- `nodes` set → legacy fields are ignored for provisioning (but `server_host`
  remains the default for `HostForInbound`/the balancer if a node leaves it
  empty).
- Validation: `name` unique and non-empty; `api_addr` non-empty; with
  `mode="grpc"` the address must be in a private range OR have a `tls` block
  (mTLS) OR an explicit `allow_public_grpc:true` flag (anti-footgun against
  plaintext-over-the-internet, §7). The `tls` block, if set, requires
  `ca_cert`+`client_cert`+`client_key`.

## 5. DB model

Today: `users`, `inbounds`, `user_clients(user_id, inbound_id, config_json)`,
`app_settings`, `emergency_profiles`. There is no node concept.

Decision **A2 — per-node opt-in** (as the author requests: "enabled on selected
targets"). A goose migration, additive, default = no-op for single-node:

```sql
-- migration NNN_multi_node.sql  (+goose Up)
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

-- which users on which node. Absence of a row = user is NOT on the node.
CREATE TABLE IF NOT EXISTS user_nodes (
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    node_id INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, node_id)
);
CREATE INDEX IF NOT EXISTS idx_user_nodes_node ON user_nodes(node_id);
```

Backfill on upgrade (in `+goose Up`, idempotent):
1. If the config has one implicit node → create a `nodes('local', …)` row and
   `INSERT INTO user_nodes SELECT id, <local_node_id> FROM users` (all existing
   users → on the local node). Behavior does not change.
2. The config is the source of truth for `nodes` rows: at startup Raven
   reconciles `config.nodes` → the `nodes` table (upsert by `name`, mark
   `enabled=0` for those that vanished, without deletion — so that `user_nodes`
   FKs survive).

Invariant: `user_clients` (the credential blob) stays **per-inbound**, not
per-node. Since nodes are homogeneous (§2), the same credential is valid on all
nodes with that `inbound_tag`. `user_nodes` governs only *placement*, not the
credentials. This keeps the migration small.

## 6. Changes by layer

### 6.1 `internal/core` — fan-out as `AdminAPI`

No new interfaces. Multi-node is a **composite implementation** of the existing
`core.AdminAPI`:

```go
// internal/xray/fanout.go
type fanoutAdmin struct {
    targets []namedAdmin // {name string; admin core.AdminAPI (grpc per node)}
}

func NewFanoutAdmin(nodes []core.NodeTarget) core.AdminAPI { … }

// AddClient on the fan-out = AddClient on each target node; collects per-node
// results. A partial success is NOT rolled back (see §6.4 — this is by design:
// "partial failures remain visible"), but reported upward.
func (f *fanoutAdmin) AddClient(inboundTag, identity string, h core.AddClientHint) (string, error)
```

Important: `AddClient` must return **one** `storedConfigJSON` to write to the
DB. Since nodes are homogeneous, the credential is identical across all — take
it from the first successful node and cross-check the rest (a mismatch =
config drift between nodes, log loudly). This closes the open question "which
config_json to write".

The signatures of `core.AdminAPI` (`AddClient/RemoveClient/AddInboundFromJSON/…`)
**do not change** — the fan-out hides behind the same interface. `api.Server`,
having received an `admin core.AdminAPI` field, does not notice the difference
between one node and N.

**State of the core refactor (verified 2026-06-07):** frozen at Phase 1 — all
4 interfaces and value types are declared (`internal/core/*.go`,
`TestCoreInvariants`), but `internal/xray` **does not implement** them (no
`impl.go`, no `NewGRPCAdmin/NewFileAdmin`), and `api`/`syncer` import `xray`
directly — **28 call sites**. So multi-node does not wait for the whole
refactor (§9): we take a **narrow adoption** — implement only `core.AdminAPI`
(write-path) + injection into `Server`. The full migration of builder/parser/syncer
(Phase 3/4 of internal-core-design) is left as a separate project.

### 6.2 `internal/api` — CRUD and node selection

- `POST /api/users` accepts an optional `nodes: ["eu-1","eu-2"]`. Empty → all
  `enabled` nodes (or the default policy). Populates `user_nodes`.
- CRUD goes through `s.admin` (= fanoutAdmin) — no `if apiAddr` branches
  (which Phase 3 of the core refactor removes anyway).
- New admin endpoints: `GET/POST/DELETE /api/nodes`, `POST /api/users/{id}/nodes`.

### 6.3 `internal/xray/generator.go` — outbound per node

Today the generator makes outbound(s) from the inbound structure +
`InboundHosts`/`InboundPorts` (keyed by tag). For multi-node:

- for each node where the user is placed (`user_nodes`), emit **one outbound**
  with `node.public_host:node.public_port`, inheriting protocol/security/REALITY
  from the `inbound_tag` structure;
- put all these outbounds under the **existing balancer** (`BalancerStrategy`).
  The client picks a live node itself by leastPing/leastLoad — for free, out of
  the box.

This is exactly the "subscription generation includes target endpoint selection"
from the issue: the endpoints = the user's nodes, the selection = the balancer.

Wire invariant: at N=1 (single-node) the outbound is byte-identical to today's
(golden test, §10).

### 6.4 `internal/syncer` — per-node drift/status

**Verified 2026-06-07 against `xray-core v1.260327.0`:** HandlerService **returns
the inbound's user list over gRPC** — `GetInboundUsers(tag, email="")` →
`um.GetUsers(ctx)` (the whole list), plus `GetInboundUsersCount(tag)` and
`ListInbounds(isOnlyTags)`
(`app/proxyman/command/command.go:125-163`, `command.proto:48-97`). These RPCs
**are not wrapped** in our `internal/xray/apiclient.go` today — but they are
available.

Consequence: a remote node has a **full reconcile over the network** — take the
"have" set from `GetInboundUsers`, "want" from the DB, resolve the diff with
`AddUser`/`RemoveUser`. This is the same semantics as the local config.d path,
but without a dependency on files on disk.

The model for remote gRPC nodes is a **real diff (DB=truth)**:
- periodically (per `SyncInterval`) for each `enabled` node:
  `have = GetInboundUsers(tag)`; `want = user_nodes ∩ inbound`;
  add `want\have` via `AddUser`, optionally remove `have\want`;
- `AddUser` on a duplicate → `"User X already exists."`; `RemoveUser` on a
  missing one → `"User X not found."` — the fan-out **must** swallow both as
  benign (string-match), otherwise idempotent re-apply spams errors. Right now
  apiclient swallows the exist error only for AddInbound (see §13);
- per-node health = (gRPC reachable) + (drift count after diff).

**⚠️ A node restart wipes gRPC users (see §13).** The reconcile tick is not
just drift insurance but a **mandatory** recovery mechanism: after a remote node
restarts, all of its users disappear until the next pass. For gRPC-only nodes
this means an outage ≤ `SyncInterval`.

**Bonus simplification (out of multi-node scope, but it opens up):** the local
path today, in gRPC mode, reads the disk via `GetExistingIdentitiesInInbound`
(`syncer.go:237`). After wrapping `GetInboundUsers`, both branches
(local/remote) can be unified on gRPC, removing the disk read from drift
detection entirely. Not required for multi-node — flag it as a follow-up.

The local node (with `config_dir`), until unification, continues to use the
existing config.d-based drift from `syncXray()` **unchanged**.

`SyncStatus` → per-node:

```go
type MultiSyncStatus struct {
    Nodes map[string]NodeSyncStatus `json:"nodes"` // key = node.name
}
type NodeSyncStatus struct {
    SyncStatus               // reuse the existing struct
    Reachable   bool   `json:"reachable"`     // last gRPC dial succeeded
    LastApplyOK bool   `json:"last_apply_ok"`
    UsersTarget int    `json:"users_target"`  // how many there should be (DB)
    ApplyErrors int    `json:"apply_errors"`
}
```

`/api/sync/status` returns `MultiSyncStatus`. For single-node — one key
`"local"`, the shape extends additively (the dashboard reads `.nodes["local"]`).
This is exactly "sync status displays per-target rather than global" + "partial
failures remain visible".

## 7. Security (a blocker, not a TODO)

HandlerService = full control over users/inbounds (and, without per-RPC ACL,
over inbound/outbound — §13). Over the internet without auth/TLS this is a
critical hole: anyone on the network can add/remove users on all nodes.
**Multi-node must not ship with plaintext gRPC over the public network.** The
transport is chosen **per-node by `api_addr`** in the single chokepoint
`dialXrayAPI` (`resolveCredentials`): a node from the mTLS map → TLS, any other
address (WG/loopback) → plaintext. Two paths, in order of preference:

1. **gRPC over WireGuard only (default recommendation).** The Xray API listens
   on the node's WG address; encryption and mutual authentication are handled at
   the WG layer. gRPC stays plaintext but is unreachable from outside the tunnel.
   It fits onto our existing EU/RU mesh (`f3_second_ru_research`), requires no
   gRPC-TLS scaffolding or cert rotation. Config: `api_addr` in a private range,
   no `tls` block — validation passes via `isPrivateHostPort`.
2. **mTLS on gRPC (for nodes without WG).** Implemented (Phase 5): an optional
   per-node `tls` block `{ca_cert, client_cert, client_key, server_name?}`.
   Raven is the TLS client: presents the client-cert, verifies the server-cert
   against the CA; `server_name` empty → host from `api_addr`; TLS 1.3 forced.
   Certs are read **once at startup** (`main.configureNodeCredentials` →
   `xray.BuildTLSCredentials` + `SetNodeCredentials`), **fail-closed** — a
   broken cert = fatal, never a silent fallback to plaintext against a public
   address. Cert provisioning (server-cert + clientAuth on the Xray side) is
   done by the node's Ansible role.

Anti-footgun in `config.Load` (§4): `mode="grpc"` with a public `api_addr` and
no protection → startup error. The guard is lifted by **any** of: a private/WG
address, a `tls` block (mTLS by itself = encryption+authentication), or an
explicit `allow_public_grpc:true` (escape-hatch for external mTLS termination by
a sidecar).

## 8. SSH/rsync deploy — a durability mechanism, not just an option

I initially treated rsync as a second-class option. The Xray runtime research
(§13) re-classifies it: **gRPC mutations are ephemeral (runtime-only), rsync
into `config.d` gives durability across a node restart.** These are two
orthogonal axes:

| | live update | survives node restart |
|---|---|---|
| gRPC-only | ✅ instant | ❌ users lost until reconcile |
| rsync config.d only | ❌ only on the next pull/reload | ✅ |
| **gRPC + rsync** (like the local node) | ✅ | ✅ |

The local node already does both (dual-write gRPC + `config.d`,
`RestoreOnStartup`). A remote node needs, for the same durability, that Raven
write into its `config.d` — and that is precisely SSH/rsync.

Priority decision:
- **Iteration 1 — gRPC-only**, durability via frequent reconcile (outage ≤
  `SyncInterval` after a node restart). Sufficient for small installs.
  `deploy.mode="ssh_rsync"` is laid into the schema but returns "not implemented".
- **Iteration 2 (on demand) — gRPC + rsync** for durability. The downsides
  remain: SSH keys, sudo, a race with reload; off by default.

## 9. Migration plan

Phases are merged as separate PRs; on each, `make build && go test ./... -race`
and integration tests are green; prod is not touched until Phase 5.

### Phase 0 — narrow adoption of `core.AdminAPI` (NOT the whole refactor)
A precondition, but **only the write-path**, not the whole internal-core
migration:
- `internal/xray/impl.go`: `NewGRPCAdmin(apiAddr)` and `NewFileAdmin(dir, perm)`
  — thin wrapper methods over the existing free functions
  (`AddClientToInboundViaAPI`/`AddExistingClientToInboundViaAPI`/
  `RemoveUserFromInboundViaAPI`/`AddInboundFromJSONViaAPI`/`RemoveInboundViaAPI`
  and the file analogs). Static-check `var _ core.AdminAPI = (*grpcAdmin)(nil)`.
- `api.Server` gets an `admin core.AdminAPI` field; **5 scattered if/else
  branches** (`addUserToInbound` server.go:612-616, `removeUserFromXray`
  :682-697, `addClientToXray` :710-720, `removeClientFromXray` :729-743,
  `addUserToXray` :756-771) collapse into `s.admin.AddClient(...)` etc.
- The `parser`/`builder`/`sub_links` migration (Phase 3/4 of
  internal-core-design) **we do not touch** — they are not needed for multi-node.

**Risk:** low-medium (the write-path is localized in ~5 methods of one file).
**Acceptance:** existing unit+integration green; N=1 behavior identical;
`grep -n 'XrayAPIAddr' internal/api/server.go` → only in building `admin`, not
in the CRUD branches.

### Phase 1 — config + DB, additive, single-node no-op
`config.nodes` section + implicit-node synthesis; goose migration `nodes`/
`user_nodes` + backfill. Downstream does not use it yet — model only.
**Risk:** low. **Acceptance:** upgrade of an existing DB → all users on
`local`, subscriptions byte-identical.

### Phase 2 — `fanoutAdmin`
`internal/xray/fanout.go` implements `core.AdminAPI` over `[]NewGRPCAdmin`.
`main.go` builds the fanout from `nodes`. CRUD starts fanning out.
**Risk:** low-medium. **Acceptance:** unit on partial-failure
(2 nodes, one fails → user on the live one, status shows the second's failure).

### Phase 3 — generator per-node outbounds + balancer
`user_nodes` → one outbound per node under the balancer.
**Risk:** medium (wire format). **Acceptance:** golden test N=1 byte-for-byte
with the old one; new golden for N=2.

### Phase 4 — per-node status + dashboard
`MultiSyncStatus`, `/api/sync/status` extended; idempotent re-apply loop for
remote nodes; admin endpoints `/api/nodes`.
**Risk:** low. **Acceptance:** a down node shows as `reachable:false`, does not
bring down the sync of the others.

### Phase 5 — security + docs + Ansible
WG-bound gRPC as the default (recommendation); `allow_public_grpc` guard;
optional mTLS. **mTLS — DONE** (per-node `tls` block, §7): `dialXrayAPI`
resolves creds per-`api_addr`, WG/loopback stays plaintext, mTLS lifts the
public-grpc guard, fail-closed at startup, TLS 1.3. Remaining: README + INSTALL
multi-node section; an Ansible role for homogeneous nodes (cross-check with
Raven-server-install) — provision server-cert + `clientAuth` on the Xray gRPC
inbound. **Risk:** low.

## 10. Acceptance (the "didn't break the subscriber" invariant)

- With `nodes` **absent** from the config:
  - all unit + integration tests green;
  - `/sub/{token}` (all views: full / links_txt / links_b64 / compact) and
    `/c/{token}` give a **byte-identical** response before and after, on the
    golden set (VLESS+REALITY, VLESS+XHTTP, fallback);
  - `/api/sync/status` contains exactly one key `"local"` with the same fields.
- With `nodes=[A,B]` homogeneous:
  - a user placed on A and B gets a balancer of 2 outbounds;
  - B down → the user still works through A; `status.nodes["eu-2"].reachable=false`;
  - plaintext gRPC to a public address without `tls`/`allow_public_grpc` →
    startup refusal;
  - a node with a `tls` block and a public `api_addr` → startup allowed
    (mTLS = guard);
  - a `tls` block with a broken/missing cert → fatal at startup (fail-closed).
- `insecure.NewCredentials()` lives only in `resolveCredentials` (the default
  for WG/loopback nodes); all mTLS nodes are dialed via `credentials.NewTLS`
  from the `SetNodeCredentials` map (Phase 5 security review).

## 11. Open questions

- **Default placement policy for new users.** All `enabled` nodes, or is an
  explicit choice mandatory? Proposal: default = all `enabled`, overridable via
  `nodes:[...]` in `POST /api/users`. Decide in Phase 2.
- **Reality keys per-node or shared.** §2 fixes homogeneity (shared). If someone
  needs different keys — that's heterogeneous nodes, a separate project. Record
  it in the README as a limitation.
- **Emergency rotation × multi-node.** `fallback.go` brings an inbound up via
  gRPC on one address. With multi-node the killswitch/rotation must fan out too.
  Out of scope for the first iteration — but `fanoutAdmin` covers
  `AddInboundFromJSON`/`RemoveInbound` with the same fan-out, so it's nearly
  free. Flag it in Phase 4.
- **Stats/metrics per-node.** xray-stats-exporter currently scrapes one Xray.
  Multi-node → N exporters or one multi-target one. Out of scope for this doc
  (that's Raven-subscribe), but flag it for the observability roadmap.

## 12. What this does **not** give

- Does not make nodes heterogeneous (different protocols/keys) — §2.
- Does not orchestrate Xray deploy — that's Ansible.
- Does not solve the SPOF of Raven itself (one control-plane = one SQLite). HA
  Raven is another project (probably a replicated store, which we deliberately
  avoid, see internal-core-design §10).
- Does not increase a single node's throughput — this is about horizontal
  placement of users, not performance.

## 13. Viability of Xray in a multi-node regime (verified 2026-06-07)

A study of `xray-core v1.260327.0` (vendored) for the runtime semantics that
decide whether the "one control-plane → N Xray over gRPC" scheme works.

### 13.1 gRPC mutations are ephemeral (the central fact)

All user state is a `MemoryValidator` (`sync.Map`, `proxy/vless/validator.go:28`;
similarly trojan/vmess/shadowsocks). `AddInboundHandler`
(`core/xray.go:98`) is a runtime registration. Across all of `app/proxyman` and
`app/commander` there is **zero** disk persistence (`WriteFile`/`SaveConfig`).

**Consequence:** an Xray restart on a node wipes everyone added by gRPC alone.
Only users in `config.d` (loaded at startup) survive a restart.
- Local node: already closed by dual-write (gRPC + `config.d`) +
  `RestoreOnStartup` (`server.go:688`, `syncer.go:105`).
- Remote gRPC-only node: has no local `config.d` → its restart = a full loss of
  users until the next reconcile. **The reconcile tick is mandatory as recovery,
  not just as drift insurance.** Durability is via rsync (§8).

### 13.2 Error semantics (idempotency)

| Operation | On duplicate / absence | Source |
|---|---|---|
| `AddUser` of an existing one | `"User X already exists."` | `vless/validator.go:39`, `trojan/validator.go:23` |
| `RemoveUser` of an absent one | `"User X not found."` | `vless/validator.go:54`, `shadowsocks/validator.go:66` |
| `AddInbound` of an existing one | `*exist*` (already swallowed) | `fallback.go` |

The fan-out reconcile **must** string-match "already exists" (AddUser) and
"not found" (RemoveUser) as benign. The current `apiclient.go` swallows only
exist for AddInbound — for AddUser/RemoveUser it needs to be added.

### 13.3 `GetInboundUsers` returns runtime state

`GetUsers` → `validator.GetAll()` (`vless/inbound/inbound.go:256`) ranges over
the `email` map. The diff is correct (it sees both gRPC- and config-loaded
users). Edge case: a user with an **empty email will not be returned** (`GetAll`
iterates over `v.email`). For us the email is always set (username) — fine, but
for foreign configs flag it.

### 13.4 Nodes = a single trust domain (REALITY)

Homogeneity requires **identical** REALITY `privateKey`, `shortIds` (and
`mldsa65Seed`, if PQ) on all nodes — otherwise one client config won't
authenticate across the balancer. The cost: compromise of the key on any node =
compromise of the REALITY identity of all nodes. This is the same model as our
bridge (mirror EU, `feedback_mldsa65_compat`) — acceptable, but document it in
the README as an explicit security property.

### 13.5 Node precondition (Ansible, not Raven)

Every node must have:
- an `api` inbound + a routing rule `inboundTag:["api"]` + `services` with a
  list of `HandlerService` (users) and optionally `StatsService` (metrics);
- the API listener on the **WG address** (§7), not on `0.0.0.0`;
- an identical inbound structure (tag, protocol, REALITY) — homogeneous.

Raven does not configure Xray — nodes are provisioned by an Ansible role in
advance (Phase 5).

### 13.6 Stats are smeared across nodes

`StatsService` is per-instance. Under balancing a user's traffic is split across
nodes, and counters live on the one that served the session. Per-user
aggregation = the sum across nodes. It concerns `xray-stats-exporter` (N targets
or a multi-scrape), out of the scope of this doc, but critical for quotas/billing
— flag it in observability.

### 13.7 Verdict

The scheme is **viable** provided: (1) reconcile as recovery after a node
restart, (2) benign handling of idempotency errors, (3) homogeneous nodes with a
shared REALITY key in one trust domain, (4) gRPC over WG only, (5) nodes
pre-provisioned by Ansible with an `api` inbound. Durability across a restart
for gRPC-only nodes is limited to `SyncInterval` — for strict durability the
rsync path is needed (§8, iteration 2).

## 14. Industry patterns (enterprise Xray multi-node, research 2026-06-07)

How multi-node Xray is actually built in mature OSS panels — external validation
of our design.

### 14.1 Consensus: a node agent, NOT direct cross-network Xray gRPC

| Panel | Node component | Panel↔node | Engine on the node |
|---|---|---|---|
| **Marzban** | `marzban-node` (agent) | REST/HTTPS or RPyC, SSL certs | Xray (the agent owns the lifecycle) |
| **Marzneshin** (a Marzban fork "for scalability") | `marznode` (agent) | **gRPC + client SSL cert** (`CLIENT_SSL_CERT`) | Xray / Hysteria / sing-box (multi-backend) |
| **Remnawave** | `remnanode` (agent, NestJS+Xray) | config push over a "secure internal API", `NODE_PORT` firewalled only to the panel's IP | Xray |
| **Hiddify-Manager** | — (per-server install) | multi-node — an **open request** (issue #5111) | Xray/sing-box |

**Everyone with mature multi-node puts an agent on the node.** No one pokes the
Xray HandlerService directly over the network from the center. The agent gives
exactly what our raw gRPC lacks: **durability** (the agent holds the config on
the node → brings Xray up itself after a restart, not depending on a reconnect
from the panel), **autonomy** (Remnawave: the node works even if the panel is
offline), **a clean security boundary** (its own protocol + TLS + firewall-on-IP,
not raw Xray API), **config push** (the agent provisions inbounds, not just
users) and **stats collection**.

### 14.2 What this teaches our design

- **Our agentless choice is a deliberate departure from the mainstream, not
  ignorance.** Justified by scale (we are not a panel for thousands of nodes),
  the presence of a WG mesh and Ansible, and the issue author's explicit request
  "no separate Raven on each node". But the **durability gap is real precisely
  because it is why everyone else keeps an agent** — meaning reconcile+rsync
  (§8, §13.1) is not an option but a mandatory compensation.
- **Our live-delta via `AlterInbound` is BETTER than Marzban's.** Marzban
  historically rewrites `config.json` and **restarts Xray** on a user's
  change/expiry → tearing others' connections (3x-ui issue #4777, Marzban #105).
  We change users with hot gRPC without a restart — this is an advantage, keep it.
- **Config push we deliberately do NOT do** (inbounds are provisioned by
  Ansible) — agent-based panels do. For us it's fine: nodes are homogeneous and
  static.
- **Multi-backend (xray/hysteria/singbox) was chosen precisely by the
  scalability fork (marznode).** In `internal-core-design §2` we explicitly
  rejected it. The trade-off is conscious: we optimize for Xray-only, not for
  "any engine".
- **Per-node stats is an unsolved problem for everyone.** Marzban introduces a
  `usage_coefficient` (a per-node billing multiplier) + central collection. If
  we go into quotas/billing — a similar multi-target collection is needed
  (`xray-stats-exporter`, §13.6).

### 14.3 Growth path

If AlchemyLink ever grows into a product with many nodes/sales — **the agent
model (`raven-node`) becomes justified**: durability + autonomy + security
boundary will outweigh the cost of an extra component. Then the narrow
`core.AdminAPI` adoption (Phase 0) is reused: `raven-node` implements the same
interface locally, and the fan-out from the center goes to agents instead of
raw Xray. That is, **the current agentless design is not a dead end but a first
step**, compatible with a future agent. Performance note (for node sizing, not
the control-plane): Xray holds ~40-55 KB/conn, memory grows to ~1 GB and is not
released; at thousands of users OS tuning is needed (`fs.file-max`, `somaxconn`,
`nofile`) — the node, not Raven, hits the wall first.

## 15. The agent model and a single point of config management — an in-depth analysis

The request: consider the **agent** variant and a **single point of config
management** seriously. All four drivers are recognized as interesting:
durability, autonomy when the panel is offline, eliminating the Ansible↔Raven
split-brain, dynamic management of node config. This section analyzes them
without simplifications and arrives at a non-trivial conclusion: **a correctly
trimmed thin agent is safer than our own agentless variant** — that is, there is
an argument FOR an agent even at our scale, but not the one Marzban has.

### 15.1 Two independent axes (they must not be conflated)

"Agent" and "single point of config management" are **different** decisions:

- **Axis 1 — runtime-plane**: who applies user/lifecycle operations to Xray.
  Options: raw Xray gRPC from the center (A) ↔ an agent on the node (B/C).
- **Axis 2 — config-plane**: who is the source of truth for inbound/REALITY/routing.
  Options: git+Ansible (A/B) ↔ Raven as authority (C).

The drivers map onto different axes: durability/autonomy — **axis 1**;
split-brain/dynamic config — **axis 2**. So "let's do agents" and "let's do a
single config point" are two decisions, and they must be made separately.

### 15.2 Seizure re-appraisal: raw gRPC is covertly MORE dangerous than an agent

Earlier (§7, §14) I put agentless-A in the "low seizure radius" bucket, because
Raven does not *store* configs. This is **incomplete**. The raw Xray
HandlerService **has no per-RPC ACL** — whoever reached the port can do
everything: `AddInbound` (bring up a backdoor listener), `AddOutbound`
(redirect traffic), `RemoveInbound` (DoS), not just `AddUser`
(`app/proxyman/command/command.go` — all RPCs on one service without
authorization).

In model A, Raven reaches the HandlerService of **every** node over WG. That
means **seizing Raven = full control of the runtime config of all nodes**, even
without storing keys: the attacker adds their own inbound/outbound to the whole
fleet. "Doesn't store config" ≠ "can't change it".

| Model | Raven *stores* keys/config | Raven *can do* on the node | Seizure radius |
|---|---|---|---|
| **A** agentless raw-gRPC | no | **everything** (Add/RemoveInbound/Outbound) — Xray API without ACL | medium-high (full runtime control of the fleet) |
| **B** thin agent, capability-trimmed | no | **only** user-ops (the agent does not expose the inbound/outbound API) | **low** (max add/remove users — rotatable) |
| **C** full control-plane | **yes** (all keys) | everything + stores the truth | high |

**Conclusion, reversing §14:** a thin agent that exposes to Raven **only**
`AddUser/RemoveUser/GetUsers/GetStats` (but not `AddInbound` etc.) gives a
**smaller** seizure radius than our agentless raw gRPC. This is
capability-confinement, which Xray itself cannot do. So there is "a point to an
agent" under our threat model too — but the value is not durability (rsync
provides that too), it is **narrowing what a seized center can do to the nodes**.

### 15.3 How to build a config-plane without amplifying a seizure

If we want a single config point too (axis 2), but without "seized Raven →
pushes malware to all nodes" — the key is in **capability separation** and
**signing**:

1. **Pull, not push.** The agent pulls the config artifact itself (from
   git/CI/artifact store), Raven does not push it. Seizing Raven gives no push
   channel.
2. **Signed config bundles, the signing key offline.** The config is signed with
   the operator's key (laptop/CI/HSM), the agent rejects the unsigned/wrongly
   signed. A seized Raven can hand out only already-signed (old) bundles — it
   can't fabricate a new malicious one.
3. **Capability split = the main principle.** The always-on plane (Raven, users)
   holds only low-blast-radius operations; the high-value (keys/inbound/routing)
   lives in git+vault and arrives as a signed bundle. We separate "what is online
   and reachable" from "what is valuable".
4. **Per-node scoping.** An mTLS client-cert per node; the agent applies commands
   only for its node-id. A narrow Raven→agent credential (user-ops scope).

This turns "single config point" from a **push-authority** (dangerous) into a
**signed-distribution** (safe): the single logical source = git, the agent
verifies the signature, Raven in the config chain is at most transport, not
trusted.

### 15.4 What concretely "moves" in each model

| Object | A (current) | B (thin agent) | B+ (agent + signed config) | C (full plane) |
|---|---|---|---|---|
| Users (CRUD) | Raven→gRPC | Raven→agent | Raven→agent | Raven |
| User durability on restart | reconcile/rsync | agent (local store) | agent | agent |
| inbound structure | Ansible | Ansible | git→signed→agent pull | Raven push |
| REALITY keys/secrets | Ansible+vault | Ansible+vault | git+vault→signed | **Raven DB** |
| routing/outbounds | Ansible | Ansible | git→signed | Raven push |
| emergency rotation | Raven gRPC (exists) | Raven→agent | Raven→agent | Raven |
| stats | exporter scrapes Xray | agent relays | agent relays | agent relays |
| split-brain removed? | no (a contract-test option exists) | no | **yes** (one signed source) | yes |
| seizure radius | medium-high | low | low | high |

### 15.5 Drivers → what closes them

| Driver | Minimally sufficient | Note |
|---|---|---|
| Durability on restart | A+rsync **or** B | an agent is not required |
| Autonomy when the panel is offline | **B** (agent only) | rsync gives no management autonomy, only data |
| Kill split-brain | contract tests (cheap) **or** B+/C | git source + verify |
| Dynamic node-config management | B+ (signed pull) **or** C | C is more dangerous on seizure |

All four at once are closed by **B+** (thin agent + pull-based signed config),
**without** touching Raven as the key authority — that is, without the seizure
amplification of C.

### 15.6 Recommendation

**Max goal: B+ — a capability-trimmed `raven-node` + pull-based signed config
from git.** Closes all four drivers AND *improves* the seizure posture versus
the current agentless-A. Config authority stays in git/vault (we do not
duplicate Ansible as the source, but Ansible reduces to bootstrapping the agent;
config rendering can stay in Ansible-CI as the "signing" step).

**What NOT to do: naive C** (Raven stores keys + push-authority) — a single
point at the cost of the maximal seizure radius and vault duplication. Under our
threat model (A2 seizure-reduction, RU-compromise runbook) — a bad trade.

**Pragmatic interim (if B+ not now): A + rsync** (§8) — durability without an
agent, but **without** capability-confinement and without autonomy. We
consciously accept that a seized Raven has full runtime control of the fleet
(the risk is the same as today's single-node EU, just multiplied by the number
of nodes).

**An order compatible with the phases already described:**
- Phases 0-4 (§9) implement **A** — this is the base and the first step (the
  narrow `core.AdminAPI`).
- **B+ as Phase 6** (new, by decision): `raven-node` implements the same
  `core.AdminAPI` locally + a user-ops-only external API + config-pull+verify.
  The fan-out from the center switches from raw Xray gRPC to agents — **a swap
  of one `core.AdminAPI` implementation, without rewriting api/syncer**. This is
  what the narrow core seam in Phase 0 pays off for.

### 15.7 What to validate before B+ (open)

- **Agent language/deploy.** Go (one binary, like Raven/exporter; reuse
  apiclient) vs repeating Marzban's mistakes with a panel↔node version matrix.
- **Signed-bundle format.** minisign/cosign/age + a git tag? Who holds the
  signing key (laptop/CI/offline)? Binding to our vault flow.
- **Pull trigger.** The agent polls git? Or Raven sends "update yourself" (a
  signal only, not content — then content still comes from git, the push radius
  does not grow)?
- **Backward-compat with A.** A node can run in mode A (raw-gRPC) OR B+ (agent)
  — a config flag `node.mode`, to migrate nodes one at a time.
- **Cost.** A new `raven-node` repo (AGPL, like the others), CI, release, an
  Ansible role, integration tests agent↔Xray. Estimate: B+ ≈ 3-4 weeks vs
  ~1-1.5 weeks for A+rsync. The decision is a function of how much autonomy and
  capability-confinement are prioritized against the timeline.
