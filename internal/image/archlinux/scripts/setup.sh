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

# Use a home copy because retained media can be mounted without execution.
prepare_lexr() {
    local binary=/usr/share/lexr/arch-media/sp11/companion/bin/linux-arm64/lexr
    if [ ! -f "$binary" ]; then
        printf 'The companion was omitted. Install the latest Lexr as described in the guide.\n' >&2
        return 1
    fi
    mkdir -p "$HOME/.local/bin"
    install -m 0755 "$binary" "$HOME/.local/bin/lexr"
}

# The operator mounts and selects the existing OS; never scan or mount disks.
register_grub() {
    local arch_root grub_directory esp confirm
    printf '\nAdd the installed Arch loader to another Linux OS\047s existing GRUB menu.\n'
    printf 'Mount that OS and its separate /boot (if used) first; see the guide.\n'
    read -r -p 'Mounted installed Arch root [/mnt]: ' arch_root || return 1
    arch_root=${arch_root:-/mnt}
    read -r -p 'Existing OS GRUB directory (absolute path): ' grub_directory || return 1
    [ -n "$grub_directory" ] || { printf 'No directory selected; cancelled.\n'; return 0; }
    read -r -p "Shared ESP mount [$arch_root/boot/efi]: " esp || return 1
    esp=${esp:-$arch_root/boot/efi}
    prepare_lexr || return 1
    local args=(kernel boot register-arch --arch-root "$arch_root" --grub-directory "$grub_directory" --esp "$esp")
    sudo "$HOME/.local/bin/lexr" "${args[@]}" --dry-run || return 1
    read -r -p 'Add the previewed Arch entry? [y/N]: ' confirm || return 1
    case "$confirm" in
        y|Y|yes) sudo "$HOME/.local/bin/lexr" "${args[@]}" --yes ;;
        *) printf 'No boot menu changed.\n' ;;
    esac
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
  4. Read the getting-started and installation guide
  5. Open the Lexr included on this USB
  6. Install Arch with the guided Surface installer
  7. After installation: add Arch to an existing GRUB menu
  0. Return to the shell and customise Arch

The live session is temporary. Option 6 opens Archinstall and requires you to
select and confirm a supported partition layout before installation.
MENU
    read -r -p 'Select an option: ' choice || exit 0
    case "$choice" in
        1) show_status ;;
        2) sudo nmtui ;;
        3) sudo pacman-key --init && sudo pacman-key --populate archlinuxarm ;;
        4) less /usr/share/lexr/LEXR_GETTING_STARTED.txt ;;
        5) if prepare_lexr; then "$HOME/.local/bin/lexr" --help; fi ;;
        0) exit 0 ;;
        6)
            if sudo /usr/local/bin/archinstall; then
                printf '\nIf you boot through another OS\047s GRUB, use option 7 after installation.\n'
                printf 'The installer\047s BootNext selection lasts for one boot only.\n'
            fi ;;
        7) register_grub || printf 'Registration did not complete; review the message above.\n' ;;
        *) printf 'Choose one of the listed options.\n' ;;
    esac
done
