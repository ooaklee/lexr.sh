package manager

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/catalog"
	"github.com/ooaklee/lexr.sh/internal/platform"
)

// TestImageManagerRoutesChildDiagnostics checks the production process
// boundaries, including companion compilation outside Docker, with real output.
func TestImageManagerRoutesChildDiagnostics(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process fixture; portable runner behaviour is tested in platform")
	}
	t.Parallel()
	var diagnostics bytes.Buffer
	manager := NewImageManager(catalog.Loader{}, &diagnostics)
	runners := map[string]platform.Runner{
		"ubuntu tooling":       manager.Remaster.Docker.Runner,
		"fedora tooling":       manager.FedoraRemaster.Docker.Runner,
		"elementary tooling":   manager.ElementaryRemaster.Docker.Runner,
		"ubuntu companion":     manager.Remaster.Companions.Runner,
		"fedora companion":     manager.FedoraRemaster.Companions.Runner,
		"elementary companion": manager.ElementaryRemaster.Companions.Runner,
		"companion probe":      manager.CompanionRunner,
	}
	for name, runner := range runners {
		t.Run(name, func(t *testing.T) {
			diagnostics.Reset()
			command := platform.Command{
				Name: "sh", Args: []string{"-c", "printf 'tool stdout\\n'; printf 'tool stderr\\n' >&2"},
			}
			if err := runner.Run(t.Context(), command); err != nil {
				t.Fatal(err)
			}
			for _, expected := range []string{"tool stdout\n", "tool stderr\n"} {
				if !strings.Contains(diagnostics.String(), expected) {
					t.Fatalf("%s bypassed caller diagnostics: %q", name, diagnostics.String())
				}
			}
		})
	}
	// Replacing one adapter's injected runner must not reconfigure its peers.
	manager.Remaster.Docker.Runner = nil
	if manager.FedoraRemaster.Docker.Runner == nil || manager.ElementaryRemaster.Docker.Runner == nil {
		t.Fatal("adapter Docker dependencies are coupled")
	}
}
