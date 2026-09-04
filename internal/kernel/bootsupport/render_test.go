package bootsupport

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
)

const (
	// testABI is the exact fixture ABI bound into rendered scripts.
	testABI = "7.2.0-lexr-qcom"
	// testVersion is the deterministic Debian fixture version.
	testVersion = "7.2.0-1lexr1"
	// testMaintainer is the fixture Debian Maintainer field.
	testMaintainer = "Lexr Maintainers <maintainers@example.invalid>"
)

// testRequest returns an intentionally unsorted generic platform inventory.
func testRequest() Request {
	return Request{
		ABI:        testABI,
		Version:    testVersion,
		Maintainer: testMaintainer,
		Platforms: []Platform{
			{
				ID: "zeta-tablet", Title: "Zeta Tablet", DeviceTreePath: "vendor/zeta-tablet.dtb",
				DeviceTreeSHA256: strings.Repeat("b", 64), Compatibles: []string{"vendor,zeta-v2", "vendor,zeta-v1"},
			},
			{
				ID: "alpha-laptop", Title: "Alpha Laptop", DeviceTreePath: "vendor/alpha-laptop.dtb",
				DeviceTreeSHA256: strings.Repeat("a", 64), MachineIdentities: []string{"Alpha Laptop Rev B", "Alpha Laptop Rev A"},
			},
		},
	}
}

// TestRenderIsDeterministic proves caller ordering does not affect output.
func TestRenderIsDeterministic(t *testing.T) {
	request := testRequest()
	first, err := Render(request)
	if err != nil {
		t.Fatal(err)
	}
	slices.Reverse(request.Platforms)
	slices.Reverse(request.Platforms[0].MachineIdentities)
	slices.Reverse(request.Platforms[1].Compatibles)
	second, err := Render(request)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("equivalent platform ordering produced different staging payloads")
	}
	if first.Digest() == "" || first.Digest() != second.Digest() {
		t.Fatalf("payload digests differ: %q and %q", first.Digest(), second.Digest())
	}
	if first.Name != PackageName || first.Version != testVersion || first.Architecture != Architecture {
		t.Fatalf("unexpected package identity: %+v", first)
	}
	for index := 1; index < len(first.Files); index++ {
		if first.Files[index-1].Path >= first.Files[index].Path {
			t.Fatalf("files are not strictly sorted at %q", first.Files[index].Path)
		}
	}
	platforms := mustFile(t, first, buildRoot+"/platforms")
	if got, want := string(platforms.Data), "alpha-laptop\nzeta-tablet\n"; got != want {
		t.Fatalf("platform inventory = %q, want %q", got, want)
	}
	machines := mustFile(t, first, platformRoot+"/alpha-laptop/machine-identities")
	if got, want := string(machines.Data), "Alpha Laptop Rev A\nAlpha Laptop Rev B\n"; got != want {
		t.Fatalf("machine identities = %q, want %q", got, want)
	}
}

// TestRenderControlAndModes verifies package metadata and permissions.
func TestRenderControlAndModes(t *testing.T) {
	payload, err := Render(testRequest())
	if err != nil {
		t.Fatal(err)
	}
	wantControl := "Package: lexr-kernel-boot-support\n" +
		"Version: 7.2.0-1lexr1\n" +
		"Section: admin\nPriority: optional\nArchitecture: all\n" +
		"Maintainer: Lexr Maintainers <maintainers@example.invalid>\n" +
		"Depends: coreutils, grub-common, grub2-common, util-linux\n" +
		"Recommends: linux-image-7.2.0-lexr-qcom (= 7.2.0-1lexr1), linux-modules-7.2.0-lexr-qcom (= 7.2.0-1lexr1)\n" +
		"Enhances: linux-image-7.2.0-lexr-qcom, linux-modules-7.2.0-lexr-qcom\n" +
		"Description: exact-ABI external device-tree boot support for Lexr kernels\n" +
		" Materialises one verified ABI-scoped device tree for the stock GRUB generator.\n"
	control := mustFile(t, payload, controlPath)
	if string(control.Data) != wantControl || control.Mode.Perm() != 0o644 {
		t.Fatalf("control file = mode %04o, data %q", control.Mode.Perm(), control.Data)
	}
	triggers := mustFile(t, payload, "DEBIAN/triggers")
	if got, want := string(triggers.Data), "interest-noawait linux-update-"+testABI+"\n"; got != want || triggers.Mode.Perm() != 0o644 {
		t.Fatalf("triggers file = mode %04o, data %q", triggers.Mode.Perm(), got)
	}
	for _, item := range payload.Files {
		wantMode := os.FileMode(0o644)
		if strings.HasPrefix(item.Path, "DEBIAN/post") || item.Path == "DEBIAN/prerm" || item.Path == helperPath || item.Path == postInstallPath || item.Path == postRemovePath {
			wantMode = 0o755
		}
		if item.Mode.Perm() != wantMode {
			t.Errorf("%s mode = %04o, want %04o", item.Path, item.Mode.Perm(), wantMode)
		}
		if len(item.Data) > 0 && item.Data[len(item.Data)-1] != '\n' {
			t.Errorf("%s lacks a final newline", item.Path)
		}
	}
}

