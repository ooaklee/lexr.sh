package fedora

import (
	"fmt"
	"regexp"
	"strings"
)

// fedoraKernelPackageName is the RPM identity registered in the Fedora live root.
const fedoraKernelPackageName = "lexr-kernel-sp11"

// rpmVersionCharacter matches characters that cannot be carried into our RPM version.
var rpmVersionCharacter = regexp.MustCompile(`[^A-Za-z0-9._+~]+`)

// rpmVersion converts an upstream kernel version into a deterministic RPM version.
func rpmVersion(version string) string {
	value := strings.Trim(rpmVersionCharacter.ReplaceAllString(version, "."), ".")
	if value == "" {
		return "1"
	}
	return value
}

// kernelRPMSpec returns the native package metadata used by Anaconda and kernel-install.
func kernelRPMSpec(abi, version string) string {
	return fmt.Sprintf(`Name:           %s
Version:        %s
Release:        1.lexr.fc44
Summary:        Surface Pro 11 Stubble kernel for Fedora Live
License:        GPL-2.0-only
BuildArch:      aarch64
AutoReqProv:    no

Provides:       kernel = %s
Provides:       kernel-uname-r
Provides:       kernel-core-uname-r
Provides:       kernel-modules-core-uname-r
Provides:       installonlypkg(kernel)
Requires:       /usr/bin/kernel-install
Requires:       /usr/bin/dracut

%%description
Exact digest-verified Surface Pro 11 kernel payload repackaged for Fedora's
RPM, kernel-install, BLS, and dracut lifecycle. The boot image preserves its
Stubble PE sections and is unsigned.

%%prep

%%build

%%install
rm -rf %%{buildroot}
cp -a /linux-work/rpm-payload/. %%{buildroot}/

%%files
%%defattr(-,root,root,-)
/boot/vmlinuz-%s
/boot/System.map-%s
/boot/config-%s
/usr/lib/modules/%s
/usr/lib/firmware/%s/device-tree
/usr/lib/lexr/sp11
/usr/lib/systemd/system/lexr-sp11-installed-finalize.service
/usr/lib/systemd/system/multi-user.target.wants/lexr-sp11-installed-finalize.service
/etc/kernel/install.conf

%%posttrans
/usr/sbin/depmod -a %s || exit 1
/usr/bin/dracut --force /boot/initramfs-%s.img %s || exit 1
KERNEL_INSTALL_LAYOUT=other /usr/bin/kernel-install add %s /usr/lib/modules/%s/vmlinuz-dtbloader.efi || exit 1

%%preun
if [ "$1" -eq 0 ]; then
    KERNEL_INSTALL_LAYOUT=other /usr/bin/kernel-install remove %s || :
fi

%%changelog
* Mon Aug 31 2026 Lexr maintainers <maintainers@lexr.sh> - %s-1.lexr.fc44
- Preserve the verified Surface kernel while integrating Fedora lifecycle.
`, fedoraKernelPackageName, rpmVersion(version), rpmVersion(version),
		abi, abi, abi, abi, abi, abi, abi, abi, abi, abi, abi, rpmVersion(version))
}

// kernelInstallConfiguration forces Stubble's split kernel/initramfs through
// Fedora's normal BLS path even though systemd detects a misleading .osrel PE section.
func kernelInstallConfiguration() string {
	return "layout=other\n"
}

