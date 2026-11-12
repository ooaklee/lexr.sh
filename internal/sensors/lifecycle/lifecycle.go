// Package lifecycle executes sensor activation and compensation qualification
// against a closed, explicitly supplied backend. No live backend, process
// runner, existing-root constructor or CLI registration is provided here.
// Its observations are a trusted test-adapter boundary, not real attestation.
package lifecycle

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ooaklee/lexr.sh/internal/sensors/integration"
	"github.com/ooaklee/lexr.sh/internal/sensors/runtimepkg"
)

// Phase describes observed qualification progress, not a resumable journal.
type Phase string

// The finite phase set has no live activation or crash-recovery authority.
const (
	Captured         Phase = "captured"
	Excluded         Phase = "excluded"
	VendorQuiescent  Phase = "vendor-quiescent"
	NativeStaged     Phase = "native-staged"
	StartAttempted   Phase = "start-attempted"
	Active           Phase = "active"
	Restored         Phase = "restored"
	RecoveryRequired Phase = "recovery-required"
)

// Process is an adapter-attested process identity and complete runtime binding.
// A future live adapter must derive it independently, not trust a JSON receipt.
type Process struct {
	PID            uint32
	StartTime      uint64
	InvocationID   string
	UnitName       string
	CGroup         string
	RuntimePackage runtimepkg.Reference
	ProgramPath    string
	ProgramSHA256  string
	LoaderPath     string
	LibraryPath    string
	Argv           []string
}

// Unit records one closed unit observation without caller-selected operations.
type Unit struct {
	Name             string
	LoadState        string
	ActiveState      string
	SubState         string
	MainPID          uint32
	ControlPID       uint32
	PendingJob       bool
	CGroupEmpty      bool
	DefinitionSHA256 string
	Process          Process
}

// BusOwner binds the well-known name to a unique connection and process epoch.
// An empty UniqueName represents independently observed absence, not failure.
type BusOwner struct {
	Name         string
	UniqueName   string
	PID          uint32
	StartTime    uint64
	InvocationID string
	UnitName     string
}

// Fixture records a specific backend-owned runtime file and its effective state.
// Exact requires the claim's complete bytes/mode or exact /dev/null mask.
type Fixture struct {
	Present   bool
	Exact     bool
	Effective bool
	ClaimID   string
}

// Observation is trusted backend evidence, never accepted as a mutation plan.
// KnownVendor means the adapter authenticated its fixed vendor definition and
// required policy/sandbox; this engine does not invent that authentication.
type Observation struct {
	BootID                 string
	ManagerID              string
	KnownVendor            bool
	VendorDefinitionSHA256 string
	VendorFileState        string
	VendorDropIns          bool
	VendorAliases          bool
	Vendor                 Unit
	Native                 Unit
	Owner                  BusOwner
	Inhibition             Fixture
	NativeFile             Fixture
}

// Backend is the complete qualification operation vocabulary. It has no
// arbitrary executable, unit name, path, bus method or process-kill argument.
// No default implementation exists; tests supply a fresh-root fixture backend.
// A backend must obey context cancellation and finish its call-owned effects
// before returning. Timeouts here are cooperative, not forcible containment of
// an uncooperative implementation or an assertion about real systemd jobs.
// It must recheck claim ownership and mutation preconditions at its own effect
// boundary and report pending jobs rather than hiding possible late effects.
type Backend interface {
	// Observe returns normalised fixture service, process and ownership evidence.
	Observe(context.Context) (Observation, error)
	// InhibitVendor exclusively creates the claim's fixed runtime vendor mask.
	InhibitVendor(context.Context, *Claim) error
	// InstallNative exclusively publishes the claim's fixed manual unit bytes.
	InstallNative(context.Context, *Claim) error
	// RemoveNative removes only unchanged owned registration after quiescence.
	RemoveNative(context.Context, *Claim) error
	// ReleaseVendor removes only the owned mask after native absence is proved.
	ReleaseVendor(context.Context, *Claim) error
	// Reload models registration changes without starting or enabling a unit.
	Reload(context.Context) error
	// StopVendor stops only the known fixed vendor fixture unit.
	StopVendor(context.Context) error
	// StartNative starts only the fixed qualification unit, not arbitrary argv.
	StartNative(context.Context) error
	// StopNative stops only the admitted owned native fixture invocation.
	StopNative(context.Context) error
	// StartVendor restores only the recognised fixed vendor fixture unit.
	StartVendor(context.Context) error
}

