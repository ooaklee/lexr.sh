package bootsupport

import "strings"

// containerPackageBuildScript reproducibly assembles the staged payload in Linux.
const containerPackageBuildScript = `#!/bin/sh
set -eu
umask 022

[ "$#" -eq 3 ] || { echo "usage: build-boot-support STAGING OUTPUT SOURCE_DATE_EPOCH" >&2; exit 64; }
staging=$1
output=$2
source_epoch=$3
case "$staging" in /*) ;; *) echo "staging path must be absolute" >&2; exit 65 ;; esac
case "$output" in /*) ;; *) echo "output path must be absolute" >&2; exit 65 ;; esac
case "$source_epoch" in ""|*[!0-9]*) echo "SOURCE_DATE_EPOCH must be an integer" >&2; exit 65 ;; esac
[ -d "$staging" ] && [ ! -L "$staging" ] || { echo "staging path is not a real directory" >&2; exit 65; }
[ "$(readlink -f -- "$staging")" = "$staging" ] || { echo "staging path is redirected" >&2; exit 65; }
output_parent=${output%/*}
[ -d "$output_parent" ] && [ ! -L "$output_parent" ] || { echo "output parent is not a real directory" >&2; exit 65; }
[ "$(readlink -f -- "$output_parent")" = "$output_parent" ] || { echo "output parent is redirected" >&2; exit 65; }

export LC_ALL=C
export TZ=UTC
export SOURCE_DATE_EPOCH=$source_epoch
find "$staging" -exec touch -h -d "@$SOURCE_DATE_EPOCH" {} +
temporary="$output.tmp.$$"
trap 'rm -f -- "$temporary"' EXIT HUP INT TERM
dpkg-deb --root-owner-group -Zxz -z9 --build "$staging" "$temporary"
sync -f "$temporary"
mv -f -- "$temporary" "$output"
trap - EXIT HUP INT TERM
sync -f "$output_parent"
`

// renderPackagePostInstall binds the package maintainer script to its exact ABI.
func renderPackagePostInstall(abi string) string {
	return strings.ReplaceAll(packagePostInstallTemplate, "{{ABI}}", abi)
}

// renderKernelPostInstall binds the kernel post-install hook to its owning ABI.
func renderKernelPostInstall(abi string) string {
	return strings.ReplaceAll(kernelPostInstallTemplate, "{{ABI}}", abi)
}

// renderKernelPostRemove binds the kernel post-removal hook to its owning ABI.
func renderKernelPostRemove(abi string) string {
	return strings.ReplaceAll(kernelPostRemoveTemplate, "{{ABI}}", abi)
}

// packagePostInstallTemplate reconciles exact-ABI state after arbitrary dpkg ordering.
const packagePostInstallTemplate = `#!/bin/sh
set -eu

case "${1:-}" in
	configure)
		status=0
		/usr/libexec/lexr/kernel-boot-refresh refresh --root / --abi '{{ABI}}' --image '/boot/vmlinuz-{{ABI}}' --platform auto || status=$?
		[ "$status" -eq 0 ] && exit 0
		# Image or firmware packages may not yet be unpacked in glob order. The
		# exact linux-update trigger is the mandatory final reconciliation.
		[ "$status" -eq 75 ] && exit 0
		[ "$status" -eq 76 ] && exit 0
		exit "$status"
		;;
	triggered)
		# A deferred source is not acceptable once dpkg processes the exact-ABI
		# linux-update trigger after the transaction's packages are configured.
		# Missing hardware identity remains deferrable for an offline image whose
		# orchestrator must explicitly select one narrowed profile afterwards.
		status=0
		/usr/libexec/lexr/kernel-boot-refresh refresh --root / --abi '{{ABI}}' --image '/boot/vmlinuz-{{ABI}}' --platform auto || status=$?
		[ "$status" -eq 75 ] && exit 0
		exit "$status"
		;;
	abort-upgrade|abort-remove|abort-deconfigure) exit 0 ;;
	*) exit 0 ;;
esac
`

