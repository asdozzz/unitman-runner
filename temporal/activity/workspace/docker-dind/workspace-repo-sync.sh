#!/bin/sh
set -eu

WORKSPACE_DIR=${UNITMAN_WORKSPACE_DIR:-/workspace}
PROJECT_ID=${UNITMAN_PROJECT_ID:-}
PROJECT_CODE=${UNITMAN_PROJECT_CODE:-}
URL=${UNITMAN_REPO_URL:-}
BRANCH=${UNITMAN_REPO_BRANCH:-}
READ_ONLY=${UNITMAN_READ_ONLY:-false}

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
        echo "$OUT" >&2
        case "$OUT" in
            *"not found"*|*"Could not find remote branch"*|*"Remote branch"*)
                fail "opencode.workspace.project_branch_not_found"
                ;;
            *)
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

    if ! git rev-parse --verify --quiet "origin/$BRANCH" >/dev/null; then
        fail "opencode.workspace.project_branch_not_found"
    fi

    STASHED=0
    if [ -n "$(git status --porcelain)" ]; then
        git stash push --include-untracked -m "unitman-before-workspace-sync"
        STASHED=1
    fi

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
fi

# Read-only: токен не должен остаться в origin, а push запрещаем хуком.
if [ "$READ_ONLY" = "true" ]; then
    cd "$DIR"

    CLEAN_URL=$(printf '%s' "$URL" | sed -E 's#^([a-zA-Z][a-zA-Z0-9+.-]*://)[^/@]*@#\1#')
    if [ "$CLEAN_URL" != "$URL" ]; then
        git remote set-url origin "$CLEAN_URL"
    fi

    HOOK="$DIR/.git/hooks/pre-push"
    cat > "$HOOK" <<'UNITMAN_PRE_PUSH_EOF'
#!/bin/sh
echo "workspace is read-only: git push is forbidden" >&2
exit 1
UNITMAN_PRE_PUSH_EOF
    chmod +x "$HOOK"
fi

echo "workspace project $PROJECT_CODE ($PROJECT_ID) is ready on branch $BRANCH"
