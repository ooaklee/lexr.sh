package build

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// recipePackageRoleABI is a synthetic test ABI, not a released kernel claim.
const recipePackageRoleABI = "7.2.9-jg-0sp11v1-qcom-x1e"

// recipePackageRoleVersion is the coherent Debian version used by tiny archives.
const recipePackageRoleVersion = "7.2.9-jg-0sp11v1"

// recipePackageRolePayload is inert data; these tests never execute a boot image.
const recipePackageRolePayload = "inert test-only boot payload\n"

// recipePackageRoleFixture independently describes archive naming, control
// identity, lifecycle members and payload ownership for one real Debian archive.
type recipePackageRoleFixture struct {
	filename     string
	packageName  string
	version      string
	architecture string
	controls     []recipeTarMember
	payload      []recipeTarMember
}

// recipePackageRoleFixtures uses the pinned source templates as inert image
// controls. Modules deliberately have no image update trigger, as in Ubuntu's
// split Stubble layout; neither package's maintainer scripts are executed.
func recipePackageRoleFixtures(t *testing.T, family, bootRole string) []recipePackageRoleFixture {
	t.Helper()
	hooks := recipeKernelHookReparameterize(t, recipeKernelHookSourceFixture(t, family), recipePackageRoleABI)
	image := recipePackageRoleFixture{
		packageName: "linux-image-" + recipePackageRoleABI, version: recipePackageRoleVersion, architecture: "arm64",
		controls: []recipeTarMember{
			{name: "./postinst", data: []byte(hooks.postinst)},
			{name: "./postrm", data: []byte(hooks.postrm)},
			{name: "./triggers", data: []byte(hooks.triggers)},
		},
	}
	modules := recipePackageRoleFixture{
		packageName: "linux-modules-" + recipePackageRoleABI, version: recipePackageRoleVersion, architecture: "arm64",
		controls: []recipeTarMember{
			{name: "./postinst", data: []byte("#!/bin/sh\n# Inert module fixture; never execute.\nexit 99\n")},
			{name: "./postrm", data: []byte("#!/bin/sh\n# Inert module fixture; never execute.\nexit 99\n")},
		},
	}
	image.filename = image.packageName + "_" + image.version + "_arm64.deb"
	modules.filename = modules.packageName + "_" + modules.version + "_arm64.deb"
	payload := recipeTarMember{name: "./boot/vmlinuz-" + recipePackageRoleABI, data: []byte(recipePackageRolePayload)}
	switch bootRole {
	case "image":
		image.payload = []recipeTarMember{payload}
	case "modules":
		modules.payload = []recipeTarMember{payload}
	default:
		t.Fatalf("unknown test boot role %q", bootRole)
	}
	return []recipePackageRoleFixture{image, modules}
}

