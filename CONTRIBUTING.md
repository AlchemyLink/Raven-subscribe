# Contributing

## License and Developer Certificate of Origin

This project is licensed under [AGPL-3.0-or-later](./LICENSE). By submitting a contribution, you agree that your contribution is licensed under the same terms.

### Sign-off requirement

We use the [Developer Certificate of Origin](https://developercertificate.org/) (DCO) v1.1 to certify the provenance of contributions. Every commit must include a `Signed-off-by:` trailer:

```text
Signed-off-by: Your Real Name <your.email@example.com>
```

Add it automatically with `git commit -s`. The name and email must match the commit author identity. CI rejects commits without a sign-off.

### Full DCO text

```
Developer Certificate of Origin
Version 1.1

Copyright (C) 2004, 2006 The Linux Foundation and its contributors.

Everyone is permitted to copy and distribute verbatim copies of this
license document, but changing it is not allowed.


Developer's Certificate of Origin 1.1

By making a contribution to this project, I certify that:

(a) The contribution was created in whole or in part by me and I
    have the right to submit it under the open source license
    indicated in the file; or

(b) The contribution is based upon previous work that, to the best
    of my knowledge, is covered under an appropriate open source
    license and I have the right under that license to submit that
    work with modifications, whether created in whole or in part
    by me, under the same open source license (unless I am
    permitted to submit under a different license), as indicated
    in the file; or

(c) The contribution was provided directly to me by some other
    person who certified (a), (b) or (c) and I have not modified
    it.

(d) I understand and agree that this project and the contribution
    are public and that a record of the contribution (including all
    personal information I submit with it, including my sign-off) is
    maintained indefinitely and may be redistributed consistent with
    this project and the open source license(s) involved.
```

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