// installedFinalizeScript returns the one-shot post-install boot-policy finalizer.
func installedFinalizeScript(abi string) string {
	return fmt.Sprintf(`#!/usr/bin/bash
set -eu

abi=%q
state=/var/lib/lexr/sp11-installed-finalized
[ ! -e "$state" ] || exit 0

# Anaconda may persist its live-hardware denylist. It must not keep ADSP
# disabled on NVMe, where q6v5_pas supplies audio and related services.
rm -f -- /etc/modprobe.d/anaconda-denylist.conf

/usr/sbin/depmod -a "$abi"
/usr/bin/dracut --force "/boot/initramfs-$abi.img" "$abi"
KERNEL_INSTALL_LAYOUT=other /usr/bin/kernel-install add "$abi" \
	"/usr/lib/modules/$abi/vmlinuz-dtbloader.efi"

# The live root hides stock /boot/vmlinuz-* files so Anaconda selects the
# custom ABI. Restore package-owned stock images and fallback BLS entries only
# after the installed system has successfully booted the custom kernel.
fallback_dtb=qcom/x1e80100-microsoft-denali-oled.dtb
fallback_dtb_source="/usr/lib/modules/$abi/dtb/$fallback_dtb"
[ -s "$fallback_dtb_source" ]
for module_dir in /usr/lib/modules/*; do
	[ -d "$module_dir" ] || continue
	stock_abi=${module_dir##*/}
	[ "$stock_abi" != "$abi" ] || continue
	stock_image="$module_dir/vmlinuz-dtbloader.efi"
	boot_image="/boot/vmlinuz-$stock_abi"
	[ -s "$stock_image" ] || continue
	if rpm -q --whatprovides "$boot_image" >/dev/null 2>&1; then
		install -m 0755 "$stock_image" "$boot_image"
		/usr/sbin/depmod -a "$stock_abi"
		/usr/bin/dracut --force "/boot/initramfs-$stock_abi.img" "$stock_abi"
		stock_dtb="/boot/dtb-$stock_abi/$fallback_dtb"
		install -D -m 0644 "$fallback_dtb_source" "$stock_dtb"
		GRUB_DEVICETREE="$fallback_dtb" KERNEL_INSTALL_LAYOUT=other \
			/usr/bin/kernel-install add "$stock_abi" "$stock_image"
		# 20-grub.install may copy the stock module DTB directory while adding the
		# entry. Reassert the manifest-bound Surface DTB and prove BLS names it.
		install -D -m 0644 "$fallback_dtb_source" "$stock_dtb"
		[ -f "$stock_dtb" ] && [ ! -L "$stock_dtb" ]
		cmp "$fallback_dtb_source" "$stock_dtb"
		bls_entry=
		for candidate in /boot/loader/entries/*.conf; do
			[ -f "$candidate" ] || continue
			grep -Fx "version $stock_abi" "$candidate" >/dev/null || continue
			[ -z "$bls_entry" ] || exit 1
			bls_entry=$candidate
		done
		[ -n "$bls_entry" ]
		grep -Fx "devicetree /dtb-$stock_abi/$fallback_dtb" "$bls_entry" >/dev/null
		command -v restorecon >/dev/null 2>&1 && restorecon -R "/boot/dtb-$stock_abi" "$bls_entry" || :
	fi
done

if command -v grubby >/dev/null 2>&1 && [ -s "/boot/vmlinuz-$abi" ]; then
	grubby --update-kernel="/boot/vmlinuz-$abi" \
		--remove-args="modprobe.blacklist=qcom_q6v5_pas rd.driver.blacklist=qcom_q6v5_pas" \
		--args="clk_ignore_unused pd_ignore_unused systemd.tpm2_wait=0 soundwire_qcom.sp11_feedback_active_offset2_zero=1"
	grubby --set-default="/boot/vmlinuz-$abi"
fi

install -d -m 0755 /var/lib/lexr
: > "$state"
`, abi)
}

// installedFinalizeService gates finalization to the installed, non-live system.
func installedFinalizeService() string {
	return `[Unit]
Description=Finalize Lexr Surface Pro 11 Fedora kernel after installation
ConditionKernelCommandLine=!rd.live.image
ConditionPathExists=!/var/lib/lexr/sp11-installed-finalized
After=local-fs.target
Before=multi-user.target

[Service]
Type=oneshot
ExecStart=/usr/lib/lexr/sp11/finalize-installed

[Install]
WantedBy=multi-user.target
`
}

// installedGrubDefaults contains the permanent boot arguments without USB-only policy.
func installedGrubDefaults() string {
	return `GRUB_DEFAULT=saved
GRUB_DISABLE_SUBMENU=true
GRUB_DISABLE_RECOVERY=true
GRUB_CMDLINE_LINUX_DEFAULT="quiet rhgb clk_ignore_unused pd_ignore_unused systemd.tpm2_wait=0 soundwire_qcom.sp11_feedback_active_offset2_zero=1"
GRUB_ENABLE_BLSCFG=true
GRUB_GFXMODE=auto
GRUB_TERMINAL_INPUT="console"
GRUB_TERMINAL_OUTPUT="console"
GRUB_TIMEOUT=10
`
}
