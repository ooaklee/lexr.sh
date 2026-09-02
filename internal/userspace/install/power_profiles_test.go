package install

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/platform"
)

// TestPowerProfilesDryRunAndInstall verifies the no-from input contract,
// private staging, exact apt transaction, and bounded daemon activation.
func TestPowerProfilesDryRunAndInstall(t *testing.T) {
	repository, packageContent := makePowerProfilesRepository(t)
	runner := &fakeRunner{}
	installer := New(runner)
	installer.euid = func() int { return 501 }

	dryRun, err := installer.PowerProfiles(context.Background(), Options{
		RepositoryRoot: repository, Root: "/", DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if dryRun.Component != PowerProfilesComponent || dryRun.Command == nil || dryRun.Command.Name != "apt-get" ||
		len(dryRun.Commands) != 1 || dryRun.Commands[0].Name != "/usr/bin/systemctl" || len(runner.commands) != 0 {
		t.Fatalf("dry-run result = %#v, commands = %#v", dryRun, runner.commands)
	}

	installer.euid = func() int { return 0 }
	runner.inspect = func(command platform.Command) error {
		if command.Name != "apt-get" {
			return nil
		}
		staged := command.Args[len(command.Args)-1]
		content, err := os.ReadFile(staged)
		if err != nil {
			return err
		}
		if string(content) != string(packageContent) {
			return fmt.Errorf("staged package content changed")
		}
		return nil
	}
	result, err := installer.PowerProfiles(context.Background(), Options{RepositoryRoot: repository, Root: "/"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.FilesInstalled || !result.ActivationRequired || !result.ActivationComplete || len(runner.commands) != 2 {
		t.Fatalf("install result = %#v, commands = %#v", result, runner.commands)
	}
	if runner.commands[0].Name != "apt-get" || runner.commands[1].Name != "/usr/bin/systemctl" {
		t.Fatalf("commands = %#v", runner.commands)
	}
}

// TestPowerProfilesRejectsChangedPackage proves a matching filename and
// self-authored checksum file cannot replace compiled authority.
func TestPowerProfilesRejectsChangedPackage(t *testing.T) {
	repository, _ := makePowerProfilesRepository(t)
	packagePath := filepath.Join(repository, filepath.FromSlash(powerProfilesBuildPath), powerProfilesPackageName)
	if err := os.WriteFile(packagePath, []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	installer := New(&fakeRunner{})
	_, err := installer.PowerProfiles(context.Background(), Options{
		RepositoryRoot: repository, Root: "/", DryRun: true,
	})
	if err == nil || !strings.Contains(err.Error(), "identity mismatch") {
		t.Fatalf("error = %v", err)
	}
}

// makePowerProfilesRepository constructs one independently pinned synthetic output.
func makePowerProfilesRepository(t *testing.T) (string, []byte) {
	t.Helper()
	originalSpec := powerProfilesSpec
	originalSource := powerProfilesSourceIdentity
	t.Cleanup(func() {
		powerProfilesSpec = originalSpec
		powerProfilesSourceIdentity = originalSource
	})
	repository := t.TempDir()
	sourceContent := []byte("fixture power profiles source authority\n")
	sourcePath := filepath.Join(repository, filepath.FromSlash(powerProfilesSourceIdentityPath))
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourcePath, sourceContent, 0o644); err != nil {
		t.Fatal(err)
	}
	powerProfilesSourceIdentity = immutableFile{
		name: powerProfilesSourceIdentityPath, sha256: digestBytes(sourceContent), size: int64(len(sourceContent)),
	}

	packageContent := []byte("fixture arm64 deb\n")
	buildinfo := []byte("fixture buildinfo\n")
	changes := []byte("fixture changes\n")
	files := []immutableFile{
		{name: "power-profiles-daemon_0.30-2+sp11.1_arm64.buildinfo", sha256: digestBytes(buildinfo), size: int64(len(buildinfo))},
		{name: "power-profiles-daemon_0.30-2+sp11.1_arm64.changes", sha256: digestBytes(changes), size: int64(len(changes))},
		{name: powerProfilesPackageName, sha256: digestBytes(packageContent), size: int64(len(packageContent))},
	}
	var sums strings.Builder
	contents := map[string][]byte{
		files[0].name: buildinfo,
		files[1].name: changes,
		files[2].name: packageContent,
	}
	for _, file := range files {
		_, _ = fmt.Fprintf(&sums, "%s  %s\n", file.sha256, file.name)
	}
	checksumContent := []byte(sums.String())
	powerProfilesSpec = releaseSpec{
		component: PowerProfilesComponent,
		tag:       "fixture",
		files: append([]immutableFile{{
			name: "SHA256SUMS", sha256: digestBytes(checksumContent), size: int64(len(checksumContent)),
		}}, files...),
	}
	buildDirectory := filepath.Join(repository, filepath.FromSlash(powerProfilesBuildPath))
	if err := os.MkdirAll(buildDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	contents["SHA256SUMS"] = checksumContent
	for name, content := range contents {
		if err := os.WriteFile(filepath.Join(buildDirectory, name), content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return repository, packageContent
}

// digestBytes returns the lowercase authority representation used by fixtures.
func digestBytes(content []byte) string {
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:])
}