// recipePackageRoleTar builds a real compressed tar member entirely in Go. It
// can retain duplicate names for deliberate archive-cardinality regressions.
func recipePackageRoleTar(t *testing.T, members []recipeTarMember) []byte {
	t.Helper()
	var compressed bytes.Buffer
	zipper := gzip.NewWriter(&compressed)
	archive := tar.NewWriter(zipper)
	for _, member := range members {
		kind := member.typeflag
		if kind == 0 {
			kind = tar.TypeReg
		}
		size := int64(len(member.data))
		if kind != tar.TypeReg && kind != tar.TypeRegA {
			size = 0
		}
		if err := archive.WriteHeader(&tar.Header{Name: member.name, Mode: 0o755, Typeflag: kind, Size: size, Linkname: member.linkname}); err != nil {
			t.Fatal(err)
		}
		if size > 0 {
			if _, err := archive.Write(member.data); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zipper.Close(); err != nil {
		t.Fatal(err)
	}
	return compressed.Bytes()
}

// recipePackageRoleDeb constructs the standard three-member ar envelope rather
// than substituting mocks for dpkg-deb or running a package build/install hook.
func recipePackageRoleDeb(t *testing.T, fixture recipePackageRoleFixture) []byte {
	t.Helper()
	control := fmt.Sprintf("Package: %s\nVersion: %s\nArchitecture: %s\nMaintainer: Lexr Test <test@example.invalid>\nDescription: inert package-role regression fixture\n",
		fixture.packageName, fixture.version, fixture.architecture)
	controls := append([]recipeTarMember{{name: "./control", data: []byte(control)}}, fixture.controls...)
	var output bytes.Buffer
	output.WriteString("!<arch>\n")
	for _, member := range []struct {
		name string
		data []byte
	}{
		{name: "debian-binary", data: []byte("2.0\n")},
		{name: "control.tar.gz", data: recipePackageRoleTar(t, controls)},
		{name: "data.tar.gz", data: recipePackageRoleTar(t, fixture.payload)},
	} {
		header := fmt.Sprintf("%-16s%-12d%-6d%-6d%-8o%-10d`\n", member.name+"/", 0, 0, 0, 0o644, len(member.data))
		if len(header) != 60 {
			t.Fatalf("invalid test ar header length %d", len(header))
		}
		output.WriteString(header)
		output.Write(member.data)
		if len(member.data)%2 != 0 {
			output.WriteByte('\n')
		}
	}
	return output.Bytes()
}

// TestRecipePackageRoleArchiveEnvelope checks the synthetic archive machinery
// even on hosts which cannot run the Linux dpkg-deb integration below.
func TestRecipePackageRoleArchiveEnvelope(t *testing.T) {
	fixture := recipePackageRoleFixtures(t, "upstream729", "modules")[1]
	archive := bytes.NewReader(recipePackageRoleDeb(t, fixture))
	magic := make([]byte, 8)
	if _, err := io.ReadFull(archive, magic); err != nil || string(magic) != "!<arch>\n" {
		t.Fatalf("invalid ar magic %q: %v", magic, err)
	}
	for _, name := range []string{"debian-binary", "control.tar.gz", "data.tar.gz"} {
		header := make([]byte, 60)
		if _, err := io.ReadFull(archive, header); err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(string(header[:16])) != name+"/" || string(header[58:]) != "`\n" {
			t.Fatalf("unexpected ar header %q", header)
		}
		size, err := strconv.Atoi(strings.TrimSpace(string(header[48:58])))
		if err != nil || size < 0 || size > archive.Len() {
			t.Fatalf("invalid ar member size %d: %v", size, err)
		}
		data := make([]byte, size)
		if _, err := io.ReadFull(archive, data); err != nil {
			t.Fatal(err)
		}
		if size%2 != 0 {
			if padding, err := archive.ReadByte(); err != nil || padding != '\n' {
				t.Fatalf("invalid ar padding %q: %v", padding, err)
			}
		}
		if name == "debian-binary" {
			if string(data) != "2.0\n" {
				t.Fatalf("unexpected Debian format marker %q", data)
			}
			continue
		}
		zipper, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		tarball := tar.NewReader(zipper)
		var members []string
		for {
			member, err := tarball.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			members = append(members, member.Name)
		}
		if err := zipper.Close(); err != nil {
			t.Fatal(err)
		}
		if name == "data.tar.gz" && (len(members) != 1 || members[0] != "./boot/vmlinuz-"+recipePackageRoleABI) {
			t.Fatalf("unexpected modules payload members %q", members)
		}
		if name == "control.tar.gz" && strings.Contains(strings.Join(members, "\n"), "triggers") {
			t.Fatalf("module fixture must not impersonate the image trigger: %q", members)
		}
	}
	if archive.Len() != 0 {
		t.Fatal("unexpected trailing ar data")
	}
}

// requireRecipePackageRoleTools makes unavailable native tool coverage visible;
// an ARM64 qualification run must execute these cases with no skips.
func requireRecipePackageRoleTools(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"bash", "dpkg-deb", "tar", "awk", "cmp", "python3", "basename", "wc"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("actual package-role recipe integration requires %s: %v", tool, err)
		}
	}
}

