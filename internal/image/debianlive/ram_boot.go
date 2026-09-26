package debianlive

// ramBootScriptPath is included only in the live initramfs. Debian's native
// copier runs before this hook, and /run moves into the live root afterwards.
const ramBootScriptPath = "scripts/live-bottom/lexr-verify-ram"

// ramBootChecks verifies the result of live-boot's whole-medium RAM copy. The
// parameters name observations so the same checks can exercise failed copies
// without mounting filesystems or requiring a particular host kernel.
const ramBootChecks = `lexr_verify_ram() {
    medium=$1
    mounts=$2
    blocks=$3
    checksums=$4
    if ! awk -v medium="$medium" '
        $2 == medium { count++; if ($3 != "tmpfs") bad=1 }
        index($2, medium "/") == 1 { bad=1 }
        END { exit (count != 1 || bad) }
    ' "$mounts"; then
        echo "Lexr: RAM copy is not an isolated tmpfs medium." >&2
        return 1
    fi
    loop=$(awk '$2 == "/run/live/rootfs/filesystem.squashfs" && $3 == "squashfs" { print $1 }' "$mounts")
    case "$loop" in
        /dev/loop[0-9]*)
            number=${loop#/dev/loop}
            case "$number" in *[!0-9]*) return 1 ;; esac
            ;;
        *) echo "Lexr: RAM root has no single mounted SquashFS loop." >&2; return 1 ;;
    esac
    backing=$(cat "$blocks/${loop#/dev/}/loop/backing_file") || return 1
    if [ "$backing" != "$medium/live/filesystem.squashfs" ]; then
        echo "Lexr: the live filesystem loop is not backed by the RAM copy." >&2
        return 1
    fi
    if ! awk '
        NF != 2 || length($1) != 32 || $1 ~ /[^0-9a-f]/ { bad=1; next }
        $2 !~ /^\.\// || $2 ~ /\\/ || $2 ~ /\/\// || $2 ~ /\/\.\.?\// || $2 ~ /\/\.\.?$/ { bad=1; next }
        seen[$2]++ { bad=1; next }
        $2 == "./live/filesystem.squashfs" { root++ }
        $2 == "./sp11/lexr-manifest.json" { manifest++ }
        $2 !~ /^\.\/\.disk\// { print }
        END { if (bad || root != 1 || manifest != 1) exit 1 }
    ' "$medium/md5sum.txt" > "$checksums"; then
        echo "Lexr: RAM media checksum inventory is missing or invalid." >&2
        return 1
    fi
    # The pinned Debian copier omits the top-level hidden .disk directory.
    # Check every file it copies, including /live and the complete /sp11 bundle.
    # These MD5 records detect failed copies; release authenticity uses SHA-256.
    if ! (cd "$medium" && md5sum --quiet -c "$checksums"); then
        echo "Lexr: RAM media verification failed; the copy is incomplete or damaged." >&2
        return 1
    fi
    echo "Lexr: verified live filesystem and companion media in RAM."
}
`

// ramBootWait repeats verification after every diagnostic shell. A shell exit
// is never permission to continue booting from a failed or incomplete copy.
const ramBootWait = `lexr_ram_boot() {
    cmdline=$1
    shift
    set -f
    ram_mode=
    for argument in $(cat "$cmdline"); do
        case "$argument" in
            toram) [ "$ram_mode" = module ] || ram_mode=whole ;;
            toram=*) ram_mode=module ;;
        esac
    done
    [ -n "$ram_mode" ] || return 0
    while :; do
        if [ "$ram_mode" = whole ]; then
            if lexr_verify_ram "$@"; then
                return 0
            fi
        else
            echo "Lexr: use bare toram, not toram=filesystem.squashfs, to preserve the installer and companion paths." >&2
        fi
        panic "Lexr RAM boot verification failed. Reboot or inspect the copy; exiting this shell repeats verification."
    done
}
`

// ramBootScript prevents live-boot from silently continuing from USB or a
// partial copy when its native toram helper ignores a mount or copy failure.
const ramBootScript = `#!/bin/sh
case "$1" in prereqs) exit 0 ;; esac
. /scripts/functions
` + ramBootChecks + ramBootWait + `lexr_ram_boot /proc/cmdline /run/live/medium /proc/mounts /sys/class/block /run/lexr-ram-checksums
`
