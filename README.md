# Flyboard clean-room rebuild

Independent Go implementation based only on the observable contract in `research/`; no XBoard/V2Board source, image, or frontend asset is included.

## Phase 2 runnable slice

- Transactional PostgreSQL migrations: `flyboardctl migrate up|status`.
- Authenticated upstream CRUD. `subscribe_url` uses XChaCha20-Poly1305 envelope encryption with per-record AAD and a file master key; APIs never return it.
- `POST /api/v2/$ADMIN_PATH/flyboard/upstreams/pull` accepts `{"upstream_id":1}`, atomically deduplicates active pulls, and immediately returns HTTP 202.
- Worker claims via `FOR UPDATE SKIP LOCKED`, safely fetches, parses URI/base64 URI lists, and transactionally replaces snapshots. Failure preserves old nodes.
- Scheduler marks stale pending/running tasks errored every ten seconds.
- HTTPS-default safe fetch disables proxies and rejects userinfo, fragments, IPv6, nonstandard ports, private/loopback/link-local/CGNAT/metadata/documentation/multicast/reserved IPs, and mixed DNS. Every redirect is revalidated; IP is pinned; timeout/body limits apply.

## One-command install

```sh
git clone --depth 1 https://github.com/sohefan5118-cmd/Flyboard.git && \
cd Flyboard && \
FLYBOARD_ADMIN_EMAIL='admin@example.com' bash install.sh
```

After installation, visit the randomized admin URL printed by the installer:
`http://SERVER_IP:7001/<random-admin-path>/flyboard`. Save the one-time password immediately.
Port 7001 binds publicly for this quick-install path; restrict it with your cloud firewall and configure HTTPS promptly. For localhost-only deployment, run with `FLYBOARD_BIND=127.0.0.1` behind a reverse proxy.

Manual lifecycle commands:

```sh
./install.sh
ADMIN_PATH=$(cat secrets/admin_path) docker compose up --build -d postgres valkey
docker compose run --rm migrate migrate up
docker compose run --rm migrate migrate status
docker compose run --rm --entrypoint /app migrate bootstrap-token
ADMIN_PATH=$(cat secrets/admin_path) docker compose up --build -d panel worker scheduler
```

The installer assigns and persists a random Compose project name, creates 0600
secrets (including the unguessable admin path), migrates, starts the complete
service set, checks readiness, and emits the bootstrap password once. The installer
defaults to public port 7001 for quick installation; set `FLYBOARD_BIND=127.0.0.1`
for a reverse-proxy-only deployment. It is
safe to rerun. Images should normally be supplied by a release; local builds
are intentionally serialized during upgrades (`COMPOSE_PARALLEL_LIMIT=1`).

## Lifecycle operations

```sh
./backup.sh                         # writes outside the source tree's tracked files: backups/
FLYBOARD_RESTORE_CONFIRM=YES FLYBOARD_RESTORE_SECRETS_CONFIRM=REPLACE ./restore.sh backups/FILE.tar
./upgrade.sh                        # verified full backup before pull/build/migrate/readiness
./uninstall.sh                      # keeps volumes, secrets and backups
./uninstall.sh --purge              # interactive PURGE + project-name confirmation
```

Backups contain a database dump archive, `compose.yaml`, all secrets, and a
SHA-256 manifest. Restore validates paths and every checksum before changing
state, refuses existing secrets by default, and first takes a safety backup.
Keep backups off-host as well. A failed upgrade prints the verified rollback
artifact and captured old-image inventory; restore that artifact after checking
out/tagging the prior release images.

Use the one-time token with `POST /bootstrap`, then `POST /login`; mutations require the session cookie, returned `X-CSRF-Token`, and `Origin`. CRUD is under `/api/v2/$ADMIN_PATH/flyboard/{upstreams,users,clients,nodes}`; update/delete use `?id=`. Omitting `subscribe_url` during update preserves it.

Back up `secrets/master_key` separately (0600): losing it makes credentials unrecoverable. Production requires TLS and `COOKIE_SECURE=true`.

## Verification

```sh
gofmt -w ./cmd ./internal
go test ./...
go vet ./...
bash -n install.sh
shellcheck install.sh backup.sh restore.sh upgrade.sh uninstall.sh
docker compose config
./tests/lifecycle-mock.sh
# Optional PostgreSQL dedup/status integration test:
TEST_DATABASE_URL='postgres://...' go test ./internal/pull -run Integration
```

Unit tests cover URL/IP policy, mixed DNS, parsing, encryption, device leases, rate limiting, and node-agent behavior. The product API now includes user/client/node CRUD, one-time secret disclosure, subscription quota/expiry/device enforcement, raw/base64/Clash output, QR, Owner Lock, management CIDRs/RBAC, and node HTTP/WebSocket control. Remaining release scope is the fresh-server black-box suite (including browser flows and backup/restore/uninstall); HTTP-level product API coverage is still thinner than the domain tests and must be expanded before a production-complete claim.

