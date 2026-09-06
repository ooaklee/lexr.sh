package sp11

import (
	"context"
	"fmt"
	"path"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/ooaklee/lexr.sh/internal/platform"
)

// earlyModules includes platform dependencies which ELF module dependencies
// alone do not describe. The OLED DT uses samsung,atna33xc20 and the LCD DT
// uses edp-panel; msm must not take over the firmware display without them.
// QMI/PDR opens an AF_QIPCRTR socket, so its QRTR protocol and remote transport
// must be available before the PMIC GLINK USB role service can initialise.
var earlyModules = []string{
	"qcom_q6v5_pas", "qrtr", "qrtr_smd", "qcom_pd_mapper",
	"msm", "panel_samsung_atna33xc20", "panel_edp",
	"surface_aggregator_hub",
}

// EarlyModuleHook copies modules and their dependencies for normal coldplug.
// It neither forces a load nor restarts the DSP, and remains useful after install.
func EarlyModuleHook() string {
	return `#!/bin/sh
set -e
case "${1:-}" in
prereqs) exit 0 ;;
esac
. /usr/share/initramfs-tools/hook-functions
manual_add_modules ` + strings.Join(earlyModules, " ") + "\n"
}

// modulePathPattern bounds kmod's data-only output before it becomes a file path.
var modulePathPattern = regexp.MustCompile(`^[a-zA-Z0-9_./+-]+$`)

// moduleClosure normalises trusted kmod query output without accepting install
// commands, module parameters, other ABIs or paths outside the inspected tree.
func moduleClosure(output, root, abi string) ([]string, error) {
	var records []string
	seen := make(map[string]bool)
	variants := make(map[string]string)
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 2 {
			return nil, fmt.Errorf("unexpected module dependency record %q", line)
		}
		var record string
		switch fields[0] {
		case "builtin":
			if !modulePathPattern.MatchString(fields[1]) || strings.ContainsAny(fields[1], "/.") {
				return nil, fmt.Errorf("invalid built-in module %q", fields[1])
			}
			record = "builtin " + strings.ReplaceAll(fields[1], "-", "_")
		case "insmod":
			prefix := root + "/lib/modules/" + abi + "/"
			relative := strings.TrimPrefix(fields[1], prefix)
			if relative == fields[1] || !modulePathPattern.MatchString(relative) ||
				path.IsAbs(relative) || path.Clean(relative) != relative ||
				relative == ".." || strings.HasPrefix(relative, "../") ||
				!(strings.HasSuffix(relative, ".ko") || strings.HasSuffix(relative, ".ko.zst") || strings.HasSuffix(relative, ".ko.xz")) {
				return nil, fmt.Errorf("module dependency escapes the selected kernel: %q", fields[1])
			}
			// Debian decompresses modules inside its initramfs. Compare the
			// kernel object identity here and its complete decoded bytes below.
			record = "insmod " + strings.TrimSuffix(strings.TrimSuffix(relative, ".zst"), ".xz")
			if previous, exists := variants[record]; exists && previous != relative {
				return nil, fmt.Errorf("ambiguous module representations for %q", record)
			}
			variants[record] = relative
		default:
			return nil, fmt.Errorf("module query returned a non-data action %q", fields[0])
		}
		if !seen[record] {
			records = append(records, record)
			seen[record] = true
		}
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("required early module dependency closure is empty")
	}
	// depmod may order independent dependency branches differently in the full
	// package and the smaller initramfs tree. Compare membership, not one
	// particular topological ordering of the same required objects.
	sort.Strings(records)
	return records, nil
}

