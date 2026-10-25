# Security audit (2026-10-25)

Scope: all Go, JavaScript, shell and SQL in this repository. Review was source-only and resource-constrained; no image build, service startup, container mutation, upload, PostgreSQL or Valkey was used.

## Fixed

### High
- **WebSocket cross-site access and unbounded messages:** node control sockets previously accepted every browser Origin and had no explicit read ceiling. Browser Origins are now rejected and reads are capped at 1 MiB.
- **Restore archive extraction:** the lifecycle restore script only partially screened names and allowed links/special entries. It now rejects absolute/traversal paths and all entries except regular files/directories, and ignores archived ownership/permissions.

### Medium
- **Stored XSS in settings UI:** setting keys were interpolated into an inline event-handler JavaScript string. The settings view now creates DOM nodes and listeners without executable interpolation.
- **Missing browser hardening:** added restrictive CSP, frame denial, Permissions-Policy, nosniff and no-referrer headers.
- **Session persistence failure:** login previously returned a cookie even if database session creation failed. It now fails closed.

## Reviewed / existing controls

- Dynamic SQL identifiers in `server.go` are selected only from hard-coded allowlists (`api`, `special`, `overview`); values use parameters. No user-controlled table/column injection found.
- Other `innerHTML` rendering escapes remote values; the risky settings event-handler context was removed.
- Unsafe admin methods require a session, constant-time CSRF token match and same-host HTTP(S) Origin. Cookies are HttpOnly, SameSite=Strict, Secure by default.
- RBAC blocks auditor writes and reserves CIDR/settings/node writes for owner. Management forwarding headers are used only when the direct peer is in a configured CIDR.
- SSRF fetches validate every redirect, reject non-public/mixed DNS answers and pin the selected validated address, preventing DNS rebinding between validation and connect.
- Subscription/node/admin/login rate limits use a shared limiter and fail closed. Tokens are random, stored as digests where appropriate; upstream URLs/credentials are encrypted.
- Backup extraction in `flyboardctl` allowlists two regular filenames, bounds sizes and verifies checksums. External commands use argument arrays, not a shell.
- Owner/device operations use PostgreSQL advisory locks/transactions. QR data is escaped, generated locally, non-cacheable, and does not log the token.
- No permissive CORS response headers or embedded production secrets found. Logs inspected do not intentionally print tokens/passwords.

## Requires real integration validation

- Reverse-proxy deployment: verify TLS termination, `COOKIE_SECURE=true`, Host preservation, and exact trusted-proxy/management CIDRs.
- PostgreSQL/Valkey failure and concurrency behavior, including advisory-lock contention and transaction rollback.
- WebSocket compatibility with deployed node agents after Origin rejection, plus load/backpressure behavior.
- End-to-end CSP compatibility and browser UI regression.
- Restore/backup against real GNU tar and PostgreSQL data, including interrupted restore recovery.
- Rate limiting under multiple replicas and proxy NAT; assess whether deployment-specific per-account limits are also desirable.
