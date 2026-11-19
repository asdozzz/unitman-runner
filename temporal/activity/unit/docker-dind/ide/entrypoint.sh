#!/bin/sh
set -eu

DATA_DIR="/tmp/code-server-data"
EXTENSIONS_DIR="/tmp/code-server-extensions"
PROJECT_DIR="/home/coder/project"
SETTINGS_SOURCE="/opt/unitman-ide/settings.json"
GIT_INDEX_TARGET="/tmp/git-index"

mkdir -p "$DATA_DIR/User" "$EXTENSIONS_DIR"

# Корневая ФС read-only, поэтому настройки копируются в tmpfs.
cp "$SETTINGS_SOURCE" "$DATA_DIR/User/settings.json"

# Рабочая копия смонтирована только для чтения. Git не сможет писать индекс
# в .git, поэтому используем копию индекса в /tmp (GIT_INDEX_FILE).
if [ -f "$PROJECT_DIR/.git/index" ]; then
    cp "$PROJECT_DIR/.git/index" "$GIT_INDEX_TARGET"
fi

# Marketplace отключается пустым gallery: любые запросы установки расширений
# уходят на заведомо недоступный адрес. Встроенный git при этом остаётся.
export EXTENSIONS_GALLERY='{"serviceUrl":"http://127.0.0.1:1/vscode/gallery","itemUrl":"http://127.0.0.1:1/vscode/item","resourceUrlTemplate":"http://127.0.0.1:1/vscode/extension"}'

set -- \
    --bind-addr "0.0.0.0:${UNITMAN_IDE_PORT:-47000}" \
    --auth none \
    --disable-telemetry \
    --disable-update-check \
    --disable-file-downloads \
    --disable-file-uploads \
    --disable-proxy \
    --disable-workspace-trust \
    --disable-getting-started-override \
    --user-data-dir "$DATA_DIR" \
    --extensions-dir "$EXTENSIONS_DIR"

# Безопасный вариант доступа за публичным proxy: per-unit base path.
if [ -n "${UNITMAN_IDE_BASE_PATH:-}" ]; then
    set -- "$@" --base-path "$UNITMAN_IDE_BASE_PATH"
fi

exec /usr/bin/code-server "$@" "$PROJECT_DIR"
