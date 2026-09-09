package archlinux

import (
	"context"
	"fmt"
	"path"
	"reflect"
	"regexp"
	"sort"
	"strings"
)

// earlyModules includes platform dependencies which ELF module dependencies
// alone do not describe. The OLED DT uses samsung,atna33xc20 and the LCD DT
// uses edp-panel; msm must not take over the firmware display without them.
// QMI/PDR opens an AF_QIPCRTR socket, so its QRTR protocol and remote transport
// must be available before the PMIC GLINK USB role service can initialise.
var earlyModules = []string{
	"qcom_q6v5_pas", "qrtr", "qrtr_smd", "qcom_pd_mapper",
	"msm", "panel_samsung_atna33xc20", "panel_edp",
	"surface_aggregator_hub", "ath12k",
}

// modulePathPattern bounds kmod's data-only output before it becomes a file path.
var modulePathPattern = regexp.MustCompile(`^[a-zA-Z0-9_./+-]+$`)

// moduleClosure normalises trusted kmod query output without accepting install
// commands, module parameters, other ABIs or paths outside the inspected tree.
func moduleClosure(output, root, abi string) ([]string, error) {
	var records []string
	seen := make(map[string]bool)
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
			record = "insmod " + relative
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

// validateEarlyModules resolves dependencies from freshly indexed kernel package
// bytes, then compares the shipped initramfs indices and objects. Only container
// kmod runs; image executables and modprobe configuration are never executed.
func (v *Validator) validateEarlyModules(ctx context.Context, image, workspace, volume, abi string) error {
	if !abiPattern.MatchString(abi) {
		return fmt.Errorf("invalid Arch early module kernel ABI")
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
    test ! -e "$merged" && test ! -L "$merged"
    mkdir -p "$merged/lib/modules/$abi"
    # unmkinitramfs emits early, early2 ... earlyN, then main. Reproduce
    # that overlay in an isolated view using links to data, never executables.
    while IFS= read -r section; do
        [[ "$section" = early || "$section" = main || "$section" =~ ^early([2-9]|[1-9][0-9]+)$ ]]
        test -d "$unpacked/$section" && test ! -L "$unpacked/$section"
        directory="$unpacked/$section/usr/lib/modules/$abi"
        if [ -d "$directory" ]; then
            test "$(realpath "$directory")" = "$directory"
            cp -al --remove-destination "$directory/." "$merged/lib/modules/$abi/"
        fi
    done < <(find "$unpacked" -mindepth 1 -maxdepth 1 -printf '%f\n' | sort -V)
    for metadata in modules.dep modules.dep.bin modules.builtin modules.builtin.bin; do
        file="$merged/lib/modules/$abi/$metadata"
        test -f "$file" && test ! -L "$file"
        test "$(realpath "$file")" = "$file"
    done
done
`
	if err := v.Docker.RunInWorkspaceVolume(ctx, image, workspace, volume, "bash", "-ceu", prepare, "lexr-early-module-view", expected, abi); err != nil {
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
		output, err := v.Docker.CaptureInWorkspaceVolume(ctx, image, workspace, volume, args...)
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
		const compare = `expected=$1
merged=$2
unpacked=$3
abi=$4
shift 4
for relative in "$@"; do
    source="$expected/usr/lib/modules/$abi/$relative"
    target="$merged/lib/modules/$abi/$relative"
    for file in "$source" "$target"; do
        test -f "$file" && test ! -L "$file"
        test "$(realpath "$file")" = "$file"
    done
    cmp "$source" "$target"
    # A stale copy in an earlier CPIO section must not silently survive.
    for section in "$unpacked"/*; do
        file="$section/usr/lib/modules/$abi/$relative"
        if [ -e "$file" ] || [ -L "$file" ]; then
            test -f "$file" && test ! -L "$file"
            test "$(realpath "$file")" = "$file"
            cmp "$source" "$file"
        fi
    done
done
`
		args := []string{"bash", "-ceu", compare, "lexr-early-module-bytes", expected, merged, "/linux-work/" + kind + "-initrd", abi}
		for _, record := range want {
			if relative, ok := strings.CutPrefix(record, "insmod "); ok {
				args = append(args, relative)
			}
		}
		if err := v.Docker.RunInWorkspaceVolume(ctx, image, workspace, volume, args...); err != nil {
			return fmt.Errorf("%s initramfs early driver bytes differ from the kernel package: %w", kind, err)
		}
	}
	return nil
}
