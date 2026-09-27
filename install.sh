#!/bin/sh
# Lexr installer.
#
# Downloads a Lexr CLI executable from GitHub releases, verifies its SHA-256
# digest and available build provenance, and installs it atomically on the
# user's PATH.
#
# Typical usage (POSIX hosts: Linux and macOS):
#
#   curl -fsSL https://raw.githubusercontent.com/ooaklee/lexr.sh/refs/heads/main/install.sh | sh
#   curl -fsSL .../install.sh | sh -s -- --version 0.2.0
#   curl -fsSL .../install.sh | sh -s -- --help
#
# Supported options:
#   --version <version>   Install a specific release version (default: latest).
#   --binary <path>       Install a local, already-verified binary instead
#                         of downloading one.
#   --force               Reinstall the latest release even when it is current.
#   --no-modify-path      Do not edit shell startup files; only print guidance.
#   --help                Print usage and exit.
#
# The script is POSIX sh, idempotent, and safe to re-run: the latest release is
# not reinstalled unless forced, and PATH edits are added at most once.
set -eu

REPO="ooaklee/lexr.sh"
GITHUB_BASE="https://github.com/${REPO}"
COMMAND_NAME="lexr"
ATTESTATION_REQUIRED_VERSION="0.5.0"
DOWNLOAD_RETRIES=3
DOWNLOAD_CONNECT_TIMEOUT=10
DOWNLOAD_MAX_TIME=120

VERSION=""
VERSION_WAS_EXPLICIT=0
LOCAL_BINARY=""
FORCE=0
MODIFY_PATH=1
TMPDIR_INSTALL=""
INSTALL_TMP=""
VERSION_TMP=""

usage() {
    cat <<'EOF'
Install Lexr (https://github.com/ooaklee/lexr.sh)

Usage:
  curl -fsSL https://raw.githubusercontent.com/ooaklee/lexr.sh/refs/heads/main/install.sh | sh -s -- [options]

Options:
  --version <version>  Install a specific release version (default: latest stable)
  --binary <path>      Install a local binary instead of downloading one
  --force              Reinstall the latest release even when already current
  --no-modify-path     Do not modify shell startup files to extend PATH
  -h, --help           Show this help text and exit

Environment:
  LEXR_INSTALL_DIR     Override the installation directory (absolute path)
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

warn() {
    echo "lexr install: warning: $*" >&2
}

cleanup() {
    [ -z "$VERSION_TMP" ] || rm -f "$VERSION_TMP"
    [ -z "$INSTALL_TMP" ] || rm -f "$INSTALL_TMP"
    [ -z "$TMPDIR_INSTALL" ] || rm -rf "$TMPDIR_INSTALL"
}

trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

is_semver() {
    candidate="$1"
    case "$candidate" in
        ""|*[!0-9A-Za-z.+-]*) return 1 ;;
    esac
    printf '%s\n' "$candidate" | LC_ALL=C grep -Eq \
        '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-((0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(\.(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*))?(\+([0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*))?$'
}

shell_quote() {
    printf "'"
    printf '%s' "$1" | sed "s/'/'\\\\''/g"
    printf "'"
}

# ---------------------------------------------------------------------------
# Argument parsing
# ---------------------------------------------------------------------------
while [ $# -gt 0 ]; do
    case "$1" in
        --version)
            [ $# -ge 2 ] || die "--version requires a value"
            VERSION="$2"
            VERSION_WAS_EXPLICIT=1
            shift 2
            ;;
        --binary)
            [ $# -ge 2 ] || die "--binary requires a path"
            LOCAL_BINARY="$2"
            shift 2
            ;;
        --force)
            FORCE=1
            shift
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

if [ -n "$LOCAL_BINARY" ] && [ "$VERSION_WAS_EXPLICIT" -eq 1 ]; then
    die "--binary and --version cannot be used together"
fi
if [ -n "$LOCAL_BINARY" ] && [ "$FORCE" -eq 1 ]; then
    die "--binary and --force cannot be used together"
fi
if [ "$VERSION_WAS_EXPLICIT" -eq 1 ] && [ "$FORCE" -eq 1 ]; then
    die "--version already reinstalls the selected release; do not combine it with --force"
fi
if [ "$VERSION_WAS_EXPLICIT" -eq 1 ] && ! is_semver "$VERSION"; then
    die "--version must be SemVer without a leading v (for example, 0.5.0 or 0.5.0-rc.1)"
fi

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
case "$TARGET_OS" in
    linux|darwin) ;;
    *) die "unsupported operating system override: ${TARGET_OS} (supported: linux, darwin)" ;;