// Claim is an opaque process-local fixture identity. Decoded records cannot
// manufacture one or grant service commands, file deletion or root adoption.
type Claim struct {
	id       string
	snapshot integration.Snapshot
	unit     []byte
}

// ID returns a descriptive nonce; a string alone is never ownership authority.
func (c *Claim) ID() string {
	if c == nil {
		return ""
	}
	return c.id
}

// Unit returns copied qualification bytes for the backend's one fixed unit.
func (c *Claim) Unit() ([]byte, error) {
	if c == nil || c.id == "" {
		return nil, errors.New("qualification claim is invalid")
	}
	return slices.Clone(c.unit), nil
}

// Binding returns a defensive diagnostic copy, not an execution grant.
func (c *Claim) Binding() (integration.Snapshot, error) {
	if c == nil || c.id == "" {
		return integration.Snapshot{}, errors.New("qualification claim is invalid")
	}
	return cloneBinding(c.snapshot), nil
}

// Transaction is a process-local engine-owned handle, not a durable receipt.
type Transaction struct {
	engine              *Qualification
	claim               *Claim
	baseline            Observation
	phase               Phase
	transitions         []Phase
	maskAttempted       bool
	unitAttempted       bool
	startAttempted      bool
	stopFailed          bool
	vendorStopAttempted bool
	nativeEpoch         *Process
}

// Status returns copied diagnostic progress without backend or mutation rights.
func (t *Transaction) Status() (Phase, []Phase) {
	if t == nil || t.engine == nil {
		return "", nil
	}
	t.engine.mu.Lock()
	defer t.engine.mu.Unlock()
	return t.phase, slices.Clone(t.transitions)
}

// Qualification serialises one fixture transaction and exact managed repeats.
// A fresh explicitly supplied backend is required for a separate experiment.
type Qualification struct {
	mu               sync.Mutex
	backend          Backend
	binding          integration.Snapshot
	unit             []byte
	transaction      *Transaction
	operationTimeout time.Duration
	cleanupTimeout   time.Duration
	poll             time.Duration
}

// NewQualification retains the original opaque 0.4.7 integration profile. It
// enables no shipped installer, live root or target service command.
func NewQualification(bundle *integration.Bundle, backend Backend) (*Qualification, error) {
	if bundle == nil || !bundle.IsLegacy047() {
		return nil, errors.New("opaque original-profile integration bundle is required")
	}
	return qualificationFromBundle(bundle, backend)
}

// NewQualification048 accepts only the explicit opaque metadata 0.4.8 profile
// for the same fixture-only engine. It grants no live service or root access.
func NewQualification048(bundle *integration.Bundle, backend Backend) (*Qualification, error) {
	if bundle == nil || !bundle.IsMetadata048() {
		return nil, errors.New("opaque metadata 0.4.8 integration bundle is required")
	}
	return qualificationFromBundle(bundle, backend)
}

// qualificationFromBundle preserves the shared finite fixture engine after
// an explicitly selected constructor has checked the sealed bundle profile.
func qualificationFromBundle(bundle *integration.Bundle, backend Backend) (*Qualification, error) {
	binding, err := bundle.Snapshot()
	if err != nil {
		return nil, err
	}
	unit, err := bundle.Unit()
	if err != nil {
		return nil, err
	}
	return newQualification(binding, unit, backend)
}

