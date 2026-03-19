# Contributing

## Quality Gate

All pull requests should pass:

- `go test ./...`
- `E2E_DOCKER=1 go test ./integration/... -count=1` (when touching integration paths)
- `golangci-lint run --timeout=5m -E gosec -E misspell -E revive`

## Lint Policy

Use this priority model for lint issues:

- **P1 (must fix):** issues that fail CI with low-risk fixes (unchecked errors, unsafe file mode in tests, unused parameters, context argument order).
- **P2 (fix or document):** style and naming warnings that are safe to fix without API/JSON compatibility risk.
- **P3 (explicit exception):** warnings where fixing can break protocol compatibility or public contracts.

## `nolint` Rules

`//nolint` is allowed only when:

- changing names/behavior can break compatibility (for example, Xray JSON field names),
- and the suppression is **narrow** (single line or field),
- and it includes a short reason.

Example:

```go
//nolint:revive // Keep Xray-compatible JSON field naming.
AlterId int `json:"alterId,omitempty"`
```

Do not add package-wide or file-wide blanket suppressions.

## Security Notes For Tests

For test helper files:

- Use file permissions `0o600` for Xray config files. Run Raven as the same user as Xray (e.g. `User=xray` in systemd).
- Avoid obvious hardcoded credential names/values that trigger `gosec` (`G101`).
- Prefer neutral constants for test headers/tokens.

