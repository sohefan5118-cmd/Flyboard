# Phase 5 traceability (2026-10-25)

Clean-room implementation: no XBoard/V2Board source, image, assets, or binary is used.

| Requirement | Implementation / evidence | Status |
|---|---|---|
| Login/bootstrap/CSRF | `httpapi`: one-use bootstrap digest, Argon password, strict session cookie, CSRF+Origin | implemented; unit/build gates pass |
| Full management surface | embedded UI/API plus `internal/httpapi/product_integration_test.go`: users/nodes/upstreams/clients CRUD paths, one-time secrets, pull/device/audit surfaces | implemented; real-service HTTP acceptance passed |
| Embedded UI contract | `internal/httpapi/web/app.test.js`: all 12 navigation surfaces, API/action strings, POST/PUT/DELETE headers, CSP-safe markup, text-only data rendering, secret filtering, responsive breakpoint | static contract implemented; real browser remains required |
| UI management actions | create/edit/delete for supported records; upstream pull; user/client/node token rotation; user device clear; Owner whitelist enable/disable; JSON settings edit | implemented; destructive actions confirm and credentials display once |
| Client token and subscriptions | digest-only token (create response only), raw/base64/Clash, generated QR, expiry/quota/window, PostgreSQL advisory lock | implemented |
| Main users / Owner Lock | user CRUD returns subscription token once; quota/expiry checks; atomic owner locks, 429, whitelist bypass, access/device logs | implemented |
| Nodes | one-time registration/rotation credential, signed HTTP/WS, config/users/devices full sync, agent `/v1/admit` fail-closed | implemented |
| Management security | same-origin CSRF+Origin, trusted-proxy CIDR resolution, owner/operator/auditor enforcement, shared fail-closed Valkey management rate limiter (`ADMIN_RATE_LIMIT`) | implemented and HTTP-tested |
| Operations lifecycle | `install.sh`: stable random project, 0600 secrets, migrate/full stack/readiness/one-use bootstrap; `backup.sh`: DB+config+secrets+SHA-256 manifest; `restore.sh`: path/checksum validation, overwrite guards, safety backup, migrate/readiness; `upgrade.sh`: full pre-backup, serial build, migrate/doctor/readiness, failure artifact+image inventory; `uninstall.sh`: keep by default, double-confirm purge | implemented; static/mock gate below |
| Migration | `0004_phase5.sql` embedded and source copy | implemented |
| Static/race | `go test ./...`, `go test -race ./...`, `go vet ./...`, production/test Compose config | passed 2026-10-25 |
| Real services | isolated `flyboard-accept-20261025` Compose project, PostgreSQL 17.11 + Valkey 8.1.10, all four migrations and black-box acceptance | passed 2026-10-25; volumes removed afterward |
| Product API/control-plane black-box | bootstrap once, login/cookie/CSRF/origin, CRUD/secret storage, pull dedupe, subscription formats/QR/headers, quota/expiry, owner/client device locks, whitelist, logs, node rotation + HTTP/WS auth, clear/outbox, CIDR/proxy and admin rate | passed against real PostgreSQL/Valkey; installer lifecycle remains separate |
| API rate limiting | shared fail-closed login/public subscription/node/management quotas | implemented and management quota acceptance-tested |
| Five image builds | panel/worker/scheduler/node-agent/flyboardctl (`flyboard-accept-*:local`) | passed 2026-10-25 |

Lifecycle static/mock evidence is provided by `tests/lifecycle-mock.sh`. A
fresh-host real-container lifecycle run remains required before a
production-complete claim; it was deliberately not run on the constrained
production host during this gate.

Real-browser acceptance still required: login/session bootstrap and expiry;
keyboard/focus behavior for both dialogs; each create/edit/delete/action against
live PostgreSQL and Valkey; one-time credential copy/close behavior; horizontal
tables and navigation at 320/768/desktop widths; screen-reader labels/status;
network failures, 401 redirect, validation errors, and long/untrusted field
values. This static gate deliberately starts no service or browser.

## Management UI static hardening (2026-10-25)

| Requirement | Implementation / evidence | Status |
|---|---|---|
| Keyboard dialogs and focus | `web/app.js` `trap`, `openDialog`, `closeDialog`; labelled `<dialog>` | Static + Node test |
| ARIA/live feedback | `index.html` status/live regions, dialog labels; alert errors | Static + Node test |
| Mobile accessible tables | scoped column headers and per-cell `data-label`; stacked CSS | Static + Node test |
| Session/API failure safety | 401/419 replace redirect; network/non-JSON/invalid JSON/204/size-bound parser | Node test |
| Untrusted values | DOM `textContent`, bounded display; HTML-shaped payload never inserted/executed | Node/static test |
| One-time secrets | copy failure announcement; clear on close; focus restore | Static + Node test |
| Mutations | disabled/aria-busy while pending; confirmations for delete, clear, token rotation | Static + Node test |
| Real browser / AT validation | keyboard traversal, focus behavior, mobile screen reader | **Pending new server** (no browser/service started in this pass) |
