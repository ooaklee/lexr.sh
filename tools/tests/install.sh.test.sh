#!/bin/sh
# shellcheck disable=SC2015
# Tests for install.sh. Serves fake GitHub release assets over a local HTTP
# server so download, provenance, checksum, and installation paths stay offline.
# Assertion helpers always succeed after recording the result, so the compact
# condition && ok || bad form is intentional.
#
# Usage: sh tools/tests/install.sh.test.sh
set -eu

TEST_DIR="$(cd "$(dirname "$0")" && pwd)"
INSTALL_SH="$(cd "$(dirname "$0")/../.." && pwd)/install.sh"
REQUESTED_PORT="${STUB_PORT:-0}"
FAKE_SUM="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
PASS=0
FAIL=0

log() { echo "==> $*"; }
ok()  { echo " ok: $*"; PASS=$((PASS + 1)); }
bad() { echo " FAIL: $*" >&2; FAIL=$((FAIL + 1)); }

digest_of() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | cut -d' ' -f1
    else
        shasum -a 256 "$1" | cut -d' ' -f1
    fi
}

mode_of() {
    if stat -c '%a' "$1" >/dev/null 2>&1; then
        stat -c '%a' "$1"
    else
        stat -f '%Lp' "$1"
    fi
}

command -v python3 >/dev/null 2>&1 || {
    echo "python3 required for tests" >&2
    exit 1
}

TEST_TMPDIR="$(mktemp -d "${TMPDIR:-/tmp}/lexr-install-test.XXXXXX" 2>/dev/null || true)"
if [ -z "$TEST_TMPDIR" ]; then
    TEST_TMPDIR="$(dirname "$TEST_DIR")/../../.tmp/lexr-install-test.$$.tmp"
    mkdir -p "$TEST_TMPDIR"
fi
STUB_ROOT="$TEST_TMPDIR/stub"
STUB_BIN="$TEST_TMPDIR/bin"
TMP_HOME="$TEST_TMPDIR/home"
GH_STUB_LOG="$TEST_TMPDIR/gh.log"
PORT_FILE="$TEST_TMPDIR/port"
mkdir -p "$STUB_ROOT" "$STUB_BIN" "$TMP_HOME"
STUB_PID=""
trap 'rm -rf "$TEST_TMPDIR"; [ -z "$STUB_PID" ] || kill "$STUB_PID" 2>/dev/null || true' EXIT INT TERM

make_release() {
    release_version="$1"
    binary="${STUB_ROOT}/lexr-v${release_version}-linux-amd64"
    cat > "$binary" <<EOF
#!/bin/sh
echo "fake-lexr ${release_version}"
EOF
    chmod +x "$binary"
    release_sum="$(digest_of "$binary")"
    echo "${release_sum}  lexr-v${release_version}-linux-amd64" \
        > "${STUB_ROOT}/lexr-v${release_version}.sha256sums"
}

make_release "9.9.9"
make_release "0.4.0"
make_release "0.5.0-rc.1"
make_release "0.5.0"
make_release "0.6.0-rc.1"
echo "${FAKE_SUM}  lexr-v9.9.9-linux-amd64" \
    > "${STUB_ROOT}/lexr-v9.9.9.bad.sha256sums"
cat "${STUB_ROOT}/lexr-v9.9.9.sha256sums" \
    "${STUB_ROOT}/lexr-v9.9.9.sha256sums" \
    > "${STUB_ROOT}/lexr-v9.9.9.duplicate.sha256sums"
printf '{"tag_name":"v9.9.9"}' > "${STUB_ROOT}/latest.json"
printf '{"fake":"attestation bundle"}\n' \
    > "${STUB_ROOT}/lexr-v9.9.9.sha256sums.intoto.jsonl"

cat > "${STUB_BIN}/gh" <<'EOF'
#!/bin/sh
mode="${GH_STUB_MODE:-unavailable}"
if [ "${1:-}" = "attestation" ] && [ "${2:-}" = "verify" ] && [ "${3:-}" = "--help" ]; then
    [ "$mode" != "unavailable" ]
    exit
fi
if [ "${1:-}" = "attestation" ] && [ "${2:-}" = "verify" ]; then
    printf '%s\n' "$*" >> "${GH_STUB_LOG}"
    [ "$mode" = "success" ]
    exit
fi
exit 1
EOF
chmod +x "${STUB_BIN}/gh"

REAL_INSTALL="$(command -v install || true)"
if [ -n "$REAL_INSTALL" ]; then
    cat > "${STUB_BIN}/install" <<EOF
