package build

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
)

const (
	// containerCommonHeadersTarget packages the architecture-independent headers first.
	containerCommonHeadersTarget = "binary-headers"
	// containerFlavourTarget packages the flavour-specific headers and runtime pair.
	containerFlavourTarget = "binary-qcom-x1e"
	// containerBuildTarget records the ordered Debian package target set exposed by this CLI.
	containerBuildTarget = containerCommonHeadersTarget + " " + containerFlavourTarget
	// containerMinimumFreeGiB preserves the established kernel free-space guard.
	containerMinimumFreeGiB = 40
	// containerRecipeTemplate is the complete container-side build policy template.
	containerRecipeTemplate = `#!/usr/bin/env bash
set -euo pipefail
umask 022

git_url="$1"
git_ref="$2"
jobs="$3"
reset_source="$4"
skip_clean="$5"
boot_image_mode="$6"
recipe_sha256="${LEXR_RECIPE_SHA256:?}"

case "$boot_image_mode" in
  source) flavour_make_args=() ;;
  stubble) flavour_make_args=(do_stubble=true) ;;
  nostubble) flavour_make_args=(do_stubble=false) ;;
  *) echo "Invalid compiled boot-image mode." >&2; exit 1 ;;
esac

work_root=/linux-work
source_parent="$work_root/source"
source_dir="$source_parent/kernel"
artifact_dir=/exchange/artifacts
provenance_dir=/exchange/provenance

release_exchange_outputs() {
  chmod -R a+rwX "$artifact_dir" "$provenance_dir" 2>/dev/null || true
}
trap release_exchange_outputs EXIT

export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get install -y --no-install-recommends \
  bc binutils bison build-essential ca-certificates cpio debhelper devscripts dpkg-dev \
  device-tree-compiler dwarves equivs flex git kmod libelf-dev libssl-dev python3 python3-dev rsync tar

mkdir -p "$source_parent" "$artifact_dir" "$provenance_dir"
if [ -e "$source_dir" ] && [ ! -d "$source_dir" ]; then
  echo "Managed source path is not a directory: $source_dir" >&2
  exit 1
fi
if [ -d "$source_dir" ] && [ ! -d "$source_dir/.git" ]; then
  if [ "$reset_source" = true ]; then
    rm -rf -- "$source_dir"
  else
    echo "Managed source path is not a Git work tree; use --reset-source." >&2
    exit 1
  fi
fi
if [ ! -d "$source_dir" ]; then
  mkdir -p "$source_dir"
  git -C "$source_dir" init
  git -C "$source_dir" remote add origin "$git_url"
else
  actual_remote="$(git -C "$source_dir" remote get-url origin)"
  if [ "$actual_remote" != "$git_url" ]; then
    echo "Managed source remote differs from the requested HTTPS repository." >&2
    exit 1
  fi
  if [ "$reset_source" = true ]; then
    git -C "$source_dir" reset --hard
    git -C "$source_dir" clean -ffdx
  else
    git -C "$source_dir" diff --quiet
    git -C "$source_dir" diff --cached --quiet
    test -z "$(git -C "$source_dir" ls-files --others --exclude-standard)"
  fi
fi

ref_kind=""
if git ls-remote --exit-code --heads "$git_url" "refs/heads/$git_ref" >/dev/null; then
  ref_kind=branch
  git -C "$source_dir" fetch --force --depth=1 origin "refs/heads/$git_ref"
elif git ls-remote --exit-code --tags "$git_url" "refs/tags/$git_ref" >/dev/null; then
  ref_kind=tag
  git -C "$source_dir" fetch --force --depth=1 origin "refs/tags/$git_ref"
else
  echo "Requested Git ref is not a branch or tag: $git_ref" >&2
  exit 1
fi

revision="$(git -C "$source_dir" rev-parse --verify 'FETCH_HEAD^{commit}')"
if [ "$reset_source" != true ] &&
   git -C "$source_dir" rev-parse --verify HEAD >/dev/null 2>&1; then
  local_commits="$(git -C "$source_dir" rev-list --count "$revision..HEAD")"
  if [ "$local_commits" != 0 ]; then
    echo "Managed source contains commits outside the requested remote ref; use --reset-source." >&2
    exit 1
  fi
fi
git -C "$source_dir" checkout --detach "$revision"
git -C "$source_dir" reset --hard "$revision"
test -z "$(git -C "$source_dir" status --porcelain)"

tree="$(git -C "$source_dir" rev-parse --verify 'HEAD^{tree}')"
commit_time="$(git -C "$source_dir" show -s --format=%cI HEAD)"
actual_remote="$(git -C "$source_dir" remote get-url origin)"
printf '%s' "$actual_remote" > "$provenance_dir/git-url"
printf '%s' "$git_ref" > "$provenance_dir/git-ref"
printf '%s' "$boot_image_mode" > "$provenance_dir/boot-image-mode"
printf '%s' "$ref_kind" > "$provenance_dir/ref-kind"
printf '%s' "$revision" > "$provenance_dir/revision"
printf '%s' "$tree" > "$provenance_dir/tree"
printf '%s' "$commit_time" > "$provenance_dir/commit-time"
printf '%s' "$recipe_sha256" > "$provenance_dir/recipe-sha256"

available_kb="$(df -Pk "$work_root" | awk 'NR == 2 { print $4 }')"
required_kb=$(({{MINIMUM_FREE_GIB}} * 1024 * 1024))
if [ -n "$available_kb" ] && [ "$available_kb" -lt "$required_kb" ]; then
  echo "Kernel build requires at least {{MINIMUM_FREE_GIB}} GiB free in the managed Docker volume." >&2
  exit 1
fi

if [ "$jobs" = 0 ]; then
  jobs="$(getconf _NPROCESSORS_ONLN 2>/dev/null || echo 1)"
fi
case "$jobs" in
  ''|*[!0-9]*) echo "Invalid compiled job count." >&2; exit 1 ;;
esac
if [ "$jobs" -lt 1 ] || [ "$jobs" -gt 512 ]; then
  echo "Container job count is outside the compiled limit." >&2
  exit 1
fi

if [ -x "$source_dir/debian/rules" ]; then
  rules=debian/rules
elif [ -x "$source_dir/.debian/rules" ]; then
  rules=.debian/rules
else
  echo "Kernel source has no executable Debian rules file." >&2
  exit 1
fi

if [ ! -f "$source_dir/debian/control" ]; then
  (cd "$source_dir" && "$rules" debian/control)
fi
if [ ! -f "$source_dir/debian/control" ]; then
  echo "Kernel source did not produce debian/control." >&2
  exit 1
fi
dependency_dir="$(mktemp -d)"
(cd "$dependency_dir" && mk-build-deps --install --remove \
  --tool 'apt-get -y --no-install-recommends' "$source_dir/debian/control")
dpkg-query -W -f='${binary:Package}=${Version}\n' | LC_ALL=C sort | sha256sum | awk '{printf "%s", $1}' > "$provenance_dir/toolchain-sha256"

export DEB_BUILD_OPTIONS="parallel=$jobs nocheck noautodbgsym"
if [ "$skip_clean" != true ]; then
  (cd "$source_dir" && "$rules" clean)
fi
find "$source_parent" -mindepth 1 -maxdepth 1 -type f -name '*.deb' -delete
(cd "$source_dir" && "$rules" {{COMMON_HEADERS_TARGET}})
(cd "$source_dir" && "$rules" "${flavour_make_args[@]}" {{FLAVOUR_TARGET}})

find "$source_parent" -mindepth 1 -maxdepth 1 -type f -name '*.deb' -print0 |
  sort -z |
  while IFS= read -r -d '' package; do
    name="$(basename "$package")"
    case "$name" in
      linux-image-unsigned-*) continue ;;
      linux-modules-extra-*) continue ;;
      linux-image-*-qcom-x1e_*_arm64.deb|\
      linux-modules-*-qcom-x1e_*_arm64.deb|\
      linux-headers-*-qcom-x1e_*_arm64.deb|\
      linux-qcom-x1e-headers-*_all.deb)
        destination="$artifact_dir/$name"
        if [ -e "$destination" ]; then
          cmp -s "$package" "$destination" || {
            echo "Conflicting generated package basename: $name" >&2
            exit 1
          }
        else
          install -m 0644 "$package" "$destination"
        fi
        ;;
    esac
  done

inspection_root="$(mktemp -d)"
member_list="$inspection_root/package-members"
control_member_list="$inspection_root/control-members"
image_postinst="$inspection_root/image-postinst"
image_postrm="$inspection_root/image-postrm"
image_triggers="$inspection_root/image-triggers"
expected_image_triggers="$inspection_root/expected-image-triggers"
triggered_postinst="$inspection_root/triggered-postinst"
postinst_hook_command="$inspection_root/postinst-hook-command"
inspection_image="$inspection_root/vmlinuz"
section_candidate="$inspection_root/dtbauto-candidate"
section_list="$inspection_root/sections"
inventory_input="$inspection_root/device-tree-inventory.tsv"
selected_package=""
selected_member=""
vmlinuz_count=0
declare -A dtb_package=()
declare -A dtb_member=()
declare -A dtb_count=([x1e-oled]=0 [x1p-lcd]=0)
declare -a candidate_package=()
declare -a candidate_member=()
candidate_count=0

for package in \
  "$artifact_dir"/linux-image-*-qcom-x1e_*_arm64.deb \
  "$artifact_dir"/linux-modules-*-qcom-x1e_*_arm64.deb; do
  [ -f "$package" ] || continue
  if ! dpkg-deb --fsys-tarfile "$package" | tar -tf - > "$member_list"; then
    echo "Could not inspect a generated runtime package." >&2
    exit 1
  fi
  while IFS= read -r member; do
    selected_package="$package"
    selected_member="$member"
    vmlinuz_count=$((vmlinuz_count + 1))
  done < <(awk '/^\.\/boot\/vmlinuz-[^/]+$/ { print }' "$member_list")
  while IFS= read -r member; do
    case "$member" in
      ./usr/lib/firmware/*/device-tree/*.dtb)
        candidate_count=$((candidate_count + 1))
        if [ "$candidate_count" -gt 1024 ]; then
          echo "Generated runtime packages contain more than 1024 DTB candidates." >&2
          exit 1
        fi
        candidate_package[$candidate_count]="$package"
        candidate_member[$candidate_count]="$member"
        ;;
      *) continue ;;
    esac
    case "$member" in
      */qcom/x1e80100-microsoft-denali-oled.dtb) device=x1e-oled ;;
      */qcom/x1p64100-microsoft-denali.dtb) device=x1p-lcd ;;
      *) continue ;;
    esac
    dtb_package[$device]="$package"
    dtb_member[$device]="$member"
    dtb_count[$device]=$((dtb_count[$device] + 1))
  done < "$member_list"
done

if [ "$vmlinuz_count" -ne 1 ] || [ "$candidate_count" -eq 0 ]; then
  echo "Kernel delivery validation requires exactly one packaged /boot/vmlinuz ABI across the runtime packages." >&2
  exit 1
fi
abi=${selected_member#./boot/vmlinuz-}
case "$abi" in ''|.*|*[!A-Za-z0-9._+~-]*|-*) echo "Generated kernel ABI is unsafe." >&2; exit 1 ;; esac
if ! dpkg-deb --ctrl-tarfile "$selected_package" | tar -tf - > "$control_member_list"; then
  echo "Could not inspect the generated linux-image control archive." >&2
  exit 1
fi
postinst_member="$(awk '/^(\.\/)?postinst$/ { print }' "$control_member_list")"
postrm_member="$(awk '/^(\.\/)?postrm$/ { print }' "$control_member_list")"
triggers_member="$(awk '/^(\.\/)?triggers$/ { print }' "$control_member_list")"
if [ "$(printf '%s\n' "$postinst_member" | awk 'NF { count++ } END { print count + 0 }')" -ne 1 ] ||
   [ "$(printf '%s\n' "$postrm_member" | awk 'NF { count++ } END { print count + 0 }')" -ne 1 ] ||
   [ "$(printf '%s\n' "$triggers_member" | awk 'NF { count++ } END { print count + 0 }')" -ne 1 ]; then
  echo "Generated linux-image package must contain exactly one postinst, postrm, and triggers control member." >&2
  exit 1
fi
if ! dpkg-deb --ctrl-tarfile "$selected_package" | tar -xOf - "$postinst_member" > "$image_postinst" ||
   ! dpkg-deb --ctrl-tarfile "$selected_package" | tar -xOf - "$postrm_member" > "$image_postrm" ||
   ! dpkg-deb --ctrl-tarfile "$selected_package" | tar -xOf - "$triggers_member" > "$image_triggers"; then
  echo "Could not extract the generated linux-image maintainer contract." >&2
  exit 1
fi
if [ ! -s "$image_postinst" ] || [ "$(wc -c < "$image_postinst")" -gt 131072 ] ||
   [ ! -s "$image_postrm" ] || [ "$(wc -c < "$image_postrm")" -gt 131072 ]; then
  echo "Generated linux-image maintainer script is empty or exceeds the inspection bound." >&2
  exit 1
fi
printf 'interest linux-update-%s\n' "$abi" > "$expected_image_triggers"
if ! cmp -s "$expected_image_triggers" "$image_triggers"; then
  echo "Generated linux-image package does not own the exact ABI update trigger." >&2
  exit 1
fi
awk '$0 == "if [ \"$1\" = triggered ]; then" { active=1 } active { print } active && $0 == "    exit 0" { exit }' \
  "$image_postinst" > "$triggered_postinst"
awk '$0 == "    cat - >/usr/lib/linux/triggers/$version <<EOF" { active=1 } active { print } active && $0 == "EOF" { exit }' \
  "$image_postinst" > "$postinst_hook_command"
if ! grep -Fqx "version=$abi" "$image_postinst" ||
   ! grep -Fqx 'image_path=/boot/vmlinuz-$version' "$image_postinst" ||
   ! grep -Fqx 'if [ "$1" = triggered ]; then' "$image_postinst" ||
   ! grep -Fqx '    trigger=/usr/lib/linux/triggers/$version' "$triggered_postinst" ||
   ! grep -Fqx $'\tsh "$trigger"' "$triggered_postinst" ||
   ! grep -Fqx $'\trm -f "$trigger"' "$triggered_postinst" ||
   ! grep -Fqx 'if [ -d /etc/kernel/postinst.d ]; then' "$image_postinst" ||
   ! grep -Fqx '    cat - >/usr/lib/linux/triggers/$version <<EOF' "$postinst_hook_command" ||
   ! grep -Fqx 'DEB_MAINT_PARAMS="$*" run-parts --report --exit-on-error --arg=$version \' "$postinst_hook_command" ||
   ! grep -Fqx '      --arg=$image_path /etc/kernel/postinst.d' "$postinst_hook_command" ||
   ! grep -Fqx 'EOF' "$postinst_hook_command" ||
   ! grep -Fqx '    dpkg-trigger --no-await linux-update-$version' "$image_postinst"; then
  echo "Generated linux-image postinst does not implement the exact ABI kernel-hook lifecycle." >&2
  exit 1
fi
if ! grep -Fqx "version=$abi" "$image_postrm" ||
   ! grep -Fqx 'image_path=/boot/vmlinuz-$version' "$image_postrm" ||
   ! grep -Fqx 'if [ -d /etc/kernel/postrm.d ]; then' "$image_postrm" ||
   ! grep -Fq 'DEB_MAINT_PARAMS="$*" run-parts --report --exit-on-error --arg=$version \' "$image_postrm" ||
   ! grep -Fq -- '--arg=$image_path /etc/kernel/postrm.d' "$image_postrm"; then
  echo "Generated linux-image postrm does not propagate its package lifecycle action to exact-ABI kernel hooks." >&2
  exit 1
fi
if ! dpkg-deb --fsys-tarfile "$selected_package" | tar -xOf - "$selected_member" > "$inspection_image"; then
  echo "Could not extract the generated boot image for structural validation." >&2
  exit 1
fi
if [ ! -s "$inspection_image" ] || ! objdump -h "$inspection_image" > "$section_list"; then
  echo "Generated boot image has no inspectable PE section table." >&2
  exit 1
fi

declare -a candidate_file=()
declare -a candidate_compatibles=()
declare -a candidate_digest=()
declare -a candidate_matches=()
declare -A extracted_package_root=()
package_number=0
for package in \
  "$artifact_dir"/linux-image-*-qcom-x1e_*_arm64.deb \
  "$artifact_dir"/linux-modules-*-qcom-x1e_*_arm64.deb; do
  [ -f "$package" ] || continue
  package_number=$((package_number + 1))
  candidate_list="$inspection_root/package-$package_number-dtbs"
  : > "$candidate_list"
  for ((candidate=1; candidate<=candidate_count; candidate++)); do
    if [ "${candidate_package[$candidate]}" = "$package" ]; then
      expected_prefix="./usr/lib/firmware/$abi/device-tree/"
      case "${candidate_member[$candidate]}" in "$expected_prefix"*) ;; *) echo "Packaged DTB is not scoped to the generated ABI." >&2; exit 1 ;; esac
      printf '%s\n' "${candidate_member[$candidate]}" >> "$candidate_list"
    fi
  done
  if [ -s "$candidate_list" ]; then
    extracted="$inspection_root/package-$package_number"
    mkdir -p "$extracted"
    if ! dpkg-deb --fsys-tarfile "$package" | tar -xf - -C "$extracted" -T "$candidate_list"; then
      echo "Could not extract the packaged DTB candidate set." >&2
      exit 1
    fi
    extracted_package_root[$package]="$extracted"
  fi
done
for ((candidate=1; candidate<=candidate_count; candidate++)); do
  candidate_package_path="${candidate_package[$candidate]}"
  dtb="${extracted_package_root[$candidate_package_path]}/${candidate_member[$candidate]#./}"
  if [ ! -f "$dtb" ] || [ ! -s "$dtb" ]; then
    echo "Could not extract packaged DTB candidate $candidate." >&2
    exit 1
  fi
  compatibles="$inspection_root/candidate-$candidate.compatibles"
  if ! fdtget -t s "$dtb" / compatible | tr ' ' '\n' | awk 'NF && !seen[$0]++' > "$compatibles" || [ ! -s "$compatibles" ]; then
    echo "Packaged DTB candidate $candidate has no inspectable compatible selector." >&2
    exit 1
  fi
  candidate_file[$candidate]="$dtb"
  candidate_compatibles[$candidate]="$compatibles"
  candidate_digest[$candidate]="$(sha256sum "$dtb" | awk '{print $1}')"
  candidate_matches[$candidate]=0
done

linux_sections="$(awk '$2 == ".linux" { count++ } END { print count + 0 }' "$section_list")"
hwids_sections="$(awk '$2 == ".hwids" { count++ } END { print count + 0 }' "$section_list")"
machdb_sections="$(awk '$2 == ".machdb" { count++ } END { print count + 0 }' "$section_list")"
fixed_dtb_sections="$(awk '$2 == ".dtb" { count++ } END { print count + 0 }' "$section_list")"
dtbauto_sections="$(awk '$2 == ".dtbauto" { count++ } END { print count + 0 }' "$section_list")"
if [ "$fixed_dtb_sections" -ne 0 ]; then
  echo "Generated boot image contains a fixed .dtb fallback outside the declared selection model." >&2
  exit 1
fi
if [ "$dtbauto_sections" -gt 64 ]; then
  echo "Generated boot image contains more than 64 embedded DTBs." >&2
  exit 1
fi
if [ "$dtbauto_sections" -gt 0 ]; then
  effective_delivery=embedded
  if [ "$linux_sections" -ne 1 ] || [ "$hwids_sections" -ne 1 ]; then
    echo "Embedded delivery requires exactly one .linux and one .hwids PE section." >&2
    exit 1
  fi
  if [ "$machdb_sections" -gt 1 ]; then
    echo "Embedded delivery contains more than one .machdb PE section." >&2
    exit 1
  fi
else
  effective_delivery=external-required
fi
if [ "$boot_image_mode" = stubble ] && [ "$effective_delivery" != embedded ]; then
  echo "Explicit Stubble mode did not produce embedded DTB delivery." >&2
  exit 1
fi
if [ "$boot_image_mode" = nostubble ] && [ "$effective_delivery" != external-required ]; then
  echo "Explicit non-Stubble mode unexpectedly produced embedded DTB delivery." >&2
  exit 1
fi

section_number=0
while read -r section_size section_offset; do
  section_number=$((section_number + 1))
  if ! dd if="$inspection_image" of="$section_candidate" iflag=skip_bytes,count_bytes \
      skip="$((16#$section_offset))" count="$((16#$section_size))" status=none; then
    echo "Could not extract generated .dtbauto section $section_number." >&2
    exit 1
  fi
  section_matches=0
  for ((candidate=1; candidate<=candidate_count; candidate++)); do
    if cmp -s "${candidate_file[$candidate]}" "$section_candidate"; then
      candidate_matches[$candidate]=$((candidate_matches[$candidate] + 1))
      section_matches=$((section_matches + 1))
    fi
  done
  if [ "$section_matches" -ne 1 ]; then
    echo "Embedded DTB section $section_number is missing, stale, ambiguous, or undeclared." >&2
    exit 1
  fi
done < <(awk '$2 == ".dtbauto" { print $3, $6 }' "$section_list")

for ((candidate=1; candidate<=candidate_count; candidate++)); do
  package_path=${candidate_member[$candidate]#./}
  basename=${package_path##*/}
  required=false
  case "$basename" in
    x1e80100-microsoft-denali-oled.dtb) stable_id=surface-pro-11-x1e-oled; [ "$effective_delivery" = embedded ] && required=true ;;
    x1p64100-microsoft-denali.dtb) stable_id=surface-pro-11-x1p-lcd ;;
    *) stable_id="dtb-$(printf '%s' "$package_path" | sha256sum | awk '{print substr($1, 1, 24)}')" ;;
  esac
  include=false
  if [ "$effective_delivery" = embedded ] && [ "${candidate_matches[$candidate]}" -gt 0 ]; then
    include=true
  fi
  if [ "$effective_delivery" = external-required ] && { [ "$stable_id" = surface-pro-11-x1e-oled ] || [ "$stable_id" = surface-pro-11-x1p-lcd ]; }; then
    include=true
    required=true
  fi
  if [ "$required" = true ] && [ "${candidate_matches[$candidate]}" -ne 1 ] && [ "$effective_delivery" = embedded ]; then
    echo "Required DTB $stable_id does not have exactly one embedded copy." >&2
    exit 1
  fi
  if [ "$include" = true ]; then
    printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
      "$stable_id" "$basename" "$package_path" "${candidate_digest[$candidate]}" "${candidate_matches[$candidate]}" \
      "${candidate_compatibles[$candidate]}" "$required" >> "$inventory_input"
  fi
done
for device in x1e-oled x1p-lcd; do
  if [ "$effective_delivery" = external-required ] && [ "${dtb_count[$device]}" -ne 1 ]; then
    echo "External delivery requires exactly one packaged DTB for $device." >&2
    exit 1
  fi
done
if [ "$effective_delivery" = embedded ] && [ "${dtb_count[x1e-oled]}" -ne 1 ]; then
  echo "Embedded SP11 OLED profile requires exactly one packaged X1E/OLED DTB." >&2
  exit 1
fi
if [ ! -s "$inventory_input" ]; then
  echo "Kernel delivery produced no declared DTB inventory." >&2
  exit 1
fi

hwids_root=-
machdb_input=-
hwids_digest_output=-
selection_records_output=-
if [ "$effective_delivery" = embedded ]; then
  for required_input in \
    /usr/lib/stubble/stubble.efi \
    /usr/libexec/stubble/finddtbs.py \
    /usr/share/stubble/hwids \
    /usr/share/stubble/sbat \
    /usr/bin/ukify; do
    if [ ! -e "$required_input" ]; then
      echo "Embedded delivery attribution input is missing." >&2
      exit 1
    fi
  done
  stubble_stub="$(readlink -f /usr/lib/stubble/stubble.efi)"
  stubble_helper="$(readlink -f /usr/libexec/stubble/finddtbs.py)"
  ukify_executable="$(readlink -f /usr/bin/ukify)"
  for executable in "$stubble_stub" "$stubble_helper" "$ukify_executable"; do
    if [ ! -f "$executable" ]; then
      echo "Embedded delivery executable identity is not a regular file." >&2
      exit 1
    fi
  done
  ukify_package="$(dpkg-query -S /usr/bin/ukify | awk -F ': ' '$2 == "/usr/bin/ukify" { print $1 }' | LC_ALL=C sort -u)"
  if [ -z "$ukify_package" ] || [ "$(printf '%s\n' "$ukify_package" | wc -l)" -ne 1 ]; then
    echo "Could not identify exactly one package owning ukify." >&2
    exit 1
  fi
  case "$ukify_package" in ''|*[!A-Za-z0-9.+:-]*) echo "ukify package identity is unsafe." >&2; exit 1 ;; esac
  printf '%s' stubble > "$provenance_dir/stubble-tool"
  dpkg-query -W -f='${Version}' stubble > "$provenance_dir/stubble-version"
  sha256sum "$stubble_stub" | awk '{printf "%s", $1}' > "$provenance_dir/stubble-stub-sha256"
  sha256sum "$stubble_helper" | awk '{printf "%s", $1}' > "$provenance_dir/stubble-helper-sha256"
  sha256sum /usr/share/stubble/sbat | awk '{printf "%s", $1}' > "$provenance_dir/stubble-sbat-sha256"
  printf '%s' ukify > "$provenance_dir/ukify-tool"
  printf '%s' "$ukify_package" > "$provenance_dir/ukify-package"
  dpkg-query -W -f='${Version}' "$ukify_package" > "$provenance_dir/ukify-version"
  sha256sum "$ukify_executable" | awk '{printf "%s", $1}' > "$provenance_dir/ukify-sha256"
  hwids_root=/usr/share/stubble/hwids
  hwids_digest_output="$provenance_dir/stubble-hwids-sha256"
  selection_records_output="$provenance_dir/device-tree-selection-records.json"
  if [ "$machdb_sections" -eq 1 ]; then
    if [ ! -f /usr/share/stubble/machdb.txt ]; then
      echo "Embedded .machdb section has no installed Stubble machdb input." >&2
      exit 1
    fi
    machdb_input=/usr/share/stubble/machdb.txt
    read -r machdb_size machdb_offset < <(awk '$2 == ".machdb" { print $3, $6 }' "$section_list")
    if ! dd if="$inspection_image" of="$section_candidate" iflag=skip_bytes,count_bytes \
        skip="$((16#$machdb_offset))" count="$((16#$machdb_size))" status=none ||
       ! cmp -s "$machdb_input" "$section_candidate"; then
      echo "Embedded .machdb section differs from the installed Stubble machdb input." >&2
      exit 1
    fi
    sha256sum "$machdb_input" | awk '{printf "%s", $1}' > "$provenance_dir/stubble-machdb-sha256"
  else
    rm -f -- "$provenance_dir/stubble-machdb-sha256"
  fi
else
  rm -f -- \
    "$provenance_dir/stubble-tool" \
    "$provenance_dir/stubble-version" \
    "$provenance_dir/stubble-stub-sha256" \
    "$provenance_dir/stubble-helper-sha256" \
    "$provenance_dir/stubble-sbat-sha256" \
    "$provenance_dir/stubble-hwids-sha256" \
    "$provenance_dir/stubble-machdb-sha256" \
    "$provenance_dir/ukify-tool" \
    "$provenance_dir/ukify-package" \
    "$provenance_dir/ukify-version" \
    "$provenance_dir/ukify-sha256" \
    "$provenance_dir/device-tree-selection-records.json"
fi

python3 - \
  "$effective_delivery" \
  "$inventory_input" \
  "$provenance_dir/device-tree-inventory.json" \
  "$hwids_root" \
  "$machdb_input" \
  "$hwids_digest_output" \
  "$selection_records_output" <<'PY_SELECTION'
import hashlib
import json
import os
import pathlib
import stat
import sys
import uuid

MAX_DATABASE_FILES = 1024
MAX_DATABASE_FILE_BYTES = 256 * 1024
MAX_DATABASE_BYTES = 8 * 1024 * 1024
MAX_RECORDS_PER_DEVICE = 64
MAX_SELECTORS_PER_DEVICE = 128
MAX_VALUES_PER_RECORD = 128
MAX_VALUE_BYTES = 512
MAX_PUBLIC_JSON_BYTES = 1024 * 1024


def public_text(value, field):
    if not isinstance(value, str):
        raise ValueError(f"{field} is not text")
    encoded = value.encode("utf-8")
    if not encoded or len(encoded) > MAX_VALUE_BYTES or value.strip() != value:
        raise ValueError(f"{field} is empty, padded, or too long")
    if not all(character.isprintable() for character in value):
        raise ValueError(f"{field} contains non-printable text")
    return value


def read_regular(path, limit):
    before = path.lstat()
    if not stat.S_ISREG(before.st_mode) or before.st_size <= 0 or before.st_size > limit:
        raise ValueError("selection input is not a bounded non-empty regular file")
    value = path.read_bytes()
    after = path.lstat()
    if before.st_dev != after.st_dev or before.st_ino != after.st_ino or before.st_size != after.st_size:
        raise ValueError("selection input changed while it was read")
    return value


def hwid_database(root):
    root_stat = root.lstat()
    if not stat.S_ISDIR(root_stat.st_mode):
        raise ValueError("Stubble HWID input is not a directory")
    paths = []
    for current, directories, files in os.walk(root, followlinks=False):
        directories.sort()
        files.sort()
        current_path = pathlib.Path(current)
        for directory in tuple(directories):
            if stat.S_ISLNK((current_path / directory).lstat().st_mode):
                raise ValueError("Stubble HWID input contains a directory symlink")
        for name in files:
            if name.endswith(".json"):
                paths.append(current_path / name)
    paths.sort(key=lambda path: path.relative_to(root).as_posix().encode("utf-8"))
    if not paths or len(paths) > MAX_DATABASE_FILES:
        raise ValueError("Stubble HWID input has an invalid file count")
    digest = hashlib.sha256(b"lexr-stubble-hwids-v1\0")
    records = {}
    total = 0
    for path in paths:
        raw = read_regular(path, MAX_DATABASE_FILE_BYTES)
        total += len(raw)
        if total > MAX_DATABASE_BYTES:
            raise ValueError("Stubble HWID input exceeds the aggregate size limit")
        relative = path.relative_to(root).as_posix().encode("utf-8")
        digest.update(len(relative).to_bytes(4, "big"))
        digest.update(relative)
        digest.update(len(raw).to_bytes(8, "big"))
        digest.update(raw)
        try:
            data = json.loads(raw)
        except (UnicodeDecodeError, json.JSONDecodeError) as error:
            raise ValueError("Stubble HWID input is not valid UTF-8 JSON") from error
        if not isinstance(data, dict):
            raise ValueError("Stubble HWID record is not an object")
        record_type = public_text(data.get("type"), "Stubble HWID type")
        public_text(data.get("name"), "Stubble HWID name")
        raw_hwids = data.get("hwids")
        if not isinstance(raw_hwids, list) or not raw_hwids or len(raw_hwids) > MAX_VALUES_PER_RECORD:
            raise ValueError("Stubble HWID record has an invalid hwids list")
        try:
            hwids = sorted({str(uuid.UUID(public_text(value, "Stubble HWID"))) for value in raw_hwids})
        except (ValueError, AttributeError) as error:
            raise ValueError("Stubble HWID record contains an invalid UUID") from error
        if record_type == "devicetree":
            compatible = public_text(data.get("compatible"), "Stubble compatible")
            records.setdefault(compatible, set()).update(hwids)
        elif record_type == "uefi-fw":
            public_text(data.get("fwid"), "Stubble firmware identifier")
        else:
            raise ValueError("Stubble HWID record has an unsupported type")
    return digest.hexdigest(), [
        {"source": "hwids", "compatible": compatible, "hwids": sorted(hwids)}
        for compatible, hwids in sorted(records.items())
    ]


def machdb_records(path):
    raw = read_regular(path, MAX_DATABASE_BYTES)
    try:
        lines = raw.decode("utf-8").splitlines()
    except UnicodeDecodeError as error:
        raise ValueError("Stubble machdb is not valid UTF-8") from error
    records = {}
    models = []
    for line in lines:
        stripped = line.strip()
        if not stripped or stripped.startswith("#"):
            continue
        key, separator, value = stripped.partition(":")
        if not separator:
            raise ValueError("Stubble machdb contains an invalid record")
        value = public_text(value.strip(), "Stubble machdb value")
        if key.strip() == "Model":
            models.append(value)
            if len(models) > MAX_VALUES_PER_RECORD:
                raise ValueError("Stubble machdb record has too many models")
        elif key.strip() == "Compatible":
            if not models:
                raise ValueError("Stubble machdb Compatible has no Model")
            records.setdefault(value, set()).update(models)
            models = []
        else:
            raise ValueError("Stubble machdb contains an unsupported key")
    if models:
        raise ValueError("Stubble machdb has Models without a Compatible")
    return [
        {"source": "machdb", "compatible": compatible, "models": sorted(models)}
        for compatible, models in sorted(records.items())
    ]


def write_public_json(path, value):
    encoded = json.dumps(value, indent=2, sort_keys=True) + "\n"
    if len(encoded.encode("utf-8")) > MAX_PUBLIC_JSON_BYTES:
        raise ValueError("public selection evidence exceeds the size limit")
    pathlib.Path(path).write_text(encoded, encoding="utf-8")


delivery, inventory_input, inventory_output, hwids_root, machdb_input, digest_output, selection_output = sys.argv[1:]
if delivery not in {"embedded", "external-required"}:
    raise ValueError("unsupported effective DTB delivery")

database_records = []
if delivery == "embedded":
    database_digest, database_records = hwid_database(pathlib.Path(hwids_root))
    pathlib.Path(digest_output).write_text(database_digest, encoding="utf-8")
    if machdb_input != "-":
        database_records.extend(machdb_records(pathlib.Path(machdb_input)))

inventory = []
selection_evidence = []
for line in pathlib.Path(inventory_input).read_text(encoding="utf-8").splitlines():
    device, basename, package_path, digest, matches, compatible_path, required_text = line.split("\t")
    compatibles = list(dict.fromkeys(pathlib.Path(compatible_path).read_text(encoding="utf-8").splitlines()))
    if not compatibles:
        raise ValueError("required DTB has no compatible strings")
    for compatible in compatibles:
        public_text(compatible, "DTB compatible")
    if delivery == "embedded":
        matching = [record for record in database_records if record["compatible"] == compatibles[0]]
        if not matching:
            raise ValueError(f"required embedded DTB {device} has no matching Stubble selector record")
        if len(matching) > MAX_RECORDS_PER_DEVICE:
            raise ValueError("required embedded DTB has too many matching selector records")
        selectors = {("compatible", record["compatible"]) for record in matching}
        for record in matching:
            selectors.update(("hwid", value) for value in record.get("hwids", ()))
        public_records = sorted(matching, key=lambda record: (
            record["source"], record["compatible"], record.get("hwids", ()), record.get("models", ()),
        ))
        selection_evidence.append({"device": device, "records": public_records})
    else:
        external_compatible = {
            "surface-pro-11-x1e-oled": "microsoft,denali-oled",
            "surface-pro-11-x1p-lcd": "microsoft,denali-lcd",
        }.get(device)
        if external_compatible not in compatibles:
            raise ValueError(f"external DTB {device} lacks its unambiguous compatible selector")
        selectors = {("compatible", external_compatible)}
    if not selectors or len(selectors) > MAX_SELECTORS_PER_DEVICE:
        raise ValueError("required DTB has an invalid bounded selector count")
    for kind, value in selectors:
        public_text(value, f"{kind} selector")
    inventory.append({
        "device": device,
        "basename": basename,
        "path": package_path,
        "compatible_strings": compatibles,
        "sha256": digest,
        "embedded_matches": int(matches),
        "selectors": [{"kind": kind, "value": value} for kind, value in sorted(selectors)],
        "required": required_text == "true",
    })

inventory.sort(key=lambda record: (record["device"], record["path"]))
write_public_json(inventory_output, inventory)
if delivery == "embedded":
    selection_evidence.sort(key=lambda record: record["device"])
    write_public_json(selection_output, selection_evidence)
PY_SELECTION
printf '%s' "$effective_delivery" > "$provenance_dir/effective-dtb-delivery"
printf '%s' "$dtbauto_sections" > "$provenance_dir/embedded-dtb-count"
`
)

// containerRecipe is the immutable recipe produced from compiled policy values.
var containerRecipe = strings.NewReplacer(
	"{{COMMON_HEADERS_TARGET}}", containerCommonHeadersTarget,
	"{{FLAVOUR_TARGET}}", containerFlavourTarget,
	"{{MINIMUM_FREE_GIB}}", strconv.Itoa(containerMinimumFreeGiB),
).Replace(containerRecipeTemplate)

// compiledRecipeSHA256 returns the stable identity of the embedded build policy.
func compiledRecipeSHA256() string {
	digest := sha256.Sum256([]byte(containerRecipe))
	return hex.EncodeToString(digest[:])
}
