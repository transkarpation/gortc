# gortc

A small, authenticated **WebSocket pub/sub server** built with
[`gorilla/websocket`](https://github.com/gorilla/websocket) and
[`go-chi/chi`](https://github.com/go-chi/chi).

Clients connect over WebSocket purely to **receive** real-time updates. They are
authenticated per-app with a JWT and auto-subscribed to their own personal
channel. Messages are pushed in by trusted backend services through an HTTP
`POST /publish` endpoint — the socket itself is receive-only.

## What it does

- **Hub-based pub/sub.** A single goroutine owns all client and channel state
  (no mutexes). Each published message is routed only to the clients subscribed
  to that channel. Slow clients whose send buffer fills up are dropped rather
  than blocking the hub.
- **Receive-only sockets.** Clients never publish over the WebSocket; data
  frames they send are ignored. The read loop exists only for keepalive
  (ping/pong) and disconnect detection.
- **Per-app authentication.** A config file lists apps, each with an
  `appId`, `apiKey`, and `apiSecret`. On connect the handshake is verified
  **before** the upgrade:
  1. `apiKey` (query param) selects the app.
  2. `authorization` (query param) is an HMAC-signed JWT, verified with that
     app's `apiSecret`.
  3. The JWT's `userId` claim must equal the `userId` query param.
- **Personal channels.** On connect a client is subscribed to exactly one
  channel: `appId:userId`. Backends target an individual user by publishing to
  that channel name.
- **Internal publish endpoint.** `POST /publish` is guarded by a shared secret
  (sent in the `Authorization` header) so only trusted services can publish.
- **Graceful shutdown** on `SIGINT`/`SIGTERM`.
- **Structured logging** with `log/slog` (text or JSON). Sensitive query
  params (`apiKey`, `authorization`) are redacted from request logs.
- **Built-in test page** at `/` and a dev-only JWT minting endpoint.

## Endpoints

| Method & path | Auth | Description |
|---|---|---|
| `GET /` | — | HTML test client. |
| `GET /healthz` | — | Liveness check, returns `ok`. |
| `GET /ws` | `apiKey` + `userId` + `authorization` (JWT) query params | WebSocket connection; auto-subscribes to `appId:userId`. |
| `POST /publish` | `Authorization` header == publish secret | Body `{"channel":"...","data":"..."}`; delivers to that channel's subscribers. Returns `202`. |
| `GET /dev/token` | dev mode only | Mints a test JWT. Query: `apiKey`, `userId`. |

Messages delivered to subscribers are JSON: `{"channel":"...","data":"..."}`.

## Configuration

App credentials come from a JSON file (default `apps.json`, gitignored). Copy
the example and edit it:

```bash
cp apps.example.json apps.json
```

```json
{
  "apps": [
    { "appId": "demo-app", "apiKey": "demo-key", "apiSecret": "a-long-random-secret" }
  ]
}
```

Other settings come from flags or environment variables (a local `.env` is
loaded automatically if present; see `.env.example`):

| Flag | Env | Default | Description |
|---|---|---|---|
| `-addr` | — | `:8080` | HTTP listen address. |
| `-apps-file` | `WS_APPS_FILE` | `apps.json` | Path to the apps config. |
| `-publish-secret` | `WS_PUBLISH_SECRET` | *(required)* | Shared secret for `POST /publish`. |
| `-log-format` | `LOG_FORMAT` | `text` | Log output: `text` or `json`. |
| `-dev` | — | `false` | Enable `GET /dev/token` (never use in production). |

## How to run

### 1. Prerequisites

- Go 1.26+

### 2. Set up config

```bash
cp apps.example.json apps.json   # edit with your apps
cp .env.example .env             # set WS_PUBLISH_SECRET
```

### 3. Run

```bash
go run ./cmd/server
```

Or with everything inline (no files needed):

```bash
WS_PUBLISH_SECRET=internal-secret go run ./cmd/server -dev
```

The server listens on `http://localhost:8080`.

### 4. Try it in the browser

Open <http://localhost:8080/> (requires `-dev` for the **Mint** button):

1. Set `apiKey` and `userId`, click **Mint** to get a JWT (auto-fills the token
   and your personal channel).
2. Click **Connect** — you're now subscribed to `appId:userId`.
3. Fill in the **publish secret**, then publish a message to your personal
   channel and watch it arrive in the log.

### 5. Publish from the command line

```bash
curl -X POST http://localhost:8080/publish \
  -H "Authorization: internal-secret" \
  -H "Content-Type: application/json" \
  -d '{"channel":"demo-app:alice","data":"hello"}'
```

Every connected `alice` of `demo-app` receives `{"channel":"demo-app:alice","data":"hello"}`.

## Project layout

```
cmd/server/        entrypoint, HTTP routing, /publish + /dev/token handlers, test page
internal/ws/       hub (pub/sub), client (read/write pumps), authenticator
internal/config/   apps config loading + validation
```
