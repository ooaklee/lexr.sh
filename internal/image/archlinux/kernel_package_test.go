package archlinux

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// validKernelPackageIdentity is the canonical identity used as the mutation base.
var validKernelPackageIdentity = KernelPackageIdentity{
	ABI:     "7.2.0-jg-0sp11v23-qcom-x1e",
	Version: "1:7.2.0-1sp11v23",
}

// TestKernelPackageIdentityRejectsUnsafeValues checks the Go-side charset and
// shape checks block argument injection, shell metacharacters, and path-like
// identities before any string is rendered.
func TestKernelPackageIdentityRejectsUnsafeValues(t *testing.T) {
	for _, mutate := range []func(*KernelPackageIdentity){
		func(identity *KernelPackageIdentity) { identity.ABI = "" },
		func(identity *KernelPackageIdentity) { identity.ABI = "../../kernel" },
		func(identity *KernelPackageIdentity) { identity.ABI = "/boot/vmlinuz" },
		func(identity *KernelPackageIdentity) { identity.ABI = "kernel;reboot" },
		func(identity *KernelPackageIdentity) { identity.ABI = "kernel\nreboot" },
		func(identity *KernelPackageIdentity) { identity.ABI = "kernel $(reboot)" },
		func(identity *KernelPackageIdentity) { identity.ABI = "kernel`reboot`" },
		func(identity *KernelPackageIdentity) { identity.ABI = "Kernel-ABI" },
		func(identity *KernelPackageIdentity) { identity.ABI = strings.Repeat("a", 128) },
		func(identity *KernelPackageIdentity) { identity.Version = "" },
		func(identity *KernelPackageIdentity) { identity.Version = "1;reboot" },
		func(identity *KernelPackageIdentity) { identity.Version = "1 $(reboot)" },
		func(identity *KernelPackageIdentity) { identity.Version = "1\nreboot" },
		func(identity *KernelPackageIdentity) { identity.Version = "../escape" },
		func(identity *KernelPackageIdentity) { identity.Version = "/absolute" },
		func(identity *KernelPackageIdentity) { identity.Version = "version with spaces" },
		func(identity *KernelPackageIdentity) { identity.Version = strings.Repeat("1", 129) },
	} {
		identity := validKernelPackageIdentity
		mutate(&identity)
		if err := identity.Validate(); err == nil {
			t.Errorf("accepted unsafe kernel package identity: %+v", identity)
		}
		if _, err := identity.PackagePkgver(); err == nil {
			t.Errorf("pkgver derived from unsafe identity: %+v", identity)
		}
		if _, err := identity.PackageVersion(); err == nil {
			t.Errorf("version derived from unsafe identity: %+v", identity)
		}
	}
	if err := validKernelPackageIdentity.Validate(); err != nil {
		t.Fatalf("rejected the canonical identity: %v", err)
	}
}

// TestKernelPackageVersionSemantics checks pacman version semantics from
// PKGBUILD(5) and .PKGINFO: the pkgver portion never contains a hyphen
// (hyphens and Debian epochs collapse to underscores), pkgrel is always 1,
// and the combined version is pkgver-pkgrel with exactly one hyphen as the
// delimiter pacman splits full versions on.
func TestKernelPackageVersionSemantics(t *testing.T) {
	cases := []struct {
		version string
		pkgver  string
		full    string
	}{
		{"1:7.2.0-1sp11v23", "1_7.2.0_1sp11v23", "1_7.2.0_1sp11v23-1"},
		{"7.2.0-1", "7.2.0_1", "7.2.0_1-1"},
		{"1:7.2.0~rc2+lexr-1", "1_7.2.0_rc2+lexr_1", "1_7.2.0_rc2+lexr_1-1"},
		{"5.13", "5.13", "5.13-1"},
		{"1:::::", "1", "1-1"},
	}
	for _, item := range cases {
		identity := KernelPackageIdentity{ABI: validKernelPackageIdentity.ABI, Version: item.version}
		pkgver, err := identity.PackagePkgver()
		if err != nil || pkgver != item.pkgver {
			t.Errorf("version %q produced pkgver %q (%v); want %q", item.version, pkgver, err, item.pkgver)
		}
		full, err := identity.PackageVersion()
		if err != nil || full != item.full {
			t.Errorf("version %q produced full version %q (%v); want %q", item.version, full, err, item.full)
		}
		if strings.Contains(pkgver, "-") {
			t.Errorf("pkgver portion %q contains a forbidden hyphen", pkgver)
		}
		parts := strings.Split(full, "-")
		if len(parts) != 2 || parts[1] != kernelPackagePkgrel {
			t.Errorf("full version %q is not exactly pkgver-pkgrel with pkgrel %q", full, kernelPackagePkgrel)
		}
	}
}