esac
case "$TARGET_ARCH" in
    amd64|arm64) ;;
    *) die "unsupported architecture override: ${TARGET_ARCH} (supported: amd64, arm64)" ;;
esac
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
        curl --retry "$DOWNLOAD_RETRIES" --retry-delay 1 \
            --connect-timeout "$DOWNLOAD_CONNECT_TIMEOUT" \
            --max-time "$DOWNLOAD_MAX_TIME" -fsSL "$1"
    elif command -v wget >/dev/null 2>&1; then
        wget -q -t "$DOWNLOAD_RETRIES" -T "$DOWNLOAD_CONNECT_TIMEOUT" -O- "$1"
    else
        die "neither curl nor wget is available; install one and retry"
    fi
}

download_to() {
    # download_to <url> <destination>
    if command -v curl >/dev/null 2>&1; then
        curl --retry "$DOWNLOAD_RETRIES" --retry-delay 1 \
            --connect-timeout "$DOWNLOAD_CONNECT_TIMEOUT" \
            --max-time "$DOWNLOAD_MAX_TIME" -fsSL "$1" -o "$2"
    elif command -v wget >/dev/null 2>&1; then
        wget -q -t "$DOWNLOAD_RETRIES" -T "$DOWNLOAD_CONNECT_TIMEOUT" -O "$2" "$1"
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

attestation_required() {
    candidate="${1%%+*}"
    candidate_core="${candidate%%-*}"
    candidate_prerelease=0
    [ "$candidate" = "$candidate_core" ] || candidate_prerelease=1
    candidate_major="${candidate_core%%.*}"
    candidate_rest="${candidate_core#*.}"
    candidate_minor="${candidate_rest%%.*}"
    candidate_patch="${candidate_rest#*.}"

    boundary_major="${ATTESTATION_REQUIRED_VERSION%%.*}"
    boundary_rest="${ATTESTATION_REQUIRED_VERSION#*.}"
    boundary_minor="${boundary_rest%%.*}"
    boundary_patch="${boundary_rest#*.}"

    if [ "$candidate_major" -gt "$boundary_major" ]; then return 0; fi
    if [ "$candidate_major" -lt "$boundary_major" ]; then return 1; fi
    if [ "$candidate_minor" -gt "$boundary_minor" ]; then return 0; fi
    if [ "$candidate_minor" -lt "$boundary_minor" ]; then return 1; fi
    if [ "$candidate_patch" -gt "$boundary_patch" ]; then return 0; fi
    if [ "$candidate_patch" -lt "$boundary_patch" ]; then return 1; fi
    [ "$candidate_prerelease" -eq 0 ]
}

verify_manifest_attestation() {
    manifest_path="$1"
    attestation_path="$2"
    attestation_url="$3"
    has_bundle=0

    if download_to "$attestation_url" "$attestation_path" 2>/dev/null; then
        has_bundle=1
    else
        rm -f "$attestation_path"
    fi

    has_verifier=0
    if command -v gh >/dev/null 2>&1 && \
       gh attestation verify --help >/dev/null 2>&1; then
        has_verifier=1
    fi

    if [ "$has_verifier" -eq 1 ] && \
       { [ "$has_bundle" -eq 1 ] || attestation_required "$VERSION"; }; then
        log "Verifying GitHub build provenance"
        if [ "$has_bundle" -eq 1 ]; then
            GH_PAGER=cat GH_PROMPT_DISABLED=1 gh attestation verify "$manifest_path" \
                --repo "$REPO" \
                --bundle "$attestation_path" \
                --signer-workflow "${REPO}/.github/workflows/lexr.yml" \
                --source-ref "refs/tags/v${VERSION}" \
                --deny-self-hosted-runners >/dev/null 2>&1 \
                || die "build provenance verification failed for ${MANIFEST_NAME}"
        else
            GH_PAGER=cat GH_PROMPT_DISABLED=1 gh attestation verify "$manifest_path" \
                --repo "$REPO" \
                --signer-workflow "${REPO}/.github/workflows/lexr.yml" \
                --source-ref "refs/tags/v${VERSION}" \
                --deny-self-hosted-runners >/dev/null 2>&1 \
                || die "required build provenance is missing or invalid for ${MANIFEST_NAME}"
        fi
        log "Verified GitHub build provenance"
    elif [ "$has_bundle" -eq 1 ] || attestation_required "$VERSION"; then
        warn "build provenance was not verified; install GitHub CLI with attestation support to verify it"
    else
        log "Release ${VERSION} predates required build provenance; using checksum verification"
    fi
}

install_file() {
    if command -v install >/dev/null 2>&1; then
        install -m 0755 "$1" "$2"
    else
        cp "$1" "$2" && chmod 0755 "$2"
    fi
}

write_version_file() {
    version_file="$1"
    version_value="$2"
    VERSION_TMP="$(mktemp "${INSTALL_DIR}/.lexr.version.XXXXXX" 2>/dev/null)" || return 1
    printf '%s\n' "$version_value" > "$VERSION_TMP" || return 1
    chmod 0644 "$VERSION_TMP" || return 1
    mv -f "$VERSION_TMP" "$version_file" || return 1
    VERSION_TMP=""
}

# ---------------------------------------------------------------------------
# Choose the install destination before downloading so an existing release can
# be compared with the requested one. The directory is not created until an
# install is actually required.
# ---------------------------------------------------------------------------
if [ -n "${LEXR_INSTALL_DIR:-}" ]; then
    case "$LEXR_INSTALL_DIR" in
        /*) ;;
        *) die "LEXR_INSTALL_DIR must be an absolute path" ;;
    esac
    if printf '%s' "$LEXR_INSTALL_DIR" | LC_ALL=C grep -q '[[:cntrl:]]'; then
        die "LEXR_INSTALL_DIR must not contain control characters"
    fi
    INSTALL_DIR="$LEXR_INSTALL_DIR"
elif [ -w /usr/local/bin ] 2>/dev/null; then
    INSTALL_DIR="/usr/local/bin"
else
    INSTALL_DIR="${HOME}/.local/bin"
fi
DEST="${INSTALL_DIR}/${COMMAND_NAME}"
VERSION_FILE="${DEST}.version"

# ---------------------------------------------------------------------------
# Resolve and validate the version, then download the small checksum manifest.
# The manifest digest, not executable output, decides whether the destination
# is already the published release.
# ---------------------------------------------------------------------------
SKIP_INSTALL=0
INSTALLED_VERSION=""
if [ -n "$LOCAL_BINARY" ]; then
    [ -f "$LOCAL_BINARY" ] || die "local binary not found: $LOCAL_BINARY"
    log "Installing a local Lexr binary"
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
    is_semver "$VERSION" || die "release tag is not valid SemVer: ${VERSION}"

    ASSET_NAME="lexr-v${VERSION}-${TARGET_OS}-${TARGET_ARCH}"
    MANIFEST_NAME="lexr-v${VERSION}.sha256sums"
    ATTESTATION_NAME="${MANIFEST_NAME}.intoto.jsonl"
    TMPDIR_INSTALL="$(mktemp -d 2>/dev/null || die "could not create a temporary directory")"
    BINARY_PATH="${TMPDIR_INSTALL}/${ASSET_NAME}"
    MANIFEST_PATH="${TMPDIR_INSTALL}/${MANIFEST_NAME}"
    ATTESTATION_PATH="${TMPDIR_INSTALL}/${ATTESTATION_NAME}"

    log "Downloading ${MANIFEST_NAME}"
    download_to "${GITHUB_BASE}/releases/download/v${VERSION}/${MANIFEST_NAME}" "$MANIFEST_PATH" \
        || die "download failed for the checksum manifest ${MANIFEST_NAME}"
    verify_manifest_attestation \
        "$MANIFEST_PATH" \
        "$ATTESTATION_PATH" \
        "${GITHUB_BASE}/releases/download/v${VERSION}/${ATTESTATION_NAME}"

    manifest_matches="$(awk -v f="$ASSET_NAME" '$2 == f && NF == 2 { count++; digest = $1 } END { if (count == 1) print digest }' "$MANIFEST_PATH")"
    case "$manifest_matches" in
        ""|*[!0-9A-Fa-f]*) die "checksum manifest has no unique SHA-256 entry for ${ASSET_NAME}" ;;
    esac
    [ "${#manifest_matches}" -eq 64 ] \
        || die "checksum manifest has a malformed SHA-256 entry for ${ASSET_NAME}"
    EXPECTED_SHA256="$(printf '%s' "$manifest_matches" | tr 'A-F' 'a-f')"

    if [ -f "$VERSION_FILE" ] && [ ! -L "$VERSION_FILE" ]; then
        sidecar_version=""
        IFS= read -r sidecar_version < "$VERSION_FILE" || true
        if is_semver "$sidecar_version"; then
            INSTALLED_VERSION="$sidecar_version"
        fi
    fi

    if [ "$VERSION_WAS_EXPLICIT" -eq 0 ] && [ "$FORCE" -eq 0 ] && [ -f "$DEST" ]; then
        installed_sha256="$(sha256_of "$DEST")"
        if [ "$installed_sha256" = "$EXPECTED_SHA256" ]; then
            log "lexr ${VERSION} is already the latest verified release; pass --force to reinstall"
            SKIP_INSTALL=1
        fi
    fi

    if [ "$SKIP_INSTALL" -eq 0 ]; then
        if [ -n "$INSTALLED_VERSION" ] && [ "$INSTALLED_VERSION" = "$VERSION" ]; then
            log "Reinstalling Lexr version ${VERSION}"
        elif [ -n "$INSTALLED_VERSION" ]; then
            log "Updating Lexr from ${INSTALLED_VERSION} to ${VERSION}"
        elif [ -e "$DEST" ] || [ -L "$DEST" ]; then
            log "Replacing an existing Lexr installation with version ${VERSION}"
        else
            log "Installing Lexr version ${VERSION}"
        fi
    fi
fi

# ---------------------------------------------------------------------------
# Download and verify the binary unless the installed bytes already match.
# Publish through a sibling temporary file so replacement is one same-filesystem
# rename and an interrupted copy cannot truncate the working executable.
# ---------------------------------------------------------------------------
if [ "$SKIP_INSTALL" -eq 0 ]; then
    if [ -z "$LOCAL_BINARY" ]; then
        log "Downloading ${ASSET_NAME}"
        download_to "${GITHUB_BASE}/releases/download/v${VERSION}/${ASSET_NAME}" "$BINARY_PATH" \
            || die "download failed for ${ASSET_NAME}; check that version ${VERSION} exists for ${TARGET_OS}/${TARGET_ARCH}"

        log "Verifying SHA-256 checksum"
        actual="$(sha256_of "$BINARY_PATH")"
        if [ "$EXPECTED_SHA256" != "$actual" ]; then
            die "checksum mismatch for ${ASSET_NAME}
  expected: ${EXPECTED_SHA256}
  actual:   ${actual}"
        fi
    else
        BINARY_PATH="$LOCAL_BINARY"
    fi

    mkdir -p "$INSTALL_DIR" || die "could not create install directory: ${INSTALL_DIR}"
    if [ -e "$DEST" ] && [ ! -f "$DEST" ]; then
        die "install destination is not a regular file: ${DEST}"
    fi
    if [ -e "$DEST" ] || [ -L "$DEST" ]; then
        log "Replacing an existing installation at ${DEST}"
    fi
    INSTALL_TMP="$(mktemp "${INSTALL_DIR}/.lexr.install.XXXXXX" 2>/dev/null \
        || die "could not create a temporary install file in ${INSTALL_DIR}")"
    install_file "$BINARY_PATH" "$INSTALL_TMP" \
        || die "could not stage installation in ${INSTALL_DIR}"
    mv -f "$INSTALL_TMP" "$DEST" || die "could not install to ${DEST}"
    INSTALL_TMP=""

    if [ -n "$LOCAL_BINARY" ]; then
        if ! rm -f "$VERSION_FILE" 2>/dev/null; then
            warn "could not remove stale version metadata at ${VERSION_FILE}"
        fi
    elif ! write_version_file "$VERSION_FILE" "$VERSION"; then
        warn "installed ${DEST}, but could not record version metadata at ${VERSION_FILE}"
    fi
    log "Installed ${DEST}"
elif ! write_version_file "$VERSION_FILE" "$VERSION"; then
    warn "could not record version metadata at ${VERSION_FILE}"
fi

# ---------------------------------------------------------------------------
# PATH handling
# ---------------------------------------------------------------------------
VERIFY_COMMAND="${DEST} version"
case ":${PATH}:" in
    *":${INSTALL_DIR}:"*)
        log "${INSTALL_DIR} is already on your PATH"
        resolved_command="$(command -v "$COMMAND_NAME" 2>/dev/null || true)"
        if [ "$resolved_command" = "$DEST" ]; then
            VERIFY_COMMAND="${COMMAND_NAME} version"
        else
            case "$resolved_command" in
                /*) warn "${resolved_command} resolves before ${DEST} on PATH; using the installed path below" ;;
            esac
        fi
        ;;
    *)
        quoted_install_dir="$(shell_quote "$INSTALL_DIR")"
        path_hint="export PATH=${quoted_install_dir}:\$PATH"
        fish_shell=0
        case "${SHELL:-}" in
            */fish) fish_shell=1 ;;
        esac
        if [ "$MODIFY_PATH" -eq 1 ]; then
            MARKER="# lexr install"
            shell_rc=""
            case "${SHELL:-}" in
                */zsh)
                    shell_rc="${ZDOTDIR:-$HOME}/.zshrc"
                    ;;
                */bash)
                    if [ -f "$HOME/.bash_profile" ]; then
                        shell_rc="$HOME/.bash_profile"
                    elif [ -f "$HOME/.bash_login" ]; then
                        shell_rc="$HOME/.bash_login"
                    else
                        shell_rc="$HOME/.profile"
                    fi
                    ;;
                */fish)
                    shell_rc=""
                    fish_shell=1
                    ;;
                *) shell_rc="$HOME/.profile" ;;
            esac
            if [ -n "$shell_rc" ]; then
                line="export PATH=\"\$HOME/.local/bin:\$PATH\" ${MARKER}"
                case "$INSTALL_DIR" in
                    "$HOME"/.local/bin) ;;
                    *) line="export PATH=${quoted_install_dir}:\$PATH ${MARKER}" ;;
                esac
                if [ -f "$shell_rc" ] && grep -q "$MARKER" "$shell_rc" 2>/dev/null; then
                    log "PATH entry already present in ${shell_rc}"
                else
                    { echo ""; echo "$line"; } >> "$shell_rc"
                    log "Added ${INSTALL_DIR} to PATH in ${shell_rc}"
                fi
            fi
        fi
        if [ "$fish_shell" -eq 1 ]; then
            log "NOTE: Fish detected; run fish_add_path for the installation directory shown above"
        else
            log "NOTE: start a new shell, or run: ${path_hint}"
        fi
        ;;
esac

log "Done. Verify with: ${VERIFY_COMMAND}"
