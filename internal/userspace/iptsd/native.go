package iptsd

// FedoraNativeRPMSharedSleepHookPath is the one operational path shared with
// the portable layout. Its Fedora byte identity is distinct because RPM's
// build-root policy rewrites the interpreter to /usr/bin/sh.
const FedoraNativeRPMSharedSleepHookPath = "usr/lib/systemd/system-sleep/sp11-iptsd-restart"

// NativeRPMFile describes one fixed file in the distribution-native IPTSD
// package layout. Static files carry an exact digest and length; binaries are
// rebuilt by the target distribution and are instead covered by bounded ELF
// architecture and runtime inspection.
type NativeRPMFile struct {
	// Path is the canonical target-root-relative installed path.
	Path string
	// SHA256 is the exact lowercase digest for deterministic integration bytes.
	SHA256 string
	// Size is the exact length for deterministic integration bytes.
	Size int64
	// Executable requires at least one executable permission bit.
	Executable bool
}

// fedoraNativeRPMStaticFiles is the closed operational and provenance marker
// set emitted by the Fedora lexr-sp11-iptsd RPM. The executables are excluded
// because Fedora rebuilds them from the pinned source release.
var fedoraNativeRPMStaticFiles = []NativeRPMFile{
	{Path: "usr/share/iptsd/surface-pro-11-0c80.conf", SHA256: "e629f67248df412d69952accc874b848e3e45ad3d8b31cbec4626f85c12c8c34", Size: 98},
	{Path: "usr/share/iptsd/surface-pro-11-0c83.conf", SHA256: "358953d2171b36879043dc46084cc9344ea2c28cc718ff75690acd479214bf59", Size: 98},
	{Path: "usr/lib/systemd/system/sp11-iptsd@.service", SHA256: "b5ead3d61c206ca5169056e3187b675c868771b2f6d4cd309c5a9947e517a0c0", Size: 356},
	{Path: "usr/lib/udev/rules.d/70-sp11-iptsd.rules", SHA256: "3e50a5a9a18a3950c3323c89ff336a24d4e77d7983331b3b3a6d42d2eaefc3c3", Size: 719},
	{Path: FedoraNativeRPMSharedSleepHookPath, SHA256: "8fb0dd02bcc5b212d753687879d2582db26ac2be546f1f2afcedc86e0dbd2a27", Size: 2840, Executable: true},
	{Path: "usr/share/doc/lexr-sp11-iptsd/SOURCE.env", SHA256: "1ff7395738b95a0ef4ffd780a9b6415733e7003040ae0b56b4b984c3bcd25278", Size: 336},
}

// fedoraNativeRPMBinaries are the distribution-rebuilt executable members of
// the same package contract.
var fedoraNativeRPMBinaries = []NativeRPMFile{
	{Path: "usr/libexec/sp11-iptsd", Executable: true},
	{Path: "usr/libexec/sp11-iptsd-check-device", Executable: true},
}

// FedoraNativeRPMStaticFiles returns a detached copy of the exact static file
// identities used to recognise the Fedora-native installation layout.
func FedoraNativeRPMStaticFiles() []NativeRPMFile {
	return append([]NativeRPMFile(nil), fedoraNativeRPMStaticFiles...)
}

// FedoraNativeRPMBinaries returns a detached copy of the Fedora-native binary
// paths whose architecture and runtime closure must be inspected separately.
func FedoraNativeRPMBinaries() []NativeRPMFile {
	return append([]NativeRPMFile(nil), fedoraNativeRPMBinaries...)
}