#!/bin/sh
if [ "\${INSTALL_STUB_MODE:-}" = "fail" ]; then
    for destination do :; done
    printf 'partial install' > "\$destination"
    exit 1
fi
exec "${REAL_INSTALL}" "\$@"
EOF
    chmod +x "${STUB_BIN}/install"
fi

cat > "${STUB_ROOT}/server.py" <<'EOF'
import functools
import pathlib
import sys
from http.server import HTTPServer, SimpleHTTPRequestHandler

root = pathlib.Path(sys.argv[3])

class Quiet(SimpleHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def do_GET(self):
        failure_file = root / "fail-latest-count"
        if self.path == "/latest.json" and failure_file.exists():
            remaining = int(failure_file.read_text().strip() or "0")
            if remaining > 0:
                failure_file.write_text(str(remaining - 1))
                self.send_response(503)
                self.end_headers()
                return
        super().do_GET()

handler = functools.partial(Quiet, directory=str(root))
server = HTTPServer(("127.0.0.1", int(sys.argv[1])), handler)
pathlib.Path(sys.argv[2]).write_text(str(server.server_port))
server.serve_forever()
EOF
python3 "${STUB_ROOT}/server.py" "$REQUESTED_PORT" "$PORT_FILE" "$STUB_ROOT" &
STUB_PID=$!
i=0
while [ ! -s "$PORT_FILE" ]; do
    i=$((i + 1))
    [ "$i" -lt 50 ] || { echo "stub server failed to start" >&2; exit 1; }
    sleep 0.1
done
ACTUAL_PORT="$(cat "$PORT_FILE")"
BASE="http://127.0.0.1:${ACTUAL_PORT}"

sed \
    -e "s#^GITHUB_BASE=.*#GITHUB_BASE=\"${BASE}\"#" \
    -e 's#^REPO=.*#REPO="lexr.sh"#' \
    -e "s#https://api.github.com/repos/\${REPO}/releases/latest#${BASE}/latest.json#" \
    "$INSTALL_SH" > "${STUB_ROOT}/install.test.sh"
sed -i.bak "s#\${GITHUB_BASE}/releases/download/v\${VERSION}/#\${GITHUB_BASE}/#g" \
    "${STUB_ROOT}/install.test.sh"

run() {
    HOME="$TMP_HOME" \
    LEXR_OS="${LEXR_OS:-linux}" \
    LEXR_ARCH="${LEXR_ARCH:-amd64}" \
    GH_STUB_MODE="${GH_STUB_MODE:-unavailable}" \
    GH_STUB_LOG="$GH_STUB_LOG" \
    INSTALL_STUB_MODE="${INSTALL_STUB_MODE:-}" \
    PATH="${STUB_BIN}:$PATH" \
        sh "${STUB_ROOT}/install.test.sh" "$@"
}

fresh_home() {
    rm -rf "$TMP_HOME"
    mkdir -p "$TMP_HOME"
    : > "$GH_STUB_LOG"
    rm -f "${STUB_ROOT}/fail-latest-count"
}

write_existing_binary() {
    output="$1"
    mkdir -p "${TMP_HOME}/.local/bin"
    cat > "${TMP_HOME}/.local/bin/lexr" <<EOF
#!/bin/sh
touch "${TMP_HOME}/destination-was-executed"
echo "${output}"
EOF
    chmod +x "${TMP_HOME}/.local/bin/lexr"
}

assert_no_install_temps() {
    if find "${TMP_HOME}/.local/bin" -name '.lexr.install.*' -print 2>/dev/null | grep -q .; then
        bad "temporary install file was left behind"
    else
        ok "temporary install files cleaned up"
    fi
}

log "help output"
fresh_home
out="$(run --help)"
case "$out" in
    *"Install Lexr"*"--force"*) ok "--help prints usage and --force" ;;
    *) bad "--help output incomplete" ;;
esac

log "latest install"
fresh_home
run --no-modify-path >/dev/null 2>&1
[ -x "${TMP_HOME}/.local/bin/lexr" ] && ok "installed executable" || bad "executable missing"
[ "$("${TMP_HOME}/.local/bin/lexr")" = "fake-lexr 9.9.9" ] \
    && ok "installed binary runs" || bad "installed binary did not run"
[ "$(cat "${TMP_HOME}/.local/bin/lexr.version")" = "9.9.9" ] \
    && ok "version sidecar recorded" || bad "version sidecar missing"
assert_no_install_temps

