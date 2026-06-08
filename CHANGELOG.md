# Changelog

All notable changes to this project will be documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versioning follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [v0.3.3] - 2026-06-08

### Added
- **Default `xPaddingBytes "100-1000"` injected into XHTTP `extra`.** Every XHTTP client now pads each HTTP request/response body to a random length, flattening the packet-size distribution so RU TSPU volumetric/fingerprint DPI (net4people #490) cannot key on it. Previously padding only appeared if the server inbound set it explicitly, so most clients had none. Injected with the same precedence as `xmux` (server top-level → server-nested-in-`extra` → default) and nested inside `extra` (Xray discards siblings of a non-empty `extra`). Regression tests assert the default is present in `extra`, a server-provided value is preserved, and nothing leaks to the top level. **Clients must re-fetch their subscription** to pick it up.

## [v0.3.2] - 2026-06-07

### Changed
- **Balancer reverted to XHTTP-primary** (re-confirms v0.3.0; undoes the v0.3.1 Reality/Vision-primary flip). A containerized xray-lab measurement from the actual RU vantage (AS42610) proved the DPI **resets Reality+Vision on the client→relay first hop** (`connection reset by peer` to the relay; the EU server logs 0 resets), while **XHTTP sustains ~16 MB/s with 10/10 concurrent streams**. XHTTP-primary is also safer against the burstObservatory false-positive (#5897) that can mark a TSPU-reset Reality "healthy" and trap users on a dead path. The v0.3.1 flip had been based on a field report the lab now contradicts; "primary transport" is volatile — measure before changing.

## [v0.3.1] - 2026-06-07

### Fixed
- **XHTTP `xmux` is now nested inside `streamSettings.xhttpSettings.extra`** instead of being emitted as a top-level sibling. Xray's XHTTP transport (`infra/conf/transport_internet.go`) rebuilds its config from `extra` when present — copying back only `host`/`path`/`mode` from the outer object and **silently discarding every sibling field** (`xmux`, `scMaxEachPostBytes`, `xPaddingBytes`, `downloadSettings`). As a result the tuned client `xmux` shipped in v0.3.0 was **inert on every XHTTP client whose `extra` was non-empty** — which is always, since `xPaddingBytes` is present. `convertXHTTPSettings` now packs `xmux` and all advanced fields inside `extra`, so the anti-DPI connection rotation (net4people #490/#546) actually reaches the dialer. xmux precedence: server top-level → server-nested-in-`extra` → tuned default. Regression test asserts `extra.xmux` is set, server-nested xmux is preserved, and no top-level `xmux`/advanced field leaks.
- **Clients must re-fetch their subscription** to pick up the now-active xmux. Live connections are unaffected until they refetch.

### Changed
- **Balancer reverted to Reality/Vision-primary** (reverses the v0.3.0 XHTTP-primary selector). Field report 2026-06-07: on the current RU vantage VLESS+Reality+`xtls-rprx-vision` (TCP, via relay) is the working transport while XHTTP does not come up. The balancer `Selector` and Observatory `SubjectSelector` now target the non-XHTTP (Reality/Vision) outbound(s), and XHTTP is demoted to `FallbackTag` — engaged only when every non-XHTTP outbound is observed down. When no non-XHTTP outbound exists, balances across whatever proxies remain. DPI is a moving target; this is empirical ground truth overriding the 2026-06-05 first-hop measurement.

## [v0.3.0] - 2026-06-05

### Changed
- **XHTTP-primary balancer.** When XHTTP outbounds exist, the generated balancer `Selector` targets XHTTP tags only with the first Reality tag as `FallbackTag`, and the Observatory `SubjectSelector` probes XHTTP only. Traffic stays on XHTTP (which survives the 2026 RU TSPU first-hop behavioral kill) and engages Reality only when every XHTTP outbound is observed down. Prior behavior preserved when no XHTTP outbound exists.

### Added
- Tuned client `xmux` defaults for XHTTP outbounds (bounded reuse + randomized lifetime) as an anti-DPI connection-rotation measure. (NB: not actually effective until the v0.3.1 `extra`-nesting fix.)

## [v0.2.1] - 2026-05-25

### Fixed
- `streamSettings.network` `"raw"` alias normalized to `"tcp"` on JSON unmarshal — Xray-core v24.9.30+ renamed the bare-TCP transport name to `"raw"` (v2rayN 7.21.3+ emits this by default); downstream share-link / XHTTP / Mux logic now sees a single canonical value.

### Security
- Bumped `golang.org/x/net` v0.53.0 → v0.55.0 — fixes GO-2026-5026 (`idna.ToASCII` did not reject ASCII-only Punycode-encoded labels), reached via `parser.go` HTTP path.

### Changed
- Bumped `google.golang.org/grpc` 1.81.0 → 1.81.1
- Bumped `modernc.org/sqlite` 1.50.0 → 1.50.1
- Bumped `golang.org/x/crypto` 0.50.0 → 0.51.0
- Bumped `golang.org/x/sys` 0.44.0 → 0.45.0 (transitive)
- CI: `codecov/codecov-action` 6.0.0 → 6.0.1

---

## [Unreleased]

### Added
- `sub_urls` field in all user API responses with all subscription URL variants (`full`, `links_txt`, `links_b64`, `compact`, `compact_txt`, `compact_b64`)
- User lookup by username (including email format) in addition to numeric id — applies to all `/api/users/{id}/…` routes

### Fixed
- Timing attack on admin token validation — replaced `!=` with `subtle.ConstantTimeCompare`
- Typo `selcdn.ne` → `selcdn.net` in default routing rules
- Duplicate `okko.tv` entry in default routing rules
- `fmt.Printf` → `log.Printf` in `GenerateClientConfig` for consistent structured logging
- Dead variable `res` in `UpsertInbound` removed


- GitHub issue templates (bug report, feature request) with security advisory link
- `config.json.example` with all available configuration fields
- `SECURITY.md` with vulnerability reporting guidelines and deployment recommendations
- `CONTRIBUTING.md` with code quality gates, lint policy and `nolint` rules
- `dependabot.yml` for automated Go module and GitHub Actions dependency updates
- `CODEOWNERS` requiring maintainer review for CI, Dockerfile and database changes
- `PULL_REQUEST_TEMPLATE.md` with pre-merge checklist
- CI badges (Test, Security Scan, Go Report Card) in README
- `govulncheck` job in CI and Security Scan workflows
- `concurrency` and `timeout-minutes` on all workflow jobs
- `-race` flag on unit test runs in CI
- Support for all major Xray protocols: VLESS, VMess, Trojan, Shadowsocks, SOCKS
- Support for all transport layers: TCP, WebSocket, gRPC, HTTP/2, KCP, QUIC, HTTPUpgrade, XHTTP
- REALITY and TLS security layer handling
- Per-user and global routing rules API (`direct` / `proxy` / `block`)
- Docker Compose end-to-end test suite

### Changed
- Upgraded Go toolchain to **1.26** (closes all known stdlib vulnerabilities)
- Updated all direct dependencies to latest versions (`fsnotify`, `x/crypto`, `modernc/sqlite`)
- Replaced `golangci-lint-action@v6` with `v9.2.0` (supports Go 1.26 modules)
- All exported types, functions and packages now have Go doc comments

### Fixed
- All `errcheck` lint errors: `defer rows/body/watcher.Close()` now properly handle errors
- All `revive` lint errors: package comments and exported symbol documentation added
- All `gosec` findings: file permissions, subprocess variables, secret pattern false-positives suppressed with justification comments

---

## How to release

1. Update this file — move items from `[Unreleased]` to a new `[vX.Y.Z]` section.
2. Commit: `git commit -m "chore: release vX.Y.Z"`
3. Tag: `git tag vX.Y.Z && git push origin vX.Y.Z`
4. GitHub Actions `build.yml` will run tests, build binaries, create a release and push a Docker image automatically.
