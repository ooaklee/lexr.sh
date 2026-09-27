package releaseprep

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	imagecontract "github.com/ooaklee/lexr.sh/internal/image"
	"github.com/ooaklee/lexr.sh/internal/image/fedora"
	"github.com/ooaklee/lexr.sh/internal/plan"
)

// newFedoraJournalFixture supplies the real producer's complete checkpoint order
// and typed media evidence with the historical contextual digest-map entries.
func newFedoraJournalFixture(t *testing.T) (fixture, imagecontract.Manifest, plan.Journal) {
	t.Helper()
	fixture := newFixture(t)
	manifest := readFixtureManifest(t, fixture.image)
	manifest.Adapter = fedora.AdapterID
	manifest.KernelBundle.ABI = "7.2.0-jg-0sp11v23-qcom-x1e"
	manifest.KernelBundle.Version = "7.2.0-jg-0sp11v23"
	manifest.BootArtifacts.Kernel.Path = "boot/aarch64/loader/linux"
	manifest.BootArtifacts.Initrd.Path = "boot/aarch64/loader/initrd"
	manifest.MediaDiscovery = imagecontract.MediaDiscoveryRecord{
		Strategy: "direct-hybrid-iso", Protocol: "dracut-live",
		Evidence: []imagecontract.MediaDiscoveryEvidence{
			{Role: "iso-volume-label", Scope: "iso9660-pvd", Value: fedora.SourceVolumeID},
			{Role: "grub-search-marker", Scope: "grub", Value: "/boot/0x12345678"},
			{Role: "live-root", Scope: "grub", Path: "boot/grub2/grub.cfg", Value: "root=live:CDLABEL=" + fedora.SourceVolumeID + " rd.live.image"},
		},
	}
	for _, artifact := range []struct {
		role string
		path string
	}{
		{role: "installed-kernel-rpm", path: "sp11/fedora/lexr-kernel-sp11.aarch64.rpm"},
		{role: "stock-fallback-kernel", path: "boot/aarch64/loader/linux-fedora"},
		{role: "stock-fallback-initramfs", path: "boot/aarch64/loader/initrd-fedora"},
	} {
		manifest.MediaDiscovery.Evidence = append(manifest.MediaDiscovery.Evidence, imagecontract.MediaDiscoveryEvidence{
			Role: artifact.role, Scope: "iso9660", Path: artifact.path,
			Artifact: &imagecontract.ArtifactRecord{Path: artifact.path, SHA256: strings.Repeat("a", 64), Size: 1},
		})
	}
	operation, err := fedora.BuildPlan(fedora.Request{
		SourceISO: "source.iso", OutputISO: fixture.image, Bundle: manifest.KernelBundle,
	})
	if err != nil {
		t.Fatal(err)
	}
	journal := plan.NewJournal("image.create")
	for index, step := range operation.Steps {
		record := plan.StepRecord{
			StepID: step.ID, CompletedAt: time.Date(2026, 9, 27, 12, 0, index, 0, time.UTC),
		}
		switch step.ID {
		case "verify-source":
			record.Digests = map[string]string{"source.iso": manifest.SourceImage.SHA256}
		case "bind-live-media":
			record.Digests = map[string]string{
				"iso-volume-label": fedora.SourceVolumeID, "grub-search-marker": "/boot/0x12345678",
			}
		case "validate-output", "publish-output":
			record.Digests = map[string]string{"output.iso": fixture.report.SHA256}
		}
		journal.Records = append(journal.Records, record)
	}
	journal.Output = &plan.OutputRecord{Path: fixture.image, SHA256: fixture.report.SHA256, Size: fixture.report.Size}
	return fixture, manifest, *journal
}

// fedoraJournalBindRecord locates the producer checkpoint without assuming an
// index that could silently shift when the producer changes its workflow.
func fedoraJournalBindRecord(t *testing.T, journal *plan.Journal) *plan.StepRecord {
	t.Helper()
	for index := range journal.Records {
		if journal.Records[index].StepID == "bind-live-media" {
			return &journal.Records[index]
		}
	}
	t.Fatal("Fedora fixture lacks its media-binding checkpoint")
	return nil
}

