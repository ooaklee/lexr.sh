package releaseprep

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ooaklee/lexr.sh/internal/image/archlinux"
	"github.com/ooaklee/lexr.sh/internal/image/elementary"
	"github.com/ooaklee/lexr.sh/internal/image/fedora"
	"github.com/ooaklee/lexr.sh/internal/image/ubuntu"
	"github.com/ooaklee/lexr.sh/internal/kernel"
	"github.com/ooaklee/lexr.sh/internal/plan"
)

// TestReleaseJournalUsesEachProducersWorkflow prevents equal journal lengths
// from accepting another distro's creation sequence, including Fedora's
// userspace step, elementary's GRUB preparation and Arch's rootfs workflow.
func TestReleaseJournalUsesEachProducersWorkflow(t *testing.T) {
	bundle := kernel.Bundle{ABI: "resolved-at-execution"}
	ubuntuPlan, err := ubuntu.BuildPlan(ubuntu.Request{SourceISO: "source.iso", OutputISO: "image.iso", Bundle: bundle})
	if err != nil {
		t.Fatal(err)
	}
	elementaryPlan, err := elementary.BuildPlan(elementary.Request{SourceISO: "source.iso", OutputISO: "image.iso", Bundle: bundle})
	if err != nil {
		t.Fatal(err)
	}
	fedoraPlan, err := fedora.BuildPlan(fedora.Request{SourceISO: "source.iso", OutputISO: "image.iso", Bundle: bundle})
	if err != nil {
		t.Fatal(err)
	}
	archPlan, err := archlinux.BuildPlan(archlinux.Request{
		SourceRootfs: "source.tar.gz", SourceSHA256: strings.Repeat("c", 64), OutputISO: "image.iso",
		Bundle: kernel.Bundle{ABI: "7.2.0-jg-0sp11v23-qcom-x1e"},
	})
	if err != nil {
		t.Fatal(err)
	}
	plans := map[string]plan.Plan{ubuntu.AdapterID: ubuntuPlan, elementary.AdapterID: elementaryPlan, fedora.AdapterID: fedoraPlan, archlinux.AdapterID: archPlan}
	for adapter, operation := range plans {
		journal := plan.NewJournal("image.create")
		identity := FileRecord{Name: "image.iso", SHA256: strings.Repeat("a", 64), Size: 2048}
		journal.Output = &plan.OutputRecord{Path: "image.iso", SHA256: identity.SHA256, Size: identity.Size}
		var ids []string
		for _, step := range operation.Steps {
			ids = append(ids, step.ID)
			digests := map[string]string{}
			if step.ID == "prepare-wifi" {
				digests["ath12k/WCN7850/hw2.0/board.bin"] = strings.Repeat("b", 64)
			}
			if step.ID == "validate-output" || step.ID == "publish-output" {
				digests["output.iso"] = identity.SHA256
			}
			journal.Records = append(journal.Records, plan.StepRecord{StepID: step.ID, CompletedAt: time.Now().UTC(), Digests: digests})
		}
		if !reflect.DeepEqual(imageStepsForAdapter(adapter, len(ids)), ids) {
			t.Fatalf("%s release order differs from its producer", adapter)
		}
		for candidate := range plans {
			err := validateImageJournal(*journal, identity, candidate)
			if (err == nil) != (candidate == adapter) {
				t.Errorf("%s journal under %s: %v", adapter, candidate, err)
			}
		}
	}
	if imageStepsForAdapter("unknown", 14) != nil {
		t.Fatal("unknown producer accepted")
	}
}
