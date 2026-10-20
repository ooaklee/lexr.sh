// Package builderimage prepares a native Linux dependency image. It never
// receives OE source, installs on the host, or qualifies physical sensors.
package builderimage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/ooaklee/lexr.sh/internal/hostcap"
	"github.com/ooaklee/lexr.sh/internal/platform"
)

// Request describes preparation or reuse of this process's own verified image.
type Request struct {
	DryRun bool
	// NoPull forbids acquisition and dependency installation, not just docker pull.
	NoPull bool
	// Prepared must be a successful result of this exact Manager. Persistent
	// cross-process cache authority is deliberately not implemented here yet.
	Prepared *Prepared
}

// Plan is read-only and does not inspect Docker or create filesystem entries.
type Plan struct {
	DryRun           bool   `json:"dry_run"`
	NoPull           bool   `json:"no_pull"`
	Executable       bool   `json:"executable"`
	ExecutionBlocker string `json:"execution_blocker,omitempty"`
	BaseImage        string `json:"base_image"`
	RecipeSHA256     string `json:"recipe_sha256"`
	NetworkPhase     string `json:"network_phase"`
	RetainsContainer bool   `json:"retains_container"`
}

// Terminal is an observed state of the exact owned dependency container.
type Terminal struct {
	ContainerID string `json:"container_id"`
	Status      string `json:"status"`
	Running     bool   `json:"running"`
	PID         int64  `json:"pid"`
	ExitCode    int    `json:"exit_code"`
	OOMKilled   bool   `json:"oom_killed"`
	Error       string `json:"error,omitempty"`
	StartedAt   string `json:"started_at"`
	FinishedAt  string `json:"finished_at"`
}

// Provenance describes an observed producer, not bit-reproducibility or a
// publicly fetchable builder release. Inventories are complete captured text.
type Provenance struct {
	EngineID                 string   `json:"engine_id"`
	ServerVersion            string   `json:"server_version"`
	BaseImage                string   `json:"base_image"`
	BaseImageID              string   `json:"base_image_id"`
	ImageID                  string   `json:"image_id"`
	RecipeSHA256             string   `json:"recipe_sha256"`
	PackageInventory         string   `json:"package_inventory"`
	PackageInventorySHA256   string   `json:"package_inventory_sha256"`
	ToolchainInventory       string   `json:"toolchain_inventory"`
	ToolchainInventorySHA256 string   `json:"toolchain_inventory_sha256"`
	Terminal                 Terminal `json:"terminal"`
}

// Prepared cannot be populated by adjacent metadata, labels or a caller's tag.
// Its authority is this Manager's controlled create-to-terminal-to-commit chain.
// Same-UID process and Docker-administrator integrity are assumed.
type Prepared struct {
	origin     *Manager
	provenance Provenance
}

// ImageID returns the exact immutable local object, never a mutable tag.
func (p *Prepared) ImageID() string {
	if p == nil {
		return ""
	}
	return p.provenance.ImageID
}

// Provenance returns a value copy; no mutable maps or slices escape.
func (p *Prepared) Provenance() Provenance {
	if p == nil {
		return Provenance{}
	}
	return p.provenance
}

// Result retains failures and terminal uncertainty, with no automatic deletion.
type Result struct {
	Plan             Plan        `json:"plan"`
	ContainerName    string      `json:"container_name,omitempty"`
	ContainerID      string      `json:"container_id,omitempty"`
	ObservedImageID  string      `json:"observed_image_id,omitempty"`
	Terminal         *Terminal   `json:"terminal,omitempty"`
	TerminalUnknown  bool        `json:"terminal_unknown"`
	RecoveryError    string      `json:"recovery_error,omitempty"`
	DependencyStdout string      `json:"dependency_stdout,omitempty"`
	DependencyStderr string      `json:"dependency_stderr,omitempty"`
	Prepared         *Prepared   `json:"-"`
	Provenance       *Provenance `json:"provenance,omitempty"`
}

// Manager owns one controlled producer chain and its opaque result handles.
type Manager struct {
	Runner          platform.Runner
	host            hostcap.Host
	token           func() (string, error)
	timeout         time.Duration
	recoveryTimeout time.Duration
}

// New uses bounded direct execution; construction itself has no effects.
func New(runner platform.Runner) *Manager {
	if runner == nil {
		runner = platform.NewDiagnosticRunner(nil)
	}
	return &Manager{Runner: runner, host: hostcap.Current(), token: func() (string, error) {
		var b [12]byte
		_, err := rand.Read(b[:])
		return hex.EncodeToString(b[:]), err
	}, timeout: 20 * time.Minute, recoveryTimeout: 25 * time.Second}
}

// Plan validates local request data without touching Docker or the filesystem.
func (m *Manager) Plan(ctx context.Context, request Request) (Plan, error) {
	return m.plan(ctx, request)
}
