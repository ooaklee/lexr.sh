package image

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPublishISOOutputsPublishesExactSetWithoutStagingResidue verifies the
// public wrapper commits one exact three-member set and consumes every stage.
func TestPublishISOOutputsPublishesExactSetWithoutStagingResidue(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	isoBytes := []byte("validated ISO bytes\x00")
	manifestBytes := []byte("{\n  \"manifest\": true\n}\n")
	journalBytes := []byte("{\n  \"journal\": true\n}\n")
	sourceISO := filepath.Join(directory, "partial.iso")
	destinationISO := filepath.Join(directory, "fedora-surface.iso")
	if err := os.WriteFile(sourceISO, isoBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	manifestPath, journalPath, err := PublishISOOutputs(
		sourceISO, destinationISO, manifestBytes, journalBytes, IdentifyBytes(isoBytes),
	)
	if err != nil {
		t.Fatal(err)
	}
	for path, expected := range map[string][]byte{
		destinationISO: isoBytes,
		manifestPath:   manifestBytes,
		journalPath:    journalBytes,
	} {
		actual, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(actual, expected) {
			t.Fatalf("published bytes at %s = %q, want %q", path, actual, expected)
		}
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".fedora-surface.iso.") {
			t.Fatalf("private staging entry remains after success: %s", entry.Name())
		}
	}
	actualSource, err := os.ReadFile(sourceISO)
	if err != nil || !bytes.Equal(actualSource, isoBytes) {
		t.Fatalf("source ISO changed after publication: bytes=%q error=%v", actualSource, err)
	}
}

// TestPublishISOOutputsRejectsExistingFinalsBeforeStaging proves no existing
// final member is replaced or joined into a new output set.
func TestPublishISOOutputsRejectsExistingFinalsBeforeStaging(t *testing.T) {
	t.Parallel()

	for _, suffix := range []string{"", ".manifest.json", ".journal.json"} {
		suffix := suffix
		t.Run(suffix, func(t *testing.T) {
			t.Parallel()
			directory := t.TempDir()
			isoBytes := []byte("ISO")
			sourceISO := filepath.Join(directory, "partial.iso")
			destinationISO := filepath.Join(directory, "output.iso")
			if err := os.WriteFile(sourceISO, isoBytes, 0o600); err != nil {
				t.Fatal(err)
			}
			existingPath := destinationISO + suffix
			existingBytes := []byte("preserve me")
			if err := os.WriteFile(existingPath, existingBytes, 0o600); err != nil {
				t.Fatal(err)
			}

			_, _, err := PublishISOOutputs(
				sourceISO, destinationISO, []byte("manifest"), []byte("journal"), IdentifyBytes(isoBytes),
			)
			if err == nil || !strings.Contains(err.Error(), "already exists") {
				t.Fatalf("PublishISOOutputs() error = %v, want existing-destination rejection", err)
			}
			actual, readErr := os.ReadFile(existingPath)
			if readErr != nil || !bytes.Equal(actual, existingBytes) {
				t.Fatalf("existing publication changed: bytes=%q error=%v", actual, readErr)
			}
		})
	}
}

// TestPublishISOOutputsRetainsSafeRecoveryEvidenceOnIdentityMismatch proves a
// byte mismatch exposes no final while retaining a descriptor-owned recovery object.
func TestPublishISOOutputsRetainsSafeRecoveryEvidenceOnIdentityMismatch(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	sourceISO := filepath.Join(directory, "partial.iso")
	destinationISO := filepath.Join(directory, "output.iso")
	if err := os.WriteFile(sourceISO, []byte("actual ISO"), 0o600); err != nil {
		t.Fatal(err)
	}

	manifestPath, journalPath, err := PublishISOOutputs(
		sourceISO,
		destinationISO,
		[]byte("manifest"),
		[]byte("journal"),
		IdentifyBytes([]byte("different validated ISO")),
	)
	if err == nil || !strings.Contains(err.Error(), "staging residue retained") {
		t.Fatalf("PublishISOOutputs() error = %v, want mismatch and recovery evidence", err)
	}
	if manifestPath != "" || journalPath != "" {
		t.Fatalf("failed publication returned sidecars %q, %q", manifestPath, journalPath)
	}
	for _, path := range []string{destinationISO, destinationISO + ".manifest.json", destinationISO + ".journal.json"} {
		if _, statErr := os.Lstat(path); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("unexpected final publication at %s: %v", path, statErr)
		}
	}
	entries, readErr := os.ReadDir(directory)
	if readErr != nil {
		t.Fatal(readErr)
	}
	foundRecoveryEntry := false
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".output.iso.image-") {
			foundRecoveryEntry = true
		}
	}
	if !foundRecoveryEntry {
		t.Fatal("identity mismatch did not retain the transaction staging object")
	}
}

// TestRequireAbsentPublicationRejectsEveryExistingObject verifies even a
// dangling symbolic link occupies and protects a requested destination.
func TestRequireAbsentPublicationRejectsEveryExistingObject(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	target := filepath.Join(directory, "output.iso")
	if err := os.Symlink("missing-target", target); err != nil {
		t.Fatal(err)
	}
	if err := RequireAbsentPublication(target, "output ISO"); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("RequireAbsentPublication() error = %v, want symlink rejection", err)
	}
}
