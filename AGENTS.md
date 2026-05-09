# AGENTS.md — Multiplayer Notepad

## Purpose

Go HTTP/WebSocket server + static HTML/JS client: shared text documents at `/d/<ULID>`, real-time sync via Yjs over WebSockets, SQLite persistence.

## Layout

- `cmd/mpnotepad` — CLI: `serve [-addr host:port]`, loads `.env` via `internal/config`.
- `internal/store` — SQLite (WAL), schema, documents + session tokens, opaque `yjs_state` blob.
- `internal/server` — chi routes, templates/static from `mpnotepad/web`, password gate + cookies, per-doc **hub** relaying Yjs binary frames.
- `web/` — `embed.FS` assets: `templates/home.html`, `document.html`, `password.html`, `static/*` (Bootstrap + ESM `static/js/editor.js`).

## Main flows

1. **Create** — `POST /new` → ULID row + session cookie (path `/d/<id>`) → redirect to editor.
2. **Password** — If `password_hash` set and cookie invalid → `password.html`; `POST /d/<id>/auth` sets session.
3. **Sync** — `GET /d/<id>/ws` (authorized). Hub parses Yjs wire just enough: answer `SyncStep1` with `SyncStep2` from stored state; forward awareness; merge `Update` / replace on client `SyncStep2`; persist debounced to `yjs_state`.
4. **Settings** — `GET/POST /d/<id>/settings(.json)` JSON: title, markdown side panel, optional bcrypt password / clear password (invalidates other sessions).

## Conventions

- No JS bundler: browser loads ESM from esm.sh; server only embeds `web/static`.
- Keep collaboration logic in the browser (Yjs); server stores/forwards opaque bytes.
- `db/` and `.env` are gitignored; use `.env.example` as template.

## Checks

```bash
go build ./...
go vet ./...
```