// packagePreRemove removes all state only for final singleton package removal.
const packagePreRemove = `#!/bin/sh
set -eu

case "${1:-}" in
	remove)
		/usr/libexec/lexr/kernel-boot-refresh remove-all --root / --defer-grub
		;;
	upgrade|deconfigure|failed-upgrade) ;;
	*) ;;
esac
`

// packagePostRemove refreshes GRUB after singleton package removal.
const packagePostRemove = `#!/bin/sh
set -eu

case "${1:-}" in
	remove|purge)
		if [ -x /usr/sbin/update-grub ]; then
			/usr/sbin/update-grub
		fi
		;;
	*) ;;
esac
`

// kernelPostInstallTemplate reconciles state whenever a supported image is installed.
const kernelPostInstallTemplate = `#!/bin/sh
set -eu

abi=${1:-}
[ -n "$abi" ] || exit 0
case "$abi" in *[!a-z0-9.+-]*|.*) exit 0 ;; esac
[ "${#abi}" -le 127 ] || exit 0
if [ "$abi" != '{{ABI}}' ] && [ ! -d "/var/lib/lexr/kernel-boot/$abi" ]; then
	exit 0
fi
status=0
/usr/libexec/lexr/kernel-boot-refresh refresh --root / --abi "$abi" --image "/boot/vmlinuz-$abi" --platform auto --defer-grub || status=$?
[ "$status" -eq 0 ] && exit 0
[ "$status" -eq 75 ] && exit 0
exit "$status"
`

// kernelPostRemoveTemplate removes only the state belonging to the removed ABI.
const kernelPostRemoveTemplate = `#!/bin/sh
set -eu

abi=${1:-}
[ -n "$abi" ] || exit 0
case "$abi" in *[!a-z0-9.+-]*|.*) exit 0 ;; esac
[ "${#abi}" -le 127 ] || exit 0
[ "$abi" = '{{ABI}}' ] || [ -d "/var/lib/lexr/kernel-boot/$abi" ] || exit 0
maintainer_action=${DEB_MAINT_PARAMS:-}
maintainer_action=${maintainer_action%% *}
case "$maintainer_action" in
	remove|purge) ;;
	*) exit 0 ;;
esac
exec /usr/libexec/lexr/kernel-boot-refresh remove --root / --abi "$abi" --defer-grub
`