// TestRenderedScriptsEnforceGenericExactABILifecycle checks ABI-scoped scripts.
func TestRenderedScriptsEnforceGenericExactABILifecycle(t *testing.T) {
	payload, err := Render(testRequest())
	if err != nil {
		t.Fatal(err)
	}
	helper := string(mustFile(t, payload, helperPath).Data)
	for _, required := range []string{
		`[ "$image" = "/boot/vmlinuz-$abi" ]`,
		`"$target_root/boot/dtbs/$abi/$dtb_path"`,
		`"$target_root/boot/dtb-$abi"`,
		`sha256sum`, `mktemp`, `sync -f`, `flock 9`,
		`findmnt --fstab --tab-file`, `--mountpoint /boot`,
		`platform identity did not select exactly one declared record`,
		`exact ABI is already bound to a different platform`,
	} {
		if !strings.Contains(helper, required) {
			t.Errorf("helper does not contain %q", required)
		}
	}
	for _, forbidden := range []string{"sp11", "denali", "uname -r", "sort -V", "vmlinuz-*", "/boot/device-tree.dtb"} {
		if strings.Contains(strings.ToLower(helper), strings.ToLower(forbidden)) {
			t.Errorf("helper contains forbidden policy %q", forbidden)
		}
	}
	postInstall := string(mustFile(t, payload, postInstallPath).Data)
	postRemove := string(mustFile(t, payload, postRemovePath).Data)
	for name, script := range map[string]string{"postinstall": postInstall, "postremove": postRemove} {
		if !strings.Contains(script, "'"+testABI+"'") || !strings.Contains(script, `${1:-}`) {
			t.Errorf("%s hook is not bound to its build ABI and exact hook argument", name)
		}
	}
	for _, required := range []string{`${DEB_MAINT_PARAMS:-}`, `${maintainer_action%% *}`, "remove|purge)"} {
		if !strings.Contains(postRemove, required) {
			t.Errorf("postremove hook does not guard package lifecycle action %q", required)
		}
	}
	packagePostInstall := string(mustFile(t, payload, "DEBIAN/postinst").Data)
	for _, required := range []string{"configure)", "triggered)", `"$status" -eq 76`, "linux-update trigger"} {
		if !strings.Contains(packagePostInstall, required) {
			t.Errorf("package postinst does not contain %q", required)
		}
	}
	for _, item := range payload.Files {
		if strings.HasPrefix(item.Path, "etc/grub.d/") {
			t.Errorf("payload installs competing GRUB generator %s", item.Path)
		}
	}
}

