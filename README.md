# pim

`pim` is a terminal UI for managing Pi custom provider/model libraries and safely applying the selected models to `models.json`.

## Features

- Keeps the complete provider/model inventory separate from Pi's active `models.json`.
- Provides provider and model checkboxes with independent scrolling.
- Shows staged changes before Apply.
- Detects active providers or models that no longer exist in the library and offers an explicit repair preview.
- Backs up the current `models.json`, replaces it atomically, verifies it with `pi --list-models`, and rolls back on failure.
- Preserves unknown provider and model JSON fields.

## Requirements

- Go 1.24 or newer when installing from source.
- `pi` available on `PATH` for Apply verification.

## Install

```sh
go install github.com/limars874/pim/cmd/pim@latest
```

Or build the local checkout:

```sh
go build -o ~/.local/bin/pim ./cmd/pim
```

Make sure the selected install directory is on `PATH`, then run:

```sh
pim
```

## Usage

| Key | Action |
|---|---|
| `Up` / `Down`, `j` / `k` | Move within the focused list |
| `Space` | Toggle the focused provider or model |
| `Enter` / `Right` | Open the selected provider's models |
| `Esc` / `Left` | Return to providers or leave the current screen |
| `A` | Preview and apply staged selection |
| `R` | Reset staged selection to the active configuration |
| `q` | Quit, with confirmation when selection is dirty |

Apply preview requires `Enter` before any file is changed.

## Data Model

By default, `pim` uses `~/.pi/agent`. Set `PI_CODING_AGENT_DIR` to manage another Pi agent directory.

```text
~/.pi/agent/
├── models.json
└── model-library/
    ├── providers/
    │   ├── provider-a.json
    │   └── provider-b.json
    └── history/
        └── models-<timestamp>.json
```

Each `providers/<provider-id>.json` stores one complete provider object. The library is the inventory source of truth; `models.json` contains only the currently selected providers and models.

On first run, when `model-library/providers` does not exist, `pim` imports the current `models.json`. Once the providers directory exists, including when it is empty, `pim` never repopulates it from `models.json`.

## Apply Safety

Apply performs the following transaction:

1. Generate a complete `models.json` from the library and staged selection.
2. Save the previous file under `model-library/history`.
3. Sync a temporary file and atomically rename it over `models.json`.
4. Run `pi --list-models` in an isolated `PI_CODING_AGENT_DIR` environment.
5. Restore the backup if replacement or verification fails.

When startup detects selected providers or models missing from the library, `pim` shows a conflict screen. **Fix and continue** opens a removal preview; quitting or pressing `Esc` before confirmation leaves `models.json` unchanged.

## Development

```sh
gofmt -w library/*.go apply/*.go tui/*.go cmd/pim/*.go
go test -race -cover ./...
go vet ./...
go build ./...
```

Run the opt-in smoke test against the real `pi` executable using a temporary agent directory:

```sh
PIM_PI_SMOKE=1 go test ./apply -run TestApplySmokeWithPi -v
```

## License

MIT