// bootRefreshHelper atomically materialises and removes verified per-ABI state.
const bootRefreshHelper = `#!/bin/sh
set -eu

fail() { echo "lexr kernel boot support: $*" >&2; exit 65; }
safe_abi() { case "$1" in ""|.*|*[!a-z0-9.+-]*) return 1 ;; esac; [ "${#1}" -le 127 ]; }
safe_platform() { case "$1" in ""|[!a-z0-9]*|*[!a-z0-9-]*) return 1 ;; *) [ "${#1}" -le 63 ] ;; esac; }
safe_dtb_path() {
	case "$1" in ""|/*|*..*|*//*|*[!A-Za-z0-9._+~/-]*|*.dtb/) return 1 ;; esac
	[ "${#1}" -le 512 ] || return 1
	dtb_remainder=$1
	dtb_components=0
	while [ -n "$dtb_remainder" ]; do
		case "$dtb_remainder" in */*) dtb_component=${dtb_remainder%%/*}; dtb_remainder=${dtb_remainder#*/} ;; *) dtb_component=$dtb_remainder; dtb_remainder= ;; esac
		case "$dtb_component" in ""|[!A-Za-z0-9]*|*[!A-Za-z0-9._+~-]*) return 1 ;; esac
		dtb_components=$((dtb_components + 1))
	done
	[ "$dtb_components" -ge 2 ] || return 1
	case "$1" in *.dtb) return 0 ;; *) return 1 ;; esac
}
one_line() {
	value=$(sed -n '1p' "$1")
	[ -n "$value" ] && [ "$(wc -l < "$1" | tr -d ' ')" -eq 1 ] || fail "invalid record file $1"
	printf '%s' "$value"
}
digest_file() { sha256sum "$1" | awk '{print $1}'; }
canonical_regular() {
	[ -f "$1" ] && [ ! -L "$1" ] || return 1
	[ "$(readlink -f -- "$1")" = "$1" ] || return 1
}
canonical_directory() {
	[ -d "$1" ] && [ ! -L "$1" ] || return 1
	[ "$(readlink -f -- "$1")" = "$1" ] || return 1
}
copy_platform_identity() {
	if ! dd if="$1" of="$2" bs=4097 count=1 2>/dev/null; then
		fail "could not read $3"
	fi
	identity_size=$(wc -c < "$2" | tr -d ' ')
	[ "$identity_size" -le 4096 ] || fail "$3 exceeds 4096-byte limit"
}
ensure_directory() {
	remainder=$1
	current=$target_root
	while [ -n "$remainder" ]; do
		case "$remainder" in */*) component=${remainder%%/*}; remainder=${remainder#*/} ;; *) component=$remainder; remainder= ;; esac
		[ -n "$component" ] && [ "$component" != . ] && [ "$component" != .. ] || fail "unsafe directory component"
		current="$current/$component"
		if [ -e "$current" ]; then
			[ -d "$current" ] && [ ! -L "$current" ] || fail "directory route is redirected"
		else
			mkdir -m 0755 -- "$current"
		fi
		[ "$(readlink -f -- "$current")" = "$current" ] || fail "directory route is redirected"
	done
}
run_update_grub() {
	[ "$defer_grub" = true ] && return 0
	if [ "$root" = / ]; then
		[ -x /usr/sbin/update-grub ] || fail "update-grub is unavailable"
		/usr/sbin/update-grub
	else
		[ -x "$target_root/usr/sbin/update-grub" ] || fail "target update-grub is unavailable"
		chroot "$root" /usr/sbin/update-grub
	fi
}
guard_boot() {
	boot="$target_root/boot"
	[ -d "$boot" ] && [ ! -L "$boot" ] || fail "boot path is not a real directory"
	[ "$(readlink -f -- "$boot")" = "$boot" ] || fail "boot path is redirected"
	if [ -f "$target_root/etc/fstab" ] && findmnt --fstab --tab-file "$target_root/etc/fstab" --mountpoint /boot >/dev/null 2>&1; then
		findmnt --mountpoint "$boot" >/dev/null 2>&1 || fail "declared separate /boot is not mounted"
	fi
}
select_platform() {
	registry="$target_root/usr/lib/lexr/kernel-platforms"
	[ -d "$registry" ] || fail "platform registry is unavailable"
	if [ "$platform" != auto ]; then
		safe_platform "$platform" || fail "unsafe platform identifier"
		[ -d "$state_root/$abi/$platform" ] || [ -d "$registry/$platform" ] || fail "undeclared platform"
		printf '%s' "$platform"
		return 0
	fi
	identity_temporary=$(mktemp -d /tmp/lexr-kernel-identity.XXXXXX)
	trap 'rm -f -- "$identity_temporary/machine" "$identity_temporary/compatible"; rmdir -- "$identity_temporary" 2>/dev/null || true' EXIT HUP INT TERM
	machine=
	machine_file="$target_root/sys/devices/virtual/dmi/id/product_name"
	if [ -e "$machine_file" ] || [ -L "$machine_file" ]; then
		canonical_regular "$machine_file" || fail "machine identity route is redirected"
		copy_platform_identity "$machine_file" "$identity_temporary/machine" "machine identity"
		machine=$(sed -n '1p' "$identity_temporary/machine")
	fi
	compatible_file=
	canonical_device_tree="$target_root/sys/firmware/devicetree/base"
	if [ -e "$canonical_device_tree" ] || [ -L "$canonical_device_tree" ]; then
		canonical_directory "$canonical_device_tree" || fail "canonical device-tree identity route is redirected"
		canonical_compatible="$canonical_device_tree/compatible"
		if [ -e "$canonical_compatible" ] || [ -L "$canonical_compatible" ]; then
			canonical_regular "$canonical_compatible" || fail "canonical device-tree compatibility route is redirected"
			copy_platform_identity "$canonical_compatible" "$identity_temporary/compatible" "canonical device-tree compatibility"
			compatible_file="$identity_temporary/compatible"
		fi
	fi
	if [ -z "$compatible_file" ]; then
		fallback_device_tree="$target_root/proc/device-tree"
		if [ -e "$fallback_device_tree" ] || [ -L "$fallback_device_tree" ]; then
			canonical_directory "$fallback_device_tree" || fail "fallback device-tree identity route is redirected"
			fallback_compatible="$fallback_device_tree/compatible"
			if [ -e "$fallback_compatible" ] || [ -L "$fallback_compatible" ]; then
				canonical_regular "$fallback_compatible" || fail "fallback device-tree compatibility route is redirected"
				copy_platform_identity "$fallback_compatible" "$identity_temporary/compatible" "fallback device-tree compatibility"
				compatible_file="$identity_temporary/compatible"
			fi
		fi
	fi
	[ -n "$machine" ] || [ -f "$compatible_file" ] || return 75
	matches=
	count=0
	for records in "$state_root/$abi" "$registry"; do
		[ -d "$records" ] || continue
		for record in "$records"/*; do
			[ -d "$record" ] && [ ! -L "$record" ] || continue
			matched=false
			if [ -n "$machine" ] && grep -Fqx -- "$machine" "$record/machine-identities" 2>/dev/null; then matched=true; fi
			if [ -n "$compatible_file" ] && tr '\000' '\n' < "$compatible_file" | grep -Fqxf "$record/compatibles" 2>/dev/null; then matched=true; fi
			if [ "$matched" = true ]; then
				candidate=${record##*/}
				safe_platform "$candidate" || fail "registry returned unsafe platform identifier"
				case " $matches " in *" $candidate "*) ;; *) matches="$matches $candidate"; count=$((count + 1)) ;; esac
			fi
		done
	done
	[ "$count" -eq 1 ] || fail "platform identity did not select exactly one declared record"
	selected_platform=${matches# }
	rm -f -- "$identity_temporary/machine" "$identity_temporary/compatible"
	rmdir -- "$identity_temporary"
	trap - EXIT HUP INT TERM
	printf '%s' "$selected_platform"
}
record_state() {
	state="$state_root/$abi/$selected"
	if [ -d "$state" ]; then
		[ "$(one_line "$state/title")" = "$title" ] || fail "immutable platform title changed for ABI"
		[ "$(one_line "$state/dtb-path")" = "$dtb_path" ] || fail "immutable DTB path changed for ABI"
		[ "$(one_line "$state/dtb-sha256")" = "$dtb_sha256" ] || fail "immutable DTB digest changed for ABI"
		return 0
	fi
	parent="$state_root/$abi"
	ensure_directory "var/lib/lexr/kernel-boot/$abi"
	temporary=$(mktemp -d "$parent/.state.XXXXXX")
	trap 'rm -f -- "$temporary/title" "$temporary/dtb-path" "$temporary/dtb-sha256" "$temporary/machine-identities" "$temporary/compatibles"; rmdir -- "$temporary" 2>/dev/null || true' EXIT HUP INT TERM
	printf '%s\n' "$title" > "$temporary/title"
	printf '%s\n' "$dtb_path" > "$temporary/dtb-path"
	printf '%s\n' "$dtb_sha256" > "$temporary/dtb-sha256"
	cp -- "$record/machine-identities" "$temporary/machine-identities"
	cp -- "$record/compatibles" "$temporary/compatibles"
	chmod 0644 "$temporary/title" "$temporary/dtb-path" "$temporary/dtb-sha256" "$temporary/machine-identities" "$temporary/compatibles"
	mv -- "$temporary" "$state"
	trap - EXIT HUP INT TERM
}
validate_existing_state() {
	state="$state_root/$abi/$selected"
	[ ! -L "$state" ] || fail "owned state is redirected"
	[ ! -e "$state" ] || [ -d "$state" ] || fail "owned state is not a directory"
	[ ! -d "$state" ] || canonical_directory "$state" || fail "owned state is redirected"
	if [ -d "$state" ]; then
		canonical_regular "$state/title" && canonical_regular "$state/dtb-path" && canonical_regular "$state/dtb-sha256" && canonical_regular "$state/machine-identities" && canonical_regular "$state/compatibles" || fail "owned state file is redirected"
		[ "$(one_line "$state/title")" = "$title" ] || fail "immutable platform title changed for ABI"
		[ "$(one_line "$state/dtb-path")" = "$dtb_path" ] || fail "immutable DTB path changed for ABI"
		[ "$(one_line "$state/dtb-sha256")" = "$dtb_sha256" ] || fail "immutable DTB digest changed for ABI"
	fi
}
ensure_single_profile() {
	abi_state="$state_root/$abi"
	[ ! -e "$abi_state" ] || canonical_directory "$abi_state" || fail "ABI state is redirected"
	[ -d "$abi_state" ] || return 0
	for existing in "$abi_state"/*; do
		[ -e "$existing" ] || [ -L "$existing" ] || continue
		canonical_directory "$existing" || fail "ABI state contains a redirected profile"
		existing_platform=${existing##*/}
		safe_platform "$existing_platform" || fail "ABI state contains an unsafe platform"
		[ "$existing_platform" = "$selected" ] || fail "exact ABI is already bound to a different platform"
	done
}
refresh() {
	safe_abi "$abi" || fail "unsafe exact ABI"
	[ "$image" = "/boot/vmlinuz-$abi" ] || fail "image does not match exact ABI"
	guard_boot
	if [ ! -e "$target_root$image" ]; then
		[ ! -L "$target_root$image" ] || fail "exact-ABI image is redirected"
		return 0
	fi
	canonical_regular "$target_root$image" || fail "exact-ABI image is redirected"
	selected=$(select_platform) || return $?
	safe_platform "$selected" || fail "registry returned unsafe platform identifier"
	ensure_single_profile
	if [ -d "$state_root/$abi/$selected" ]; then record="$state_root/$abi/$selected"; else record="$target_root/usr/lib/lexr/kernel-platforms/$selected"; fi
	canonical_directory "$record" || fail "platform record is redirected"
	canonical_regular "$record/title" && canonical_regular "$record/dtb-path" && canonical_regular "$record/dtb-sha256" && canonical_regular "$record/machine-identities" && canonical_regular "$record/compatibles" || fail "platform record file is redirected"
	title=$(one_line "$record/title")
	dtb_path=$(one_line "$record/dtb-path")
	dtb_sha256=$(one_line "$record/dtb-sha256")
	safe_dtb_path "$dtb_path" || fail "registry contains unsafe DTB path"
	case "$dtb_sha256" in ""|*[!0-9a-f]*) fail "registry contains malformed DTB digest" ;; esac
	[ "${#dtb_sha256}" -eq 64 ] || fail "registry contains malformed DTB digest"
	if [ -e "$state_root" ] || [ -L "$state_root" ]; then canonical_directory "$state_root" || fail "owned state is redirected"; fi
	validate_existing_state
	source=
	for candidate in \
		"$target_root/usr/lib/firmware/$abi/device-tree/$dtb_path" \
		"$target_root/usr/lib/linux-image-$abi/$dtb_path"; do
		[ ! -L "$candidate" ] || fail "DTB source is redirected"
		[ ! -e "$candidate" ] || canonical_regular "$candidate" || fail "DTB source is redirected"
		[ ! -f "$candidate" ] || [ "$(digest_file "$candidate")" = "$dtb_sha256" ] || fail "package-side DTB digest differs from registry"
		[ -n "$source" ] || [ ! -f "$candidate" ] || source=$candidate
	done
	if [ -z "$source" ]; then
		[ ! -d "$state_root/$abi/$selected" ] || fail "owned exact-ABI DTB source is unavailable"
		return 76
	fi
	compatibility="$target_root/boot/dtb-$abi"
	if [ -e "$compatibility" ] || [ -L "$compatibility" ]; then
		canonical_regular "$compatibility" || fail "exact-ABI GRUB DTB is redirected"
		if [ "$(digest_file "$compatibility")" != "$dtb_sha256" ] && [ ! -d "$state_root/$abi/$selected" ]; then
			fail "exact-ABI GRUB DTB already exists with an unowned digest"
		fi
	fi
	destination="$target_root/boot/dtbs/$abi/$dtb_path"
	destination_dir=${destination%/*}
	ensure_directory "boot/dtbs/$abi/${dtb_path%/*}"
	if [ -e "$destination" ] || [ -L "$destination" ]; then canonical_regular "$destination" || fail "boot DTB is redirected"; fi
	if [ ! -f "$destination" ] || [ "$(digest_file "$destination")" != "$dtb_sha256" ]; then
		temporary=$(mktemp "$destination_dir/.dtb.XXXXXX")
		trap 'rm -f -- "$temporary"' EXIT HUP INT TERM
		install -m 0644 "$source" "$temporary"
		[ "$(digest_file "$temporary")" = "$dtb_sha256" ] || fail "staged DTB digest mismatch"
		sync -f "$temporary"
		mv -f -- "$temporary" "$destination"
		trap - EXIT HUP INT TERM
		sync -f "$destination_dir"
	fi
	[ "$(digest_file "$destination")" = "$dtb_sha256" ] || fail "boot DTB digest mismatch"
	if [ ! -f "$compatibility" ] || [ "$(digest_file "$compatibility")" != "$dtb_sha256" ]; then
		temporary=$(mktemp "$target_root/boot/.dtb-$abi.XXXXXX")
		trap 'rm -f -- "$temporary"' EXIT HUP INT TERM
		install -m 0644 "$source" "$temporary"
		[ "$(digest_file "$temporary")" = "$dtb_sha256" ] || fail "staged exact-ABI GRUB DTB digest mismatch"
		sync -f "$temporary"
		mv -f -- "$temporary" "$compatibility"
		trap - EXIT HUP INT TERM
		sync -f "$target_root/boot"
	fi
	[ "$(digest_file "$compatibility")" = "$dtb_sha256" ] || fail "exact-ABI GRUB DTB digest mismatch"
	record_state
	if [ -e "$target_root/boot/initrd.img-$abi" ] || [ -L "$target_root/boot/initrd.img-$abi" ]; then
		canonical_regular "$target_root/boot/initrd.img-$abi" || fail "exact-ABI initramfs is redirected"
		run_update_grub
	fi
}
remove_one() {
	safe_abi "$abi" || fail "unsafe exact ABI"
	abi_state="$state_root/$abi"
	[ -d "$abi_state" ] || return 0
	guard_boot
	canonical_directory "$state_root" && canonical_directory "$abi_state" || fail "owned state is redirected"
	state=
	count=0
	for state in "$abi_state"/*; do
		[ -e "$state" ] || [ -L "$state" ] || continue
		canonical_directory "$state" || fail "owned state is redirected"
		canonical_regular "$state/title" && canonical_regular "$state/dtb-path" && canonical_regular "$state/dtb-sha256" && canonical_regular "$state/machine-identities" && canonical_regular "$state/compatibles" || fail "owned state file is redirected"
		count=$((count + 1))
	done
	[ "$count" -eq 1 ] || fail "owned ABI state must contain exactly one platform"
	dtb_path=$(one_line "$state/dtb-path")
	dtb_sha256=$(one_line "$state/dtb-sha256")
	safe_dtb_path "$dtb_path" || fail "owned state contains unsafe DTB path"
	case "$dtb_sha256" in ""|*[!0-9a-f]*) fail "owned state contains malformed DTB digest" ;; esac
	[ "${#dtb_sha256}" -eq 64 ] || fail "owned state contains malformed DTB digest"
	destination="$target_root/boot/dtbs/$abi/$dtb_path"
	compatibility="$target_root/boot/dtb-$abi"
	for owned in "$destination" "$compatibility"; do
		if [ -e "$owned" ] || [ -L "$owned" ]; then
			canonical_regular "$owned" || fail "owned boot DTB is redirected"
			[ "$(digest_file "$owned")" = "$dtb_sha256" ] || fail "owned boot DTB digest changed before removal"
		fi
	done
	rm -f -- "$destination" "$compatibility"
	rm -f -- "$state/title" "$state/dtb-path" "$state/dtb-sha256" "$state/machine-identities" "$state/compatibles"
	rmdir -- "$state"
	rmdir -- "$abi_state" 2>/dev/null || true
	run_update_grub
}
remove_all() {
	[ -d "$state_root" ] || return 0
	canonical_directory "$state_root" || fail "owned state is redirected"
	# Validate every ABI before removing any state. Package removal must never
	# strand an installed raw kernel by deleting the DTB used by stock GRUB.
	for abi_state in "$state_root"/*; do
		[ -e "$abi_state" ] || [ -L "$abi_state" ] || continue
		canonical_directory "$abi_state" || fail "owned ABI state is redirected"
		abi=${abi_state##*/}
		safe_abi "$abi" || fail "owned state contains an unsafe ABI"
		image="$target_root/boot/vmlinuz-$abi"
		if [ -e "$image" ] || [ -L "$image" ]; then
			fail "cannot remove boot support while exact-ABI image remains installed: $abi"
		fi
	done
	for abi_state in "$state_root"/*; do
		[ -d "$abi_state" ] || continue
		abi=${abi_state##*/}
		remove_one
	done
}

operation=${1:-}
[ -n "$operation" ] || fail "operation is required"
shift
root=
abi=
image=
platform=
defer_grub=false
while [ "$#" -gt 0 ]; do
	case "$1" in
		--root) [ "$#" -ge 2 ] || fail "--root requires a value"; root=$2; shift 2 ;;
		--abi) [ "$#" -ge 2 ] || fail "--abi requires a value"; abi=$2; shift 2 ;;
		--image) [ "$#" -ge 2 ] || fail "--image requires a value"; image=$2; shift 2 ;;
		--platform) [ "$#" -ge 2 ] || fail "--platform requires a value"; platform=$2; shift 2 ;;
		--defer-grub) defer_grub=true; shift ;;
		*) fail "unsupported argument" ;;
	esac
done
[ -n "$root" ] && [ "${root#/}" != "$root" ] || fail "absolute --root is required"
root=${root%/}; [ -n "$root" ] || root=/
[ -d "$root" ] && [ ! -L "$root" ] || fail "root is not a real directory"
root=$(readlink -f -- "$root")
target_root=$root
[ "$target_root" != / ] || target_root=
state_root="$target_root/var/lib/lexr/kernel-boot"
ensure_directory "run/lock"
exec 9>"$target_root/run/lock/lexr-kernel-boot.lock"
flock 9

case "$operation" in
	refresh) [ -n "$abi" ] && [ -n "$image" ] && [ -n "$platform" ] || fail "refresh requires exact ABI, image, and platform"; refresh ;;
	remove) [ -n "$abi" ] || fail "remove requires exact ABI"; [ -z "$image$platform" ] || fail "remove received refresh-only arguments"; remove_one ;;
	remove-all) [ -z "$abi$image$platform" ] || fail "remove-all does not accept ABI or platform"; remove_all ;;
	*) fail "unsupported operation" ;;
esac
`
