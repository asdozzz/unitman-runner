#!/bin/sh
# unit-skills-sync — материализация OpenCode-навыков из read-only staging-а в
# конфиг-каталог OpenCode. Скрипт идемпотентен и НИКОГДА не валит контейнер:
# любая ошибка логируется в stderr, но код возврата всегда 0.
#
# Источник (read-only bind mount): /opt/unitman/skills
#   <containerName>/SKILL.md
#   skills-index.md
#   manifest.json
#
# Целевое размещение:
#   $HOME/.config/opencode/skill/<containerName>/SKILL.md
#   $HOME/.config/opencode/skills-index.md
#   $HOME/.config/opencode/.unitman-skills-hash   (manifestHash)
#   /app/.unitman/skills-manifest.json            (диагностика)
#
# Флаг --if-changed: если сохранённый hash совпадает с manifestHash, скрипт
# завершается сразу, не трогая файлы.

set -u

SRC=/opt/unitman/skills
HOME="${HOME:-/root}"
CONFIG_DIR="$HOME/.config/opencode"
DEST_DIR="$CONFIG_DIR/skill"
INDEX_FILE="$CONFIG_DIR/skills-index.md"
HASH_FILE="$CONFIG_DIR/.unitman-skills-hash"
DIAG_DIR=/app/.unitman
DIAG_FILE="$DIAG_DIR/skills-manifest.json"

log() {
  echo "unit-skills-sync: $*" >&2
}

manifest_hash() {
  # Извлекаем значение "manifestHash": "..."; первое совпадение.
  sed -n 's/.*"manifestHash"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$SRC/manifest.json" 2>/dev/null | head -n 1
}

write_empty_index() {
  mkdir -p "$CONFIG_DIR" 2>/dev/null || true
  if [ ! -f "$INDEX_FILE" ]; then
    printf '%s\n' "# Agent Skills" "" "No project skills are configured." >"$INDEX_FILE" 2>/dev/null || true
  fi
}

# ─── fast path ───────────────────────────────────────────────────────────────
if [ "${1:-}" = "--if-changed" ]; then
  if [ ! -d "$SRC" ] || [ ! -f "$SRC/manifest.json" ]; then
    exit 0
  fi
  want="$(manifest_hash)"
  have=""
  [ -f "$HASH_FILE" ] && have="$(cat "$HASH_FILE" 2>/dev/null)"
  if [ -n "$want" ] && [ "$want" = "$have" ]; then
    exit 0
  fi
fi

# ─── отсутствие источника: обратная совместимость ────────────────────────────
if [ ! -d "$SRC" ]; then
  # Не трогаем существующий skill-каталог, но гарантируем, что instructions-
  # файл из OPENCODE_CONFIG_CONTENT существует и не ломает ход.
  write_empty_index
  exit 0
fi

# ─── материализация ──────────────────────────────────────────────────────────
mkdir -p "$CONFIG_DIR" 2>/dev/null || log "cannot create $CONFIG_DIR"

if ! rm -rf "$DEST_DIR" 2>/dev/null; then
  log "cannot clean $DEST_DIR"
fi
mkdir -p "$DEST_DIR" 2>/dev/null || log "cannot create $DEST_DIR"

for dir in "$SRC"/*/; do
  [ -d "$dir" ] || continue
  name="$(basename "$dir")"
  if [ ! -f "$dir/SKILL.md" ]; then
    continue
  fi
  if ! mkdir -p "$DEST_DIR/$name" 2>/dev/null; then
    log "skip $name: cannot create directory"
    continue
  fi
  if ! cp "$dir/SKILL.md" "$DEST_DIR/$name/SKILL.md" 2>/dev/null; then
    log "skip $name: copy failed"
  fi
done

if [ -f "$SRC/skills-index.md" ]; then
  if ! cp "$SRC/skills-index.md" "$INDEX_FILE" 2>/dev/null; then
    log "cannot copy skills-index.md"
    write_empty_index
  fi
else
  write_empty_index
fi

want="$(manifest_hash)"
if [ -n "$want" ]; then
  if ! printf '%s' "$want" >"$HASH_FILE" 2>/dev/null; then
    log "cannot write $HASH_FILE"
  fi
fi

# Диагностическая копия манифеста. Единственная запись за пределами
# $HOME/.config/opencode — /app/.unitman/skills-manifest.json.
if [ -f "$SRC/manifest.json" ]; then
  mkdir -p "$DIAG_DIR" 2>/dev/null || true
  cp "$SRC/manifest.json" "$DIAG_FILE" 2>/dev/null || log "cannot copy manifest to $DIAG_FILE"
fi

exit 0