## Node control plane (v1)

The panel exposes authenticated HTTP `/api/v1/node/devices` and WebSocket `/api/v1/node/ws`; see `docs/protocol/node-control-v1.md` and `protocol/schema/envelope-v1.json`. Node tokens are shown only at registration. The optional `node-agent` exposes local pre-forward `POST /v1/admit`; new devices fail closed during control outages while previously verified leases remain valid until their signed lease deadline. Configure `AGENT_LOCAL_TOKEN` for the local apply endpoint.

Device and subscription ownership state is atomic across replicas (PostgreSQL advisory transaction locks for Owner Lock, Valkey Lua for global devices). Scheduler prunes leases every 10 seconds and writes PostgreSQL projections/outbox records. Outbox delivery is at least once and consumers must apply monotonic revisions idempotently.

## Phase 4 operations

The node agent actively dials `PANEL_WS_URL` using `NODE_ID`/`NODE_TOKEN`, authenticates within the protocol deadline, replies to pings, exponentially reconnects (capped at 30s), and atomically persists monotonic revisions plus last-valid admission leases at `AGENT_STATE`. A restart restores those leases; new devices remain fail-closed while disconnected.

Administrator `POST /api/v2/$ADMIN_PATH/flyboard/users/{id}/devices/clear` requires the normal session, CSRF header, and Origin. It clears only that user's device state, projection, and count, writes an audit row/outbox revision, and immediately fans out an empty snapshot. Owner migration releases only slots matching the old owner IP.

Panel runs the outbox dispatcher continuously. Rows are claimed with `SKIP LOCKED`, delivered at least once, then acknowledged; agents discard stale/duplicate revisions.

Isolated real-service verification: `docker compose -f compose.test.yaml up --build --abort-on-container-exit --exit-code-from test`; always clean with `docker compose -f compose.test.yaml down -v --remove-orphans`.

## Phase 5 administration and operations

The embedded, dependency-free console covers overview, users, independent clients,
registered nodes, upstreams and imported nodes, device state, Owner whitelist,
access/block logs, pull tasks, management CIDRs, and settings. It provides
create/edit/delete forms where supported, upstream pull, user/client/node token
rotation, per-user device clear, and whitelist enable/disable actions. API values
are rendered through DOM text nodes (not HTML), and the UI has no inline script,
style, or event handlers, so the strict CSP remains effective. User/client/node
secrets are returned only by create/rotate responses, shown in a one-time dialog,
and only digests are stored. Subscription endpoints support raw, base64 and Clash
output plus a scanner-valid SVG QR. Owner Lock returns 429; independent-client
device-window overflow returns 403; both admission paths are serialized atomically.

Roles are `owner`, `operator`, and read-only `auditor`; owner-only mutations protect nodes, CIDRs, and settings. Forwarding headers are accepted only from a configured CIDR peer. An empty CIDR list permits first-run administration; `flyboardctl emergency-whitelist-off` is the recovery path. Node registration/rotation, signed HTTP/WS authentication, full config/users/devices sync and fail-closed local admission are documented in `docs/protocol/node-control-v1.md`.

`install.sh` creates persistent secrets, migrates, starts the complete panel/worker/scheduler stack, waits for readiness, and prints the private admin URL and one-time bootstrap credential. `upgrade.sh` runs doctor and creates a checksum-manifest backup before migration. `flyboardctl restore FILE` validates archive members and checksum before `pg_restore`. `uninstall.sh` requires an explicit keep/purge mode and an interactive `PURGE` confirmation; secrets are moved aside when trash is unavailable.

Mandatory release gates: `gofmt`, `go test ./...`, `go test -race ./...`, `go vet ./...`, shell syntax, Compose validation, all Docker targets, and the real PostgreSQL+Valkey Compose suite. The suite is integration infrastructure; a production release must additionally run the black-box install/browser/WS/backup/restore/uninstall scenarios listed in `docs/traceability.md`.

The lightweight UI contract gate requires only Node.js: `node --test
internal/httpapi/web/app.test.js`. It checks route/action coverage, secret-field
filtering, text-only rendering, mutation headers, CSP compatibility, and the
mobile breakpoint. It does not replace real browser keyboard, dialog, responsive,
or end-to-end API verification.

### Management UI hardening status
The dependency-free console now statically covers keyboard dialog focus trapping/restoration, labelled dialogs and live announcements, responsive labelled table cells, session-expiry redirects, bounded plain-text errors (including network, non-JSON, invalid JSON, validation, oversized and HTML-shaped payloads), one-time-secret clearing/copy failure, duplicate-submit suppression, and destructive-action confirmation. Node built-in tests exercise response parsing and static wiring. **A real-browser accessibility/keyboard pass remains pending on the new server**; these static tests do not substitute for assistive-technology/browser validation.