// writeFedoraJournalFixture stores the selected private inputs and refreshes
// fake structural evidence to bind it to the exact adjacent manifest bytes.
func writeFedoraJournalFixture(t *testing.T, fixture *fixture, manifest imagecontract.Manifest, journal plan.Journal) []byte {
	t.Helper()
	rewriteFixtureManifest(t, fixture.image, manifest)
	manifestData, err := os.ReadFile(fixture.image + ".manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	manifestDigest := sha256.Sum256(manifestData)
	fixture.report.Adapter = manifest.Adapter
	fixture.report.KernelABI = manifest.KernelBundle.ABI
	fixture.report.ManifestSHA256 = hex.EncodeToString(manifestDigest[:])
	fixture.report.ManifestSize = int64(len(manifestData))
	data, err := json.MarshalIndent(journal, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(fixture.image+".journal.json", data, 0o644); err != nil {
		t.Fatal(err)
	}
	return data
}

// TestPrepareAcceptsFedoraJournalMediaEvidence preserves original journal
// provenance while projecting only genuine digests from legacy and current runs.
func TestPrepareAcceptsFedoraJournalMediaEvidence(t *testing.T) {
	for _, test := range []struct {
		name       string
		legacy     bool
		bindDigest bool
	}{
		{name: "legacy-context-only", legacy: true},
		{name: "legacy-with-digest", legacy: true, bindDigest: true},
		{name: "current-nil-bind"},
		{name: "current-bind-digest", bindDigest: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture, manifest, journal := newFedoraJournalFixture(t)
			bind := fedoraJournalBindRecord(t, &journal)
			if !test.legacy {
				bind.Digests = nil
			}
			if test.bindDigest {
				if bind.Digests == nil {
					bind.Digests = make(map[string]string)
				}
				bind.Digests["boot/grub2/grub.cfg"] = strings.Repeat("b", 64)
			}
			raw := writeFedoraJournalFixture(t, &fixture, manifest, journal)
			manager := New(fakeValidator{report: fixture.report}, fakeCompressor{})
			receipt, err := manager.Prepare(context.Background(), Request{
				RepositoryRoot: fixture.root, ImagePath: fixture.image, ReleaseName: "fedora-release", PartSizeBytes: 31,
			})
			if err != nil {
				t.Fatal(err)
			}
			validated, err := manager.Validate(context.Background(), receipt.Plan.OutputDirectory)
			if err != nil || !validated.Valid {
				t.Fatalf("Validate() rejected the prepared Fedora release: %v", err)
			}
			rawDigest := sha256.Sum256(raw)
			wantIdentity := FileRecord{
				Name: filepath.Base(fixture.image) + ".journal.json", SHA256: hex.EncodeToString(rawDigest[:]), Size: int64(len(raw)),
			}
			if receipt.Plan.ImageJournal != wantIdentity {
				t.Fatalf("Plan.ImageJournal = %#v; want original identity %#v", receipt.Plan.ImageJournal, wantIdentity)
			}
			after, err := os.ReadFile(fixture.image + ".journal.json")
			if err != nil || !bytes.Equal(raw, after) {
				t.Fatalf("Prepare() or Validate() changed the original journal: %v", err)
			}
			if !reflect.DeepEqual(validated.Manifest.ImageContract, manifest) {
				t.Fatal("release projection changed the complete typed image-manifest evidence")
			}
			projected := validated.Manifest.ImageCreation
			if len(projected.Records) != len(journal.Records) {
				t.Fatal("release projection changed the checkpoint count")
			}
			for index, original := range journal.Records {
				got := projected.Records[index]
				if got.StepID != original.StepID || !got.CompletedAt.Equal(original.CompletedAt) {
					t.Fatalf("release projection changed checkpoint %s or its timestamp", original.StepID)
				}
				want := make(map[string]string)
				for name, digest := range original.Digests {
					if test.legacy && original.StepID == "bind-live-media" && (name == "iso-volume-label" || name == "grub-search-marker") {
						continue
					}
					want[name] = digest
				}
				if len(got.Digests) != len(want) {
					t.Fatalf("release projection changed genuine digest coverage at %s", original.StepID)
				}
				for _, digest := range got.Digests {
					if want[digest.Name] != digest.SHA256 {
						t.Fatalf("release projection changed digest %s at %s", digest.Name, original.StepID)
					}
				}
			}
		})
	}
}

// TestPlanRejectsInvalidLegacyFedoraJournalEvidence ensures compatibility cannot
// remove contextual entries without an exact, uniquely typed manifest match.
func TestPlanRejectsInvalidLegacyFedoraJournalEvidence(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, *plan.Journal, *imagecontract.Manifest)
	}{
		{name: "wrong-strategy", mutate: func(_ *testing.T, _ *plan.Journal, m *imagecontract.Manifest) {
			m.MediaDiscovery.Strategy = "partition-copy"
		}},
		{name: "wrong-protocol", mutate: func(_ *testing.T, _ *plan.Journal, m *imagecontract.Manifest) { m.MediaDiscovery.Protocol = "casper" }},
		{name: "wrong-adapter", mutate: func(_ *testing.T, j *plan.Journal, m *imagecontract.Manifest) {
			m.Adapter = "ubuntu-casper"
			for index, record := range j.Records {
				if record.StepID == "install-userspace" {
					j.Records = append(j.Records[:index], j.Records[index+1:]...)
					break
				}
			}
		}},
		{name: "wrong-step", mutate: func(t *testing.T, j *plan.Journal, _ *imagecontract.Manifest) {
			bind := fedoraJournalBindRecord(t, j)
			j.Records[0].Digests = bind.Digests
			bind.Digests = nil
		}},
		{name: "duplicate-legacy-step", mutate: func(t *testing.T, j *plan.Journal, _ *imagecontract.Manifest) {
			j.Records = append(j.Records, *fedoraJournalBindRecord(t, j))
		}},
		{name: "unknown-nonhash-at-bind", mutate: func(t *testing.T, j *plan.Journal, _ *imagecontract.Manifest) {
			fedoraJournalBindRecord(t, j).Digests["unknown-context"] = "not-a-sha256-digest"
		}},
		{name: "unknown-nonhash-at-other-step", mutate: func(_ *testing.T, j *plan.Journal, _ *imagecontract.Manifest) {
			j.Records[0].Digests["unknown-context"] = "not-a-sha256-digest"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture, manifest, journal := newFedoraJournalFixture(t)
			test.mutate(t, &journal, &manifest)
			writeFedoraJournalFixture(t, &fixture, manifest, journal)
			manager := New(fakeValidator{report: fixture.report}, fakeCompressor{})
			if _, err := manager.Plan(context.Background(), Request{RepositoryRoot: fixture.root, ImagePath: fixture.image}); err == nil {
				t.Fatal("Plan() accepted invalid legacy Fedora media evidence")
			}
		})
	}
	for evidenceIndex, role := range []string{"iso-volume-label", "grub-search-marker"} {
		for _, mutation := range []string{"partial-pair", "mismatched-value", "empty-value", "missing-evidence", "duplicate-evidence", "wrong-scope", "path", "artifact"} {
			t.Run(role+"/"+mutation, func(t *testing.T) {
				fixture, manifest, journal := newFedoraJournalFixture(t)
				bind := fedoraJournalBindRecord(t, &journal)
				evidence := &manifest.MediaDiscovery.Evidence[evidenceIndex]
				switch mutation {
				case "partial-pair":
					delete(bind.Digests, role)
				case "mismatched-value":
					bind.Digests[role] += "-different"
				case "empty-value":
					bind.Digests[role], evidence.Value = "", ""
				case "missing-evidence":
					manifest.MediaDiscovery.Evidence = append(manifest.MediaDiscovery.Evidence[:evidenceIndex], manifest.MediaDiscovery.Evidence[evidenceIndex+1:]...)
				case "duplicate-evidence":
					manifest.MediaDiscovery.Evidence = append(manifest.MediaDiscovery.Evidence, *evidence)
				case "wrong-scope":
					evidence.Scope = "initramfs"
				case "path":
					evidence.Path = "boot/grub2/grub.cfg"
				case "artifact":
					evidence.Artifact = &imagecontract.ArtifactRecord{Path: "boot/grub2/grub.cfg", SHA256: strings.Repeat("a", 64), Size: 1}
				}
				writeFedoraJournalFixture(t, &fixture, manifest, journal)
				manager := New(fakeValidator{report: fixture.report}, fakeCompressor{})
				if _, err := manager.Plan(context.Background(), Request{RepositoryRoot: fixture.root, ImagePath: fixture.image}); err == nil {
					t.Fatal("Plan() accepted ambiguous or mismatched Fedora media evidence")
				}
			})
		}
	}
}

