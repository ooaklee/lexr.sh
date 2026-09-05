package ubuntu

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	imagecontract "github.com/ooaklee/lexr.sh/internal/image"
	"github.com/ooaklee/lexr.sh/internal/image/companion"
	"github.com/ooaklee/lexr.sh/internal/platform"
)

// companionISOValidationRunner simulates only the xorriso listing and
// extraction calls made by the focused companion validation boundary.
type companionISOValidationRunner struct {
	listing    []byte
	captureErr error
	runErr     error
	commands   []platform.Command
}

// Run records a simulated companion extraction and returns its configured
// result without changing the pre-staged validation fixture.
func (runner *companionISOValidationRunner) Run(_ context.Context, command platform.Command) error {
	runner.commands = append(runner.commands, command)
	if runner.runErr == nil && runtime.GOOS == "darwin" {
		workspace := ""
		probe := ""
		for _, argument := range command.Args {
			if strings.HasSuffix(argument, ":/work") {
				workspace = strings.TrimSuffix(argument, ":/work")
			}
			if strings.HasPrefix(argument, ".lexr-workspace-owner-") {
				probe = argument
			}
		}
		if workspace != "" && probe != "" {
			return os.WriteFile(filepath.Join(workspace, probe), []byte("lexr-workspace-owner"), 0o600)
		}
	}
	return runner.runErr
}

// Capture records a simulated /sp11 directory listing and returns the
// configured xorriso output.
func (runner *companionISOValidationRunner) Capture(_ context.Context, command platform.Command) ([]byte, error) {
	runner.commands = append(runner.commands, command)
	return runner.listing, runner.captureErr
}

// TestValidateCompanionBundleAcceptsExplicitAbsence verifies an omitted
// companion passes only when the reserved media directory is genuinely absent.
func TestValidateCompanionBundleAcceptsExplicitAbsence(t *testing.T) {
	runner := &companionISOValidationRunner{listing: []byte("'README.txt'\n'dtb'\n'kernel'\n")}
	validator := NewValidator(platform.NewDocker(runner))
	record := companion.Absent(companion.OmissionReasonNotRequested)

	checks := validator.validateCompanionBundle(
		context.Background(), "tools:test", t.TempDir(), record, companion.ValidateRecord(record),
	)

	assertValidationCheck(t, checks, "companion-bundle-record", true)
	assertValidationCheck(t, checks, "companion-bundle-presence", true)
	if len(runner.commands) != 1 {
		t.Fatalf("runner command count = %d, want listing only", len(runner.commands))
	}
	if !containsArgumentSequence(runner.commands[0].Args, "-ls", "/sp11") {
		t.Fatalf("listing command arguments = %q", runner.commands[0].Args)
	}
}

// TestValidateCompanionBundleRejectsStrayDirectory verifies a manifest cannot
// mark the payload absent while leaving an untracked companion tree on media.
func TestValidateCompanionBundleRejectsStrayDirectory(t *testing.T) {
	runner := &companionISOValidationRunner{listing: []byte("'README.txt'\n'companion'\n")}
	validator := NewValidator(platform.NewDocker(runner))
	record := companion.Absent(companion.OmissionReasonNotRequested)

	checks := validator.validateCompanionBundle(
		context.Background(), "tools:test", t.TempDir(), record, companion.ValidateRecord(record),
	)

	check := assertValidationCheck(t, checks, "companion-bundle-presence", false)
	if !strings.Contains(check.Details, companion.ISOFilesystemRoot) {
		t.Fatalf("presence details = %q, want reserved path", check.Details)
	}
}

// TestValidateCompanionBundleVerifiesIncludedContents proves an included
// record triggers extraction followed by closed-set digest and format checks.
func TestValidateCompanionBundleVerifiesIncludedContents(t *testing.T) {
	workspace := t.TempDir()
	record := writeCompanionValidationFixture(t, filepath.Join(workspace, "companion"))
	runner := &companionISOValidationRunner{listing: []byte("'README.txt'\n'companion'\n")}
	validator := NewValidator(platform.NewDocker(runner))

	checks := validator.validateCompanionBundle(
		context.Background(), "tools:test", workspace, record, companion.ValidateRecord(record),
	)

	assertValidationCheck(t, checks, "companion-bundle-record", true)
	assertValidationCheck(t, checks, "companion-bundle-presence", true)
	assertValidationCheck(t, checks, "companion-bundle-contents", true)
	if len(runner.commands) != 2 ||
		!containsArgumentSequence(runner.commands[1].Args, "-extract", "/"+companion.ISOFilesystemRoot, "/work/companion") {
		t.Fatalf("extraction commands = %#v", runner.commands)
	}
}