// ValidateEarlyModules resolves dependencies from freshly indexed kernel package
// bytes, then compares the shipped initramfs indices and objects. Only container
// kmod runs; image executables and modprobe configuration are never executed.
func ValidateEarlyModules(ctx context.Context, docker *platform.Docker, image, workspace, volume, abi string) error {
	if !SafeKernelABI(abi) {
		return fmt.Errorf("invalid SP11 early module kernel ABI")
	}
	expected := "/linux-work/package-linux-modules-" + abi
	const prepare = `set -o pipefail
expected=$1
abi=$2
test -d "$expected/usr/lib/modules/$abi"
test "$(realpath "$expected/usr/lib/modules/$abi")" = "$expected/usr/lib/modules/$abi"
if [ ! -e "$expected/lib" ] && [ ! -L "$expected/lib" ]; then
    ln -s usr/lib "$expected/lib"
fi
test "$(realpath "$expected/lib/modules/$abi")" = "$expected/usr/lib/modules/$abi"
depmod -C /dev/null -b "$expected" "$abi"
for kind in live installed; do
    unpacked="/linux-work/$kind-initrd"
    merged="/linux-work/$kind-module-view"
    test ! -e "$merged"
    test ! -L "$merged"
    mkdir -p "$merged/lib/modules/$abi"
    # unmkinitramfs emits early, early2 ... earlyN, then main. Reproduce
    # that overlay in an isolated view using links to data, never executables.
    while IFS= read -r section; do
        [[ "$section" = early || "$section" = main || "$section" =~ ^early([2-9]|[1-9][0-9]+)$ ]]
        test -d "$unpacked/$section"
        test ! -L "$unpacked/$section"
        directory="$unpacked/$section/usr/lib/modules/$abi"
        if [ -d "$directory" ]; then
            test "$(realpath "$directory")" = "$directory"
            cp -al --remove-destination "$directory/." "$merged/lib/modules/$abi/"
        fi
    done < <(find "$unpacked" -mindepth 1 -maxdepth 1 -printf '%f\n' | sort -V)
    for metadata in modules.dep modules.dep.bin modules.builtin modules.builtin.bin; do
        file="$merged/lib/modules/$abi/$metadata"
        test -f "$file"
        test ! -L "$file"
        test "$(realpath "$file")" = "$file"
    done
done
`
	if err := docker.RunInWorkspaceVolume(ctx, image, workspace, volume, "bash", "-ceu", prepare, "lexr-early-module-view", expected, abi); err != nil {
		return fmt.Errorf("prepare early module validation: %w", err)
	}
	query := `root=$1
abi=$2
shift 2
for module in "$@"; do
    modprobe -d "$root" -S "$abi" -C /dev/null --ignore-install --show-depends "$module"
done
`
	queryClosure := func(root string) ([]string, error) {
		args := append([]string{"bash", "-ceu", query, "lexr-early-module-query", root, abi}, earlyModules...)
		output, err := docker.CaptureInWorkspaceVolume(ctx, image, workspace, volume, args...)
		if err != nil {
			return nil, err
		}
		return moduleClosure(string(output), root, abi)
	}
	want, err := queryClosure(expected)
	if err != nil {
		return fmt.Errorf("resolve required early drivers from kernel package: %w", err)
	}
	for _, kind := range []string{"live", "installed"} {
		merged := "/linux-work/" + kind + "-module-view"
		got, err := queryClosure(merged)
		if err != nil {
			return fmt.Errorf("%s initramfs is missing required early drivers: %w", kind, err)
		}
		if !reflect.DeepEqual(want, got) {
			return fmt.Errorf("%s initramfs early driver dependencies differ from the kernel package", kind)
		}
		const compare = `set -o pipefail
expected=$1
merged=$2
unpacked=$3
abi=$4
shift 4
scratch=$(mktemp -d /linux-work/module-compare.XXXXXX)
trap 'rm -rf -- "$scratch"' EXIT
module_file() {
    local base=$1 selected= candidate
    for candidate in "$base" "$base.xz" "$base.zst"; do
        if [ -e "$candidate" ] || [ -L "$candidate" ]; then
            test -z "$selected" || return 1
            test -s "$candidate" || return 1
            test -f "$candidate" || return 1
            test ! -L "$candidate" || return 1
            test "$(realpath "$candidate")" = "$candidate" || return 1
            test "$(stat -c %s "$candidate")" -le 134217728 || return 1
            selected=$candidate
        fi
    done
    test -n "$selected" || return 1
    printf '%s\n' "$selected"
}
decode_module() {
    local input=$1 output=$2
    case "$input" in
        *.ko.zst) timeout 30 zstd -dcq --memory=256MB "$input" | head -c 134217729 > "$output" ;;
        *.ko.xz) timeout 30 xz -dc --memlimit-decompress=256MiB "$input" | head -c 134217729 > "$output" ;;
        *.ko) head -c 134217729 "$input" > "$output" ;;
        *) return 1 ;;
    esac
    test "$(stat -c %s "$output")" -le 134217728
}
for relative in "$@"; do
    source=$(module_file "$expected/usr/lib/modules/$abi/$relative")
    target=$(module_file "$merged/lib/modules/$abi/$relative")
    decode_module "$source" "$scratch/source"
    decode_module "$target" "$scratch/target"
    cmp "$scratch/source" "$scratch/target"
    # A stale copy in an earlier CPIO section must not silently survive,
    # including a copy using a different compression representation.
    for section in "$unpacked"/*; do
        base="$section/usr/lib/modules/$abi/$relative"
        for file in "$base" "$base.xz" "$base.zst"; do
            if [ -e "$file" ] || [ -L "$file" ]; then
                previous=$(module_file "$base")
                decode_module "$previous" "$scratch/previous"
                cmp "$scratch/source" "$scratch/previous"
                break
            fi
        done
    done
done
`
		args := []string{"bash", "-ceu", compare, "lexr-early-module-bytes", expected, merged, "/linux-work/" + kind + "-initrd", abi}
		for _, record := range want {
			if relative, ok := strings.CutPrefix(record, "insmod "); ok {
				args = append(args, relative)
			}
		}
		if err := docker.RunInWorkspaceVolume(ctx, image, workspace, volume, args...); err != nil {
			return fmt.Errorf("%s initramfs early driver bytes differ from the kernel package: %w", kind, err)
		}
	}
	return nil
}