// TestKernelPackageStagedPathsConfined checks every staged path is relative,
// clean, ABI-confined, and free of traversal segments, and that an unsafe ABI
// is refused outright.
func TestKernelPackageStagedPathsConfined(t *testing.T) {
	abi := validKernelPackageIdentity.ABI
	staged, err := kernelPackageStagedPaths(abi)
	if err != nil {
		t.Fatal(err)
	}
	if len(staged) != 5 {
		t.Fatalf("expected five staged paths, got %d", len(staged))
	}
	allowed := []string{"boot/", "usr/lib/modules/" + abi, "usr/lib/firmware/" + abi}
	for _, stagedPath := range staged {
		if stagedPath == "" || filepath.Clean(stagedPath) != stagedPath || strings.HasPrefix(stagedPath, "/") {
			t.Errorf("staged path is empty or not clean and relative: %q", stagedPath)
		}
		if !strings.Contains(stagedPath, abi) {
			t.Errorf("staged path is not ABI-bound: %q", stagedPath)
		}
		confined := false
		for _, prefix := range allowed {
			if strings.HasPrefix(stagedPath, prefix) {
				confined = true
			}
		}
		if !confined {
			t.Errorf("staged path escapes the package-owned prefixes: %q", stagedPath)
		}
		for _, segment := range strings.Split(stagedPath, "/") {
			if segment == ".." || segment == "." {
				t.Errorf("staged path contains a traversal segment: %q", stagedPath)
			}
		}
	}
	for _, unsafe := range []string{"", "../../kernel", "abi;reboot", "kernel $(x)", strings.Repeat("a", 128)} {
		if _, err := kernelPackageStagedPaths(unsafe); err == nil {
			t.Errorf("staged paths accepted unsafe ABI %q", unsafe)
		}
	}
}

// fakeChrootScript is a test double for chroot(2): it rewrites the absolute
// command path under the fake root, exports the root so the fake pacman can
// find its control files, and executes the target. This lets the exact
// production script text (which always invokes chroot) run unmodified on any
// host, including ones without a real chroot(2) for unprivileged users.
const fakeChrootScript = `#!/bin/sh
root=$1
shift
# Capture the transaction conf while the packager is still running: the
# script deletes it on exit, so it cannot be read afterwards.
if [ -f "$root/tmp/lexr-pacman-local.conf" ] && [ ! -e "$root/captured-lexr-pacman-local.conf" ]; then
    cp "$root/tmp/lexr-pacman-local.conf" "$root/captured-lexr-pacman-local.conf"
fi
cmd=$1
shift
LEXR_FAKE_ROOT=$root exec "$root$cmd" "$@"
`

// fakePacmanScript is a test double for ROOT/usr/bin/pacman. It records
// every invocation (including the --config argument) into ROOT/pacman.log,
// answers -Qi/-Q/-Ql from control files in the root, and extracts
// the -U package archive into the root with bsdtar so the installed layout,
// .PKGINFO, and .MTREE digests can be asserted from the real artefact.
const fakePacmanScript = `#!/usr/bin/env bash
set -u
root=${LEXR_FAKE_ROOT:?LEXR_FAKE_ROOT missing}
log="$root/pacman.log"
printf '%s\n' "$*" >>"$log"
case " $* " in
  *" -Qi linux-aarch64 "*)
      [ -e "$root/has-linux-aarch64" ] && exit 0
      exit 1
      ;;
  *" -Rdd "*)
      exit 0
      ;;
  *" -Q lexr-kernel-sp11 "*)
      [ -e "$root/has-lexr" ] || exit 1
      printf 'lexr-kernel-sp11 1.0-1\n'
      exit 0
      ;;
  *" -Ql lexr-kernel-sp11 "*)
      printf 'lexr-kernel-sp11 /usr/lib/modules/%s/\n' "$(<"$root/lexr-abi")"
      exit 0
      ;;
  *" -U "*)
      pkg="$root/work/lexr-kernel-sp11.pkg.tar.gz"
      [ -f "$pkg" ] || { printf 'missing retained package\n' >&2; exit 1; }
      bsdtar -xpf "$pkg" --numeric-owner -C "$root" || exit 1
      mkdir -p "$root/var/lib/pacman/local/lexr-kernel-sp11-1.0-1"
      exit 0
      ;;
esac
exit 1
`