// TestDebianGlobOrderIsTriggerSafe records the physical shell order whose
// early missing-firmware condition is reconciled by the exact Linux trigger.
func TestDebianGlobOrderIsTriggerSafe(t *testing.T) {
	names := []string{
		"linux-modules-" + testABI + "_" + testVersion + "_arm64.deb",
		"linux-image-" + testABI + "_" + testVersion + "_arm64.deb",
		PackageName + "_" + testVersion + "_all.deb",
	}
	slices.Sort(names)
	want := []string{
		PackageName + "_" + testVersion + "_all.deb",
		"linux-image-" + testABI + "_" + testVersion + "_arm64.deb",
		"linux-modules-" + testABI + "_" + testVersion + "_arm64.deb",
	}
	if !slices.Equal(names, want) {
		t.Fatalf("shell glob order = %v, want %v", names, want)
	}
	payload, err := Render(testRequest())
	if err != nil {
		t.Fatal(err)
	}
	postInstall := string(mustFile(t, payload, "DEBIAN/postinst").Data)
	if strings.Count(postInstall, `"$status" -eq 76`) != 1 || !strings.Contains(postInstall, "triggered)") {
		t.Fatalf("package postinst does not defer source only during initial configure:\n%s", postInstall)
	}
}

// TestRenderedShellHasValidSyntax parses every rendered shell script.
func TestRenderedShellHasValidSyntax(t *testing.T) {
	payload, err := Render(testRequest())
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range payload.Files {
		if item.Mode.Perm()&0o111 == 0 {
			continue
		}
		t.Run(filepath.Base(item.Path), func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "script")
			if err := os.WriteFile(file, item.Data, 0o700); err != nil {
				t.Fatal(err)
			}
			command := exec.Command("/bin/sh", "-n", file)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("sh -n: %v\n%s", err, output)
			}
		})
	}
	buildScript := filepath.Join(t.TempDir(), "build-package")
	if err := os.WriteFile(buildScript, []byte(DebianPackageBuildScript()), 0o700); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("/bin/sh", "-n", buildScript).CombinedOutput(); err != nil {
		t.Fatalf("package build script sh -n: %v\n%s", err, output)
	}
}

// TestRenderRejectsUnsafeOrAmbiguousRecords exercises hostile declarations.
func TestRenderRejectsUnsafeOrAmbiguousRecords(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Request)
		want   string
	}{
		{name: "unsafe ABI", mutate: func(request *Request) { request.ABI = "../kernel" }, want: "unsafe kernel ABI"},
		{name: "non-Debian ABI", mutate: func(request *Request) { request.ABI = "7.2.0-Lexr" }, want: "unsafe kernel ABI"},
		{name: "unsafe version", mutate: func(request *Request) { request.Version = "version one" }, want: "unsafe Debian version"},
		{name: "empty maintainer", mutate: func(request *Request) { request.Maintainer = "" }, want: "unsafe maintainer"},
		{name: "no platforms", mutate: func(request *Request) { request.Platforms = nil }, want: "at least one platform"},
		{name: "unsafe platform ID", mutate: func(request *Request) { request.Platforms[0].ID = "Zeta/Tablet" }, want: "unsafe platform ID"},
		{name: "duplicate platform ID", mutate: func(request *Request) { request.Platforms[1].ID = request.Platforms[0].ID }, want: "duplicate platform ID"},
		{name: "traversal DTB", mutate: func(request *Request) { request.Platforms[0].DeviceTreePath = "vendor/../other.dtb" }, want: "unsafe relative DTB path"},
		{name: "flat DTB", mutate: func(request *Request) { request.Platforms[0].DeviceTreePath = "machine.dtb" }, want: "vendor and file"},
		{name: "malformed digest", mutate: func(request *Request) { request.Platforms[0].DeviceTreeSHA256 = strings.Repeat("A", 64) }, want: "malformed DTB SHA-256"},
		{name: "no selection", mutate: func(request *Request) { request.Platforms[0].Compatibles = nil }, want: "no exact selection identity"},
		{name: "ambiguous machine", mutate: func(request *Request) { request.Platforms[0].MachineIdentities = []string{"Alpha Laptop Rev A"} }, want: "machine identity"},
		{name: "ambiguous compatible", mutate: func(request *Request) {
			request.Platforms[1].Compatibles = append(request.Platforms[1].Compatibles, "vendor,zeta-v1")
		}, want: "compatible"},
		{name: "control newline", mutate: func(request *Request) { request.Maintainer += "\nInjected: yes" }, want: "unsafe maintainer"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := testRequest()
			test.mutate(&request)
			_, err := Render(request)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Render error = %v, want containing %q", err, test.want)
			}
		})
	}
}

