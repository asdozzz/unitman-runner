#!/bin/sh
# WS-10 dependency-free fallback secret scanner.
#
# Scans tracked files (git ls-files) for high-signal credential patterns.
# Prints ONLY "file:line: rule" - matched values are never echoed.
# Exits 1 when findings are present (fail-closed).
#
# Usage:
#   scripts/check-secrets.sh                 scan tracked files of the current repo
#   scripts/check-secrets.sh --root DIR      scan an arbitrary directory
#   scripts/check-secrets.sh --self-test     run the built-in detection test
#   scripts/check-secrets.sh --no-verify     LOCAL-ONLY escape hatch (prints warning,
#                                            exits 0). Never use in CI.
#
# A missing git repository or a scan error is a failure, not a skip.

set -eu

# Exact synthetic fixtures and rule-definition files. Never real secrets.
# Paths excluded: VCS metadata, dependency trees and the scanner's own rule
# definitions (they contain the literal patterns by necessity).
EXCLUDE_RE='(^|/)\.git/|(^|/)\.idea/|(^|/)node_modules/|(^|/)vendor/|(^|/)runner\.log$|(^|/)\.gitleaks\.toml$|(^|/)\.pre-commit-config\.yaml$|(^|/)scripts/check-secrets\.sh$'

ALLOW_RE='(TEST[A-Z0-9_]{3,}|not-for-production|dGVzdC1vbmx5LW9wZW5jb2RlLWtleS0zMmJ5dGVzISE=|dGVzdC1vbmx5LXBlcnNvbmFsLXRva2VuLWtleS0zMmI=|test-secret|change-me|admin_secret|<redacted>|://token@|://redacted@|oauth2:\[\^|AKIAIOSFODNN7EXAMPLE|0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef|sk-live-abcdef123456|glpat-(secret|secret-1|secret-1234|no-rights|abcdef123456|project-secret)([^A-Za-z0-9_-]|$)|oauth2:(token|TOKEN|secret-token|glpat-secret|<token>)@|oauth2:'"'"'\.\$repo->getCredentials\(\)->token\.|Bearer slomannyj\.token\.znachenie|ghp_EXAMPLE[A-Za-z0-9]*|github_pat_EXAMPLE[A-Za-z0-9_]*|glpat-EXAMPLE[A-Za-z0-9_-]*|dGVzdC1vbmx5LWtleS0xNg==|github_pat_([^A-Za-z0-9_]|$)|glpat-([^A-Za-z0-9_-]|$))'

