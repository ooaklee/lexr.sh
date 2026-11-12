package packageplan

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/sensors/build"
)

// TestSavedNativeArchivePlanOptIn replays retained bytes through the unchanged
// strict consumer before deriving the inactive layout. It performs bounded
// regular-file reads only: no extraction, program execution or native build.
func TestSavedNativeArchivePlanOptIn(t *testing.T) {
	archivePath := os.Getenv("LEXR_TEST_SENSOR_ARTIFACT_ARCHIVE")
	expectedSHA256 := os.Getenv("LEXR_TEST_SENSOR_ARTIFACT_SHA256")
	if archivePath == "" && expectedSHA256 == "" {
		t.Skip("explicit saved sensor artifact archive and SHA256 opt-in required")
	}
	if archivePath == "" || expectedSHA256 == "" {
		t.Fatal("both saved sensor artifact archive and SHA256 are required")
	}
	digest, err := hex.DecodeString(expectedSHA256)
	if err != nil || len(digest) != 32 || hex.EncodeToString(digest) != expectedSHA256 {
		t.Fatal("saved archive SHA256 must be 64 lowercase hexadecimal digits")
	}
	const maximum = build.MaximumArchiveBytes + (64 << 10)
	before, err := os.Lstat(archivePath)
	if err != nil || !before.Mode().IsRegular() || before.Size() <= 0 || before.Size() > maximum {
		t.Fatal("saved archive must be a bounded nonempty regular file")
	}
	file, err := os.Open(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) || opened.Size() != before.Size() || !opened.ModTime().Equal(before.ModTime()) {
		t.Fatal("saved archive identity changed before reading")
	}
	data, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		t.Fatal(err)
	}
	after, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	current, err := os.Lstat(archivePath)
	if err != nil || len(data) != int(opened.Size()) || len(data) > maximum || !os.SameFile(opened, after) || !os.SameFile(after, current) || !current.Mode().IsRegular() || after.Size() != opened.Size() || current.Size() != opened.Size() || !after.ModTime().Equal(opened.ModTime()) || !current.ModTime().Equal(opened.ModTime()) {
		t.Fatal("saved archive changed while reading")
	}
	actualDigest := sha256.Sum256(data)
	if hex.EncodeToString(actualDigest[:]) != expectedSHA256 {
		t.Fatal("saved archive SHA256 differs")
	}
	verified, err := build.DecodeDockerCopy(data, 2)
	if err != nil {
		t.Fatalf("saved source artifact validation failed: %v", err)
	}
	plan, err := FromVerifiedArtifacts(verified)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := plan.Snapshot()
	if err != nil || len(snapshot.Files) != 9 || len(snapshot.Aliases) != 3 || len(snapshot.DevelopmentOnlyArtefacts) != 7 || len(snapshot.ExcludedGlobalInstallTargets) != 6 || len(snapshot.RequiredSourceMetadata) != 4 || len(snapshot.RequiredCurrentRecipes) != 3 || len(snapshot.PendingGates) != 7 || snapshot.Status != Status || snapshot.InstallationEnabled || snapshot.ActivationEnabled {
		t.Fatal("saved archive produced an incomplete or executable runtime plan")
	}
	t.Log("retained bytes passed unchanged verification and produced an inactive private layout; no package, installation, activation or physical qualification was performed")
}
