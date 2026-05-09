# Multiplayer Notepad

Collaborative multi-user text editor: create a document, share the `/d/<ULID>` URL, edit in real time over WebSockets (Yjs + CodeMirror). No accounts. Optional per-document password (bcrypt) for opening on new browsers. Optional live Markdown preview pane.

## Requirements

- Go 1.23+ (module may resolve to a newer toolchain)
- Network access for CDN assets (Bootstrap, CodeMirror, Yjs) in the browser

## Quick start

```bash
cp .env.example .env
go build -o mpnotepad ./cmd/mpnotepad
./mpnotepad serve
```

Default bind address is `127.0.0.1:8080` (override with `MPN_ADDR` or `-addr`).

```bash
./mpnotepad serve -addr 127.0.0.1:8080
```

SQLite files are created under `db/` (see `.gitignore`). Use `MPN_DB_PATH` to change location.

## Configuration (`.env`)

| Variable | Default | Meaning |
|----------|---------|---------|
| `MPN_ADDR` | `127.0.0.1:8080` | Listen address |
| `MPN_DB_PATH` | `db/mpnotepad.sqlite` | SQLite database file |
| `MPN_SESSION_TTL_HOURS` | `720` | Cookie/session lifetime after unlock |
| `MPN_BCRYPT_COST` | `12` | bcrypt cost for document passwords |
| `MPN_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |

Environment variables override `.env` when set in the process environment.

## Running behind Nginx (WebSockets + static files)

Proxy HTML, API, and WebSockets to the Go process (forward `Upgrade` / `Connection` for Yjs). Serve embedded app assets (`/static/js`, `/static/css`) from the filesystem so Nginx can cache and gzip them; keep a copy of the project’s [`web/static/`](web/static/) tree on the server next to the binary (same layout as in the repo).

Put the `map` below once inside the `http { ... }` block (e.g. in `nginx.conf`), then use `$connection_upgrade` in the server:

```nginx
map $http_upgrade $connection_upgrade {
    default upgrade;
    ''      close;
}

server {
    listen 80;
    server_name notepad.example.com;

    # App-provided JS/CSS (must match files under web/static in your deployment)
    location /static/ {
        alias /opt/mpnotepad/web/static/;
        expires 7d;
        add_header Cache-Control "public, max-age=604800";
        access_log off;
    }

    location / {
        proxy_pass         http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header   Host              $host;
        proxy_set_header   X-Real-IP         $remote_addr;
        proxy_set_header   X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header   X-Forwarded-Proto $scheme;

        proxy_set_header   Upgrade           $http_upgrade;
        proxy_set_header   Connection        $connection_upgrade;
        proxy_read_timeout 3600s;
        proxy_send_timeout 3600s;
    }
}
```

Adjust `proxy_pass` and the `alias` path to match your install (e.g. same base directory as in the [systemd example](#systemd-example)).

If `/static/css/app.css` or `/static/js/editor.js` return **404**, either copy the full [`web/static/`](web/static/) tree (including `css/` and `js/` subfolders) to the `alias` directory, or **remove** the `location /static/` block so all requests (including static files) are proxied to Go.

## systemd example

`/etc/systemd/system/mpnotepad.service`:

```ini
[Unit]
Description=Multiplayer Notepad
After=network.target

[Service]
Type=simple
User=www-data
WorkingDirectory=/opt/mpnotepad
EnvironmentFile=/opt/mpnotepad/.env
ExecStart=/opt/mpnotepad/mpnotepad serve -addr 127.0.0.1:8080
Restart=on-failure

[Install]
WantedBy=multi-user.target
```

## Backups

- Stop the service (or ensure no writers), then copy `db/mpnotepad.sqlite` and any `db/mpnotepad.sqlite-wal` / `-shm` files, **or** use `sqlite3 db/mpnotepad.sqlite ".backup backup.sqlite"`.

## Development

```bash
go build ./...
go vet ./...
```

### Frontend bundle

The browser ESM bundle (`web/static/js/editor.js`) is produced from `web/js-src/editor.mjs` via esbuild. Whenever you change anything under `web/js-src/`, rebuild it (and then rebuild the Go binary so the new asset is re-embedded):

```bash
npm install
npm run build:js
go build -o mpnotepad ./cmd/mpnotepad
```

### Manual end-to-end test

There is no scripted browser harness. To verify multi-user editing, run two browser tabs against the same document URL and type into each — characters typed in one tab must appear in the other in real time, and the text must survive a full reload (proving Yjs state persistence).

In Cursor, the same check can be driven through the built-in `cursor-ide-browser` MCP tools (`browser_navigate`, `browser_click`, `browser_type`, `browser_snapshot`) — open `http://127.0.0.1:8080/`, click **New document**, open the resulting `/d/<ULID>` URL in a second tab, and type into both editors.

## License

Add your license here.
