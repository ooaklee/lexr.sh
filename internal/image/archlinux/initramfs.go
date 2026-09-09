package archlinux

// LiveInitramfsConfig selects mkinitcpio's BusyBox runtime for the signed archiso
// hook. The build host's autodetect results must never prune Surface drivers.
// MODULES stays empty: the Lexr hook copies dependencies for normal coldplug.
func LiveInitramfsConfig() string {
	return `MODULES=()
BINARIES=()
FILES=()
HOOKS=(base udev modconf keyboard block lexr_sp11 archiso filesystems)
COMPRESSION="gzip"
`
}

// InstalledInitramfsConfig omits live media discovery and remains independent of
// the image builder's hardware. Only this configuration is used by kernel updates.
func InstalledInitramfsConfig() string {
	return `MODULES=()
BINARIES=()
FILES=()
HOOKS=(base udev modconf keyboard block lexr_sp11 filesystems fsck)
COMPRESSION="gzip"

# Only the installed system may carry private same-device firmware. Include
# locally installed files before udev probes the early DSP/GPU modules; these
# DT-requested names are absent from modinfo's automatic firmware dependencies.
# This configuration never downloads firmware and is not the live USB config.
for lexr_firmware in \
    qcom/x1e80100/X1E80100-Microsoft-Surface-Pro-11-tplg.bin \
    qcom/x1e80100/microsoft/qcdxkmsuc8380.mbn \
    qcom/x1e80100/microsoft/qcdxkmsucpurwa.mbn \
    qcom/x1e80100/microsoft/Denali/qcdxkmsuc8380.mbn \
    qcom/x1e80100/microsoft/Denali/adsp_dtb.mbn \
    qcom/x1e80100/microsoft/Denali/qcadsp8380.mbn \
    qcom/x1e80100/microsoft/Denali/adspr.jsn \
    qcom/x1e80100/microsoft/Denali/adsps.jsn \
    qcom/x1e80100/microsoft/Denali/adspua.jsn \
    qcom/x1e80100/microsoft/Denali/battmgr.jsn \
    qcom/x1e80100/microsoft/Denali/cdsp_dtb.mbn \
    qcom/x1e80100/microsoft/Denali/qccdsp8380.mbn \
    qcom/x1e80100/microsoft/Denali/cdspr.jsn; do
    if [[ -f "/usr/lib/firmware/$lexr_firmware" ]]; then
        FILES+=("/usr/lib/firmware/$lexr_firmware")
    fi
done
unset lexr_firmware
`
}

// EarlySupportHook uses mkinitcpio's build-time dependency resolution to copy
// platform drivers and firmware. It contains no runtime hook or forced loading;
// firmware must be present before the initial udev probe, including builtin GPUs.
func EarlySupportHook() string {
	return `#!/usr/bin/env bash
build() {
    local module firmware
    for module in qcom_q6v5_pas qrtr qrtr_smd qcom_pd_mapper msm panel_samsung_atna33xc20 panel_edp surface_aggregator_hub ath12k; do
        add_module "$module" || return 1
    done
    # v23 has the PCI transport in ath12k; also copy a separate transport when
    # another coherent bundle supplies one. This queries metadata, not hardware.
    if modinfo -b "$_optmoduleroot" -k "$KERNELVERSION" ath12k_pci >/dev/null 2>&1; then
        add_module ath12k_pci || return 1
    fi
    for firmware in qcom/gen70500_gmu.bin qcom/gen70500_sqe.fw ath12k/WCN7850/hw2.0/board.bin ath12k/WCN7850/hw2.0/board-2.bin ath12k/WCN7850/hw2.0/amss.bin ath12k/WCN7850/hw2.0/m3.bin; do
        add_firmware "$firmware" || return 1
    done
}
help() {
    echo 'Copy Surface Pro 11 early drivers and public firmware for normal coldplug.'
}
`
}
