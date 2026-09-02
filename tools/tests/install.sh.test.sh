#!/bin/sh
# Tests for install.sh. Serves a fake GitHub via a local HTTP stub so the
# download, checksum and install flows can be exercised without the network.
#
# Usage: sh tools/tests/install.sh.test.sh
set -eu

TEST_DIR="$(cd "$(dirname "$0")" && pwd)"
INSTALL_SH="$(cd "$(dirname "$0")/../.." && pwd)/install.sh"
STUB_PORT="${STUB_PORT:-18091}"
BASE="http://127.0.0.1:${STUB_PORT}"
FAKE_SUM="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
PASS=0
FAIL=0

log()  { echo "==> $*"; }
ok()   { echo " ok: $*"; PASS=$((PASS + 1)); }
bad()  { echo " FAIL: $*" >&2; FAIL=$((FAIL + 1)); }

digest_of() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | cut -d' ' -f1
    else
        shasum -a 256 "$1" | cut -d' ' -f1
    fi
}

# ---------------------------------------------------------------------------
# HTTP stub: python3 is the only dependency. BASE/REPO vars are expanded into
# each served file's URL space, so the installer never touches the network.
# ---------------------------------------------------------------------------
command -v python3 >/dev/null 2>&1 || { echo "python3 required for tests" >&2; exit 1; }
# Use a TMPDIR under the workspace: /tmp may be quota-limited on some hosts.
TEST_TMPDIR="$(mktemp -d "${TMPDIR:-/tmp}/lexr-install-test.XXXXXX" 2>/dev/null || true)"
if [ -z "$TEST_TMPDIR" ]; then
    TEST_TMPDIR="$(dirname "$TEST_DIR")/../../.tmp/lexr-install-test.$$.tmp"
    mkdir -p "$TEST_TMPDIR"
fi
STUB_ROOT="$TEST_TMPDIR/stub"
TMP_HOME="$TEST_TMPDIR/home"
mkdir -p "$STUB_ROOT" "$TMP_HOME"
trap 'rm -rf "$TEST_TMPDIR"; kill "$STUB_PID" 2>/dev/null || true' EXIT INT TERM

# Build fake release assets: a binary whose real checksum is recorded in the
# manifest, plus a second manifest with a deliberately wrong checksum.
printf '#!/bin/sh\necho fake-lexr 9.9.9\n' > "${STUB_ROOT}/lexr-v9.9.9-linux-amd64"
chmod +x "${STUB_ROOT}/lexr-v9.9.9-linux-amd64"
REAL_SUM="$(digest_of "${STUB_ROOT}/lexr-v9.9.9-linux-amd64")"
echo "${REAL_SUM}  lexr-v9.9.9-linux-amd64" > "${STUB_ROOT}/lexr-v9.9.9.sha256sums"
echo "${FAKE_SUM}  lexr-v9.9.9-linux-amd64" > "${STUB_ROOT}/lexr-v9.9.9.bad.sha256sums"
printf '{"tag_name":"v9.9.9"}' > "${STUB_ROOT}/latest.json"

cat > "${STUB_ROOT}/server.py" <<EOF
import sys
from http.server import HTTPServer, SimpleHTTPRequestHandler

class Quiet(SimpleHTTPRequestHandler):
    def log_message(self, *a):
        pass

HTTPServer(("127.0.0.1", ${STUB_PORT}), Quiet).serve_forever()
EOF
(cd "$STUB_ROOT" && python3 server.py) &
STUB_PID=$!
i=0
while ! curl -s -o /dev/null "$BASE/latest.json" 2>/dev/null; do
    i=$((i + 1))
    [ "$i" -lt 50 ] || { echo "stub server failed to start" >&2; exit 1; }
    sleep 0.1
done

# Redirect the installer's GitHub URLs to the local stub. The stub mirrors the
# real layout: <base>/releases/download/v<version>/<asset>, and the rewritten
# API lookup points at <base>/latest.json in the same directory tree.
sed \
    -e "s#^GITHUB_BASE=.*#GITHUB_BASE=\"${BASE}\"#" \
    -e "s#^REPO=.*#REPO=\"lexr.sh\"#" \
    -e "s#https://api.github.com/repos/\${REPO}/releases/latest#http://127.0.0.1:${STUB_PORT}/latest.json#" \
    "$INSTALL_SH" > "${STUB_ROOT}/install.test.sh"
# The stub's release assets live directly under releases/download, so drop the
# versioned directory the real GitHub layout uses.
sed -i.bak "s#\${GITHUB_BASE}/releases/download/v\${VERSION}/#\${GITHUB_BASE}/#g" "${STUB_ROOT}/install.test.sh"