// TestValidateCompanionBundleRejectsMutatedContents verifies the finished ISO
// check rehashes extracted bytes instead of trusting manifest metadata alone.
func TestValidateCompanionBundleRejectsMutatedContents(t *testing.T) {
	workspace := t.TempDir()
	companionRoot := filepath.Join(workspace, "companion")
	record := writeCompanionValidationFixture(t, companionRoot)
	sourcePath := filepath.Join(companionRoot, "source", "lexr_v1.2.3_source.tar.gz")
	if err := os.WriteFile(sourcePath, []byte("mutated source"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := &companionISOValidationRunner{listing: []byte("'companion'\n")}
	validator := NewValidator(platform.NewDocker(runner))

	checks := validator.validateCompanionBundle(
		context.Background(), "tools:test", workspace, record, companion.ValidateRecord(record),
	)

	check := assertValidationCheck(t, checks, "companion-bundle-contents", false)
	if !strings.Contains(check.Details, "source/lexr_v1.2.3_source.tar.gz") {
		t.Fatalf("contents details = %q, want mutated path", check.Details)
	}
}

// TestValidateCompanionBundleRejectsListingFailure verifies an xorriso failure
// cannot be misreported as proof that an omitted directory is absent.
func TestValidateCompanionBundleRejectsListingFailure(t *testing.T) {
	runner := &companionISOValidationRunner{captureErr: errors.New("cannot inspect image")}
	validator := NewValidator(platform.NewDocker(runner))
	record := companion.Absent(companion.OmissionReasonNotRequested)

	checks := validator.validateCompanionBundle(
		context.Background(), "tools:test", t.TempDir(), record, companion.ValidateRecord(record),
	)

	check := assertValidationCheck(t, checks, "companion-bundle-presence", false)
	if !strings.Contains(check.Details, "cannot inspect image") {
		t.Fatalf("presence details = %q, want xorriso failure", check.Details)
	}
}

// TestISODirectoryListingContainsRequiresExactName verifies similarly prefixed
// ISO members cannot masquerade as the reserved companion entry.
func TestISODirectoryListingContainsRequiresExactName(t *testing.T) {
	listing := []byte("'companion-old'\n'not-companion'\ncompanion-data\n")
	if isoDirectoryListingContains(listing, "companion") {
		t.Fatal("isoDirectoryListingContains() accepted a prefixed child name")
	}
	if !isoDirectoryListingContains([]byte("'companion'\n"), "companion") {
		t.Fatal("isoDirectoryListingContains() rejected xorriso's exact quoted child name")
	}
}

// TestValidateInstalledGRUBGeneratorSyntaxRejectsMalformedShell proves a
// trusted-shell parse failure reaches the standalone image validation gate.
func TestValidateInstalledGRUBGeneratorSyntaxRejectsMalformedShell(t *testing.T) {
	runner := &companionISOValidationRunner{runErr: errors.New("malformed shell")}
	err := validateInstalledGRUBGeneratorSyntax(
		context.Background(), platform.NewDocker(runner), "tools:test", t.TempDir(),
	)
	if err == nil || !strings.Contains(err.Error(), "malformed shell") {
		t.Fatalf("syntax validation error = %v", err)
	}
	if len(runner.commands) != 1 || !containsArgumentSequence(
		runner.commands[0].Args, "sh", "-n", "/work/installed-root/etc/grub.d/10_linux",
	) {
		t.Fatalf("syntax validation commands = %#v", runner.commands)
	}
}

// TestValidateExtractedRegularFilesAcceptsRegularMembers verifies the
// validator accepts only canonical regular files beneath ordinary directories.
func TestValidateExtractedRegularFilesAcceptsRegularMembers(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "private", "state")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("state"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateExtractedRegularFiles(root, []string{"private/state"}); err != nil {
		t.Fatal(err)
	}
}

// TestValidateExtractedRegularFilesRejectsUnsafeMembers verifies
// symbolic-link traversal, non-regular leaves, and non-canonical paths fail.
func TestValidateExtractedRegularFilesRejectsUnsafeMembers(t *testing.T) {
	for _, test := range []struct {
		name     string
		prepare  func(*testing.T, string)
		relative string
		want     string
	}{
		{
			name: "symlink component",
			prepare: func(t *testing.T, root string) {
				t.Helper()
				outside := t.TempDir()
				if err := os.WriteFile(filepath.Join(outside, "state"), []byte("state"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(root, "private")); err != nil {
					t.Fatal(err)
				}
			},
			relative: "private/state",
			want:     "traverses a symbolic link",
		},
		{
			name: "symlink leaf",
			prepare: func(t *testing.T, root string) {
				t.Helper()
				if err := os.Symlink("target", filepath.Join(root, "state")); err != nil {
					t.Fatal(err)
				}
			},
			relative: "state",
			want:     "traverses a symbolic link",
		},
		{
			name: "directory leaf",
			prepare: func(t *testing.T, root string) {
				t.Helper()
				if err := os.Mkdir(filepath.Join(root, "state"), 0o700); err != nil {
					t.Fatal(err)
				}
			},
			relative: "state",
			want:     "is not a regular file",
		},
		{
			name:     "parent traversal",
			prepare:  func(*testing.T, string) {},
			relative: "../state",
			want:     "is not canonical and relative",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			test.prepare(t, root)
			err := validateExtractedRegularFiles(root, []string{test.relative})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validation error = %v, want %q", err, test.want)
			}
		})
	}
}

// TestReadBoundedExtractedFileRejectsUnsafeMembers verifies host-side text
// reads cannot follow an extracted symbolic-link component or leaf.
func TestReadBoundedExtractedFileRejectsUnsafeMembers(t *testing.T) {
	for _, test := range []struct {
		name    string
		prepare func(*testing.T, string)
		path    string
		want    string
	}{
		{
			name: "symlink component",
			prepare: func(t *testing.T, root string) {
				t.Helper()
				outside := t.TempDir()
				if err := os.WriteFile(filepath.Join(outside, "identity"), []byte("host contents"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(root, "main")); err != nil {
					t.Fatal(err)
				}
			},
			path: "main/identity",
			want: "traverses a symbolic link",
		},
		{
			name: "symlink leaf",
			prepare: func(t *testing.T, root string) {
				t.Helper()
				if err := os.Symlink("/etc/passwd", filepath.Join(root, "identity")); err != nil {
					t.Fatal(err)
				}
			},
			path: "identity",
			want: "traverses a symbolic link",
		},
		{
			name: "oversized regular file",
			prepare: func(t *testing.T, root string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(root, "identity"), []byte("too large"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			path: "identity",
			want: "not a bounded regular file",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			test.prepare(t, root)
			_, err := readBoundedExtractedFile(root, test.path, 4)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("bounded read error = %v, want %q", err, test.want)
			}
		})
	}
}

// TestReadBoundedExtractedFileReturnsRegularContents verifies a contained
// text record is read exactly when it remains within its explicit size bound.
func TestReadBoundedExtractedFileReturnsRegularContents(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "main", "conf"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main", "conf", "identity"), []byte("uuid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := readBoundedExtractedFile(root, "main/conf/identity", 16)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "uuid\n" {
		t.Fatalf("bounded contents = %q", data)
	}
}

// TestLifecycleAvoidsDevicePolicyAllowsOnlyTheExactABI verifies a scoped ABI
// does not masquerade as hard-coded platform policy in generic lifecycle code.
func TestLifecycleAvoidsDevicePolicyAllowsOnlyTheExactABI(t *testing.T) {
	abi := "7.2.0-jg-0sp11v23-qcom-x1e"
	if !lifecycleAvoidsDevicePolicy("refresh --abi \"$abi\"\n", abi,
		"expected='"+abi+"'\npostinst\n", "expected='"+abi+"'\npostrm\n") {
		t.Fatal("package-bound SP11 ABI was rejected as device policy")
	}
	for _, test := range []struct {
		refresh string
		hook    string
	}{
		{refresh: "hard-coded " + abi, hook: "expected='" + abi + "'"},
		{refresh: "generic refresh", hook: "expected='" + abi + "'\nselect sp11 profile"},
		{refresh: "generic refresh", hook: "expected='" + abi + "'\ncopy denali device tree"},
	} {
		if lifecycleAvoidsDevicePolicy(test.refresh, abi, test.hook) {
			t.Fatalf("independent device policy was accepted: refresh=%q hook=%q", test.refresh, test.hook)
		}
	}
}

// TestSnapshotValidationImagePinsOneSourceIdentity proves a pathname exchange
// between inspection and opening cannot pair one ISO digest with another ISO's
// embedded manifest.
func TestSnapshotValidationImagePinsOneSourceIdentity(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.iso")
	replacement := filepath.Join(root, "replacement.iso")
	destination := filepath.Join(root, "snapshot.iso")
	if err := os.WriteFile(source, []byte("first image"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(replacement, []byte("second image"), 0o644); err != nil {
		t.Fatal(err)
	}
	hook := func() error {
		if err := os.Rename(source, source+".original"); err != nil {
			return err
		}
		return os.Rename(replacement, source)
	}
	if _, _, err := snapshotValidationImageAfterInspection(context.Background(), source, destination, hook); err == nil || !strings.Contains(err.Error(), "identity changed") {
		t.Fatalf("snapshot source-swap error = %v", err)
	}
	if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed snapshot destination exists: %v", err)
	}
}

// TestSnapshotValidationImageRejectsSameInodeGrowth proves the absolute ISO
// bound is repeated on the opened descriptor rather than trusted from Lstat.
func TestSnapshotValidationImageRejectsSameInodeGrowth(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.iso")
	destination := filepath.Join(root, "snapshot.iso")
	if err := os.WriteFile(source, []byte("small image"), 0o644); err != nil {
		t.Fatal(err)
	}
	hook := func() error {
		return os.Truncate(source, maximumValidationImageBytes+1)
	}
	if _, _, err := snapshotValidationImageAfterInspection(context.Background(), source, destination, hook); err == nil || !strings.Contains(err.Error(), "identity changed") {
		t.Fatalf("snapshot same-inode growth error = %v", err)
	}
	if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed snapshot destination exists: %v", err)
	}
}

// TestSnapshotValidationImageHashesThePrivateCopy verifies successful
// snapshots report the exact bytes later supplied to all structural tools.
func TestSnapshotValidationImageHashesThePrivateCopy(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.iso")
	destination := filepath.Join(root, "snapshot.iso")
	contents := []byte("one coherent image snapshot")
	if err := os.WriteFile(source, contents, 0o644); err != nil {
		t.Fatal(err)
	}
	digest, size, err := snapshotValidationImage(context.Background(), source, destination)
	if err != nil {
		t.Fatal(err)
	}
	wantDigest := sha256.Sum256(contents)
	copied, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if digest != fmt.Sprintf("%x", wantDigest) || size != int64(len(contents)) || string(copied) != string(contents) {
		t.Fatalf("snapshot digest=%s size=%d contents=%q", digest, size, copied)
	}
}

// TestReadValidationManifestRejectsPathSwapAndOversize proves the manifest
// bound and regular-file identity survive hostile pathname changes.
func TestReadValidationManifestRejectsPathSwapAndOversize(t *testing.T) {
	root := t.TempDir()
	manifest := filepath.Join(root, "manifest.json")
	replacement := filepath.Join(root, "replacement.json")
	if err := os.WriteFile(manifest, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(replacement, []byte("{\"different\":true}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hook := func() error {
		if err := os.Rename(manifest, manifest+".original"); err != nil {
			return err
		}
		return os.Rename(replacement, manifest)
	}
	if _, err := readValidationManifestAfterInspection(manifest, hook); err == nil || !strings.Contains(err.Error(), "identity changed") {
		t.Fatalf("manifest source-swap error = %v", err)
	}
	overlong := filepath.Join(root, "overlong.json")
	file, err := os.Create(overlong)
	if err != nil {
		t.Fatal(err)
	}
	truncateErr := file.Truncate(imagecontract.MaximumManifestSize + 1)
	closeErr := file.Close()
	if err := errors.Join(truncateErr, closeErr); err != nil {
		t.Fatal(err)
	}
	if _, err := readValidationManifest(overlong); err == nil || !strings.Contains(err.Error(), "bounded") {
		t.Fatalf("overlong manifest error = %v", err)
	}
}

// TestReadValidationManifestRejectsSameInodeGrowth proves the manifest size
// observed before opening must still match the descriptor-bound file.
func TestReadValidationManifestRejectsSameInodeGrowth(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	manifest := filepath.Join(root, "manifest.json")
	if err := os.WriteFile(manifest, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hook := func() error {
		return os.Truncate(manifest, 32)
	}
	if _, err := readValidationManifestAfterInspection(manifest, hook); err == nil || !strings.Contains(err.Error(), "identity changed") {
		t.Fatalf("manifest same-inode growth error = %v", err)
	}
}

// writeCompanionValidationFixture stages the smallest valid companion closed
// set and returns matching immutable records for validator tests.
func writeCompanionValidationFixture(t *testing.T, root string) imagecontract.CompanionBundleRecord {
	t.Helper()
	files := []struct {
		relative string
		content  []byte
		mode     os.FileMode
	}{
		{relative: "bin/linux-arm64/lexr", content: minimalAArch64ELF(), mode: 0o755},
		{relative: "catalogues/supported-isos.json", content: []byte("{}\n"), mode: 0o644},
		{relative: "catalogues/supported-userspace.json", content: []byte("{}\n"), mode: 0o644},
		{relative: "source/lexr_v1.2.3_source.tar.gz", content: []byte("source snapshot"), mode: 0o644},
	}
	records := make(map[string]imagecontract.ArtifactRecord, len(files))
	for _, file := range files {
		hostPath := filepath.Join(root, filepath.FromSlash(file.relative))
		if err := os.MkdirAll(filepath.Dir(hostPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(hostPath, file.content, file.mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(hostPath, file.mode); err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(file.content)
		records[file.relative] = imagecontract.ArtifactRecord{
			Path:   companion.ISOFilesystemRoot + "/" + file.relative,
			SHA256: fmt.Sprintf("%x", digest),
			Size:   int64(len(file.content)),
		}
	}
	executable := records["bin/linux-arm64/lexr"]
	sourceArchive := records["source/lexr_v1.2.3_source.tar.gz"]
	return imagecontract.CompanionBundleRecord{
		Included: true,
		Root:     companion.ISOFilesystemRoot,
		Tool: &imagecontract.ToolIdentityRecord{
			Version:   "v1.2.3",
			Commit:    "abc123",
			BuildDate: "2026-08-30T00:00:00Z",
		},
		ProjectLicence: "not-declared",
		Executable: &imagecontract.ExecutableArtifactRecord{
			Artifact:        executable,
			OperatingSystem: "linux",
			Architecture:    "arm64",
			Format:          "ELF",
			Mode:            "0755",
		},
		SourceArchive: &sourceArchive,
		Catalogues: []imagecontract.ArtifactRecord{
			records["catalogues/supported-isos.json"],
			records["catalogues/supported-userspace.json"],
		},
		Userspace: []imagecontract.OfflineUserspaceRecord{},
	}
}

// minimalAArch64ELF returns a header-only, statically linked AArch64 ELF that
// is sufficient for the companion format and interpreter checks.
func minimalAArch64ELF() []byte {
	contents := make([]byte, 64)
	copy(contents[:4], []byte{0x7f, 'E', 'L', 'F'})
	contents[4] = 2
	contents[5] = 1
	contents[6] = 1
	binary.LittleEndian.PutUint16(contents[16:18], 2)
	binary.LittleEndian.PutUint16(contents[18:20], 183)
	binary.LittleEndian.PutUint32(contents[20:24], 1)
	binary.LittleEndian.PutUint16(contents[52:54], 64)
	binary.LittleEndian.PutUint16(contents[54:56], 56)
	binary.LittleEndian.PutUint16(contents[58:60], 64)
	return contents
}

// assertValidationCheck returns the named validation check after asserting its
// expected result, failing the test when the report omits it.
func assertValidationCheck(
	t *testing.T,
	checks []imagecontract.ValidationCheck,
	name string,
	wantPassed bool,
) imagecontract.ValidationCheck {
	t.Helper()
	for _, check := range checks {
		if check.Name == name {
			if check.Passed != wantPassed {
				t.Fatalf("validation check %q passed = %t, want %t; details: %s", name, check.Passed, wantPassed, check.Details)
			}
			return check
		}
	}
	t.Fatalf("validation check %q is missing from %#v", name, checks)
	return imagecontract.ValidationCheck{}
}

// containsArgumentSequence reports whether arguments contain the exact
// consecutive values, preserving command-boundary assertions in tests.
func containsArgumentSequence(arguments []string, values ...string) bool {
	if len(values) == 0 || len(arguments) < len(values) {
		return false
	}
	for start := 0; start <= len(arguments)-len(values); start++ {
		matched := true
		for offset := range values {
			if arguments[start+offset] != values[offset] {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

// TestValidateInstalledInitramfsListing rejects every incomplete core archive
// shape observed during offline-root generation while allowing usrmerge paths.
func TestValidateInstalledInitramfsListing(t *testing.T) {
	const abi = "7.2.0-test-qcom-x1e"
	valid := []string{
		"init",
		"usr/lib/firmware/" + liveWiFiBoard,
		"usr/bin/sh",
		"usr/bin/mount",
		"usr/sbin/modprobe",
		"usr/lib/dracut-lib.sh",
		"usr/lib/dracut/hooks/cmdline/00-parse-root.sh",
		"usr/lib/modules/" + abi + "/modules.dep",
		"usr/lib/modules/" + abi + "/kernel/drivers/test.ko.zst",
	}
	modulePath := "usr/lib/modules/" + abi + "/kernel/drivers/test.ko.zst"
	longListing := "-rw-r--r-- 1 root root 42 Jan 1 00:00 " + modulePath
	if err := validateInstalledInitramfsListing(strings.Join(valid, "\n"), longListing, abi); err != nil {
		t.Fatalf("valid listing error = %v", err)
	}
	for _, test := range []struct {
		name        string
		remove      string
		replacement string
		longListing string
	}{
		{name: "init", remove: "init"},
		{name: "Wi-Fi board", remove: "usr/lib/firmware/" + liveWiFiBoard},
		{name: "whitespace spoof", remove: "init", replacement: " init "},
		{name: "shell", remove: "usr/bin/sh"},
		{name: "mount", remove: "usr/bin/mount"},
		{name: "modprobe", remove: "usr/sbin/modprobe"},
		{name: "library", remove: "usr/lib/dracut-lib.sh"},
		{name: "root parser", remove: "usr/lib/dracut/hooks/cmdline/00-parse-root.sh"},
		{name: "exact modules dep", remove: "usr/lib/modules/" + abi + "/modules.dep", replacement: "usr/lib/modules/" + abi + "/modules.dep.extra"},
		{name: "real module", remove: modulePath, replacement: modulePath + ".extra", longListing: "drwxr-xr-x 1 root root 0 Jan 1 00:00 " + modulePath},
	} {
		t.Run(test.name, func(t *testing.T) {
			listing := slices.Clone(valid)
			for index, member := range listing {
				if member == test.remove {
					listing[index] = test.replacement
				}
			}
			testLongListing := longListing
			if test.longListing != "" {
				testLongListing = test.longListing
			}
			if err := validateInstalledInitramfsListing(strings.Join(listing, "\n"), testLongListing, abi); err == nil {
				t.Fatal("incomplete installed initramfs listing passed")
			}
		})
	}
	withCasper := append(slices.Clone(valid), "scripts/casper-bottom/01-integrity-check")
	if err := validateInstalledInitramfsListing(strings.Join(withCasper, "\n"), longListing, abi); err == nil || !strings.Contains(err.Error(), "Casper") {
		t.Fatalf("Casper listing error = %v", err)
	}
}

// TestFailedValidationSummaryPreservesBoundedEvidence keeps image-create
// failures actionable without allowing unbounded tool output into one error.
func TestFailedValidationSummaryPreservesBoundedEvidence(t *testing.T) {
	checks := []imagecontract.ValidationCheck{
		{Name: "passing", Passed: true, Details: "ignored"},
		{Name: "installed-system-initramfs\x1b[31m", Details: " missing\n  init\x00 "},
		{Name: "second", Details: strings.Repeat("x", 4096)},
	}
	for range 32 {
		checks = append(checks, imagecontract.ValidationCheck{
			Name: strings.Repeat("n", 128), Details: strings.Repeat("d", 512),
		})
	}
	summary := failedValidationSummary(checks)
	if strings.Contains(summary, "passing") || !strings.Contains(summary, "installed-system-initramfs?[31m: missing init?") ||
		!strings.Contains(summary, "second:") {
		t.Fatalf("failed validation summary = %q", summary)
	}
	if strings.ContainsAny(summary, "\x00\x1b") {
		t.Fatalf("failed validation summary retains terminal controls: %q", summary)
	}
	if len(summary) > 2048 {
		t.Fatalf("failed validation summary length = %d, want at most 2048", len(summary))
	}
	if !strings.Contains(summary, "additional failures omitted") {
		t.Fatalf("failed validation summary does not record bounded omission: %q", summary)
	}
	if got := sanitizedValidationText(strings.Repeat("é", 80), 96); len(got) != 95 || !strings.HasSuffix(got, "...") || !utf8.ValidString(got) {
		t.Fatalf("rune-safe capped validation text has length %d and value %q", len(got), got)
	}
}