rules() {
  cat <<'EOF'
github_fine_grained_pat@@github_pat_
github_classic_pat@@(ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{20,}
gitlab_pat@@glpat-
private_key@@-----BEGIN [A-Z ]*PRIVATE KEY-----
oauth2_url_credential@@oauth2:[^@/[:space:]]+@
url_token_credential@@://[^/[:space:]]*token@
env_app_secret@@(^|[^A-Za-z0-9_])APP_SECRET=[^[:space:]$][^[:space:]]*
env_jwt_passphrase@@(^|[^A-Za-z0-9_])JWT_PASSPHRASE=[^[:space:]$][^[:space:]]*
env_mediateka_upload_token_secret@@(^|[^A-Za-z0-9_])MEDIATEKA_UPLOAD_TOKEN_SECRET=[^[:space:]$][^[:space:]]*
env_centrifugo_secret@@(^|[^A-Za-z0-9_])CENTRIFUGO_SECRET=[^[:space:]$][^[:space:]]*
bearer_token@@Bearer [A-Za-z0-9._-]{20,}
openai_key@@sk-[A-Za-z0-9_-]{20,}
EOF
}

# scan_list <NUL-separated-file-list> <output-file>
# Writes "path:line: rule" lines. Values never leave the pipe.
scan_list() {
  _list=$1
  _out=$2
  : > "$_out"
  while IFS= read -r _line; do
    [ -z "$_line" ] && continue
    _rule=${_line%%@@*}
    _pat=${_line#*@@}
    # shellcheck disable=SC2016
    # -H forces the filename prefix even for a single file, so the sed below
    # always strips the matched value and only file:line survives.
    xargs -0 -r grep -nHEI -- "$_pat" < "$_list" 2>/dev/null \
      | grep -vE -- "$ALLOW_RE" \
      | sed -E 's/^(.+:[0-9]+):.*$/\1/' \
      | while IFS= read -r _loc; do
          printf '%s: %s\n' "$_loc" "$_rule"
        done >> "$_out"
  done < "$RULES_FILE"
}

collect_git() {
  git ls-files -z 2>/dev/null | grep -zvE -- "$EXCLUDE_RE"
}

collect_dir() {
  find "$1" -type f -print0 2>/dev/null | grep -zvE -- "$EXCLUDE_RE"
}

print_findings() {
  _out=$1
  _count=$(wc -l < "$_out" | tr -d ' ')
  if [ "$_count" -gt 0 ]; then
    printf 'secret-scan: %s finding(s):\n' "$_count"
    sort "$_out"
    return 1
  fi
  printf 'secret-scan: 0 findings\n'
  return 0
}

run_self_test() {
  _tmp=$(mktemp -d)
  trap 'rm -rf "$_tmp"' EXIT HUP INT TERM
  mkdir -p "$_tmp/main" "$_tmp/fixtures" "$_tmp/realistic"
  cat > "$_tmp/main/sample.txt" <<'EOF'
github_pat_ZZQONLYFIXTURE1234567890
ghp_ZZQONLYFIXTURE1234567890
glpat-ZZQONLYFIXTURE1234567890
-----BEGIN RSA PRIVATE KEY-----
https://oauth2:ZZQONLYCREDENTIAL@example.invalid/repo.git
https://user:ZZQONLYtoken@example.invalid/repo.git
APP_SECRET=ZZQONLYAPPSECRET
JWT_PASSPHRASE=ZZQONLYJWTPASS
MEDIATEKA_UPLOAD_TOKEN_SECRET=ZZQONLYMEDIATEKA
CENTRIFUGO_SECRET=ZZQONLYCENTRIFUGO
Authorization: Bearer ZZQONLYBEARERTOKEN123456
OPENAI_API_KEY=sk-ZZQONLYFIXTURE1234567890abcdef
EOF
  # Negative control: empty placeholders must not be reported.
  printf 'APP_SECRET=\nJWT_PASSPHRASE=\nMEDIATEKA_UPLOAD_TOKEN_SECRET=\nCENTRIFUGO_SECRET=\n' > "$_tmp/main/empty.env"

  # Allowlist precision control: the EXAMPLE-marked synthetic fixtures must NOT
  # be reported (they are deliberately allowlisted above).
  cat > "$_tmp/fixtures/fixtures.txt" <<'EOF'
ghp_EXAMPLE000000000000000000000000000000
github_pat_EXAMPLE000000000000000000000000000000
glpat-EXAMPLE000000000000000000000
dGVzdC1vbmx5LWtleS0xNg==
EOF

  # Precision control: realistic random-looking values that are NOT marked
  # EXAMPLE must still be detected and must fail the scan. Values are generated
  # at runtime and are never printed.
  _rand1=$(od -An -N24 -tx1 /dev/urandom | tr -d ' \n')
  _rand2=$(od -An -N16 -tx1 /dev/urandom | tr -d ' \n')
  _rand3=$(od -An -N16 -tx1 /dev/urandom | tr -d ' \n')
  {
    printf 'ghp_%s\n' "$_rand1"
    printf 'github_pat_11%s\n' "$_rand2"
    printf 'glpat-%s\n' "$_rand3"
    printf '%s\n' '-----BEGIN RSA PRIVATE KEY-----'
    printf '%s\n' "$_rand1$_rand2"
    printf '%s\n' '-----END RSA PRIVATE KEY-----'
  } > "$_tmp/realistic/realistic.txt"

  collect_dir "$_tmp/main" > "$_tmp/main.list"
  scan_list "$_tmp/main.list" "$_tmp/main.out"
  collect_dir "$_tmp/fixtures" > "$_tmp/fixtures.list"
  scan_list "$_tmp/fixtures.list" "$_tmp/fixtures.out"
  collect_dir "$_tmp/realistic" > "$_tmp/realistic.list"
  scan_list "$_tmp/realistic.list" "$_tmp/realistic.out"
  rm -f "$_tmp"/*.list

  _fail=0
  for _r in github_fine_grained_pat github_classic_pat gitlab_pat private_key \
            oauth2_url_credential url_token_credential env_app_secret \
            env_jwt_passphrase env_mediateka_upload_token_secret \
            env_centrifugo_secret bearer_token openai_key; do
    if grep -qE -- ": $_r$" "$_tmp/main.out"; then
      printf 'self-test: detected %s\n' "$_r"
    else
      printf 'self-test: MISSED %s\n' "$_r"
      _fail=1
    fi
  done

  # Synthetic EXAMPLE fixtures must be allowlisted and must not fail the scan.
  if [ -s "$_tmp/fixtures.out" ]; then
    printf 'self-test: FAILED - allowlisted EXAMPLE fixtures were reported\n'
    _fail=1
  else
    printf 'self-test: EXAMPLE fixtures ignored\n'
  fi
  if print_findings "$_tmp/fixtures.out" >/dev/null 2>&1; then
    printf 'self-test: EXAMPLE fixtures do not fail the scan\n'
  else
    printf 'self-test: FAILED - EXAMPLE fixtures caused a scan failure\n'
    _fail=1
  fi

  # Realistic random tokens must still be detected and fail closed.
  for _r in github_classic_pat github_fine_grained_pat gitlab_pat private_key; do
    if grep -qE -- "realistic\.txt:[0-9]+: $_r$" "$_tmp/realistic.out"; then
      printf 'self-test: realistic %s detected\n' "$_r"
    else
      printf 'self-test: FAILED - realistic %s missed\n' "$_r"
      _fail=1
    fi
  done
  if print_findings "$_tmp/realistic.out" >/dev/null 2>&1; then
    printf 'self-test: FAILED - realistic-token scan did not fail closed\n'
    _fail=1
  else
    printf 'self-test: realistic-token scan fails closed\n'
  fi

  if grep -q 'ZZQONLY' "$_tmp/main.out"; then
    printf 'self-test: FAILED - secret value leaked into output\n'
    _fail=1
  else
    printf 'self-test: no secret values echoed\n'
  fi

  if grep -qF -- "$_rand1" "$_tmp/realistic.out" \
     || grep -qF -- "$_rand2" "$_tmp/realistic.out" \
     || grep -qF -- "$_rand3" "$_tmp/realistic.out"; then
    printf 'self-test: FAILED - realistic secret value leaked into output\n'
    _fail=1
  else
    printf 'self-test: no realistic secret values echoed\n'
  fi

  if grep -q 'empty\.env' "$_tmp/main.out"; then
    printf 'self-test: FAILED - empty placeholder reported\n'
    _fail=1
  else
    printf 'self-test: empty placeholders ignored\n'
  fi

  if [ "$_fail" -ne 0 ]; then
    printf 'self-test: FAILED\n'
    return 1
  fi
  printf 'self-test: PASSED\n'
  return 0
}

MODE=git
ROOT=
while [ $# -gt 0 ]; do
  case "$1" in
    --self-test) MODE=selftest ;;
    --root)
      shift
      [ $# -gt 0 ] || { printf 'secret-scan: --root requires a directory\n' >&2; exit 2; }
      MODE=dir
      ROOT=$1
      ;;
    --no-verify)
      printf 'secret-scan: WARNING --no-verify used; secret scan skipped (local only)\n' >&2
      exit 0
      ;;
    -h|--help)
      sed -n '2,20p' "$0"
      exit 0
      ;;
    *)
      printf 'secret-scan: unknown argument: %s\n' "$1" >&2
      exit 2
      ;;
  esac
  shift
done

RULES_FILE=$(mktemp)
OUT=$(mktemp)
LIST=$(mktemp)
trap 'rm -f "$RULES_FILE" "$OUT" "$LIST"' EXIT HUP INT TERM
rules > "$RULES_FILE"

case "$MODE" in
  selftest)
    run_self_test
    ;;
  dir)
    [ -d "$ROOT" ] || { printf 'secret-scan: not a directory: %s\n' "$ROOT" >&2; exit 2; }
    collect_dir "$ROOT" > "$LIST"
    scan_list "$LIST" "$OUT"
    print_findings "$OUT"
    ;;
  git)
    if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
      printf 'secret-scan: not a git repository (fail-closed)\n' >&2
      exit 2
    fi
    collect_git > "$LIST"
    scan_list "$LIST" "$OUT"
    print_findings "$OUT"
    ;;
esac
