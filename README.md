# roomtodo-mcp

An unofficial [MCP](https://modelcontextprotocol.io) server for the [roomtodo.com](https://roomtodo.com) 3D room planner.
Let Claude, Codex or ChatGPT draw floor plans, furnish rooms, put doors and windows into walls and manage your roomtodo projects.

> Unofficial: not affiliated with roomtodo. It talks to the same web API the planner uses, reconstructed
> from the site itself, so it can break when the site changes.

[Russian ↓](#russian)

## What it can do

- **Floor plans:** describe rooms as rectangles plus the doorways between them, and the server builds the walls,
  partitions, floors and passages.
- **Furniture:** search the catalog and place, move, rotate, scale or remove objects.
- **Doors and windows:** they snap into the nearest wall and cut the opening, just like in the planner.
- **Materials:** floor, ceiling and wall materials or colours, and wall height.
- **Projects:** list, open, save, copy, rename, share and delete account projects, and view your renders.

### Works without an account

Without signing in you can search the catalog, open public share links, and build complete projects as local
`.json` files. To view one, open <https://roomtodo.com/planner>, press **Ctrl+Alt+O** and pick the file.
Signing in is only needed to save projects into your roomtodo account.

## Install

Download the archive for your system from [Releases](../../releases) and unpack it anywhere. It is a single
executable with no dependencies.

On macOS, remove the download quarantine once: `xattr -d com.apple.quarantine ./roomtodo-mcp`.

### Claude Desktop

Settings → Developer → Edit Config, then add:

```json
{
  "mcpServers": {
    "roomtodo": {
      "command": "C:\\Tools\\roomtodo-mcp\\roomtodo-mcp.exe"
    }
  }
}
```

### Claude Code

```bash
claude mcp add roomtodo -- /path/to/roomtodo-mcp
```

### Codex (CLI and IDE extension)

Codex runs on your computer, so like Claude it starts the server directly. No tunnel needed:

```bash
codex mcp add roomtodo -- /path/to/roomtodo-mcp
```

Or add it to `~/.codex/config.toml`:

```toml
[mcp_servers.roomtodo]
command = 'C:\Tools\roomtodo-mcp\roomtodo-mcp.exe'
```

### ChatGPT (chat)

The ChatGPT chat connects to MCP servers from OpenAI's cloud over HTTPS. Run the server in HTTP mode and expose it
with a tunnel:

```bash
roomtodo-mcp --http :8080
```

It prints an endpoint such as `http://localhost:8080/3f9c…/mcp`. The random part of the path is a secret,
because anyone with the URL can use the server and the roomtodo account signed in on it. Then open a tunnel, for example with
[cloudflared](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/do-more-with-tunnels/trycloudflare/):

```bash
cloudflared tunnel --url http://localhost:8080
```

In ChatGPT, enable developer mode for connectors and create a connector with the URL
`https://<tunnel-host>/<secret>/mcp`, with no authentication. Restart with `--public-url https://<tunnel-host>`
to get working download links for project files. Pass `--secret <value>` to keep the same URL across restarts.

## Signing in

You only need to sign in for account projects. Pick one of these:

- **Browser (default):** the first account action opens a separate Chrome/Edge window on roomtodo.com. Sign in
  there as usual and the window closes by itself. The token is cached in `~/.roomtodo-mcp/token.json`. That window
  keeps its own profile, so later sign-ins are usually automatic.
- **Token:** run with `--token` or set `ROOMTODO_TOKEN`, or call the `login` tool with a token. You can copy the
  value of `localStorage["r2d_token_…"]` from roomtodo.com.

## Options

| Flag | Env | Default | |
|---|---|---|---|
| `--http addr` | | stdio | serve Streamable HTTP instead of stdio |
| `--secret s` | `ROOMTODO_SECRET` | random | URL path secret in HTTP mode (`none` disables it) |
| `--public-url u` | `ROOMTODO_PUBLIC_URL` | | public base URL in HTTP mode, used for download links |
| `--projects-dir d` | `ROOMTODO_PROJECTS_DIR` | `~/.roomtodo-mcp/projects` | where local `.json` projects live |
| `--state-dir d` | `ROOMTODO_STATE_DIR` | `~/.roomtodo-mcp` | token and sign-in browser profile |
| `--token t` | `ROOMTODO_TOKEN` | | roomtodo token |
| `--lang l` | `ROOMTODO_LANG` | `ru` | catalog language |
| `--browser p` | `ROOMTODO_BROWSER` | auto | Chrome/Edge executable for sign-in |
| `--no-browser-login` | `ROOMTODO_NO_BROWSER_LOGIN` | | never open a browser |

In HTTP mode, project files are limited to the projects folder.

## Tools

| Tool | |
|---|---|
| `create_floorplan` | walls, rooms and doorways from rectangles |
| `add_object`, `move_object`, `remove_object` | furniture; doors and windows snap into walls |
| `set_material`, `set_walls_height` | finishes and height |
| `search_catalog`, `category_tree` | catalog (no sign-in) |
| `get_project`, `new_project`, `save_project_as`, `list_projects` | open, create, convert between file / account / share link |
| `rename_project`, `share_project`, `delete_project` | account projects |
| `login`, `logout`, `whoami` | account |
| `list_renders`, `get_render` | renders |

Each project argument accepts a `.json` file name, an account project id, or a share link. Pass `save_to` to
write the result somewhere else, for example `"new"` for a new account project.

Coordinates are floor-plan centimetres, with x to the right and y down. For example:

```json
{
  "project": "house.json",
  "rooms": [
    {"name": "Living",  "x": 0,   "y": 0, "width": 430, "depth": 660},
    {"name": "Hall",    "x": 430, "y": 0, "width": 160, "depth": 660},
    {"name": "Bedroom", "x": 590, "y": 0, "width": 390, "depth": 660}
  ],
  "openings": [{"between": ["Hall", "Living"]}, {"between": ["Hall", "Bedroom"]}]
}
```

Then call `add_object` with `{"project": "house.json", "product_id": "941", "room": "Hall", "side": "bottom"}`
to put an entrance door into the hall's outer wall.

## Build

```bash
go build -o roomtodo-mcp ./cmd/roomtodo-mcp
go test ./...          # -short skips the test that needs network
```

Releases are built by GitHub Actions when you push a `v*` tag.

---

## Russian

Неофициальный MCP-сервер для планировщика [roomtodo.com](https://roomtodo.com). С ним Claude, Codex или ChatGPT
рисуют планировки по описанию комнат, расставляют мебель, вставляют двери и окна в стены и работают с
проектами в вашем аккаунте.

- **Без аккаунта:** каталог, публичные ссылки и проекты в виде `.json`-файлов. Чтобы открыть файл, зайдите на
  roomtodo.com/planner, нажмите **Ctrl+Alt+O** и выберите его.
- **С аккаунтом:** сохранение прямо в «Мои проекты». При первом обращении откроется окно браузера: войдите,
  и оно закроется само. Вместо этого можно передать токен через `--token` или `ROOMTODO_TOKEN`.
- **Claude и Codex:** пропишите путь к бинарнику в конфиге (примеры выше), туннель не нужен.
- **Чат ChatGPT:** запустите `roomtodo-mcp --http :8080`, откройте туннель (`cloudflared tunnel --url http://localhost:8080`)
  и добавьте коннектор с адресом `https://<туннель>/<секрет>/mcp`. Секрет из пути никому не давайте: по этой
  ссылке доступен ваш аккаунт roomtodo.

## License

MIT
