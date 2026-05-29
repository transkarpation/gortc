# api-backend

A [Fastify](https://fastify.dev/) + TypeScript HTTP API that sits in front of
[`rtc-backend`](../rtc-backend). It pushes messages to users through
rtc-backend's internal publish endpoint.

## What it does

- **`POST /messages`** — pushes a message to one user by publishing to their
  personal channel `appId:userId` via rtc-backend's `POST /publish`.
- **`GET /healthz`** — liveness check.

`RTC_PUBLISH_SECRET` must equal rtc-backend's `WS_PUBLISH_SECRET`.

## Layout

```
src/
  server.ts            entrypoint: load .env, build app, listen, graceful shutdown
  app.ts               Fastify factory (decorates config + rtc client, registers routes)
  config.ts            env validation (zod) -> typed config
  services/rtcClient.ts client for rtc-backend's /publish and /subscribe
  routes/              health, messages
```

## How to run

Requires Node.js 20+.

```bash
cd api-backend
npm install
cp .env.example .env   # set RTC_PUBLISH_SECRET, APP_ID, etc.
npm run dev            # watch mode (tsx)
```

Build and run the compiled output:

```bash
npm run build
npm start
```

The API listens on `http://localhost:3000` by default.

## Try it

With rtc-backend running on `:8080` and a matching `RTC_PUBLISH_SECRET`:

```bash
# Push a message to a user (delivered to channel APP_ID:alice).
curl -X POST http://localhost:3000/messages \
  -H "Content-Type: application/json" \
  -d '{"userId":"alice","data":"hello from the API"}'
```

## Configuration

| Env | Default | Description |
|---|---|---|
| `HOST` | `0.0.0.0` | Listen host. |
| `PORT` | `3000` | Listen port. |
| `LOG_LEVEL` | `info` | Pino log level. |
| `RTC_BASE_URL` | `http://localhost:8080` | rtc-backend base URL. |
| `RTC_PUBLISH_SECRET` | *(required)* | Must equal rtc-backend's `WS_PUBLISH_SECRET`. |
| `APP_ID` | *(required)* | App id (matches rtc-backend); used for channel names. |
