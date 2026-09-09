package releaseprep

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ooaklee/lexr.sh/internal/image/archlinux"
	"github.com/ooaklee/lexr.sh/internal/image/ubuntu"
	"github.com/ooaklee/lexr.sh/internal/kernel"
	"github.com/ooaklee/lexr.sh/internal/plan"
)

// TestReleaseAcceptsCurrentProducerJournals follows the producers' public plans
// through preparation, split-release validation and malformed-evidence rejection.
func TestReleaseAcceptsCurrentProducerJournals(t *testing.T) {
	for _, adapter := range []string{ubuntu.AdapterID, archlinux.AdapterID} {
		t.Run(adapter, func(t *testing.T) {
			fixture := newFixture(t)
			operation, err := ubuntu.BuildPlan(ubuntu.Request{
				SourceISO: "source.iso", OutputISO: fixture.image,
				Bundle: kernel.Bundle{ABI: fixture.report.KernelABI},
			})
			if err != nil {
				t.Fatal(err)
			}
			if adapter == archlinux.AdapterID {
				operation, err = archlinux.BuildPlan(archlinux.Request{
					SourceRootfs: "source.tar.gz", SourceSHA256: strings.Repeat("c", 64), OutputISO: fixture.image,
					Bundle: kernel.Bundle{ABI: fixture.report.KernelABI},
				})
				if err != nil {
					t.Fatal(err)
				}
				manifest := readFixtureManifest(t, fixture.image)
				manifest.Adapter = adapter
				manifest.SourceImage.Path = "source.tar.gz"
				manifest.MediaDiscovery.Protocol = "archiso"
				rewriteFixtureManifest(t, fixture.image, manifest)
				data, err := os.ReadFile(fixture.image + ".manifest.json")
				if err != nil {
					t.Fatal(err)
				}
				digest := sha256.Sum256(data)
				fixture.report.Adapter = adapter
				fixture.report.ManifestSHA256 = hex.EncodeToString(digest[:])
				fixture.report.ManifestSize = int64(len(data))
			}
			journal := plan.NewJournal("image.create")
			for _, step := range operation.Steps {
				record := plan.StepRecord{StepID: step.ID, CompletedAt: time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)}
				switch step.ID {
				case "prepare-wifi":
					record.Digests = map[string]string{"ath12k/WCN7850/hw2.0/board.bin": strings.Repeat("a", 64)}
				case "validate-output", "publish-output":
					record.Digests = map[string]string{"output.iso": fixture.report.SHA256}
				}
				journal.Records = append(journal.Records, record)
			}
			journal.Output = &plan.OutputRecord{Path: fixture.image, SHA256: fixture.report.SHA256, Size: fixture.report.Size}
			data, err := json.Marshal(journal)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(fixture.image+".journal.json", data, 0o644); err != nil {
				t.Fatal(err)
			}
			manager := New(fakeValidator{report: fixture.report}, fakeCompressor{})
			receipt, err := manager.Prepare(context.Background(), Request{RepositoryRoot: fixture.root, ImagePath: fixture.image, ReleaseName: "wifi-release"})
			if err != nil {
				t.Fatal(err)
			}
			validated, err := manager.Validate(context.Background(), receipt.Plan.OutputDirectory)
			if err != nil || !validated.Valid {
				t.Fatalf("release validation: %+v, %v", validated, err)
			}
			want, _ := json.Marshal(projectJournal(*journal))
			got, _ := json.Marshal(validated.Manifest.ImageCreation)
			if !bytes.Equal(got, want) {
				t.Fatal("release lost or changed the native journal evidence")
			}

			for _, test := range []struct {
				name   string
				mutate func(*plan.Journal)
			}{
				{name: "missing board digest", mutate: func(j *plan.Journal) { j.Records[5].Digests = nil }},
				{name: "invalid board digest", mutate: func(j *plan.Journal) { j.Records[5].Digests["ath12k/WCN7850/hw2.0/board.bin"] = "bad" }},
				{name: "unknown step", mutate: func(j *plan.Journal) { j.Records[5].StepID = "unknown" }},
				{name: "duplicate step", mutate: func(j *plan.Journal) { j.Records[6].StepID = j.Records[5].StepID }},
				{name: "out of order", mutate: func(j *plan.Journal) { j.Records[5], j.Records[6] = j.Records[6], j.Records[5] }},
				{name: "missing timestamp", mutate: func(j *plan.Journal) { j.Records[5].CompletedAt = time.Time{} }},
				{name: "incomplete workflow", mutate: func(j *plan.Journal) { j.Records = j.Records[:len(j.Records)-1] }},
			} {
				if adapter == archlinux.AdapterID && strings.Contains(test.name, "board digest") {
					continue // Arch does not have Ubuntu's prepare-wifi checkpoint.
				}
				t.Run(test.name, func(t *testing.T) {
					var changed plan.Journal
					if err := json.Unmarshal(data, &changed); err != nil {
						t.Fatal(err)
					}
					test.mutate(&changed)
					if err := validateImageJournal(changed, receipt.Manifest.Image, receipt.Manifest.ImageContract.Adapter); err == nil {
						t.Fatal("private journal accepted invalid workflow")
					}
					public := *receipt.Manifest
					public.ImageCreation = projectJournal(changed)
					if err := validateReleaseManifest(public); err == nil {
						t.Fatal("public release accepted invalid workflow")
					}
				})
			}
		})
	}
}