// TestPayloadFileReturnsDefensiveContent proves returned bytes are isolated.
func TestPayloadFileReturnsDefensiveContent(t *testing.T) {
	payload, err := Render(testRequest())
	if err != nil {
		t.Fatal(err)
	}
	first := mustFile(t, payload, controlPath)
	original := append([]byte(nil), first.Data...)
	first.Data[0] = 'X'
	second := mustFile(t, payload, controlPath)
	if !bytes.Equal(second.Data, original) {
		t.Fatal("mutating returned file changed payload content")
	}
}

// TestLinuxHelperRefreshAndRemove exercises atomic per-ABI lifecycle.
func TestLinuxHelperRefreshAndRemove(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("rendered helper executes against Linux boot tooling")
	}
	for _, executable := range []string{"findmnt", "flock", "install", "readlink", "sha256sum", "sync"} {
		if _, err := exec.LookPath(executable); err != nil {
			t.Skipf("%s is unavailable: %v", executable, err)
		}
	}
	root, helper, dtbPath, digest := stageLinuxFixture(t)
	runHelper(t, helper, "refresh", "--root", root, "--abi", testABI, "--image", "/boot/vmlinuz-"+testABI, "--platform", "alpha-laptop", "--defer-grub")
	canonical := filepath.Join(root, "boot/dtbs", testABI, filepath.FromSlash(dtbPath))
	compatibility := filepath.Join(root, "boot", "dtb-"+testABI)
	assertDigest(t, canonical, digest)
	assertDigest(t, compatibility, digest)
	state := filepath.Join(root, "var/lib/lexr/kernel-boot", testABI, "alpha-laptop")
	assertDigest(t, filepath.Join(state, "dtb-sha256"), digestLineDigest(digest))

	// One physical root has one bootable DTB slot per ABI. A package may carry
	// many profiles, but selecting a second one must not replace the first.
	secondData := []byte("zeta device tree")
	secondSum := sha256.Sum256(secondData)
	secondDigest := hex.EncodeToString(secondSum[:])
	secondRecord := filepath.Join(root, filepath.FromSlash(platformRoot), "zeta-tablet")
	secondFiles := map[string]string{
		"title": "Zeta Tablet\n", "dtb-path": "vendor/zeta-tablet.dtb\n", "dtb-sha256": secondDigest + "\n",
		"machine-identities": "Zeta Tablet\n", "compatibles": "vendor,zeta-v1\n",
	}
	for name, content := range secondFiles {
		if err := os.MkdirAll(secondRecord, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(secondRecord, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	secondSource := filepath.Join(root, "usr/lib/firmware", testABI, "device-tree/vendor/zeta-tablet.dtb")
	if err := os.MkdirAll(filepath.Dir(secondSource), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secondSource, secondData, 0o644); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(helper, "refresh", "--root", root, "--abi", testABI, "--image", "/boot/vmlinuz-"+testABI, "--platform", "zeta-tablet", "--defer-grub")
	if output, err := command.CombinedOutput(); err == nil || !strings.Contains(string(output), "already bound to a different platform") {
		t.Fatalf("second platform selection error = %v, output %q", err, output)
	}
	assertDigest(t, compatibility, digest)

	// Reconfiguration is deliberately idempotent. Persisted ABI state remains
	// authoritative if a later singleton package changes its global registry.
	registry := filepath.Join(root, filepath.FromSlash(platformRoot), "alpha-laptop")
	if err := os.WriteFile(filepath.Join(registry, "title"), []byte("Changed Global Title\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(registry, "dtb-sha256"), []byte(strings.Repeat("c", 64)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runHelper(t, helper, "refresh", "--root", root, "--abi", testABI, "--image", "/boot/vmlinuz-"+testABI, "--platform", "alpha-laptop", "--defer-grub")
	assertDigest(t, canonical, digest)
	assertDigest(t, compatibility, digest)
	stateTitle, err := os.ReadFile(filepath.Join(state, "title"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(stateTitle), "Alpha Laptop\n"; got != want {
		t.Fatalf("persisted title = %q, want %q", got, want)
	}
	runHelper(t, helper, "remove", "--root", root, "--abi", testABI, "--defer-grub")
	for _, removed := range []string{canonical, compatibility, state} {
		if _, err := os.Lstat(removed); !os.IsNotExist(err) {
			t.Fatalf("exact-ABI removal retained %s: %v", removed, err)
		}
	}
}

// TestLinuxHelperRejectsUnsafeSourcesAndBootMount verifies safety guards.
func TestLinuxHelperRejectsUnsafeSourcesAndBootMount(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("rendered helper executes against Linux boot tooling")
	}
	for _, executable := range []string{"findmnt", "flock", "install", "readlink", "sha256sum"} {
		if _, err := exec.LookPath(executable); err != nil {
			t.Skipf("%s is unavailable: %v", executable, err)
		}
	}
	t.Run("symlink source", func(t *testing.T) {
		root, helper, dtbPath, _ := stageLinuxFixture(t)
		source := filepath.Join(root, "usr/lib/firmware", testABI, "device-tree", filepath.FromSlash(dtbPath))
		external := filepath.Join(t.TempDir(), "external.dtb")
		if err := os.WriteFile(external, []byte("alpha device tree"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(source); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(external, source); err != nil {
			t.Fatal(err)
		}
		output, err := exec.Command(helper, "refresh", "--root", root, "--abi", testABI, "--image", "/boot/vmlinuz-"+testABI, "--platform", "alpha-laptop", "--defer-grub").CombinedOutput()
		if err == nil || !strings.Contains(string(output), "DTB source is redirected") {
			t.Fatalf("symlink source error = %v, output %q", err, output)
		}
	})
	t.Run("declared unmounted boot", func(t *testing.T) {
		root, helper, _, _ := stageLinuxFixture(t)
		if err := os.MkdirAll(filepath.Join(root, "etc"), 0o755); err != nil {
			t.Fatal(err)
		}
		fstab := "UUID=00000000-0000-0000-0000-000000000000 /boot ext4 defaults 0 2\n"
		if err := os.WriteFile(filepath.Join(root, "etc/fstab"), []byte(fstab), 0o644); err != nil {
			t.Fatal(err)
		}
		output, err := exec.Command(helper, "refresh", "--root", root, "--abi", testABI, "--image", "/boot/vmlinuz-"+testABI, "--platform", "alpha-laptop", "--defer-grub").CombinedOutput()
		if err == nil || !strings.Contains(string(output), "declared separate /boot is not mounted") {
			t.Fatalf("unmounted boot error = %v, output %q", err, output)
		}
	})
}

// TestLinuxRemoveAllPreservesInstalledKernelBindings proves package removal
// fails before mutation while any owned exact-ABI kernel image still exists.
func TestLinuxRemoveAllPreservesInstalledKernelBindings(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("rendered helper executes against Linux boot tooling")
	}
	for _, executable := range []string{"findmnt", "flock", "install", "readlink", "sha256sum", "sync"} {
		if _, err := exec.LookPath(executable); err != nil {
			t.Skipf("%s is unavailable: %v", executable, err)
		}
	}
	root, helper, dtbPath, digest := stageLinuxFixture(t)
	runHelper(t, helper, "refresh", "--root", root, "--abi", testABI, "--image", "/boot/vmlinuz-"+testABI, "--platform", "alpha-laptop", "--defer-grub")
	canonical := filepath.Join(root, "boot/dtbs", testABI, filepath.FromSlash(dtbPath))
	compatibility := filepath.Join(root, "boot", "dtb-"+testABI)
	state := filepath.Join(root, "var/lib/lexr/kernel-boot", testABI, "alpha-laptop")
	command := exec.Command(helper, "remove-all", "--root", root, "--defer-grub")
	if output, err := command.CombinedOutput(); err == nil || !strings.Contains(string(output), "image remains installed") {
		t.Fatalf("guarded package removal error = %v, output %q", err, output)
	}
	assertDigest(t, canonical, digest)
	assertDigest(t, compatibility, digest)
	if _, err := os.Stat(state); err != nil {
		t.Fatalf("guarded package removal changed state: %v", err)
	}
	if err := os.Remove(filepath.Join(root, "boot", "vmlinuz-"+testABI)); err != nil {
		t.Fatal(err)
	}
	runHelper(t, helper, "remove-all", "--root", root, "--defer-grub")
	for _, removed := range []string{canonical, compatibility, state} {
		if _, err := os.Lstat(removed); !os.IsNotExist(err) {
			t.Fatalf("final support removal retained %s: %v", removed, err)
		}
	}
}

// TestLinuxRemovalRejectsTamperedOwnedDTB proves cleanup authenticates every
// present boot copy before deleting either the state or untampered companion.
func TestLinuxRemovalRejectsTamperedOwnedDTB(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("rendered helper executes against Linux boot tooling")
	}
	for _, executable := range []string{"findmnt", "flock", "install", "readlink", "sha256sum", "sync"} {
		if _, err := exec.LookPath(executable); err != nil {
			t.Skipf("%s is unavailable: %v", executable, err)
		}
	}
	root, helper, dtbPath, digest := stageLinuxFixture(t)
	runHelper(t, helper, "refresh", "--root", root, "--abi", testABI, "--image", "/boot/vmlinuz-"+testABI, "--platform", "alpha-laptop", "--defer-grub")
	canonical := filepath.Join(root, "boot/dtbs", testABI, filepath.FromSlash(dtbPath))
	compatibility := filepath.Join(root, "boot", "dtb-"+testABI)
	state := filepath.Join(root, "var/lib/lexr/kernel-boot", testABI, "alpha-laptop")
	if err := os.WriteFile(compatibility, []byte("tampered device tree"), 0o644); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(helper, "remove", "--root", root, "--abi", testABI, "--defer-grub")
	if output, err := command.CombinedOutput(); err == nil || !strings.Contains(string(output), "digest changed before removal") {
		t.Fatalf("tampered removal error = %v, output %q", err, output)
	}
	assertDigest(t, canonical, digest)
	if _, err := os.Stat(state); err != nil {
		t.Fatalf("tampered removal changed state: %v", err)
	}
	source := filepath.Join(root, "usr/lib/firmware", testABI, "device-tree", filepath.FromSlash(dtbPath))
	content, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(compatibility, content, 0o644); err != nil {
		t.Fatal(err)
	}
	runHelper(t, helper, "remove", "--root", root, "--abi", testABI, "--defer-grub")
}

// TestLinuxRefreshConvergesAfterEveryPackageOrder proves early support
// configuration can defer, while the exact-ABI final trigger must converge.
func TestLinuxRefreshConvergesAfterEveryPackageOrder(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("rendered helper executes against Linux boot tooling")
	}
	orders := [][]string{
		{"support", "image", "modules"}, {"support", "modules", "image"},
		{"image", "support", "modules"}, {"image", "modules", "support"},
		{"modules", "support", "image"}, {"modules", "image", "support"},
	}
	for _, order := range orders {
		t.Run(strings.Join(order, "-"), func(t *testing.T) {
			root, helper, dtbPath, digest := stageLinuxFixture(t)
			image := filepath.Join(root, "boot", "vmlinuz-"+testABI)
			source := filepath.Join(root, "usr/lib/firmware", testABI, "device-tree", filepath.FromSlash(dtbPath))
			if err := os.Remove(image); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(source); err != nil {
				t.Fatal(err)
			}
			for _, event := range order {
				switch event {
				case "support":
					command := exec.Command(helper, "refresh", "--root", root, "--abi", testABI, "--image", "/boot/vmlinuz-"+testABI, "--platform", "alpha-laptop", "--defer-grub")
					if output, err := command.CombinedOutput(); err != nil {
						var exitErr *exec.ExitError
						if !errors.As(err, &exitErr) || exitErr.ExitCode() != 76 {
							t.Fatalf("early support configure: %v, output %q", err, output)
						}
					}
				case "image":
					if err := os.WriteFile(image, []byte("kernel image"), 0o644); err != nil {
						t.Fatal(err)
					}
				case "modules":
					if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(source, []byte("alpha device tree"), 0o644); err != nil {
						t.Fatal(err)
					}
				}
			}
			runHelper(t, helper, "refresh", "--root", root, "--abi", testABI, "--image", "/boot/vmlinuz-"+testABI, "--platform", "alpha-laptop", "--defer-grub")
			assertDigest(t, filepath.Join(root, "boot", "dtb-"+testABI), digest)
		})
	}
}

// TestLinuxDpkgDebBuildIsReproducible verifies byte-identical archives.
func TestLinuxDpkgDebBuildIsReproducible(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("dpkg-deb packaging executes on Linux")
	}
	dpkgDeb, err := exec.LookPath("dpkg-deb")
	if err != nil {
		t.Skipf("dpkg-deb is unavailable: %v", err)
	}
	payload, err := Render(testRequest())
	if err != nil {
		t.Fatal(err)
	}
	first := buildDebianArchive(t, payload)
	second := buildDebianArchive(t, payload)
	if !bytes.Equal(first, second) {
		t.Fatal("identical staging payloads produced different Debian archives")
	}
	archive := filepath.Join(t.TempDir(), "support.deb")
	if err := os.WriteFile(archive, first, 0o644); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(dpkgDeb, "--field", archive, "Package", "Version", "Architecture")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("inspect generated package: %v\n%s", err, output)
	}
	for _, field := range []string{"Package: " + PackageName, "Version: " + testVersion, "Architecture: " + Architecture} {
		if !strings.Contains(string(output), field) {
			t.Errorf("generated package metadata lacks %q: %s", field, output)
		}
	}
}

// stageLinuxFixture materialises an isolated helper execution root.
func stageLinuxFixture(t *testing.T) (string, string, string, string) {
	t.Helper()
	dtb := []byte("alpha device tree")
	sum := sha256.Sum256(dtb)
	digest := hex.EncodeToString(sum[:])
	request := testRequest()
	request.Platforms = request.Platforms[1:]
	request.Platforms[0].DeviceTreeSHA256 = digest
	payload, err := Render(request)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for _, directory := range []string{"boot", "proc", "run", "sys"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range payload.Files {
		if strings.HasPrefix(item.Path, "DEBIAN/") || item.Path == postInstallPath || item.Path == postRemovePath {
			continue
		}
		target := filepath.Join(root, filepath.FromSlash(item.Path))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, item.Data, item.Mode.Perm()); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "boot", "vmlinuz-"+testABI), []byte("kernel image"), 0o644); err != nil {
		t.Fatal(err)
	}
	dtbPath := request.Platforms[0].DeviceTreePath
	source := filepath.Join(root, "usr/lib/firmware", testABI, "device-tree", filepath.FromSlash(dtbPath))
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, dtb, 0o644); err != nil {
		t.Fatal(err)
	}
	return root, filepath.Join(root, filepath.FromSlash(helperPath)), dtbPath, digest
}

