package fedora

import (
	"slices"
	"strings"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/kernel"
)

// Firmware scanout needs its unclaimed supplies, but retaining every regulator
// must remain a deliberate diagnostic choice rather than installed boot policy.
func TestFirmwareDisplayDiagnosticScopesRegulatorRetention(t *testing.T) {
	t.Parallel()
	const abi = "7.2.0-jg-0sp11v23-qcom-x1e"
	const argument = "regulator_ignore_unused"

	for _, delivery := range []kernel.DTBDelivery{kernel.DTBDeliveryEmbedded, kernel.DTBDeliveryExternalRequired} {
		t.Run(string(delivery), func(t *testing.T) {
			config := grubConfig(abi, delivery, fedoraLayoutFixture())
			if count := strings.Count(config, argument); count != 1 {
				t.Fatalf("regulator retention occurs %d times in generated GRUB, want only one diagnostic entry", count)
			}

			firmwareEntries := 0
			for _, entry := range strings.Split(config, "menuentry ")[1:] {
				title, _, _ := strings.Cut(entry, "\n")
				firmware := strings.HasPrefix(title, `"Surface Pro 11 X1E/OLED firmware display diagnostics"`)
				if firmware {
					firmwareEntries++
				}
				kernelLines := 0
				for _, line := range strings.Split(entry, "\n") {
					args := strings.Fields(line)
					if len(args) == 0 || args[0] != "linux" {
						continue
					}
					kernelLines++
					if got := slices.Contains(args, argument); got != firmware {
						t.Errorf("%s: regulator retention = %v, want %v", title, got, firmware)
					}
					if firmware && !slices.Contains(args, "module_blacklist=msm") {
						t.Errorf("%s: regulator retention requires the msm blacklist", title)
					}
				}
				if kernelLines != 1 {
					t.Errorf("%s: kernel lines = %d, want 1", title, kernelLines)
				}
			}
			if firmwareEntries != 1 {
				t.Fatalf("firmware-display entries = %d, want 1", firmwareEntries)
			}
		})
	}

	for name, policy := range map[string]string{
		"installed arguments": strings.Join(installedBootArguments, " "),
		"live arguments":      strings.Join(liveOnlyBootArguments, " "),
		"stock arguments":     strings.Join(stockFallbackArguments, " "),
		"installed GRUB":      installedGrubDefaults(),
		"installed finalizer": installedFinalizeScript(abi),
	} {
		if strings.Contains(policy, argument) {
			t.Errorf("%s contains diagnostic-only regulator retention", name)
		}
	}
}