log "checksum-based latest skip"
mv "${STUB_ROOT}/lexr-v9.9.9-linux-amd64" "${STUB_ROOT}/lexr-v9.9.9-linux-amd64.saved"
out="$(run --no-modify-path 2>&1)"
mv "${STUB_ROOT}/lexr-v9.9.9-linux-amd64.saved" "${STUB_ROOT}/lexr-v9.9.9-linux-amd64"
case "$out" in
    *"already the latest verified release"*) ok "matching installed bytes skipped" ;;
    *) bad "matching installed bytes were not skipped" ;;
esac

log "same-version tampering"
write_existing_binary "lexr version 9.9.9"
out="$(run --no-modify-path 2>&1)"
case "$out" in
    *"Reinstalling Lexr version 9.9.9"*) ok "tampered same-version binary reinstalled" ;;
    *) bad "tampered same-version binary did not reinstall" ;;
esac
[ ! -e "${TMP_HOME}/destination-was-executed" ] \
    && ok "existing destination was never executed" || bad "existing destination was executed"

log "forced reinstall"
out="$(run --force --no-modify-path 2>&1)"
case "$out" in
    *"Reinstalling Lexr version 9.9.9"*) ok "--force reports reinstall" ;;
    *) bad "--force did not report reinstall" ;;
esac

log "explicit version reinstall"
out="$(run --version 9.9.9 --no-modify-path 2>&1)"
case "$out" in
    *"Reinstalling Lexr version 9.9.9"*) ok "explicit version reports reinstall" ;;
    *) bad "explicit version did not report reinstall" ;;
esac

log "sidecar update progress"
fresh_home
write_existing_binary "old binary"
echo "9.8.0" > "${TMP_HOME}/.local/bin/lexr.version"
out="$(run --no-modify-path 2>&1)"
case "$out" in
    *"Updating Lexr from 9.8.0 to 9.9.9"*) ok "sidecar supplies from/to progress" ;;
    *) bad "from/to progress missing" ;;
esac
[ ! -e "${TMP_HOME}/destination-was-executed" ] \
    && ok "older destination was never executed" || bad "older destination was executed"

log "legacy install without sidecar"
fresh_home
write_existing_binary "legacy binary"
out="$(run --no-modify-path 2>&1)"
case "$out" in
    *"Replacing an existing Lexr installation with version 9.9.9"*)
        ok "legacy install uses honest replacement message" ;;
    *) bad "legacy replacement message missing" ;;
esac
[ ! -e "${TMP_HOME}/destination-was-executed" ] \
    && ok "legacy destination was never executed" || bad "legacy destination was executed"

log "local binary source preservation"
fresh_home
cp "${STUB_ROOT}/lexr-v9.9.9-linux-amd64" "${STUB_ROOT}/local-lexr"
chmod 0644 "${STUB_ROOT}/local-lexr"
run --binary "${STUB_ROOT}/local-lexr" --no-modify-path >/dev/null 2>&1
[ "$(mode_of "${STUB_ROOT}/local-lexr")" = "644" ] \
    && ok "--binary source mode unchanged" || bad "--binary mutated source mode"
[ -x "${TMP_HOME}/.local/bin/lexr" ] \
    && ok "--binary destination made executable" || bad "--binary destination not executable"
[ ! -e "${TMP_HOME}/.local/bin/lexr.version" ] \
    && ok "--binary leaves no misleading sidecar" || bad "--binary retained version sidecar"

log "option conflicts and version validation"
for arguments in \
    "--binary ${STUB_ROOT}/local-lexr --version 9.9.9" \
    "--binary ${STUB_ROOT}/local-lexr --force" \
    "--version 9.9.9 --force" \
    "--version v9.9.9" \
    "--version 9.9.9/../../escape"; do
    # Intentional field splitting builds each invalid invocation.
    # shellcheck disable=SC2086
    if run $arguments --no-modify-path >/dev/null 2>&1; then
        bad "invalid arguments accepted: ${arguments}"
    else
        ok "invalid arguments rejected: ${arguments}"
    fi
done

log "checksum mismatch"
fresh_home
# The sed expression intentionally matches a literal shell variable reference.
# shellcheck disable=SC2016
sed 's#MANIFEST_NAME="lexr-v${VERSION}.sha256sums"#MANIFEST_NAME="lexr-v9.9.9.bad.sha256sums"#' \
    "${STUB_ROOT}/install.test.sh" > "${STUB_ROOT}/install.bad.sh"
if HOME="$TMP_HOME" LEXR_OS=linux LEXR_ARCH=amd64 \
   GH_STUB_MODE=unavailable GH_STUB_LOG="$GH_STUB_LOG" PATH="${STUB_BIN}:$PATH" \
   sh "${STUB_ROOT}/install.bad.sh" --no-modify-path >/dev/null 2>&1; then
    bad "checksum mismatch was not detected"
