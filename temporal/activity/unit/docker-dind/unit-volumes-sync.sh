#!/bin/sh
set -eu

STAGE_DIR=${UNITMAN_VOLUMES_STAGE:-/seed/volumes}
TARGET_DIR=${UNITMAN_VOLUMES_TARGET:-/app/unitman_volumes}
MARKERS_DIR=${UNITMAN_VOLUMES_MARKERS:-/app/.unitman/volumes}
MANIFEST_PATH="$STAGE_DIR/.manifest"

if [ ! -f "$MANIFEST_PATH" ]; then
    echo "volume manifest not found: $MANIFEST_PATH"
    exit 0
fi

while IFS= read -r dir; do
    [ -n "$dir" ] || continue

    case "$dir" in
        /*|..|../*|*/..|*/../*)
            echo "invalid volume directory: $dir" >&2
            exit 5
            ;;
    esac

    marker_path="$MARKERS_DIR/$dir"
    target_path="$TARGET_DIR/$dir"

    if [ -e "$marker_path" ]; then
        echo "volume $dir: already seeded"
        continue
    fi

    mkdir -p "$target_path" "$(dirname "$marker_path")"

    source_path="$STAGE_DIR/$dir"

    if [ -d "$source_path" ] && [ -n "$(ls -A "$source_path" 2>/dev/null)" ]; then
        if [ -z "$(ls -A "$target_path" 2>/dev/null)" ]; then
            cp -a "$source_path"/. "$target_path"/
        else
            ( cd "$source_path" && find . -type d -print | while IFS= read -r item; do
                mkdir -p "$target_path/${item#./}"
            done )

            ( cd "$source_path" && find . -type f -print | while IFS= read -r item; do
                relative_path=${item#./}
                if [ ! -e "$target_path/$relative_path" ]; then
                    mkdir -p "$(dirname "$target_path/$relative_path")"
                    cp -p "$item" "$target_path/$relative_path"
                fi
            done )
        fi
    fi

    : > "$marker_path"
    echo "volume $dir: seeded"
done < "$MANIFEST_PATH"