// newQualification is a mechanical test seam, not a public trust constructor.
func newQualification(binding integration.Snapshot, unit []byte, backend Backend) (*Qualification, error) {
	if backend == nil {
		return nil, errors.New("explicit qualification backend is required")
	}
	if !binding.QualificationOnly || binding.ActivationEnabled || binding.RegistrationEnabled || binding.UnitName != integration.UnitName || binding.VendorUnitName != integration.VendorUnitName || binding.BusName != integration.BusName || binding.LoaderPath != integration.LoaderPath || !digestEqual(unit, binding.UnitSHA256) || int64(len(unit)) != binding.UnitBytes {
		return nil, errors.New("inert integration binding differs")
	}
	if !strings.Contains(string(unit), "\nType=exec\n") || strings.Contains(string(unit), "\nBusName=") || strings.Contains(string(unit), "\n[Install]") || len(binding.Argv) != 4 || binding.Argv[0] != binding.LoaderPath || binding.Argv[1] != "--library-path" || binding.Argv[2] != binding.LibraryPath || binding.Argv[3] != binding.ProgramPath {
		return nil, errors.New("qualification unit must remain static Type=exec with exact loader arguments")
	}
	return &Qualification{backend: backend, binding: cloneBinding(binding), unit: slices.Clone(unit), operationTimeout: 15 * time.Second, cleanupTimeout: 45 * time.Second, poll: 10 * time.Millisecond}, nil
}

