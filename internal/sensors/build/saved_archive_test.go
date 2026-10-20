package build

import (
	"encoding/hex"
	"io"
	"os"
	"testing"
)

// TestSavedNativeArtifactArchiveOptIn replays existing bytes through the public
// consumer, including its real licence, ELF, provider and loader checks. It
// does not extract files, run the contained programs, build, publish or install
// anything. A pass is host-side artifact validation, not a new native build or
// physical sensor qualification.
func TestSavedNativeArtifactArchiveOptIn(t *testing.T) {
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

	const maximum = MaximumArchiveBytes + (64 << 10)
	before, err := os.Lstat(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if !before.Mode().IsRegular() || before.Size() <= 0 || before.Size() > maximum {
		t.Fatal("saved archive must be a bounded nonempty regular file")
	}
	f, err := os.Open(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if !opened.Mode().IsRegular() || !os.SameFile(before, opened) || opened.Size() != before.Size() || !opened.ModTime().Equal(before.ModTime()) {
		t.Fatal("saved archive identity changed before reading")
	}
	data, err := io.ReadAll(io.LimitReader(f, maximum+1))
	if err != nil {
		t.Fatal(err)
	}
	after, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	current, err := os.Lstat(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != int(opened.Size()) || len(data) > maximum || !os.SameFile(opened, after) || !os.SameFile(after, current) || !current.Mode().IsRegular() || after.Size() != opened.Size() || current.Size() != opened.Size() || !after.ModTime().Equal(opened.ModTime()) || !current.ModTime().Equal(opened.ModTime()) {
		t.Fatal("saved archive changed while reading")
	}
	if got := bytesDigest(data); got != expectedSHA256 {
		t.Fatalf("saved archive SHA256 differs: got %s", got)
	}

	verified, err := DecodeDockerCopy(data, 2)
	if err != nil {
		t.Fatalf("saved source artifact validation failed: %v", err)
	}
	if verified == nil || len(verified.Files()) != 16 || len(verified.Links()) != 3 {
		t.Fatal("saved source artifact verification is incomplete")
	}
	report := verified.Report()
	if report.SourceCommit != SourceCommit || report.Jobs != 2 {
		t.Fatal("saved source artifact composition differs")
	}
	t.Log("saved bytes passed the public host consumer; no native build, publication, installation or hardware test was performed")
}
