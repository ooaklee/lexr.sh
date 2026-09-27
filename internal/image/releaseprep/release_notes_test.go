package releaseprep

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestValidateReleaseNotesCompatibility exercises both supported note formats
// and rejects altered guidance even when its release checksum is recomputed.
func TestValidateReleaseNotesCompatibility(t *testing.T) {
	fixture := newFixture(t)
	manager := New(fakeValidator{report: fixture.report}, fakeCompressor{})
	receipt, err := manager.Prepare(context.Background(), Request{
		RepositoryRoot: fixture.root, ImagePath: fixture.image,
		ReleaseName: "notes-compatibility", PartSizeBytes: 31,
	})
	if err != nil {
		t.Fatal(err)
	}
	directory := receipt.Plan.OutputDirectory
	original, err := manager.Validate(context.Background(), directory)
	if err != nil || !original.Valid {
		t.Fatalf("Validate() rejected the freshly prepared release: %v", err)
	}
	notesPath := filepath.Join(directory, NotesName)
	notesData, err := os.ReadFile(notesPath)
	if err != nil {
		t.Fatal(err)
	}
	currentNotes := string(notesData)
	if strings.HasPrefix(currentNotes, "# ") {
		t.Fatal("Prepare() generated release notes with a leading top-level heading")
	}
	if !strings.Contains(currentNotes, fixture.report.KernelABI) {
		t.Fatal("Prepare() generated release notes without the fixture kernel ABI")
	}
	// This golden file pins newFixture's output from the 0.5.0-rc.3 renderer;
	// it must remain independent of subsequent release-note renderer changes.
	historicalData, err := os.ReadFile(filepath.Join("testdata", "release-notes-rc3.golden"))
	if err != nil {
		t.Fatal(err)
	}
	historicalNotes := string(historicalData)
	const historicalHeading = "# Surface Pro 11 ARM64 installation image\n\n"
	if !strings.HasPrefix(historicalNotes, historicalHeading) {
		t.Fatal("historical release notes fixture lacks the original heading")
	}
	historicalCore := strings.TrimPrefix(historicalNotes, historicalHeading)
	if !strings.HasPrefix(currentNotes, historicalCore) {
		t.Fatal("Prepare() changed the original release notes core")
	}
	currentFooter := strings.TrimPrefix(currentNotes, historicalCore)
	const windowsGuideURL = "https://github.com/ooaklee/lexr.sh/blob/main/docs/user-guide/windows-image-usb.md"
	if !strings.Contains(currentFooter, "[Windows image download and USB guide]("+windowsGuideURL+")") {
		t.Fatal("Prepare() generated release notes without the Windows guide link")
	}
	changedCurrentBody := strings.Replace(currentNotes, fixture.report.KernelABI, "2.0-tampered-qcom-x1e", 1)
	changedHistoricalBody := strings.Replace(historicalNotes, fixture.report.KernelABI, "2.0-tampered-qcom-x1e", 1)
	changedFooter := strings.Replace(currentFooter, windowsGuideURL, "https://example.invalid/windows-guide", 1)
	tests := []struct {
		name  string
		notes string
		valid bool
	}{
		{name: "current", notes: currentNotes, valid: true},
		{name: "historical-rc3", notes: historicalNotes, valid: true},
		{name: "arbitrary-heading", notes: "# Different installation image\n\n" + currentNotes},
		{name: "missing-historical-blank-line", notes: strings.TrimSuffix(historicalHeading, "\n") + historicalCore},
		{name: "repeated-historical-heading", notes: historicalHeading + historicalNotes},
		{name: "changed-current-body", notes: changedCurrentBody},
		{name: "changed-historical-body", notes: changedHistoricalBody},
		{name: "missing-current-footer", notes: historicalCore},
		{name: "altered-current-footer", notes: historicalCore + changedFooter},
		{name: "historical-with-current-footer", notes: historicalNotes + currentFooter},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := os.WriteFile(notesPath, []byte(test.notes), 0o644); err != nil {
				t.Fatal(err)
			}
			rewriteReleaseChecksums(t, directory)
			before := directoryBytes(t, directory)
			result, err := manager.Validate(context.Background(), directory)
			if test.valid {
				if err != nil {
					t.Fatalf("Validate() rejected supported release notes: %v", err)
				}
				if !result.Valid || !reflect.DeepEqual(result.Manifest, original.Manifest) {
					t.Fatalf("Validate() result = %#v", result)
				}
			} else {
				if err == nil || result.Valid {
					t.Fatal("Validate() accepted altered release notes with recomputed checksums")
				}
				if !strings.Contains(err.Error(), "release notes differ from the manifest-derived contract") {
					t.Fatalf("Validate() failed before checking the release notes contract: %v", err)
				}
			}
			if !reflect.DeepEqual(before, directoryBytes(t, directory)) {
				t.Fatal("Validate() changed release-directory contents")
			}
		})
	}
}