// Apply executes fixture exclusion, bounded start and independent ownership
// checks. A zero command exit is never readiness. On failure it attempts one
// bounded compensation; uncertain stops retain inhibition and require review.
func (q *Qualification) Apply(ctx context.Context) (*Transaction, error) {
	if q == nil {
		return nil, errors.New("qualification engine is required")
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.transaction != nil {
		t := q.transaction
		if t.phase != Active {
			return t, errors.New("qualification transaction cannot be adopted or retried")
		}
		if _, err := q.wait(ctx, t, q.active); err != nil {
			t.set(RecoveryRequired)
			return t, err
		}
		return t, nil
	}
	baseline, err := q.stable(ctx, nil)
	if err != nil {
		return nil, err
	}
	if err := q.admit(baseline); err != nil {
		return nil, err
	}
	id, err := nonce()
	if err != nil {
		return nil, err
	}
	t := &Transaction{engine: q, claim: &Claim{id: id, snapshot: cloneBinding(q.binding), unit: slices.Clone(q.unit)}, baseline: baseline}
	t.set(Captured)
	q.transaction = t
	fail := func(cause error) (*Transaction, error) {
		cleanupErr := q.compensate(t)
		return t, errors.Join(cause, cleanupErr)
	}
	// Mark attempts before calling: a backend may change a file then return an
	// error, and its zero/nonzero status is not deletion or effect authority.
	t.maskAttempted = true
	if err := q.call(ctx, func(c context.Context) error { return q.backend.InhibitVendor(c, t.claim) }); err != nil {
		return fail(err)
	}
	if err := q.reload(ctx, t); err != nil {
		return fail(err)
	}
	if _, err := q.wait(ctx, t, q.excluded); err != nil {
		return fail(err)
	}
	t.set(Excluded)
	t.vendorStopAttempted = true
	if err := q.call(ctx, q.backend.StopVendor); err != nil {
		t.stopFailed = true
		return fail(err)
	}
	if _, err := q.wait(ctx, t, q.vendorQuiet); err != nil {
		return fail(err)
	}
	t.set(VendorQuiescent)
	t.unitAttempted = true
	if err := q.call(ctx, func(c context.Context) error { return q.backend.InstallNative(c, t.claim) }); err != nil {
		return fail(err)
	}
	if err := q.reload(ctx, t); err != nil {
		return fail(err)
	}
	if _, err := q.wait(ctx, t, q.readyToStart); err != nil {
		return fail(err)
	}
	t.set(NativeStaged)
	t.startAttempted = true
	t.set(StartAttempted)
	if err := q.call(ctx, q.backend.StartNative); err != nil {
		return fail(err)
	}
	if _, err := q.wait(ctx, t, q.active); err != nil {
		return fail(err)
	}
	t.set(Active)
	return t, nil
}

// Rollback accepts only this engine's active handle, never decoded state. A
// recovery-required transaction is deliberately not an automatic retry API.
func (q *Qualification) Rollback(_ context.Context, t *Transaction) error {
	if q == nil {
		return errors.New("qualification engine is required")
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if t == nil || t.engine != q || q.transaction != t || t.phase != Active {
		return errors.New("no active process-owned qualification transaction")
	}
	return q.compensate(t)
}

// compensate uses a fresh bounded context even after caller cancellation. It
// never starts a competitor until native process/cgroup/bus absence is proved.
func (q *Qualification) compensate(t *Transaction) error {
	ctx, cancel := context.WithTimeout(context.Background(), q.cleanupTimeout)
	defer cancel()
	stop := func(err error) error {
		t.set(RecoveryRequired)
		return fmt.Errorf("qualification recovery required; inspect actual state, no further mutation attempted: %w", err)
	}
	if t.stopFailed {
		return stop(errors.New("a service stop failed; no competitor may be started"))
	}
	o, err := q.stable(ctx, t)
	if err != nil {
		return stop(err)
	}
	if err := q.ownedOrAbsent(o, t); err != nil {
		return stop(err)
	}
	if t.vendorStopAttempted && !t.startAttempted {
		if _, err := q.wait(ctx, t, q.vendorQuiet); err != nil {
			return stop(err)
		}
		o, err = q.stable(ctx, t)
		if err != nil {
			return stop(err)
		}
	}
	if t.startAttempted {
		if !q.ownFixture(o.NativeFile, t) {
			return stop(errors.New("native registration changed after start attempt"))
		}
		if err := q.admitNativeStop(o, t); err != nil {
			return stop(err)
		}
		if err := q.call(ctx, q.backend.StopNative); err != nil {
			t.stopFailed = true
			return stop(err)
		}
		if _, err := q.wait(ctx, t, q.nativeQuiet); err != nil {
			return stop(err)
		}
	} else {
		if !quiescent(o.Native) {
			return stop(errors.New("unexpected native process before start"))
		}
		if o.Owner.UniqueName != "" && !sameOwner(o.Owner, t.baseline.Owner) {
			return stop(errors.New("foreign bus owner prevents compensation"))
		}
	}
	// Each operation is preceded by a fresh exact ownership check. Files that
	// changed after our publication are never overwritten or blindly unmasked.
	o, err = q.stable(ctx, t)
	if err != nil {
		return stop(err)
	}
	if err := q.ownedOrAbsent(o, t); err != nil {
		return stop(err)
	}
	if err := q.registrationForRemoval(o, t); err != nil {
		return stop(err)
	}
	if o.NativeFile.Present {
		if err := q.call(ctx, func(c context.Context) error { return q.backend.RemoveNative(c, t.claim) }); err != nil {
			return stop(err)
		}
	}
	o, err = q.stable(ctx, t)
	if err != nil {
		return stop(err)
	}
	if err := q.ownedOrAbsent(o, t); err != nil {
		return stop(err)
	}
	if !quiescent(o.Native) || o.NativeFile.Present {
		return stop(errors.New("native file or process remains before releasing exclusion"))
	}
	if err := q.stoppedNativeDefinition(o); err != nil {
		return stop(err)
	}
	if o.Owner.UniqueName != "" && !sameOwner(o.Owner, t.baseline.Owner) {
		return stop(errors.New("bus still owned before releasing exclusion"))
	}
	if o.Inhibition.Present {
		if err := q.call(ctx, func(c context.Context) error { return q.backend.ReleaseVendor(c, t.claim) }); err != nil {
			return stop(err)
		}
	}
	if err := q.reload(ctx, t); err != nil {
		return stop(err)
	}
	o, err = q.stable(ctx, t)
	if err != nil {
		return stop(err)
	}
	if o.Inhibition.Present || o.NativeFile.Present || !quiescent(o.Native) {
		return stop(errors.New("registration or inhibition was not removed"))
	}
	if t.baseline.Vendor.ActiveState == "active" {
		if !sameOwner(o.Owner, t.baseline.Owner) && o.Owner.UniqueName != "" {
			return stop(errors.New("foreign owner prevents vendor restoration"))
		}
		if o.Vendor.ActiveState != "active" {
			if err := q.call(ctx, q.backend.StartVendor); err != nil {
				return stop(err)
			}
		}
	}
	if _, err := q.wait(ctx, t, q.restored); err != nil {
		return stop(err)
	}
	t.set(Restored)
	return nil
}

// call bounds one closed backend operation without invoking any program here.
func (q *Qualification) call(ctx context.Context, operation func(context.Context) error) error {
	c, cancel := context.WithTimeout(ctx, q.operationTimeout)
	defer cancel()
	if err := c.Err(); err != nil {
		return err
	}
	err := operation(c)
	return errors.Join(err, c.Err())
}

// reload keeps boot/manager and source-definition identity fixed before reload.
func (q *Qualification) reload(ctx context.Context, t *Transaction) error {
	if _, err := q.stable(ctx, t); err != nil {
		return err
	}
	return q.call(ctx, q.backend.Reload)
}

// errUnstable denotes an observation changing between two immediate reads.
var errUnstable = errors.New("qualification observation changed between reads")

// stable rejects manager/boot drift and returns a defensive stable observation.
func (q *Qualification) stable(ctx context.Context, t *Transaction) (Observation, error) {
	read := func() (Observation, error) {
		var o Observation
		err := q.call(ctx, func(c context.Context) error { var err error; o, err = q.backend.Observe(c); return err })
		if err != nil {
			return Observation{}, err
		}
		o = cloneObservation(o)
		if err := q.common(o, t); err != nil {
			return Observation{}, err
		}
		return o, nil
	}
	a, err := read()
	if err != nil {
		return Observation{}, err
	}
	b, err := read()
	if err != nil {
		return Observation{}, err
	}
	if !reflect.DeepEqual(a, b) {
		return Observation{}, errUnstable
	}
	return b, nil
}

// wait bounds asynchronous state changes without retrying a mutating action.
func (q *Qualification) wait(ctx context.Context, t *Transaction, predicate func(Observation, *Transaction) (bool, error)) (Observation, error) {
	c, cancel := context.WithTimeout(ctx, q.operationTimeout)
	defer cancel()
	for {
		o, err := q.stable(c, t)
		if err == nil {
			ready, checkErr := predicate(o, t)
			if checkErr != nil {
				return Observation{}, checkErr
			}
			if ready {
				return o, nil
			}
		} else if !errors.Is(err, errUnstable) {
			return Observation{}, err
		}
		timer := time.NewTimer(q.poll)
		select {
		case <-c.Done():
			timer.Stop()
			return Observation{}, c.Err()
		case <-timer.C:
		}
	}
}

// common checks fixed names, vendor definition and stable manager identity.
func (q *Qualification) common(o Observation, t *Transaction) error {
	if o.BootID == "" || len(o.BootID) > 128 || o.ManagerID == "" || len(o.ManagerID) > 128 || !o.KnownVendor || !sha256Text(o.VendorDefinitionSHA256) || o.VendorDropIns || o.VendorAliases || o.Vendor.Name != integration.VendorUnitName || o.Native.Name != integration.UnitName || o.Owner.Name != integration.BusName {
		return errors.New("unknown or malformed qualification service identity")
	}
	if t != nil && (o.BootID != t.baseline.BootID || o.ManagerID != t.baseline.ManagerID || o.VendorDefinitionSHA256 != t.baseline.VendorDefinitionSHA256 || o.VendorFileState != t.baseline.VendorFileState) {
		return errors.New("boot, manager or vendor definition changed during qualification")
	}
	return nil
}

// admit accepts only known quiescent or attested vendor-only initial states.
func (q *Qualification) admit(o Observation) error {
	if o.Inhibition.Present || o.NativeFile.Present || o.Native.LoadState != "not-found" || !quiescent(o.Native) || o.Vendor.LoadState != "loaded" || (o.VendorFileState != "static" && o.VendorFileState != "disabled" && o.VendorFileState != "enabled") {
		return errors.New("initial service registration, masking or native state is not admissible")
	}
	if o.Vendor.DefinitionSHA256 != o.VendorDefinitionSHA256 {
		return errors.New("effective vendor definition differs from authenticated definition")
	}
	if o.Vendor.ActiveState == "inactive" && quiescent(o.Vendor) && o.Owner.UniqueName == "" {
		return nil
	}
	if o.Vendor.ActiveState == "active" && o.Vendor.SubState == "running" && !o.Vendor.PendingJob && o.Vendor.ControlPID == 0 && vendorOwner(o) {
		return nil
	}
	return errors.New("initial vendor or bus owner is not quiescent and recognised")
}

// excluded requires exact owned inhibition and an effective manager mask.
func (q *Qualification) excluded(o Observation, t *Transaction) (bool, error) {
	if err := q.ownedOrAbsent(o, t); err != nil {
		return false, err
	}
	if !q.ownFixture(o.Inhibition, t) {
		return false, errors.New("owned vendor inhibition is absent")
	}
	if o.Inhibition.Effective && o.Vendor.LoadState != "masked" {
		return false, errors.New("contradictory effective vendor mask observation")
	}
	if o.Owner.UniqueName != "" && !sameOwner(o.Owner, t.baseline.Owner) {
		return false, errors.New("foreign bus owner during exclusion")
	}
	return o.Inhibition.Effective, nil
}

// vendorQuiet requires empty cgroups and asynchronous release of the bus name.
func (q *Qualification) vendorQuiet(o Observation, t *Transaction) (bool, error) {
	ready, err := q.excluded(o, t)
	if err != nil || !ready {
		return ready, err
	}
	if o.Owner.UniqueName != "" {
		return false, nil
	}
	return quiescent(o.Vendor) && quiescent(o.Native), nil
}

// readyToStart additionally requires the exact loaded manual registration.
func (q *Qualification) readyToStart(o Observation, t *Transaction) (bool, error) {
	ready, err := q.vendorQuiet(o, t)
	if err != nil || !ready {
		return ready, err
	}
	if !q.ownFixture(o.NativeFile, t) {
		return false, errors.New("owned native registration is absent")
	}
	return o.NativeFile.Effective && o.Native.LoadState == "loaded" && o.Native.DefinitionSHA256 == q.binding.UnitSHA256, nil
}

// active proves typed unit, process and bus-owner consistency independently of
// the start operation's result. The backend must supply authentic observations.
func (q *Qualification) active(o Observation, t *Transaction) (bool, error) {
	if err := q.ownedOrAbsent(o, t); err != nil {
		return false, err
	}
	if !q.ownFixture(o.Inhibition, t) || !o.Inhibition.Effective || o.Vendor.LoadState != "masked" || !q.ownFixture(o.NativeFile, t) || !o.NativeFile.Effective || !quiescent(o.Vendor) {
		return false, errors.New("service exclusion or registration changed during activation")
	}
	if o.Owner.UniqueName == "" {
		return false, nil
	}
	p := o.Native.Process
	if o.Native.LoadState != "loaded" || o.Native.ActiveState != "active" || o.Native.SubState != "running" || o.Native.PendingJob || o.Native.ControlPID != 0 || o.Native.MainPID == 0 || o.Native.MainPID != p.PID || o.Native.DefinitionSHA256 != q.binding.UnitSHA256 || p.PID != o.Owner.PID || p.StartTime == 0 || p.StartTime != o.Owner.StartTime || p.InvocationID == "" || p.InvocationID != o.Owner.InvocationID || p.UnitName != integration.UnitName || o.Owner.UnitName != integration.UnitName || p.CGroup != "/system.slice/"+integration.UnitName || p.RuntimePackage != q.binding.RuntimePackage || p.ProgramPath != q.binding.ProgramPath || p.ProgramSHA256 != q.binding.ProgramSHA256 || p.LoaderPath != q.binding.LoaderPath || p.LibraryPath != q.binding.LibraryPath || !slices.Equal(p.Argv, q.binding.Argv) {
		return false, errors.New("native process or bus ownership does not match the fixed runtime binding")
	}
	if t.nativeEpoch != nil && !reflect.DeepEqual(*t.nativeEpoch, p) {
		return false, errors.New("native invocation changed after attestation")
	}
	if t.nativeEpoch == nil {
		captured := p
		captured.Argv = slices.Clone(p.Argv)
		t.nativeEpoch = &captured
	}
	return true, nil
}

// admitNativeStop permits compensation only for the exact loaded registration
// and recognised process binding, even when it never acquired the bus name.
// It never uses a failed start or a unit name alone as authority to stop an
// unknown replacement. The first matching epoch is retained process-locally.
func (q *Qualification) admitNativeStop(o Observation, t *Transaction) error {
	if !q.ownFixture(o.Inhibition, t) || !o.Inhibition.Effective || o.Vendor.LoadState != "masked" {
		return errors.New("vendor exclusion changed before native stop admission")
	}
	if !q.ownFixture(o.NativeFile, t) || !o.NativeFile.Effective || o.Native.LoadState != "loaded" || o.Native.DefinitionSHA256 != q.binding.UnitSHA256 {
		return errors.New("unknown loaded native registration cannot be stopped")
	}
	if quiescent(o.Native) {
		if o.Owner.UniqueName != "" {
			return errors.New("unexpected bus owner before native stop")
		}
		return nil
	}
	p := o.Native.Process
	if o.Native.ControlPID != 0 || o.Native.MainPID == 0 || o.Native.MainPID != p.PID || p.StartTime == 0 || p.InvocationID == "" || p.UnitName != integration.UnitName || p.CGroup != "/system.slice/"+integration.UnitName || p.RuntimePackage != q.binding.RuntimePackage || p.ProgramPath != q.binding.ProgramPath || p.ProgramSHA256 != q.binding.ProgramSHA256 || p.LoaderPath != q.binding.LoaderPath || p.LibraryPath != q.binding.LibraryPath || !slices.Equal(p.Argv, q.binding.Argv) {
		return errors.New("unknown native process cannot be stopped by compensation")
	}
	if t.nativeEpoch != nil && !reflect.DeepEqual(*t.nativeEpoch, p) {
		return errors.New("replacement native invocation cannot be stopped")
	}
	if o.Owner.UniqueName != "" && (o.Owner.PID != p.PID || o.Owner.StartTime != p.StartTime || o.Owner.InvocationID != p.InvocationID || o.Owner.UnitName != integration.UnitName) {
		return errors.New("foreign bus owner prevents native stop admission")
	}
	if t.nativeEpoch == nil {
		captured := p
		captured.Argv = slices.Clone(p.Argv)
		t.nativeEpoch = &captured
	}
	return nil
}

// nativeQuiet leaves exclusion held while all native resources disappear.
func (q *Qualification) nativeQuiet(o Observation, t *Transaction) (bool, error) {
	if err := q.ownedOrAbsent(o, t); err != nil {
		return false, err
	}
	if !q.ownFixture(o.Inhibition, t) || !o.Inhibition.Effective || o.Vendor.LoadState != "masked" {
		return false, errors.New("inhibition lost before native quiescence")
	}
	if !q.ownFixture(o.NativeFile, t) || !o.NativeFile.Effective || o.Native.LoadState != "loaded" || o.Native.DefinitionSHA256 != q.binding.UnitSHA256 {
		return false, errors.New("loaded native registration changed during stop")
	}
	if !quiescent(o.Vendor) {
		return false, errors.New("vendor resources reappeared while inhibited")
	}
	if o.Owner.UniqueName != "" && o.Owner.UnitName != integration.UnitName {
		return false, errors.New("foreign owner prevents safe compensation")
	}
	return quiescent(o.Native) && o.Owner.UniqueName == "", nil
}

// registrationForRemoval checks the effective definition immediately before
// registration removal, including a partial install never loaded or started.
func (q *Qualification) registrationForRemoval(o Observation, t *Transaction) error {
	if !o.NativeFile.Present {
		if t.startAttempted {
			return errors.New("native registration disappeared after start")
		}
		return q.stoppedNativeDefinition(o)
	}
	if !q.ownFixture(o.NativeFile, t) {
		return errors.New("native registration ownership changed before removal")
	}
	if o.NativeFile.Effective {
		if o.Native.LoadState != "loaded" || o.Native.DefinitionSHA256 != q.binding.UnitSHA256 {
			return errors.New("effective native definition changed before removal")
		}
		return nil
	}
	if t.startAttempted || o.Native.LoadState != "not-found" || o.Native.DefinitionSHA256 != "" {
		return errors.New("unrecognised partial native registration before removal")
	}
	return nil
}

// stoppedNativeDefinition refuses an unknown loaded unit before releasing the
// mask; our just-removed unit may remain loaded until the following reload.
func (q *Qualification) stoppedNativeDefinition(o Observation) error {
	if !quiescent(o.Native) {
		return errors.New("native process or pending work remains")
	}
	if o.Native.LoadState == "not-found" && o.Native.DefinitionSHA256 == "" || o.Native.LoadState == "loaded" && o.Native.DefinitionSHA256 == q.binding.UnitSHA256 {
		return nil
	}
	return errors.New("unknown stopped native definition prevents exclusion release")
}

// restored verifies registration removal and the prior functional vendor state.
// It does not claim restoration of an old PID or immunity to external triggers.
func (q *Qualification) restored(o Observation, t *Transaction) (bool, error) {
	if o.Inhibition.Present || o.NativeFile.Present || o.Native.LoadState != "not-found" || !quiescent(o.Native) {
		return false, errors.New("qualification registration remains after compensation")
	}
	if o.Vendor.LoadState != t.baseline.Vendor.LoadState || o.Vendor.DefinitionSHA256 != t.baseline.Vendor.DefinitionSHA256 {
		return false, errors.New("effective vendor definition was not restored")
	}
	if t.baseline.Vendor.ActiveState == "inactive" {
		if o.Owner.UniqueName != "" {
			return false, errors.New("unexpected owner after restoring inactive vendor policy")
		}
		return quiescent(o.Vendor), nil
	}
	if o.Owner.UniqueName == "" {
		return false, nil
	}
	if !vendorOwner(o) {
		return false, errors.New("restored bus owner is not the recognised vendor process")
	}
	return o.Vendor.ActiveState == "active" && o.Vendor.SubState == "running" && !o.Vendor.PendingJob, nil
}

// ownedOrAbsent refuses changed/foreign file evidence before any compensation.
func (q *Qualification) ownedOrAbsent(o Observation, t *Transaction) error {
	for _, f := range []Fixture{o.Inhibition, o.NativeFile} {
		if f.Present && !q.ownFixture(f, t) {
			return errors.New("owned qualification file changed or was replaced")
		}
	}
	return nil
}

// ownFixture compares the process-held claim, never a decoded receipt claim.
func (q *Qualification) ownFixture(f Fixture, t *Transaction) bool {
	return f.Present && f.Exact && f.ClaimID == t.claim.id
}

// quiescent requires no pending control work or surviving cgroup processes.
func quiescent(u Unit) bool {
	return u.ActiveState == "inactive" && u.SubState == "dead" && u.MainPID == 0 && u.ControlPID == 0 && !u.PendingJob && u.CGroupEmpty
}

// vendorOwner cross-checks a recognised vendor unit and bus process epoch.
func vendorOwner(o Observation) bool {
	p := o.Vendor.Process
	return o.Owner.UniqueName != "" && p.PID != 0 && o.Vendor.MainPID == p.PID && p.StartTime != 0 && p.InvocationID != "" && p.UnitName == integration.VendorUnitName && o.Owner.PID == p.PID && o.Owner.StartTime == p.StartTime && o.Owner.InvocationID == p.InvocationID && o.Owner.UnitName == integration.VendorUnitName
}

// sameOwner permits only the original unique vendor owner during shutdown.
func sameOwner(a, b BusOwner) bool { return a == b }

// set records one descriptive monotonic transition without publishing authority.
func (t *Transaction) set(phase Phase) { t.phase = phase; t.transitions = append(t.transitions, phase) }

// cloneObservation prevents mutable argument slices from rewriting old evidence.
func cloneObservation(o Observation) Observation {
	o.Vendor.Process.Argv = slices.Clone(o.Vendor.Process.Argv)
	o.Native.Process.Argv = slices.Clone(o.Native.Process.Argv)
	return o
}

// cloneBinding defensively copies every integration snapshot slice.
func cloneBinding(s integration.Snapshot) integration.Snapshot {
	s.Argv = slices.Clone(s.Argv)
	s.Assets = slices.Clone(s.Assets)
	s.RawTemplates = slices.Clone(s.RawTemplates)
	return s
}

// nonce supplies an unguessable descriptive process-local fixture identifier.
func nonce() (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(data[:]), nil
}

// digestEqual verifies complete immutable unit bytes before fixture operations.
func digestEqual(data []byte, want string) bool {
	sum := sha256.Sum256(data)
	return sha256Text(want) && hex.EncodeToString(sum[:]) == want
}

// sha256Text recognises only canonical lowercase complete digest text.
func sha256Text(text string) bool {
	data, err := hex.DecodeString(text)
	return err == nil && len(data) == sha256.Size && hex.EncodeToString(data) == text
}
