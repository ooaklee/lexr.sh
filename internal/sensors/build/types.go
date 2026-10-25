package build

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/ooaklee/lexr.sh/internal/hostcap"
	"github.com/ooaklee/lexr.sh/internal/platform"
	"github.com/ooaklee/lexr.sh/internal/sensors/builderimage"
	"github.com/ooaklee/lexr.sh/internal/sensors/source"
)

// Request selects source and fresh work/output parents, never an execution
// image, script, source authority, prepared handle or installer. Parents must
// already exist. WorkParent must be current-user-owned with mode 0700.
type Request struct {
	RepositoryRoot string
	WorkParent     string
	OutputParent   string
	Jobs           int
	DryRun         bool
	NoPull         bool
}

// Plan is a local read-only observation. Executable does not claim that Docker
// or a prepared image is currently available; Run independently checks them.
type Plan struct {
	// Metadata048 reports constructor selection only, never execution authority.
	Metadata048         bool               `json:"metadata_048,omitempty"`
	RepositoryRoot      string             `json:"repository_root"`
	WorkParent          string             `json:"work_parent"`
	OutputParent        string             `json:"output_parent"`
	Jobs                int                `json:"jobs"`
	DryRun              bool               `json:"dry_run"`
	NoPull              bool               `json:"no_pull"`
	Executable          bool               `json:"executable"`
	ExecutionBlocker    string             `json:"execution_blocker,omitempty"`
	SourceAuthenticated bool               `json:"source_authenticated"`
	Source              source.Composition `json:"source"`
	InitRecipeSHA256    string             `json:"init_recipe_sha256"`
	SourceRecipeSHA256  string             `json:"source_recipe_sha256"`
}

// Terminal describes a fresh observation of one exact owned phase container,
// distinct from historical dependency-image production lineage.
type Terminal = sourceDockerTerminal

// PhaseResult retains diagnostics and termination uncertainty. A created but
// never started container may be verified stopped, but is not a successful job.
type PhaseResult struct {
	Name            string    `json:"name"`
	ContainerID     string    `json:"container_id,omitempty"`
	Terminal        *Terminal `json:"terminal,omitempty"`
	TerminalUnknown bool      `json:"terminal_unknown"`
	RecoveryError   string    `json:"recovery_error,omitempty"`
	Stdout          string    `json:"stdout,omitempty"`
	Stderr          string    `json:"stderr,omitempty"`
}

// SourceBindObservation retains local host/daemon coordinates for diagnosis.
// It is not included in the host-path-free published receipt and does not
// prove the daemon's file-sharing implementation or physical sensor behaviour.
type SourceBindObservation struct {
	HostSnapshotPath   string `json:"host_snapshot_path"`
	DaemonSnapshotPath string `json:"daemon_snapshot_path"`
	EngineID           string `json:"engine_id"`
	ServerVersion      string `json:"server_version"`
}

// Result is a local operation report; it may contain local paths/diagnostics.
// The separately published receipt deliberately omits those fields. Publication
// is a source-build result, never installability or physical qualification.
type Result struct {
	Plan              Plan                   `json:"plan"`
	BuildID           string                 `json:"build_id,omitempty"`
	WorkDirectory     string                 `json:"work_directory,omitempty"`
	SnapshotDirectory string                 `json:"snapshot_directory,omitempty"`
	SourceBind        *SourceBindObservation `json:"source_bind,omitempty"`
	TransportArchive  string                 `json:"transport_archive,omitempty"`
	VolumeName        string                 `json:"volume_name,omitempty"`
	VolumeVerified    bool                   `json:"volume_verified"`
	Dependencies      builderimage.Result    `json:"dependencies"`
	Init              PhaseResult            `json:"init"`
	Source            PhaseResult            `json:"source"`
	Artifacts         []Artifact             `json:"artifacts,omitempty"`
	BuildReport       *BuildReport           `json:"build_report,omitempty"`
	Publication       Publication            `json:"publication"`
}

// Manager owns dependency readiness and every source/init resource it creates.
// Use one sequential owner. No caller-supplied image or reconstructed receipt
// can populate its private prepared handle. No resource is deleted implicitly.
type Manager struct {
	profile         producerProfile
	runner          platform.Runner
	dependencies    *builderimage.Manager
	prepared        *builderimage.Prepared
	transport       sourceTransportBinding
	bindTransport   func(context.Context, platform.Runner, hostcap.Host) (sourceTransportBinding, error)
	host            hostcap.Host
	token           func() (string, error)
	timeout         time.Duration
	initTimeout     time.Duration
	sourceTimeout   time.Duration
	recoveryTimeout time.Duration
}

// New creates a sequential build owner with fixed phase and recovery deadlines.
func New(runner platform.Runner) *Manager {
	return newManager(runner, producer047)
}

// NewMetadata048 selects only the exact compiled metadata-0.4.8 successor.
// It grants no installation, service activation or physical qualification.
func NewMetadata048(runner platform.Runner) *Manager {
	return newManager(runner, producer048)
}

func newManager(runner platform.Runner, profile producerProfile) *Manager {
	if runner == nil {
		runner = platform.NewDiagnosticRunner(nil)
	}
	return &Manager{profile: profile, runner: runner, dependencies: builderimage.New(runner), bindTransport: bindSourceDockerTransport, host: hostcap.Current(), token: func() (string, error) {
		var b [12]byte
		_, err := rand.Read(b[:])
		return hex.EncodeToString(b[:]), err
	}, timeout: 75 * time.Minute, initTimeout: 3 * time.Minute, sourceTimeout: 47 * time.Minute, recoveryTimeout: 25 * time.Second}
}

// Plan authenticates local inputs without contacting Docker or creating outputs.
func (m *Manager) Plan(ctx context.Context, request Request) (Plan, error) {
	return m.plan(ctx, request)
}