// runHelper executes the rendered helper with fixture arguments.
func runHelper(t *testing.T, helper string, arguments ...string) {
	t.Helper()
	command := exec.Command(helper, arguments...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("helper %v: %v\n%s", arguments, err, output)
	}
}

// assertDigest checks one materialised file against its expected SHA-256.
func assertDigest(t *testing.T, name, want string) {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("%s SHA-256 = %s, want %s", name, got, want)
	}
}

// digestLineDigest formats one digest record for fixture state.
func digestLineDigest(digest string) string {
	sum := sha256.Sum256([]byte(digest + "\n"))
	return hex.EncodeToString(sum[:])
}

// buildDebianArchive stages and packages one deterministic payload.
func buildDebianArchive(t *testing.T, payload Payload) []byte {
	t.Helper()
	workspace := t.TempDir()
	staging := filepath.Join(workspace, "staging")
	if err := os.Mkdir(staging, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, item := range payload.Files {
		target := filepath.Join(staging, filepath.FromSlash(item.Path))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, item.Data, item.Mode.Perm()); err != nil {
			t.Fatal(err)
		}
	}
	buildScript := filepath.Join(workspace, "build-package")
	if err := os.WriteFile(buildScript, []byte(DebianPackageBuildScript()), 0o700); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(workspace, "support.deb")
	command := exec.Command(buildScript, staging, archive, "1700000000")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build Debian package: %v\n%s", err, output)
	}
	data, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// mustFile returns one payload file or fails the calling test.
func mustFile(t *testing.T, payload Payload, name string) File {
	t.Helper()
	item, ok := payload.File(name)
	if !ok {
		t.Fatalf("payload has no file %q", name)
	}
	return item
}
