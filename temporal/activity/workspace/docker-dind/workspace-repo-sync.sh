#!/bin/sh
set -eu

WORKSPACE_DIR=${UNITMAN_WORKSPACE_DIR:-/workspace}
PROJECT_ID=${UNITMAN_PROJECT_ID:-}
PROJECT_CODE=${UNITMAN_PROJECT_CODE:-}
URL=${UNITMAN_REPO_URL:-}
BRANCH=${UNITMAN_REPO_BRANCH:-}
BASE_BRANCH=${UNITMAN_REPO_BASE_BRANCH:-}
READ_ONLY=${UNITMAN_READ_ONLY:-false}
GIT_ASKPASS=${UNITMAN_GIT_ASKPASS:-}

# Креденшелы передаются только через askpass-файл (0600), не через URL/env/argv.
if [ -n "$GIT_ASKPASS" ]; then
    export GIT_ASKPASS
    export GIT_ASKPASS_REQUIRE=force
fi
export GIT_TERMINAL_PROMPT=0

fail() {
    # Ключ ошибки читает Go-активность и отдаёт в ProjectResult.ErrorKey.
    echo "UNITMAN_ERROR_KEY=$1" >&2
    exit 1
}

if [ -z "$PROJECT_ID" ]; then
    fail "opencode.workspace.project_not_found"
fi

if [ -z "$BRANCH" ]; then
    fail "opencode.workspace.branch_required"
fi

DIR="$WORKSPACE_DIR/$PROJECT_ID"

git config --global --add safe.directory "$DIR" 2>/dev/null || true
git config --global user.name >/dev/null 2>&1 || git config --global user.name "unitman"
git config --global user.email >/dev/null 2>&1 || git config --global user.email "unitman@localhost"

# Клонирование при первом запуске.
if [ ! -d "$DIR/.git" ]; then
    mkdir -p "$DIR"

    if ! OUT=$(git clone --branch "$BRANCH" "$URL" "$DIR" 2>&1); then
        case "$OUT" in
            *"not found in upstream origin"*|*"Could not find remote branch"*|*"Remote branch"*)
                # Ветки workspace нет в origin — клонируем базовую ветку
                # (или дефолтную, если базовая не задана/недоступна) и создаём
                # локальную ветку без upstream.
                rm -rf "$DIR"
                mkdir -p "$DIR"

                CLONED=0
                if [ -n "$BASE_BRANCH" ]; then
                    if OUT=$(git clone --branch "$BASE_BRANCH" "$URL" "$DIR" 2>&1); then
                        CLONED=1
                    fi
                fi

                if [ "$CLONED" -eq 0 ]; then
                    rm -rf "$DIR"
                    mkdir -p "$DIR"
                    if ! OUT=$(git clone "$URL" "$DIR" 2>&1); then
                        echo "$OUT" >&2
                        fail "opencode.workspace.project_clone_failed"
                    fi
                fi

                cd "$DIR"
                if ! git checkout -b "$BRANCH"; then
                    fail "opencode.workspace.project_branch_not_found"
                fi
                ;;
            *)
                echo "$OUT" >&2
                fail "opencode.workspace.project_clone_failed"
                ;;
        esac
    fi
else
    cd "$DIR"

    if git remote get-url origin >/dev/null 2>&1; then
        git remote set-url origin "$URL"
    else
        git remote add origin "$URL"
    fi

    if ! git fetch --prune origin; then
        fail "opencode.workspace.project_clone_failed"
    fi

    if git rev-parse --verify --quiet "origin/$BRANCH" >/dev/null; then
        TARGET_ORIGIN=1
    elif git rev-parse --verify --quiet "refs/heads/$BRANCH" >/dev/null; then
        # Локальная ветка уже создана ранее — upstream у неё нет, pull не нужен.
        TARGET_ORIGIN=0
    else
        fail "opencode.workspace.project_branch_not_found"
    fi

    STASHED=0
    if [ -n "$(git status --porcelain)" ]; then
        git stash push --include-untracked -m "unitman-before-workspace-sync"
        STASHED=1
    fi

    if [ "$TARGET_ORIGIN" -eq 1 ]; then
        if ! git checkout -B "$BRANCH" "origin/$BRANCH"; then
            fail "opencode.workspace.project_branch_not_found"
        fi

        if [ "$STASHED" -eq 1 ]; then
            if ! git stash pop; then
                echo "conflict while restoring local changes; stash preserved" >&2
                fail "opencode.workspace.operation_failed"
            fi
        fi

        git branch --set-upstream-to="origin/$BRANCH" "$BRANCH" >/dev/null 2>&1 || true

        if ! git pull --rebase --autostash origin "$BRANCH"; then
            fail "opencode.workspace.operation_failed"
        fi
    else
        if ! git checkout "$BRANCH"; then
            fail "opencode.workspace.project_branch_not_found"
        fi

        if [ "$STASHED" -eq 1 ]; then
            if ! git stash pop; then
                echo "conflict while restoring local changes; stash preserved" >&2
                fail "opencode.workspace.operation_failed"
            fi
        fi
    fi
fi

# После синхронизации remote всегда без userinfo, credential helpers и
# askpass-файл удаляются. Read-only дополнительно запрещает push хуком.
cd "$DIR"

CLEAN_URL=$(printf '%s' "$URL" | sed -E 's#^([a-zA-Z][a-zA-Z0-9+.-]*://)[^/@]*@#\1#')
if git remote get-url origin >/dev/null 2>&1; then
    git remote set-url origin "$CLEAN_URL"
fi
git config --local --unset-all credential.helper >/dev/null 2>&1 || true
rm -f "${UNITMAN_GIT_ASKPASS:-}" /tmp/unitman-git-credentials

if [ "$READ_ONLY" = "true" ]; then
    HOOK="$DIR/.git/hooks/pre-push"
    cat > "$HOOK" <<'UNITMAN_PRE_PUSH_EOF'
#!/bin/sh
echo "workspace is read-only: git push is forbidden" >&2
exit 1
UNITMAN_PRE_PUSH_EOF
    chmod +x "$HOOK"
fi

echo "workspace project $PROJECT_CODE ($PROJECT_ID) is ready on branch $BRANCH"
