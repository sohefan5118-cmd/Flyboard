# Node control protocol v1

Clean-room protocol. HTTP `GET /api/v1/node/devices?user_id=N` and WebSocket `sync.devices` use the same `devices.Snapshot` DTO. HTTP returns the DTO under `devices`, `users`, and `data` compatibility keys.

Authentication headers are `X-Flyboard-Node`, one-time node token, Unix-second `X-Flyboard-Timestamp`, and lowercase HMAC-SHA256 signature. Canonical input is `timestamp + "\n" + method + "\n" + path + "\n" + body`; timestamps outside ±10 seconds fail. WebSocket first message supplies node_id/token/timestamp/signature, canonical method/path `WS` and `/api/v1/node/ws`, within 10 seconds.

Every envelope has `version: "1"` and `type`. On connect the server sends `auth.success`, then full `sync.config`, `sync.users`, and one `sync.devices` per user. Client sends `report.devices`, `request.devices`, `node.status`, and `pong`. Server pings every 55 seconds. `report.devices.result` carries the accepted snapshot or an error. Agents must not forward rejected devices.

Each node has one writer and a bounded 64-message queue. Slow consumers are disconnected. Sync is at-least-once; agents persist the highest `(aggregate, revision)` and ignore duplicate/older revisions.

The reference agent reconnects with exponential backoff plus jitter (1s through 30s). `sync.devices` revisions are monotonic per `devices:{user_id}`; full sync entries without a revision refresh state but never lower a stored revision. Revision and verified admission cache are committed together by fsync plus atomic rename before acknowledgement/use and restored after restart.