// kernelPackageFixture assembles a disposable ROOT (with the fake pacman
// binary and a linux-aarch64 marker) and a digest-style payload tree, then
// runs the unmodified production script through a PATH-injected fake chroot.
// It returns the root, payload, and the command output. No host pacman, host
// system paths, or host package databases are touched.
func kernelPackageFixture(t *testing.T, rootHasLinuxAarch64 bool) (root, payload string, output string, err error) {
	t.Helper()

	base := t.TempDir()
	root = filepath.Join(base, "root")
	payload = filepath.Join(base, "payload")
	abi := validKernelPackageIdentity.ABI

	for _, dir := range []string{
		filepath.Join(payload, "boot"),
		filepath.Join(payload, "usr", "lib", "modules", abi, "kernel", "drivers"),
		filepath.Join(payload, "usr", "lib", "firmware", abi),
		// An unrelated ABI that must not leak into the package.
		filepath.Join(payload, "usr", "lib", "modules", "6.1.0-other"),
		filepath.Join(root, "usr", "bin"),
		filepath.Join(root, "work"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	files := map[string]string{
		filepath.Join(payload, "boot", "vmlinuz-"+abi):                                      "kernel-image-bytes-" + abi,
		filepath.Join(payload, "boot", "System.map-"+abi):                                   "system-map-bytes",
		filepath.Join(payload, "boot", "config-"+abi):                                       "config-bytes",
		filepath.Join(payload, "usr", "lib", "modules", abi, "modules.dep"):                 "modules-dep-bytes",
		filepath.Join(payload, "usr", "lib", "modules", abi, "kernel", "drivers", "x1e.ko"): "module-object-bytes",
		filepath.Join(payload, "usr", "lib", "firmware", abi, "qcom-fw.bin"):                "firmware-bytes",
	}
	for name, content := range files {
		if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A symlink inside the modules tree must be preserved, not dereferenced.
	if err := os.Symlink("../build", filepath.Join(payload, "usr", "lib", "modules", abi, "build")); err != nil {
		t.Fatal(err)
	}

	// The fake chroot is injected through PATH so the production script's
	// literal `chroot "$root" /usr/bin/pacman ...` calls are exercised.
	fakeBin := filepath.Join(base, "fakebin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fakeBin, "chroot"), []byte(fakeChrootScript), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "usr", "bin", "pacman"), []byte(fakePacmanScript), 0o755); err != nil {
		t.Fatal(err)
	}
	if rootHasLinuxAarch64 {
		if err := os.WriteFile(filepath.Join(root, "has-linux-aarch64"), []byte("1"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cmd := exec.Command("bash", "-ceu", KernelPackageBuildScript(), "lexr-arch-kernel",
		root, payload, abi, validKernelPackageIdentity.Version)
	cmd.Env = append(os.Environ(), "PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	cmd.Dir = base
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	return root, payload, stdout.String() + stderr.String(), runErr
}

// parsePKGINFO parses real .PKGINFO content: one `key = value` pair per
// line, '#' comments, blank lines skipped. This mirrors libalpm's reader
// semantics closely enough to assert exact field presence and values.
func parsePKGINFO(t *testing.T, content string) map[string]string {
	t.Helper()
	fields := make(map[string]string)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "= ", 2)
		if len(parts) != 2 {
			t.Fatalf("malformed .PKGINFO line: %q", line)
		}
		fields[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
	}
	return fields
}

// extractArchiveMember reads one member out of the package archive using
// bsdtar, without extracting anything to the host system.
func extractArchiveMember(t *testing.T, archive, member string) string {
	t.Helper()
	out, err := exec.Command("bsdtar", "-xOf", archive, member).Output()
	if err != nil {
		t.Fatalf("bsdtar -xOf %s %s: %v", archive, member, err)
	}
	return string(out)
}

// checkConfFile asserts the transaction-scoped pacman conf contents: the
// signature levels are relaxed only in this confined file, no repositories
// are configured, and the persistent config is never referenced.
func checkConfFile(t *testing.T, confPath string) {
	t.Helper()
	raw, err := os.ReadFile(confPath)
	if err != nil {
		t.Fatalf("transaction conf missing: %v", err)
	}
	content := string(raw)
	if !strings.Contains(content, "LocalFileSigLevel = Never") {
		t.Errorf("conf does not relax LocalFileSigLevel for the unsigned local package:\n%s", content)
	}
	if !strings.Contains(content, "SigLevel = Never") {
		t.Errorf("conf does not set SigLevel = Never:\n%s", content)
	}
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") && trimmed != "[options]" {
			t.Errorf("conf configures a repository section %q; it must be confined to [options]", trimmed)
		}
		if strings.Contains(trimmed, "Required") {
			t.Errorf("conf line retains signature enforcement %q in the single-transaction config", trimmed)
		}
		if strings.HasPrefix(trimmed, "Include") {
			t.Errorf("conf includes external configuration: %q", trimmed)
		}
	}
}

// checkMTreeDigests parses the archive's mtree metadata and verifies the
// sha256 digest of every regular file it records matches the installed file
// under the root — proving payload bytes survived packaging unmodified.
func checkMTreeDigests(t *testing.T, root, archive string) {
	t.Helper()
	mtree := extractArchiveMember(t, archive, ".MTREE")
	entries := 0
	for _, line := range strings.Split(mtree, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// mtree special lines (#mtree, ./set ...) carry no per-file digest.
		if !strings.HasPrefix(line, "./") {
			continue
		}
		fields := strings.Fields(line)
		name := strings.TrimPrefix(fields[0], "./")
		var digest string
		for _, field := range fields[1:] {
			if strings.HasPrefix(field, "sha256digest=") {
				digest = strings.TrimPrefix(field, "sha256digest=")
			}
		}
		if digest == "" {
			continue // directories, symlinks, and set lines carry no digest
		}
		installed := filepath.Join(root, name)
		data, err := os.ReadFile(installed)
		if err != nil {
			t.Fatalf("mtree file %q not installed under root: %v", name, err)
		}
		sum := sha256.Sum256(data)
		actual := hex.EncodeToString(sum[:])
		if actual != digest {
			t.Errorf("digest mismatch for %q: mtree %s installed %s", name, digest, actual)
		}
		entries++
	}
	if entries == 0 {
		t.Fatal("mtree carried no sha256 entries to verify")
	}
}

// checkPacmanLog asserts every recorded pacman invocation used the confined
// config inside the chroot and inspects the transaction ordering.
func checkPacmanLog(t *testing.T, root string, expectPreRemoval bool) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "pacman.log"))
	if err != nil {
		t.Fatalf("pacman invocation log missing: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) == 0 {
		t.Fatal("no pacman invocations recorded")
	}
	var sawInstall, sawPreRemoval bool
	for _, line := range lines {
		if !strings.Contains(line, "--config /tmp/lexr-pacman-local.conf") {
			t.Errorf("pacman invocation bypassed the confined config: %q", line)
		}
		switch {
		case strings.Contains(line, "-Rdd") && strings.Contains(line, "linux-aarch64"):
			sawPreRemoval = true
		case strings.Contains(line, "-U /work/lexr-kernel-sp11.pkg.tar.gz"):
			sawInstall = true
		}
	}
	if !sawInstall {
		t.Errorf("no -U transaction against the retained package found in log:\n%s", raw)
	}
	if expectPreRemoval && !sawPreRemoval {
		t.Errorf("linux-aarch64 was present but never pre-removed with -Rdd:\n%s", raw)
	}
	if !expectPreRemoval && sawPreRemoval {
		t.Errorf("linux-aarch64 was absent yet -Rdd was invoked:\n%s", raw)
	}
	if expectPreRemoval {
		// Pre-removal must precede installation.
		removalIdx, installIdx := -1, -1
		for i, line := range lines {
			if strings.Contains(line, "-Rdd") {
				removalIdx = i
			}
			if strings.Contains(line, "-U ") {
				installIdx = i
			}
		}
		if removalIdx >= 0 && installIdx >= 0 && removalIdx > installIdx {
			t.Errorf("pre-removal ran after the -U install:\n%s", raw)
		}
	}
}

// TestKernelPackageScriptBuildsValidPackage runs the exact production script
// against a disposable root where linux-aarch64 is installed, and asserts
// the full result: retention path, transaction conf, chrooted pacman
// invocations, .PKGINFO contents, archive layout, installed file layout,
// symlink preservation, and .MTREE digest verification.
func TestKernelPackageScriptBuildsValidPackage(t *testing.T) {
	if _, err := exec.LookPath("bsdtar"); err != nil {
		t.Skipf("bsdtar unavailable: %v", err)
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skipf("bash unavailable: %v", err)
	}

	root, payload, output, err := kernelPackageFixture(t, true)
	if err != nil {
		t.Fatalf("packager script failed: %v\noutput:\n%s", err, output)
	}
	abi := validKernelPackageIdentity.ABI

	retained := filepath.Join(root, "work", "lexr-kernel-sp11.pkg.tar.gz")
	if info, statErr := os.Stat(retained); statErr != nil || info.Size() == 0 {
		t.Fatalf("retained package missing or empty at %s (err=%v)", retained, statErr)
	}

	checkConfFile(t, filepath.Join(root, "captured-lexr-pacman-local.conf"))
	checkPacmanLog(t, root, true)

	// Golden .PKGINFO semantics from the real artefact: the pkgver field
	// carries the combined pkgver-pkgrel per .PKGINFO format, split on the
	// final hyphen, with no hyphen inside the pkgver portion itself.
	info := parsePKGINFO(t, extractArchiveMember(t, retained, ".PKGINFO"))
	want := map[string]string{
		"pkgname":  kernelPackageName,
		"pkgver":   "1_7.2.0_1sp11v23-1",
		"arch":     "aarch64",
		"provides": "linux-aarch64",
		"conflict": "linux-aarch64",
	}
	for field, value := range want {
		if info[field] != value {
			t.Errorf(".PKGINFO %s = %q; want %q", field, info[field], value)
		}
	}
	for _, field := range []string{"pkgdesc", "url", "license", "builddate", "packager"} {
		if info[field] == "" {
			t.Errorf(".PKGINFO is missing required field %s", field)
		}
	}
	idx := strings.LastIndex(info["pkgver"], "-")
	if idx < 0 || strings.Contains(info["pkgver"][:idx], "-") || info["pkgver"][idx+1:] != "1" {
		t.Errorf(".PKGINFO pkgver %q is not hyphen-free pkgver plus pkgrel 1", info["pkgver"])
	}

	// Archive layout: only metadata files and ABI-confined payload trees.
	listing, listErr := exec.Command("bsdtar", "-tf", retained).Output()
	if listErr != nil {
		t.Fatalf("bsdtar -tf: %v", listErr)
	}
	for _, entry := range strings.Fields(string(listing)) {
		trimmed := strings.TrimSuffix(strings.TrimPrefix(entry, "./"), "/")
		// bsdtar records the parent directories of the staged trees; only
		// the exact chain down to the package-owned prefixes is allowed.
		allowed := trimmed == ".PKGINFO" || trimmed == ".INSTALL" || trimmed == ".MTREE" ||
			trimmed == "boot" || trimmed == "usr" || trimmed == "usr/lib" ||
			trimmed == "usr/lib/modules" || trimmed == "usr/lib/firmware" ||
			trimmed == "usr/lib/modules/"+abi || trimmed == "usr/lib/firmware/"+abi ||
			strings.HasPrefix(trimmed, "boot/") ||
			strings.HasPrefix(trimmed, "usr/lib/modules/"+abi+"/") ||
			strings.HasPrefix(trimmed, "usr/lib/firmware/"+abi+"/")
		if !allowed {
			t.Errorf("archive contains out-of-scope entry %q", entry)
		}
		if strings.Contains(entry, "6.1.0-other") {
			t.Errorf("archive leaked an unrelated ABI: %q", entry)
		}
	}

	// Installed layout under the root, exactly the staged set.
	for _, required := range []string{
		filepath.Join(root, "boot", "vmlinuz-"+abi),
		filepath.Join(root, "boot", "System.map-"+abi),
		filepath.Join(root, "boot", "config-"+abi),
		filepath.Join(root, "usr", "lib", "modules", abi, "modules.dep"),
		filepath.Join(root, "usr", "lib", "modules", abi, "kernel", "drivers", "x1e.ko"),
		filepath.Join(root, "usr", "lib", "firmware", abi, "qcom-fw.bin"),
	} {
		if _, statErr := os.Stat(required); statErr != nil {
			t.Errorf("required installed file missing: %s (%v)", required, statErr)
		}
	}
	link, linkErr := os.Lstat(filepath.Join(root, "usr", "lib", "modules", abi, "build"))
	if linkErr != nil || link.Mode()&os.ModeSymlink == 0 {
		t.Errorf("modules build entry was not preserved as a symlink (err=%v)", linkErr)
	}
	if _, statErr := os.Stat(filepath.Join(root, "usr", "lib", "modules", abi, "build")); statErr == nil {
		t.Errorf("symlink was dereferenced during packaging, creating %s", filepath.Join(root, "build"))
	}

	// Byte-exact payload preservation proven through the mtree digests.
	checkMTreeDigests(t, root, retained)

	// .INSTALL scriptlet contract: depmod and the installed-system initcpio
	// configuration, never the archiso one.
	install := extractArchiveMember(t, retained, ".INSTALL")
	for _, required := range []string{
		"/usr/bin/depmod -a",
		"/usr/bin/mkinitcpio -c /etc/lexr/mkinitcpio-installed.conf",
		"post_install()", "post_upgrade()",
	} {
		if !strings.Contains(install, required) {
			t.Errorf(".INSTALL missing %q:\n%s", required, install)
		}
	}
	if strings.Contains(install, "archiso") {
		t.Errorf(".INSTALL references the live-media archiso configuration:\n%s", install)
	}

	_ = payload
}

// TestKernelPackageScriptWithoutLinuxAarch64 checks that when linux-aarch64
// is absent from the root, no -Rdd pre-removal is attempted.
func TestKernelPackageScriptWithoutLinuxAarch64(t *testing.T) {
	if _, err := exec.LookPath("bsdtar"); err != nil {
		t.Skipf("bsdtar unavailable: %v", err)
	}
	root, _, output, err := kernelPackageFixture(t, false)
	if err != nil {
		t.Fatalf("packager script failed: %v\noutput:\n%s", err, output)
	}
	checkPacmanLog(t, root, false)
}

// TestKernelPackageScriptRefusesAbiSwap checks that an installed
// lexr-kernel-sp11 carrying a different ABI aborts the transaction before
// any -U install happens.
func TestKernelPackageScriptRefusesAbiSwap(t *testing.T) {
	if _, err := exec.LookPath("bsdtar"); err != nil {
		t.Skipf("bsdtar unavailable: %v", err)
	}
	root, payload, output, err := kernelPackageFixture(t, true)
	if err != nil {
		t.Fatalf("fixture failed: %v\noutput:\n%s", err, output)
	}
	if writeErr := os.WriteFile(filepath.Join(root, "has-lexr"), []byte("1"), 0o644); writeErr != nil {
		t.Fatal(writeErr)
	}
	if writeErr := os.WriteFile(filepath.Join(root, "lexr-abi"), []byte("6.1.0-old"), 0o644); writeErr != nil {
		t.Fatal(writeErr)
	}
	// Re-run the script against the same root; the recorded -Q/-Ql answers
	// now claim an installed package with a different ABI.
	cmd := exec.Command("bash", "-ceu", KernelPackageBuildScript(), "lexr-arch-kernel",
		root, payload, validKernelPackageIdentity.ABI, validKernelPackageIdentity.Version)
	cmd.Env = append(os.Environ(),
		"PATH="+filepath.Join(filepath.Dir(root), "fakebin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	cmd.Dir = filepath.Dir(root)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err == nil {
		t.Fatal("script accepted an ABI swap")
	}
	if !strings.Contains(stderr.String(), "refusing to replace") {
		t.Errorf("refusal message missing: %s", stderr.String())
	}
	raw, readErr := os.ReadFile(filepath.Join(root, "pacman.log"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	// The second run's refusal happens after its -Ql probe; nothing from
	// that point on may be an install transaction.
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	lastQl := -1
	for i, line := range lines {
		if strings.Contains(line, "-Ql lexr-kernel-sp11") {
			lastQl = i
		}
	}
	if lastQl < 0 {
		t.Fatalf("second run never queried the installed ABI:\n%s", raw)
	}
	for _, line := range lines[lastQl:] {
		if strings.Contains(line, "-U ") {
			t.Errorf("install transaction ran despite ABI refusal:\n%s", raw)
		}
	}
	if len(lines) != lastQl+1 {
		t.Errorf("unexpected pacman activity after the ABI probe:\n%s", raw)
	}
}

// TestKernelPackageScriptTextContract checks static properties of the script
// text: argv-only live values, no live-value interpolation, chroot-confined
// pacman, and no out-of-scope operations.
func TestKernelPackageScriptTextContract(t *testing.T) {
	script := KernelPackageBuildScript()
	if again := KernelPackageBuildScript(); again != script {
		t.Fatal("kernel package build script is not byte-identical across calls")
	}
	for _, argument := range []string{
		validKernelPackageIdentity.ABI,
		validKernelPackageIdentity.Version,
		"sp11v23",
		"/linux-work/rootfs",
		"/linux-work/kernel-payload",
	} {
		if strings.Contains(script, argument) {
			t.Errorf("script interpolates a live value %q", argument)
		}
	}
	for _, parameter := range []string{"$1", "$2", "$3", "$4"} {
		if !strings.Contains(script, parameter) {
			t.Errorf("script does not bind argv parameter %s", parameter)
		}
	}
	// Every pacman call must be chroot-confined to the root's real binary.
	if !strings.Contains(script, `chroot "$root" /usr/bin/pacman`) {
		t.Error("script does not invoke pacman exclusively via chroot into ROOT")
	}
	for _, required := range []string{
		"^[a-z0-9][a-z0-9.+-]{0,126}$",
		"^[0-9A-Za-z][0-9A-Za-z.+~:_-]{0,127}$",
		"LocalFileSigLevel = Never",
		"lexr-pacman-local.conf",
		"/work/lexr-kernel-sp11.pkg.tar.gz",
		"-Rdd --noconfirm linux-aarch64",
		"depmod -a",
		"mkinitcpio -c /etc/lexr/mkinitcpio-installed.conf",
		"refusing to replace it with",
		".PKGINFO", ".INSTALL", ".MTREE",
	} {
		if !strings.Contains(script, required) {
			t.Errorf("script is missing required safeguard or step %q", required)
		}
	}
	for _, forbidden := range []string{
		"grub-install", "grub2-install", "parted", "sgdisk", "fdisk", "nvram", "efibootmgr",
		"/etc/mkinitcpio-archiso.conf", "archiso",
		"mkfs.", "mount ", "umount",
	} {
		if strings.Contains(script, forbidden) {
			t.Errorf("script performs out-of-scope operation %q", forbidden)
		}
	}
	// The hookdir comment must not claim vendor-hook suppression.
	if strings.Contains(script, "empty --hookdir") && strings.Contains(script, "guarantees") {
		t.Error("script still claims an empty --hookdir guarantees vendor-hook suppression")
	}
	if regexp.MustCompile(`(?m)^pacman "`).MatchString(script) {
		t.Error("script invokes pacman directly on the build host instead of through the chroot")
	}
}

// TestKernelPackageScriptSyntax validates the embedded script with bash -n
// without executing it.
func TestKernelPackageScriptSyntax(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skipf("bash unavailable: %v", err)
	}
	cmd := exec.Command(bash, "-n")
	cmd.Stdin = strings.NewReader(KernelPackageBuildScript())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bash -n rejected the packager script: %v\n%s", err, out)
	}
}
