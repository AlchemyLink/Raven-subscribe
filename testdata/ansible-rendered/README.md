# Ansible-rendered Xray fixtures

This directory holds a snapshot of the Xray inbound JSON files that
`Raven-server-install` renders from its Ansible templates
(`roles/xray/templates/conf/inbounds/*.j2`). The snapshot drives
`internal/xray/contract_test.go`, which is the cross-repo guardrail
against schema drift between Ansible-rendered configs and the parser
that consumes them at runtime.

## Why this matters

`raven-subscribe` reads `/etc/xray/config.d/*.json` on the EU VPS, parses
each inbound with `ParseConfigDirWith`, and uses the result both to
populate the SQLite DB and to generate VLESS/Trojan/etc subscription URIs
served to clients. Ansible and `raven-subscribe` are independent code
bases joined only by these JSON files — when an Ansible template starts
emitting a renamed or restructured field, the parser silently produces
wrong URIs and **every user loses VPN access** until someone notices.

The contract test parses these fixtures and asserts the shape every
field-level consumer cares about. If you change an Ansible inbound
template and the corresponding parser/generator code is out of sync,
the test fails with a concrete diff instead of a 4 AM page.

## When to refresh

Refresh when **either** repo touches the inbound JSON shape:

- Adding/removing/renaming a top-level inbound field (`tag`, `port`,
  `protocol`, `streamSettings`, `settings.clients`, …).
- Changing `realitySettings`, `xhttpSettings`, `tcpSettings`, etc.
- Toggling VLESS Encryption (`mldsa65Seed` / `mldsa65Verify` presence,
  `decryption` value).
- Adding a new inbound file in `roles/xray/templates/conf/inbounds/`.

Do **not** refresh just because the random test secrets rotated — those
are deterministic anyway (`tests/scripts/gen-reality-keys.sh` is seeded
for tests).

## How to refresh

```bash
# From the raven-subscribe repo root, with Raven-server-install
# checked out as a sibling directory:
scripts/refresh-ansible-fixtures.sh
```

The script runs `tests/run.sh` in the sibling repo (Ansible render
only, no Docker/xray-test) and copies the resulting `tests/.output/conf.d/*.json`
into `testdata/ansible-rendered/conf.d/`. Then run the test:

```bash
go test ./internal/xray/... -run TestAnsibleContract -v
```

If the test now fails on a schema change you intended, update the
parser/types to match before committing both the fixture refresh and
the parser change in the same commit.
