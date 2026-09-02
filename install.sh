#!/bin/sh
# Lexr installer.
#
# Downloads a Lexr CLI executable from GitHub releases, verifies its SHA-256
# digest against the release manifest, and installs it on the user's PATH.
#
# Typical usage (POSIX hosts: Linux and macOS):
#
#   curl -fsSL https://raw.githubusercontent.com/ooaklee/lexr.sh/refs/heads/main/install.sh | sh
#   curl -fsSL .../install.sh | sh -s -- --version 0.2.0
#   curl -fsSL .../install.sh | sh -s -- --help
#
# Supported options:
#   --version <version>   Install a specific release version (default: latest).
#   --binary <path>       Install a local, already-verified executable instead
#                         of downloading one.
#   --no-modify-path      Do not edit shell startup files; only print guidance.
#   --help                Print usage and exit.
#
# The script is POSIX sh, idempotent, and safe to re-run: an existing
# installation is replaced, and PATH edits are added at most once.
set -eu

REPO="ooaklee/lexr.sh"
GITHUB_BASE="https://github.com/${REPO}"
COMMAND_NAME="lexr"

VERSION=""
LOCAL_BINARY=""
MODIFY_PATH=1

usage() {
    cat <<'EOF'
Install Lexr (https://github.com/ooaklee/lexr.sh)

Usage:
  curl -fsSL https://raw.githubusercontent.com/ooaklee/lexr.sh/refs/heads/main/install.sh | sh -s -- [options]

Options:
  --version <version>  Install a specific release version (default: latest stable)
  --binary <path>      Install a local executable instead of downloading one
  --no-modify-path     Do not modify shell startup files to extend PATH
  -h, --help           Show this help text and exit

Environment:
  LEXR_INSTALL_DIR     Override the installation directory
  LEXR_OS              Override detected OS (linux, darwin)
  LEXR_ARCH            Override detected architecture (amd64, arm64)
EOF
}

die() {
    echo "lexr install: error: $*" >&2
    exit 1
}

log() {
    echo "==> $*"
}

# ---------------------------------------------------------------------------
# Argument parsing
# ---------------------------------------------------------------------------
while [ $# -gt 0 ]; do
    case "$1" in
        --version)
            [ $# -ge 2 ] || die "--version requires a value"
            VERSION="$2"
            shift 2
            ;;
        --binary)
            [ $# -ge 2 ] || die "--binary requires a path"
            LOCAL_BINARY="$2"
            shift 2
            ;;
        --no-modify-path)
            MODIFY_PATH=0
            shift
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            die "unknown option: $1 (try --help)"
            ;;
    esac
done

# ---------------------------------------------------------------------------
# Platform detection. Windows hosts should use the documented PowerShell flow.
# ---------------------------------------------------------------------------
os() {
    if [ -n "${LEXR_OS:-}" ]; then
        printf '%s' "$LEXR_OS"
        return
    fi
    case "$(uname -s)" in
        Linux*)  printf linux ;;
        Darwin*) printf darwin ;;
        MINGW*|MSYS*|CYGWIN*)
            die "Windows detected. Download lexr-v<version>-windows-<arch>.exe from ${GITHUB_BASE}/releases and follow docs/getting-started/install.md"
            ;;
        *) die "unsupported operating system: $(uname -s)" ;;
    esac
}

arch() {
    if [ -n "${LEXR_ARCH:-}" ]; then
        printf '%s' "$LEXR_ARCH"
        return
    fi
    case "$(uname -m)" in
        x86_64|amd64)       printf amd64 ;;
        aarch64|arm64)      printf arm64 ;;
        *) die "unsupported architecture: $(uname -m) (supported: amd64, arm64)" ;;
    esac
}

TARGET_OS="$(os)"
TARGET_ARCH="$(arch)"
log "Detected platform: ${TARGET_OS}/${TARGET_ARCH}"

# ---------------------------------------------------------------------------
# Tooling checks
# ---------------------------------------------------------------------------
need() {
    command -v "$1" >/dev/null 2>&1 || die "required command not found: $1"
}

fetch() {
    # fetch <url> -> stdout
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL "$1"
    elif command -v wget >/dev/null 2>&1; then
        wget -qO- "$1"
    else
        die "neither curl nor wget is available; install one and retry"
    fi
}

download_to() {
    # download_to <url> <destination>
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL "$1" -o "$2"
    elif command -v wget >/dev/null 2>&1; then
        wget -qO "$2" "$1"
    else
        die "neither curl nor wget is available; install one and retry"
    fi
}

sha256_of() {
    # sha256_of <file> -> hex digest on stdout
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | cut -d' ' -f1
    elif command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$1" | cut -d' ' -f1
    else
        die "neither sha256sum nor shasum is available to verify the download"
    fi
}

# ---------------------------------------------------------------------------
# Resolve the version and asset names
# ---------------------------------------------------------------------------
if [ -n "$LOCAL_BINARY" ]; then
    [ -f "$LOCAL_BINARY" ] || die "local binary not found: $LOCAL_BINARY"
    [ -x "$LOCAL_BINARY" ] || chmod +x "$LOCAL_BINARY" 2>/dev/null || \
        die "cannot make local binary executable: $LOCAL_BINARY"
