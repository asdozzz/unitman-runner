#!/bin/sh
set -eu

DATA_DIR="/tmp/code-server-data"
EXTENSIONS_DIR="/tmp/code-server-extensions"
WORKSPACE_DIR="/home/coder/workspace"
SETTINGS_SOURCE="/opt/unitman-ide/settings.json"
CODE_WORKSPACE="$WORKSPACE_DIR/workspace.code-workspace"

# Корневая ФС read-only, поэтому XDG-каталоги (config/data/cache) уводим
# в tmpfs. Иначе code-server падает на старте:
# ENOENT: no such file or directory, mkdir '/home/coder/.config'.
export XDG_CONFIG_HOME="/tmp/code-server-xdg/config"
export XDG_DATA_HOME="/tmp/code-server-xdg/data"
export XDG_CACHE_HOME="/tmp/code-server-xdg/cache"

mkdir -p "$DATA_DIR/User" "$EXTENSIONS_DIR" \
    "$XDG_CONFIG_HOME" "$XDG_DATA_HOME" "$XDG_CACHE_HOME"

# Корневая ФС read-only — настройки копируются в tmpfs.
cp "$SETTINGS_SOURCE" "$DATA_DIR/User/settings.json"

# Marketplace отключается пустым gallery.
export EXTENSIONS_GALLERY='{"serviceUrl":"http://127.0.0.1:1/vscode/gallery","itemUrl":"http://127.0.0.1:1/vscode/item","resourceUrlTemplate":"http://127.0.0.1:1/vscode/extension"}'

set -- \
    --bind-addr "0.0.0.0:${UNITMAN_IDE_PORT:-47000}" \
    --auth none \
    --disable-telemetry \
    --disable-update-check \
    --disable-workspace-trust \
    --disable-getting-started-override \
    --user-data-dir "$DATA_DIR" \
    --extensions-dir "$EXTENSIONS_DIR"

if [ -n "${UNITMAN_IDE_BASE_PATH:-}" ]; then
    set -- "$@" --base-path "$UNITMAN_IDE_BASE_PATH"
fi

# Multi-root: если runner сгенерировал workspace-файл, открываем его.
if [ -f "$CODE_WORKSPACE" ]; then
    exec /usr/bin/code-server "$@" "$CODE_WORKSPACE"
fi

exec /usr/bin/code-server "$@" "$WORKSPACE_DIR"