else
    ok "checksum mismatch rejected"
fi
[ ! -e "${TMP_HOME}/.local/bin/lexr" ] \
    && ok "nothing installed on mismatch" || bad "binary installed despite mismatch"

log "ambiguous checksum entry"
fresh_home
# The sed expression intentionally matches a literal shell variable reference.
# shellcheck disable=SC2016
sed 's#MANIFEST_NAME="lexr-v${VERSION}.sha256sums"#MANIFEST_NAME="lexr-v9.9.9.duplicate.sha256sums"#' \
    "${STUB_ROOT}/install.test.sh" > "${STUB_ROOT}/install.duplicate.sh"
if HOME="$TMP_HOME" LEXR_OS=linux LEXR_ARCH=amd64 \
   GH_STUB_MODE=unavailable GH_STUB_LOG="$GH_STUB_LOG" PATH="${STUB_BIN}:$PATH" \
   sh "${STUB_ROOT}/install.duplicate.sh" --no-modify-path >/dev/null 2>&1; then
    bad "duplicate checksum entry was accepted"
else
    ok "duplicate checksum entry rejected"
fi

log "atomic failure preserves existing binary"
fresh_home
write_existing_binary "known-good-old-binary"
echo "9.8.0" > "${TMP_HOME}/.local/bin/lexr.version"
if (INSTALL_STUB_MODE=fail run --no-modify-path >/dev/null 2>&1); then
    bad "injected staging failure succeeded"
else
    ok "injected staging failure rejected"
fi
[ "$("${TMP_HOME}/.local/bin/lexr")" = "known-good-old-binary" ] \
    && ok "existing binary survived staging failure" || bad "existing binary was damaged"
assert_no_install_temps

log "non-regular destination"
fresh_home
mkdir -p "${TMP_HOME}/.local/bin/lexr"
if run --no-modify-path >/dev/null 2>&1; then
    bad "directory destination was replaced"
else
    ok "directory destination rejected"
fi
[ -d "${TMP_HOME}/.local/bin/lexr" ] \
    && ok "directory destination preserved" || bad "directory destination removed"

log "platform and destination validation"
fresh_home
for environment in "LEXR_OS=windows" "LEXR_ARCH=ppc64" "LEXR_INSTALL_DIR=relative/path"; do
    case "$environment" in
        LEXR_OS=*) value="${environment#*=}"; if (LEXR_OS="$value" run --no-modify-path >/dev/null 2>&1); then result=0; else result=$?; fi ;;
        LEXR_ARCH=*) value="${environment#*=}"; if (LEXR_ARCH="$value" run --no-modify-path >/dev/null 2>&1); then result=0; else result=$?; fi ;;
        LEXR_INSTALL_DIR=*) value="${environment#*=}"; if (LEXR_INSTALL_DIR="$value" run --no-modify-path >/dev/null 2>&1); then result=0; else result=$?; fi ;;
    esac
    [ "$result" -ne 0 ] && ok "invalid environment rejected: ${environment}" \
        || bad "invalid environment accepted: ${environment}"
done

log "attestation verification"
fresh_home
if out="$(GH_STUB_MODE=success run --no-modify-path 2>&1)"; then
    :
else
    bad "attestation success fixture failed: ${out}"
    out=""
fi
case "$out" in
    *"Verified GitHub build provenance"*) ok "attestation bundle verified" ;;
    *) bad "attestation verification message missing" ;;
esac
grep -q -- '--source-ref refs/tags/v9.9.9' "$GH_STUB_LOG" \
    && ok "attestation pins the release tag" || bad "attestation did not pin release tag"
grep -q -- '--repo lexr.sh' "$GH_STUB_LOG" \
    && ok "attestation pins the repository" || bad "attestation did not pin repository"
grep -q -- '--signer-workflow lexr.sh/.github/workflows/lexr.yml' "$GH_STUB_LOG" \
    && ok "attestation pins the release workflow" || bad "attestation did not pin workflow"
grep -q -- '--deny-self-hosted-runners' "$GH_STUB_LOG" \
    && ok "attestation rejects self-hosted signers" || bad "attestation signer guard missing"

log "attestation failure"
fresh_home
if (GH_STUB_MODE=failure run --no-modify-path >/dev/null 2>&1); then
    bad "invalid attestation accepted"
else
    ok "invalid attestation rejected"
fi
[ ! -e "${TMP_HOME}/.local/bin/lexr" ] \
    && ok "attestation failure installed nothing" || bad "attestation failure installed binary"