// executeRecipePackageRoles executes the exact compiled package scan, identity
// checks, closed Python lifecycle validator and boot extraction. It stops before
// PE validation: all payload bytes are inert, and no maintainer script runs.
func executeRecipePackageRoles(t *testing.T, fixtures []recipePackageRoleFixture) ([]byte, error) {
	t.Helper()
	requireRecipePackageRoleTools(t)
	const startMarker = "member_list=\"$inspection_root/package-members\"\n"
	const endMarker = "\nif [ ! -s \"$inspection_image\" ] || ! objdump -h"
	if strings.Count(containerRecipe, startMarker) != 1 || strings.Count(containerRecipe, endMarker) != 1 {
		t.Fatal("recipe package-role boundary must be unique")
	}
	start, end := strings.Index(containerRecipe, startMarker), strings.Index(containerRecipe, endMarker)
	if end <= start {
		t.Fatal("recipe package-role boundary is reversed")
	}
	root := t.TempDir()
	artifacts, inspection := filepath.Join(root, "artifacts"), filepath.Join(root, "inspection")
	for _, directory := range []string{artifacts, inspection} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, fixture := range fixtures {
		name := filepath.Join(artifacts, fixture.filename)
		file, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write(recipePackageRoleDeb(t, fixture)); err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "bash", "-s", "--", artifacts, inspection)
	command.Stdin = strings.NewReader("set -euo pipefail\nartifact_dir=$1\ninspection_root=$2\n" + containerRecipe[start:end] +
		"\nprintf 'ROLE_RESULT\\t%s\\t%s\\t%s\\t%s\\n' \"$abi\" \"$(basename \"$boot_payload_package\")\" \"$(basename \"$lifecycle_image_package\")\" \"$(basename \"$lifecycle_modules_package\")\"\n")
	output, err := command.CombinedOutput()
	if err == nil {
		payload, readErr := os.ReadFile(filepath.Join(inspection, "vmlinuz"))
		if readErr != nil || string(payload) != recipePackageRolePayload {
			t.Fatalf("selected boot payload = %q, %v", payload, readErr)
		}
	}
	return output, err
}

// TestRecipePackageRolesAcceptSplitPayloadAndLifecycle exercises both actual
// package layouts and both pinned lifecycle families through real Debian files.
func TestRecipePackageRolesAcceptSplitPayloadAndLifecycle(t *testing.T) {
	requireRecipePackageRoleTools(t)
	for _, family := range []string{"legacy", "upstream729"} {
		for _, bootRole := range []string{"image", "modules"} {
			t.Run(family+"/"+bootRole, func(t *testing.T) {
				fixtures := recipePackageRoleFixtures(t, family, bootRole)
				output, err := executeRecipePackageRoles(t, fixtures)
				if err != nil {
					t.Fatalf("actual package-role recipe rejected: %v\n%s", err, output)
				}
				boot := 0
				if bootRole == "modules" {
					boot = 1
				}
				wanted := strings.Join([]string{"ROLE_RESULT", recipePackageRoleABI, fixtures[boot].filename, fixtures[0].filename, fixtures[1].filename}, "\t") + "\n"
				if string(output) != wanted {
					t.Fatalf("roles = %q, want %q", output, wanted)
				}
			})
		}
	}
}

