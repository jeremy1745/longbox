# LongBox

A self-hosted comic book library manager.

## Features

- **Library management** — Scan and organize CBZ, CBR, CB7, and PDF files
- **Metadata matching** — Search and match series via the ComicVine API
- **Built-in reader** — Page-by-page comic reader with progress tracking
- **Want list & calendar** — Track weekly releases via WalkSoftly, manage a pull list
- **Usenet search** — Search Newznab/Prowlarr indexers and send NZBs to SABnzbd
- **File organization** — Rename and sort files with customizable naming templates
- **Background jobs** — Long-running tasks with real-time SSE progress updates
- **Single binary** — Go backend with embedded SvelteKit frontend, no external dependencies

## Quick Start

```sh
make build
./longbox
```

LongBox listens on port **22526** by default. Open `http://localhost:22526` in your browser.

## Configuration

Create a `config.yaml` in the working directory or pass `--config path/to/config.yaml`.

```yaml
port: 22526
library_dir: ~/Comics
data_dir: ~/.longbox
log_level: info           # debug, info, warn, error
comicvine_api_key: ""     # or set via Settings UI
```

Every option can be overridden with an environment variable:

| Config key          | Env var                    | Default      |
|---------------------|----------------------------|--------------|
| `port`              | `LONGBOX_PORT`             | `22526`      |
| `library_dir`       | `LONGBOX_LIBRARY_DIR`      | `~/Comics`   |
| `data_dir`          | `LONGBOX_DATA_DIR`         | `~/.longbox` |
| `log_level`         | `LONGBOX_LOG_LEVEL`        | `info`       |
| `comicvine_api_key` | `LONGBOX_COMICVINE_API_KEY`| —            |

### Naming templates

File organization uses Go-template-style variables:

```
{series}/{series} #{number|pad:3}.{format}
```

Available variables: `{series}`, `{sort_series}`, `{number}`, `{title}`, `{year}`, `{publisher}`, `{format}`, `{cover_date}`, `{store_date}`, `{writers}`, `{artists}`.

Filters: `pad:N` (zero-pad), `lower`, `upper`.

## Development

**Prerequisites:** Go 1.25+, Node.js

```sh
# Run backend with hot reload (no embedded frontend)
make dev

# Build production binary
make build

# Cross-compile Linux, macOS, Windows
make release
```

Frontend dev server (with HMR):

```sh
cd ui
npm ci
npm run dev
```

## Tech Stack

**Backend:** Go, chi, SQLite (modernc.org/sqlite — pure Go, no CGO), goose migrations

**Frontend:** Svelte 5, SvelteKit, Tailwind CSS, TypeScript
