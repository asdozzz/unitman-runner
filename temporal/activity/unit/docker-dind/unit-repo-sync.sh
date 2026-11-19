#!/bin/sh
set -eu

APP_DIR=${UNITMAN_APP_DIR:-/app}
SEED_DIR=${UNITMAN_SEED_DIR:-/seed/git}
URL=${UNITMAN_REPO_URL:-}
BRANCH=${UNITMAN_REPO_BRANCH:-}

git config --global --add safe.directory "$APP_DIR" 2>/dev/null || true
git config --global user.name >/dev/null 2>&1 || git config --global user.name "unitman"
git config --global user.email >/dev/null 2>&1 || git config --global user.email "unitman@localhost"

mkdir -p "$APP_DIR"
cd "$APP_DIR"

if [ ! -d .git ]; then
    if [ ! -d "$SEED_DIR" ]; then
        echo "seed repository not found: $SEED_DIR" >&2
        exit 10
    fi

    git init -q .

    if [ -n "$URL" ]; then
        git remote add origin "$URL"
    fi

    if [ -z "$BRANCH" ]; then
        BRANCH=$(git -C "$SEED_DIR" symbolic-ref --quiet --short HEAD 2>/dev/null || true)
    fi

    if [ -z "$BRANCH" ]; then
        echo "branch is not specified and cannot be read from seed repository" >&2
        exit 11
    fi

    if ! git fetch --prune "$SEED_DIR" "+refs/remotes/origin/$BRANCH:refs/remotes/origin/$BRANCH"; then
        git fetch --prune "$SEED_DIR" "+refs/remotes/origin/*:refs/remotes/origin/*"
    fi

    git checkout -B "$BRANCH" "refs/remotes/origin/$BRANCH"
else
    if [ -n "$URL" ]; then
        if git remote get-url origin >/dev/null 2>&1; then
            git remote set-url origin "$URL"
        else
            git remote add origin "$URL"
        fi
    fi

    CURRENT_BRANCH=$(git symbolic-ref --quiet --short HEAD 2>/dev/null || true)

    if [ -z "$BRANCH" ]; then
        BRANCH=$CURRENT_BRANCH
    fi

    if [ -z "$BRANCH" ]; then
        echo "current branch is detached and no target branch is specified" >&2
        exit 12
    fi

    if [ "$CURRENT_BRANCH" != "$BRANCH" ]; then
        git fetch --prune origin "+refs/heads/$BRANCH:refs/remotes/origin/$BRANCH"

        STASHED=0
        if [ -n "$(git status --porcelain)" ]; then
            git stash push --include-untracked -m "unitman-before-branch-switch"
            STASHED=1
        fi

        git checkout -B "$BRANCH" "refs/remotes/origin/$BRANCH"

        if [ "$STASHED" -eq 1 ]; then
            if ! git stash pop; then
                echo "conflict while restoring local changes after branch switch; stash preserved" >&2
                exit 3
            fi
        fi
    else
        git fetch --prune origin "+refs/heads/$BRANCH:refs/remotes/origin/$BRANCH"
        git branch --set-upstream-to="origin/$BRANCH" "$BRANCH" >/dev/null 2>&1 || true

        if ! git pull --rebase --autostash origin "$BRANCH"; then
            echo "rebase conflict; local changes are preserved in the working tree or stash" >&2
            exit 4
        fi
    fi
fi

git branch --set-upstream-to="origin/$BRANCH" "$BRANCH" >/dev/null 2>&1 || true

echo "repository is ready on branch $BRANCH"
