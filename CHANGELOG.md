# Changelog

All notable changes to this project will be documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versioning follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

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