// TestRecipePackageRolesRejectIdentityAndOwnershipChanges retains closed role,
// version, ABI, payload and lifecycle checks despite the Stubble split layout.
func TestRecipePackageRolesRejectIdentityAndOwnershipChanges(t *testing.T) {
	requireRecipePackageRoleTools(t)
	tests := []struct {
		name   string
		mutate func([]recipePackageRoleFixture) []recipePackageRoleFixture
		want   string
	}{
		{name: "module identity in image filename", mutate: func(f []recipePackageRoleFixture) []recipePackageRoleFixture {
			f[0].packageName = f[1].packageName
			return f
		}, want: "filename does not match"},
		{name: "metadata version differs from filename", mutate: func(f []recipePackageRoleFixture) []recipePackageRoleFixture { f[1].version += "-other"; return f }, want: "filename does not match"},
		{name: "wrong architecture", mutate: func(f []recipePackageRoleFixture) []recipePackageRoleFixture { f[1].architecture = "amd64"; return f }, want: "filename does not match"},
		{name: "mixed coherent filenames but different versions", mutate: func(f []recipePackageRoleFixture) []recipePackageRoleFixture {
			f[1].version += "-other"
			f[1].filename = f[1].packageName + "_" + f[1].version + "_arm64.deb"
			return f
		}, want: "exact boot ABI and package version"},
		{name: "different image ABI", mutate: func(f []recipePackageRoleFixture) []recipePackageRoleFixture {
			f[0].packageName = "linux-image-7.2.9-jg-0sp11v2-qcom-x1e"
			f[0].filename = f[0].packageName + "_" + f[0].version + "_arm64.deb"
			return f
		}, want: "exact boot ABI and package version"},
		{name: "missing boot payload", mutate: func(f []recipePackageRoleFixture) []recipePackageRoleFixture { f[1].payload = nil; return f }, want: "exactly one packaged /boot/vmlinuz"},
		{name: "two payload packages", mutate: func(f []recipePackageRoleFixture) []recipePackageRoleFixture { f[0].payload = f[1].payload; return f }, want: "exactly one packaged /boot/vmlinuz"},
		{name: "duplicate payload archive member", mutate: func(f []recipePackageRoleFixture) []recipePackageRoleFixture {
			f[1].payload = append(f[1].payload, f[1].payload[0])
			return f
		}, want: "exactly one packaged /boot/vmlinuz"},
		{name: "wrong payload ABI", mutate: func(f []recipePackageRoleFixture) []recipePackageRoleFixture {
			f[1].payload[0].name = "./boot/vmlinuz-7.2.9-jg-0sp11v2-qcom-x1e"
			return f
		}, want: "exact boot ABI and package version"},
		{name: "extra image package", mutate: func(f []recipePackageRoleFixture) []recipePackageRoleFixture {
			extra := f[0]
			extra.version += "-other"
			extra.filename = extra.packageName + "_" + extra.version + "_arm64.deb"
			return append(f, extra)
		}, want: "exactly one packaged /boot/vmlinuz"},
		{name: "missing postinst", mutate: func(f []recipePackageRoleFixture) []recipePackageRoleFixture {
			f[0].controls = f[0].controls[1:]
			return f
		}, want: "exactly one postinst, postrm, and triggers"},
		{name: "missing postrm", mutate: func(f []recipePackageRoleFixture) []recipePackageRoleFixture {
			f[0].controls = append(f[0].controls[:1], f[0].controls[2:]...)
			return f
		}, want: "exactly one postinst, postrm, and triggers"},
		{name: "missing image trigger despite module impersonation", mutate: func(f []recipePackageRoleFixture) []recipePackageRoleFixture {
			f[1].controls = append([]recipeTarMember(nil), f[0].controls...)
			f[0].controls = f[0].controls[:2]
			return f
		}, want: "exactly one postinst, postrm, and triggers"},
		{name: "missing image hooks despite complete module impersonation", mutate: func(f []recipePackageRoleFixture) []recipePackageRoleFixture {
			f[1].controls = append([]recipeTarMember(nil), f[0].controls...)
			f[0].controls = nil
			return f
		}, want: "exactly one postinst, postrm, and triggers"},
		{name: "duplicate postinst alias", mutate: func(f []recipePackageRoleFixture) []recipePackageRoleFixture {
			duplicate := f[0].controls[0]
			duplicate.name = "postinst"
			f[0].controls = append(f[0].controls, duplicate)
			return f
		}, want: "exactly one postinst, postrm, and triggers"},
		{name: "empty image script", mutate: func(f []recipePackageRoleFixture) []recipePackageRoleFixture { f[0].controls[0].data = nil; return f }, want: "empty or exceeds the inspection bound"},
		{name: "oversized image script", mutate: func(f []recipePackageRoleFixture) []recipePackageRoleFixture {
			f[0].controls[1].data = []byte(strings.Repeat("x", recipeKernelHookScriptBound+1))
			return f
		}, want: "empty or exceeds the inspection bound"},
		{name: "wrong image trigger ABI", mutate: func(f []recipePackageRoleFixture) []recipePackageRoleFixture {
			f[0].controls[2].data = []byte("interest linux-update-7.2.9-jg-0sp11v2-qcom-x1e\n")
			return f
		}, want: "does not own the exact ABI update trigger"},
		{name: "wrong image hook ABI", mutate: func(f []recipePackageRoleFixture) []recipePackageRoleFixture {
			f[0].controls[0].data = bytes.Replace(f[0].controls[0].data, []byte("version="+recipePackageRoleABI), []byte("version=7.2.9-jg-0sp11v2-qcom-x1e"), 1)
			return f
		}, want: "maintainer contract rejected"},
		{name: "unsupported image hook operation", mutate: func(f []recipePackageRoleFixture) []recipePackageRoleFixture {
			f[0].controls[0].data = append(f[0].controls[0].data, []byte("echo unsupported-operation\n")...)
			return f
		}, want: "maintainer contract rejected"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixtures := test.mutate(recipePackageRoleFixtures(t, "upstream729", "modules"))
			output, err := executeRecipePackageRoles(t, fixtures)
			if err == nil || !strings.Contains(string(output), test.want) {
				t.Fatalf("expected %q rejection, got %v:\n%s", test.want, err, output)
			}
		})
	}
}