run() {
    HOME="$TMP_HOME" LEXR_OS="${LEXR_OS:-linux}" LEXR_ARCH="${LEXR_ARCH:-amd64}" \
        sh "${STUB_ROOT}/install.test.sh" "$@"
}

fresh_home() {
    rm -rf "$TMP_HOME"
    mkdir -p "$TMP_HOME"
}

# ---------------------------------------------------------------------------
# 1. Help output
# ---------------------------------------------------------------------------
log "help output"
fresh_home
out="$(run --help)"
case "$out" in
    *"Install Lexr"*) ok "--help prints usage" ;;
    *) bad "--help output missing usage" ;;
esac

# ---------------------------------------------------------------------------
# 2. Latest-version install with checksum verification
# ---------------------------------------------------------------------------
log "latest install"
fresh_home
run --no-modify-path >/dev/null
[ -x "${TMP_HOME}/.local/bin/lexr" ] && ok "installed to ~/.local/bin/lexr" \
    || bad "expected ${TMP_HOME}/.local/bin/lexr to exist"
[ "$("${TMP_HOME}/.local/bin/lexr")" = "fake-lexr 9.9.9" ] \
    && ok "installed binary runs" || bad "installed binary did not run"

# ---------------------------------------------------------------------------
# 3. Explicit version
# ---------------------------------------------------------------------------
log "explicit version"
fresh_home
run --version 9.9.9 --no-modify-path >/dev/null \
    && ok "--version 9.9.9 installed" || bad "--version install failed"

# ---------------------------------------------------------------------------
# 4. Idempotency
# ---------------------------------------------------------------------------
log "idempotency"
fresh_home
run --no-modify-path >/dev/null
run --no-modify-path >/dev/null
[ -x "${TMP_HOME}/.local/bin/lexr" ] && ok "re-run succeeded" \
    || bad "second run failed"

# ---------------------------------------------------------------------------
# 5. Local binary install
# ---------------------------------------------------------------------------
log "local binary"
fresh_home
cp "${STUB_ROOT}/lexr-v9.9.9-linux-amd64" "${STUB_ROOT}/local-lexr"
run --binary "${STUB_ROOT}/local-lexr" --no-modify-path >/dev/null \
    && ok "--binary installed without download" \
    || bad "--binary install failed"
run --binary "${STUB_ROOT}/missing-file" --no-modify-path >/dev/null 2>&1 \
    && bad "missing --binary should fail" || ok "missing --binary rejected"

# ---------------------------------------------------------------------------
# 6. Checksum mismatch must fail
# ---------------------------------------------------------------------------
log "checksum mismatch"
fresh_home
# install.sh derives the manifest name as lexr-v${VERSION}.sha256sums; rewrite
# that derivation so the bad manifest is fetched instead of the real one.
sed "s#\${MANIFEST_NAME}#lexr-v9.9.9.bad.sha256sums#g" "${STUB_ROOT}/install.test.sh" \
    > "${STUB_ROOT}/install.bad.sh"
if HOME="$TMP_HOME" LEXR_OS=linux LEXR_ARCH=amd64 \
       sh "${STUB_ROOT}/install.bad.sh" --no-modify-path >/dev/null 2>&1; then
    bad "checksum mismatch was not detected"
else
    ok "checksum mismatch rejected"
fi
[ ! -e "${TMP_HOME}/.local/bin/lexr" ] && ok "nothing installed on mismatch" \
    || bad "binary installed despite checksum mismatch"

# ---------------------------------------------------------------------------
# 7. Unsupported architecture
# ---------------------------------------------------------------------------
log "unsupported architecture"
fresh_home
if LEXR_OS=linux LEXR_ARCH=ppc64 HOME="$TMP_HOME" \
       run --no-modify-path >/dev/null 2>&1; then
    bad "unsupported arch accepted"
else
    ok "unsupported arch rejected"
fi

# ---------------------------------------------------------------------------
# 8. PATH modification happens exactly once
# ---------------------------------------------------------------------------
log "path modification"
fresh_home
SHELL=/bin/sh run >/dev/null
grep -q "lexr install" "${TMP_HOME}/.profile" \
    && ok "PATH marker written to .profile" || bad "PATH marker missing"
SHELL=/bin/sh run >/dev/null
count="$(grep -c "lexr install" "${TMP_HOME}/.profile" || true)"
[ "$count" -eq 1 ] && ok "PATH marker not duplicated" \
    || bad "PATH marker written ${count} times"

echo
echo "passed: ${PASS}, failed: ${FAIL}"
[ "$FAIL" -eq 0 ] || exit 1
