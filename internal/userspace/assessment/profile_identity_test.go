package assessment

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/userspace/compatibility"
)

// canonicalBoards captures the two reviewed device identities and the exact
// device-tree evidence that must canonicalise to each profile ID.
var canonicalBoards = []struct {
	// id is the canonical profile identity ObserveRoot must report.
	id string
	// devicetree is the exact compatible-token evidence for this board.
	devicetree string
}{
	{id: "x1e80100-microsoft-denali-oled", devicetree: "microsoft,denali-oled\x00microsoft,denali\x00qcom,x1e80100\x00"},
	{id: "x1p64100-microsoft-denali", devicetree: "microsoft,denali-lcd\x00qcom,x1p64100\x00"},
}

// boardRoot prepares a minimal alternate root carrying exact identity
// evidence, so ObserveRoot never borrows facts from the host running the test.
func boardRoot(t *testing.T, devicetree string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sys/firmware/devicetree/base"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sys/firmware/devicetree/base/compatible"), []byte(devicetree), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "etc/os-release"), []byte("ID=ubuntu\nVERSION_ID=26.04\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestObserveRootCanonicalisesBothBoards proves detection reports the exact
// canonical profile ID for the OLED and LCD identities without an explicit
// selection.
func TestObserveRootCanonicalisesBothBoards(t *testing.T) {
	for _, board := range canonicalBoards {
		target, err := ObserveRoot(boardRoot(t, board.devicetree), compatibility.Target{})
		if err != nil {
			t.Fatalf("%s: %v", board.id, err)
		}
		if target.DeviceProfile != board.id {
			t.Fatalf("device profile = %q, want canonical %q", target.DeviceProfile, board.id)
		}
	}
}

// TestObserveRootAcceptsExplicitCanonicalID proves an explicit selection is
// honoured when it agrees with the board evidence, keeping the canonical ID.
func TestObserveRootAcceptsExplicitCanonicalID(t *testing.T) {
	for _, board := range canonicalBoards {
		target, err := ObserveRoot(boardRoot(t, board.devicetree), compatibility.Target{DeviceProfile: board.id})
		if err != nil {
			t.Fatalf("%s: %v", board.id, err)
		}
		if target.DeviceProfile != board.id {
			t.Fatalf("device profile = %q, want canonical %q", target.DeviceProfile, board.id)
		}
	}
}

// TestObserveRootRejectsConflictingProfileID proves an explicit identity that
// contradicts the observed board is rejected rather than silently overridden.
func TestObserveRootRejectsConflictingProfileID(t *testing.T) {
	root := boardRoot(t, canonicalBoards[0].devicetree)
	if _, err := ObserveRoot(root, compatibility.Target{DeviceProfile: canonicalBoards[1].id}); err == nil {
		t.Fatal("conflicting explicit device profile was accepted")
	}
}