else
    need uname
    if [ -z "$VERSION" ]; then
        log "Resolving the latest release"
        latest_json="$(fetch "https://api.github.com/repos/${REPO}/releases/latest")" \
            || die "could not query GitHub for the latest release"
        VERSION="$(printf '%s' "$latest_json" | sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)"
        [ -n "$VERSION" ] || die "could not parse the latest release tag from GitHub"
        case "$VERSION" in
            v*) VERSION="${VERSION#v}" ;;
        esac
    fi
    log "Installing Lexr version ${VERSION}"
fi

ASSET_NAME="lexr-v${VERSION}-${TARGET_OS}-${TARGET_ARCH}"
MANIFEST_NAME="lexr-v${VERSION}.sha256sums"

# ---------------------------------------------------------------------------
# Download and verify (skipped for local binaries, which the user verified)
# ---------------------------------------------------------------------------
TMPDIR_INSTALL="$(mktemp -d 2>/dev/null || die "could not create a temporary directory")"
trap 'rm -rf "$TMPDIR_INSTALL"' EXIT INT TERM

if [ -z "$LOCAL_BINARY" ]; then
    BINARY_PATH="${TMPDIR_INSTALL}/${ASSET_NAME}"
    MANIFEST_PATH="${TMPDIR_INSTALL}/${MANIFEST_NAME}"

    log "Downloading ${ASSET_NAME}"
    download_to "${GITHUB_BASE}/releases/download/v${VERSION}/${ASSET_NAME}" "$BINARY_PATH" \
        || die "download failed for ${ASSET_NAME}; check that version ${VERSION} exists for ${TARGET_OS}/${TARGET_ARCH}"
    download_to "${GITHUB_BASE}/releases/download/v${VERSION}/${MANIFEST_NAME}" "$MANIFEST_PATH" \
        || die "download failed for the checksum manifest ${MANIFEST_NAME}"

    log "Verifying SHA-256 checksum"
    expected="$(awk -v f="$ASSET_NAME" '$2 == f { print $1; exit }' "$MANIFEST_PATH")"
    [ -n "$expected" ] || die "no checksum entry for ${ASSET_NAME} in ${MANIFEST_NAME}"
    actual="$(sha256_of "$BINARY_PATH")"
    if [ "$expected" != "$actual" ]; then
        die "checksum mismatch for ${ASSET_NAME}
  expected: ${expected}
  actual:   ${actual}"
    fi
    chmod +x "$BINARY_PATH"
else
    BINARY_PATH="$LOCAL_BINARY"
fi

# ---------------------------------------------------------------------------
# Choose the install destination: an explicit override, a writable system
# directory, or the user-local bin directory.
# ---------------------------------------------------------------------------
if [ -n "${LEXR_INSTALL_DIR:-}" ]; then
    INSTALL_DIR="$LEXR_INSTALL_DIR"
elif [ -w /usr/local/bin ] 2>/dev/null; then
    INSTALL_DIR="/usr/local/bin"
else
    INSTALL_DIR="${HOME}/.local/bin"
fi
mkdir -p "$INSTALL_DIR" || die "could not create install directory: ${INSTALL_DIR}"

DEST="${INSTALL_DIR}/${COMMAND_NAME}"
if [ -e "$DEST" ]; then
    log "Replacing an existing installation at ${DEST}"
fi
install_file() {
    if command -v install >/dev/null 2>&1; then
        install -m 0755 "$1" "$2"
    else
        cp "$1" "$2" && chmod 0755 "$2"
    fi
}
install_file "$BINARY_PATH" "$DEST" || die "could not install to ${DEST}"
log "Installed ${DEST}"

# ---------------------------------------------------------------------------
# PATH handling
# ---------------------------------------------------------------------------
case ":${PATH}:" in
    *":${INSTALL_DIR}:"*) log "${INSTALL_DIR} is already on your PATH" ;;
    *)
        if [ "$MODIFY_PATH" -eq 1 ]; then
            MARKER="# lexr install"
            shell_rc=""
            case "${SHELL:-}" in
                */zsh)
                    [ -f "${ZDOTDIR:-$HOME}/.zshrc" ] && shell_rc="${ZDOTDIR:-$HOME}/.zshrc"
                    [ -n "$shell_rc" ] || shell_rc="${ZDOTDIR:-$HOME}/.zshrc"
                    ;;
                */fish) shell_rc="" ;;
                *) shell_rc="$HOME/.profile" ;;
            esac
            if [ -n "$shell_rc" ]; then
                line="export PATH=\"\$HOME/.local/bin:\$PATH\" ${MARKER}"
                case "$INSTALL_DIR" in
                    "$HOME"/.local/bin) ;;
                    *) line="export PATH=\"${INSTALL_DIR}:\$PATH\" ${MARKER}" ;;
                esac
                if [ -f "$shell_rc" ] && grep -q "$MARKER" "$shell_rc" 2>/dev/null; then
                    log "PATH entry already present in ${shell_rc}"
                else
                    { echo ""; echo "$line"; } >> "$shell_rc"
                    log "Added ${INSTALL_DIR} to PATH in ${shell_rc}"
                fi
            fi
        fi
        log "NOTE: start a new shell, or run: export PATH=\"${INSTALL_DIR}:\$PATH\""
        ;;
esac

log "Done. Verify with: ${DEST} version"