log "required attestation API fallback"
fresh_home
mv "${STUB_ROOT}/lexr-v9.9.9.sha256sums.intoto.jsonl" \
    "${STUB_ROOT}/lexr-v9.9.9.sha256sums.intoto.jsonl.saved"
if (GH_STUB_MODE=failure run --no-modify-path >/dev/null 2>&1); then
    bad "missing required attestation accepted by capable verifier"
else
    ok "missing required attestation rejected by capable verifier"
fi
mv "${STUB_ROOT}/lexr-v9.9.9.sha256sums.intoto.jsonl.saved" \
    "${STUB_ROOT}/lexr-v9.9.9.sha256sums.intoto.jsonl"

log "historical release compatibility"
fresh_home
(GH_STUB_MODE=failure run --version 0.4.0 --no-modify-path >/dev/null 2>&1) \
    && ok "historical release installs without attestation" \
    || bad "historical release incorrectly required attestation"

log "attestation version boundary"
fresh_home
(GH_STUB_MODE=failure run --version 0.5.0-rc.1 --no-modify-path >/dev/null 2>&1) \
    && ok "boundary prerelease remains checksum-compatible" \
    || bad "boundary prerelease incorrectly required attestation"
fresh_home
if (GH_STUB_MODE=failure run --version 0.5.0 --no-modify-path >/dev/null 2>&1); then
    bad "stable boundary release accepted missing attestation"
else
    ok "stable boundary release requires attestation"
fi
fresh_home
if (GH_STUB_MODE=failure run --version 0.6.0-rc.1 --no-modify-path >/dev/null 2>&1); then
    bad "post-boundary prerelease accepted missing attestation"
else
    ok "post-boundary prerelease requires attestation"
fi

log "attestation warning without verifier"
fresh_home
out="$(GH_STUB_MODE=unavailable run --no-modify-path 2>&1)"
case "$out" in
    *"build provenance was not verified"*) ok "missing verifier warning printed" ;;
    *) bad "missing verifier warning absent" ;;
esac

log "transient network retry"
fresh_home
echo 2 > "${STUB_ROOT}/fail-latest-count"
run --no-modify-path >/dev/null 2>&1 \
    && ok "transient latest-release failures retried" \
    || bad "transient latest-release failures were not retried"
[ "$(cat "${STUB_ROOT}/fail-latest-count")" = "0" ] \
    && ok "retry budget reached the successful response" || bad "retry count unexpected"

log "PATH modification"
fresh_home
(SHELL=/bin/sh run >/dev/null 2>&1)
grep -q "lexr install" "${TMP_HOME}/.profile" \
    && ok "PATH marker written to .profile" || bad "PATH marker missing"
(SHELL=/bin/sh run >/dev/null 2>&1)
count="$(grep -c "lexr install" "${TMP_HOME}/.profile" || true)"
[ "$count" -eq 1 ] && ok "PATH marker not duplicated" \
    || bad "PATH marker written ${count} times"

log "Bash startup file selection"
fresh_home
: > "${TMP_HOME}/.bash_profile"
(SHELL=/bin/bash run >/dev/null 2>&1)
grep -q "lexr install" "${TMP_HOME}/.bash_profile" \
    && ok "Bash uses existing .bash_profile" || bad "Bash marker missing"
[ ! -e "${TMP_HOME}/.profile" ] \
    && ok "Bash did not write ignored .profile" || bad "Bash wrote ignored .profile"

log "quoted custom PATH entry"
fresh_home
custom_dir="${TMP_HOME}/custom dir/a'b"
(LEXR_INSTALL_DIR="$custom_dir" SHELL=/bin/sh run >/dev/null 2>&1)
sh -n "${TMP_HOME}/.profile" \
    && ok "custom PATH entry is valid shell" || bad "custom PATH entry is invalid shell"
actual_path="$(HOME="$TMP_HOME" PATH=/usr/bin:/bin sh -c '. "$HOME/.profile"; printf "%s" "$PATH"')"
case "$actual_path" in
    "${custom_dir}":*) ok "custom PATH entry retains exact path" ;;
    *) bad "custom PATH entry changed" ;;
esac

log "Fish guidance"
fresh_home
out="$(SHELL=/usr/bin/fish run 2>&1)"
case "$out" in
    *"Fish detected; run fish_add_path"*) ok "Fish receives native PATH guidance" ;;
    *) bad "Fish guidance missing" ;;
esac
[ ! -e "${TMP_HOME}/.profile" ] \
    && ok "Fish did not receive POSIX startup edits" || bad "Fish wrote POSIX startup file"

echo
echo "passed: ${PASS}, failed: ${FAIL}"
[ "$FAIL" -eq 0 ] || exit 1
