#!/usr/bin/env bash
# The live setup menu changes only choices explicitly selected by the operator.
set -euo pipefail

show_status() {
    printf '\nKernel: '
    uname -r
    printf '\nNetwork interfaces:\n'
    nmcli device status || true
    printf '\nStorage (inspection only):\n'
    lsblk -o NAME,SIZE,TYPE,FSTYPE,LABEL,MOUNTPOINTS
    printf '\nNative Surface kernel package:\n'
    pacman -Q lexr-kernel-sp11
    printf '\nLive root:\n'
    findmnt -no SOURCE,FSTYPE,OPTIONS /
}

case ${1:-} in
    --status) show_status; exit 0 ;;
    --help|-h)
        printf 'Usage: lexr-arch-setup [--status|--help]\n'
        printf 'Open terminal setup, or inspect kernel/network/storage without changes.\n'
        exit 0 ;;
    '') ;;
    *) printf 'Unknown argument: %s\n' "$1" >&2; exit 2 ;;
esac
[ -t 0 ] || { printf 'An interactive terminal is required; use --status for inspection.\n' >&2; exit 2; }
while true; do
    cat <<'MENU'

Arch Linux ARM — Surface Pro 11 live setup

  1. Check kernel, network and storage
  2. Connect to Wi-Fi or configure networking
  3. Initialise package signing keys for this session
  4. Read the getting-started and installation status guide
  5. Open the Lexr included on this USB
  0. Return to the shell and customise Arch

This session is temporary. The menu does not partition or install to a disk.
MENU
    read -r -p 'Select an option: ' choice || exit 0
    case "$choice" in
        1) show_status ;;
        2) sudo nmtui ;;
        3) sudo pacman-key --init && sudo pacman-key --populate archlinuxarm ;;
        4) less /usr/share/lexr/LEXR_GETTING_STARTED.txt ;;
        5)
            binary=/usr/share/lexr/arch-media/sp11/companion/bin/linux-arm64/lexr
            if [ ! -f "$binary" ]; then
                printf 'The companion was omitted from this image. See the guide for installation.\n'
            else
                mkdir -p "$HOME/.local/bin"
                install -m 0755 "$binary" "$HOME/.local/bin/lexr"
                "$HOME/.local/bin/lexr" --help
            fi ;;
        0) exit 0 ;;
        *) printf 'Choose one of the listed options.\n' ;;
    esac
done
