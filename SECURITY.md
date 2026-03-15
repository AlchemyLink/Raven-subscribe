# Security Policy

## Supported Versions

| Version | Supported |
|---------|-----------|
| latest  | ✅        |

## Reporting a Vulnerability

**Do not open a public GitHub issue for security vulnerabilities.**

Please report security issues privately via one of these channels:

- **GitHub Security Advisories:** Use the "Report a vulnerability" button on the [Security tab](../../security/advisories/new) of this repository.
- **Email:** Contact the maintainer directly (see profile).

### What to include

- Description of the vulnerability and its potential impact
- Steps to reproduce (proof of concept if possible)
- Affected versions/components
- Suggested fix (optional)

### Response timeline

- **Acknowledgement:** within 48 hours
- **Initial assessment:** within 7 days
- **Fix / mitigation:** within 30 days for critical issues

## Security considerations for deployment

- The `admin_token` in `config.json` must be a strong random secret (UUID v4 or longer). Never reuse across environments.
- Run the service behind a reverse proxy (nginx/caddy) with TLS. Do not expose port 8080 directly.
- The SQLite database file contains client keys — restrict file permissions (`chmod 600`).
- Use the provided `systemd` unit file (`xray-subscription.service`) for isolation via `DynamicUser` and `ProtectSystem`.