// TestNormaliseFedoraJournalDoesNotMutateInputs proves legacy conversion owns
// its records, digest maps and output while generic digest validation stays strict.
func TestNormaliseFedoraJournalDoesNotMutateInputs(t *testing.T) {
	fixture, manifest, journal := newFedoraJournalFixture(t)
	fedoraJournalBindRecord(t, &journal).Digests["boot/grub2/grub.cfg"] = strings.Repeat("b", 64)
	before := writeFedoraJournalFixture(t, &fixture, manifest, journal)
	originalManifest := readFixtureManifest(t, fixture.image)
	identity := FileRecord{Name: filepath.Base(fixture.image), SHA256: fixture.report.SHA256, Size: fixture.report.Size}
	if err := validateImageJournal(journal, identity, fedora.AdapterID); err == nil {
		t.Fatal("generic journal validation accepted contextual values as SHA-256 digests")
	}
	normalised, err := normaliseImageJournal(journal, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateImageJournal(normalised, identity, fedora.AdapterID); err != nil {
		t.Fatalf("normalised journal failed generic validation: %v", err)
	}
	fedoraJournalBindRecord(t, &normalised).Digests["boot/grub2/grub.cfg"] = strings.Repeat("c", 64)
	fedoraJournalBindRecord(t, &normalised).CompletedAt = time.Time{}
	normalised.Records[0].Digests["source.iso"] = strings.Repeat("d", 64)
	normalised.Output.Path = "changed.iso"
	after, err := json.MarshalIndent(journal, "", "  ")
	if err != nil || !bytes.Equal(before, append(after, '\n')) {
		t.Fatalf("normalisation or editing its result mutated the input journal: %v", err)
	}
	if !reflect.DeepEqual(originalManifest, manifest) {
		t.Fatal("normalisation mutated the typed image manifest")
	}
}
