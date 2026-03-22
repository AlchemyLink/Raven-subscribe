# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
# Build
make build                          # build binary to ./build/xray-subscription
make test-build                     # build + verify binary exists and is executable
CGO_ENABLED=0 go build -o /dev/null . # quick compile check

# Test
go test ./... -race -count=1        # all unit tests with race detector
go test ./internal/xray/... -run TestFoo -v  # single test
E2E_DOCKER=1 go test ./integration/... -count=1 -timeout 10m -v  # Docker E2E

# Quality gates (same as CI)
go vet ./...
~/go/bin/golangci-lint run --timeout=5m
~/go/bin/govulncheck ./...
~/go/bin/staticcheck ./...          # pinned v0.7.0
~/go/bin/gosec ./...                # pinned v2.25.0
go mod tidy && git diff --exit-code go.mod go.sum

# Release
make release VERSION=v0.1.0        # runs tests, tags, pushes → CI builds release
```

## Architecture

The service has four layers that interact in a clear flow:

```
Xray config.d (JSON files)
    └─ syncer.Sync()
          └─ xray.ParseConfigDirWith()   → ParsedInbound, ParsedClient
          └─ database.UpsertInbound / UpsertUser / UpsertUserClient
                └─ SQLite (single-writer, WAL mode)

HTTP request → api.Server.Router() (gorilla/mux)
    ├─ /sub/{token}, /c/{token}  → xray.GenerateClientConfig() → JSON response
    └─ /api/*  (X-Admin-Token)   → database reads/writes + xray writes
```

**`internal/config`** — loads `config.json`, no side effects. `Config.SubURLs(token)` returns all URL variants. `Config.XrayConfigFilePerm()` returns file mode for Xray JSON writes.

**`internal/syncer`** — fsnotify watcher + periodic ticker. Calls `xray.ParseConfigDirWith(dir, cfg.VLESSClientEncryption)` then upserts DB. `RestoreOnStartup()` re-adds API-created users to Xray after restart.

**`internal/xray`** — five files with distinct roles:
- `parser.go` — reads Xray server config files, extracts inbounds/clients into `ParsedInbound`/`ParsedClient`
- `generator.go` — builds complete client Xray JSON (`ClientConfig`) from DB data; handles balancer, routing, all protocols
- `configwriter.go` — writes new clients into Xray config files on disk (`AddClientToInbound`)
- `apiclient.go` — adds/removes clients via Xray gRPC `HandlerService` (`AddClientToInboundViaAPI`)
- `restore.go` — re-adds existing DB users to Xray on startup via API or config files

**`internal/database`** — SQLite via `modernc/sqlite` (pure Go, no cgo). Schema auto-migrates on startup. `SetMaxOpenConns(1)` — SQLite is single-writer. Key tables: `users`, `inbounds`, `user_clients`, `routing_rules`, `global_routing_rules`.

**`internal/api`** — gorilla/mux router. Admin endpoints require `X-Admin-Token` header (compared with `subtle.ConstantTimeCompare`). Rate limiting via token bucket per IP. Subscription endpoints look up user by token, call `GenerateClientConfig`, return JSON or share links.

## Two user management modes

**Mode 1 — read-only sync** (default): Raven reads existing Xray configs, discovers users from `email` fields, serves their subscriptions. No write-back to Xray.

**Mode 2 — API write-back** (`api_user_inbound_tag` set): API-created users are written to Xray. Two sub-modes:
- `xray_api_addr` empty → writes to config file in `config_dir`
- `xray_api_addr` set → adds via gRPC `HandlerService.AlterInbound`

## Key conventions

- Users identified by `username` in email format (`user@domain.com`). DB column is `email`; API JSON field is `username`.
- `/api/users/{id}` accepts numeric id OR username string.
- `StoredClientConfig` (JSON blob in `user_clients.config_json`) stores only client-side credentials. Server-side VLESS decryption key never enters the DB.
- VLESS Encryption: `cfg.VLESSClientEncryption` maps inbound tag → client enc string. When active, flow is forced to `xtls-rprx-vision` and Mux is disabled.
- Commits: no `Co-Authored-By` line. Author: findias <findias@gmail.com>.

## CI pipeline

`.github/workflows/test.yml` runs jobs in parallel: `lint`, `test`, `vet`, `staticcheck`, `mod-tidy-check`, `gosec`, `govulncheck`. `e2e` and `build` run only after all gates pass. Shared setup is in `.github/actions/setup-go-env` (composite action).
