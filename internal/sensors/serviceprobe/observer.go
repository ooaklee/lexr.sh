package serviceprobe

import (
	"context"
	"errors"
	"strings"
)

// operation is the closed, read-only request vocabulary. It deliberately omits
// GetUnit, ListUnitsByNames, loading, introspection and all mutation methods.
type operation uint8

// The operation set cannot accept a caller-selected method or unit name.
const (
	readBusID operation = iota + 1
	managerPresence
	managerOwner
	proxyPresence
	proxyOwner
	peerPID
	loadedUnits
)

// request carries a validated unique destination or credential-query peer only
// where required; all other arguments are fixed by its operation.
type request struct {
	operation operation
	peer      string
}

// commandRunner performs one closed bounded query and must honour its context.
// It is an internal seam, not a public arbitrary-process execution interface.
type commandRunner interface {
	// capture returns only complete successful stdout, never partial diagnostics.
	capture(context.Context, request) ([]byte, error)
}

// bootReader observes one bounded canonical boot identifier without mutation.
type bootReader interface {
	// read returns the identifier for the same boot before and after collection.
	read(context.Context) (string, error)
}

// peerIdentity contains one presence observation and its queried credentials.
type peerIdentity struct {
	present bool
	owner   string
	pid     uint32
}

// uniqueName admits the narrow numeric unique-name form used by the supported
// local bus. Other broker grammars remain unavailable, never silently trusted.
func uniqueName(value string) bool {
	if len(value) < 4 || len(value) > 64 || value[0] != ':' {
		return false
	}
	parts := strings.Split(value[1:], ".")
	if len(parts) != 2 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for _, digit := range part {
			if digit < '0' || digit > '9' {
				return false
			}
		}
	}
	return true
}

// hexText recognises exact lowercase hexadecimal bytes without formatting.
func hexText(value string, count int) bool {
	if len(value) != count {
		return false
	}
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			if digit < 'a' || digit > 'f' {
				return false
			}
		}
	}
	return true
}

// bootIDText recognises the fixed kernel UUID spelling without whitespace.
func bootIDText(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	return hexText(strings.ReplaceAll(value, "-", ""), 32)
}

// arguments validates a closed request before returning immutable command args.
func (r request) arguments() ([]string, error) {
	args := []string{"--system", "--no-pager", "--json=short", "--auto-start=no", "--allow-interactive-authorization=no", "--timeout=2s", "call"}
	if r.operation != peerPID && r.operation != loadedUnits && r.peer != "" {
		return nil, errors.New("passive service request has an unexpected peer")
	}
	switch r.operation {
	case readBusID:
		return append(args, busName, busPath, busName, "GetId"), nil
	case managerPresence:
		return append(args, busName, busPath, busName, "NameHasOwner", "s", managerName), nil
	case managerOwner:
		return append(args, busName, busPath, busName, "GetNameOwner", "s", managerName), nil
	case proxyPresence:
		return append(args, busName, busPath, busName, "NameHasOwner", "s", proxyName), nil
	case proxyOwner:
		return append(args, busName, busPath, busName, "GetNameOwner", "s", proxyName), nil
	case peerPID:
		if !uniqueName(r.peer) {
			return nil, errors.New("passive credential request requires a validated unique peer")
		}
		return append(args, busName, busPath, busName, "GetConnectionUnixProcessID", "s", r.peer), nil
	case loadedUnits:
		if !uniqueName(r.peer) {
			return nil, errors.New("passive manager request requires a validated unique destination")
		}
		return append(args, r.peer, managerPath, managerName+".Manager", "ListUnitsByPatterns", "asas", "0", "2", vendorUnit, nativeUnit), nil
	default:
		return nil, errors.New("unrecognised passive service operation")
	}
}

// query applies the per-call deadline even to injected test runners and refuses
// late or oversized results before a wire decoder sees them.
func query(ctx context.Context, runner commandRunner, r request) ([]byte, error) {
	if _, err := r.arguments(); err != nil {
		return nil, err
	}
	callContext, cancel := context.WithTimeout(ctx, maximumCallDuration)
	defer cancel()
	if err := callContext.Err(); err != nil {
		return nil, err
	}
	data, err := runner.capture(callContext, r)
	if err != nil {
		if contextErr := callContext.Err(); contextErr != nil {
			return nil, contextErr
		}
		return nil, errors.New("passive sensor bus query is unavailable")
	}
	if err := callContext.Err(); err != nil {
		return nil, err
	}
	if len(data) == 0 || int64(len(data)) > maximumStdout {
		return nil, errors.New("passive sensor bus reply is empty or exceeds its fixed bound")
	}
	return data, nil
}

// identity resolves only the fixed manager or proxy name. A failed query never
// becomes name absence, and a present owner always needs a nonzero typed PID.
func identity(ctx context.Context, runner commandRunner, proxy bool) (peerIdentity, error) {
	presence, owner := managerPresence, managerOwner
	if proxy {
		presence, owner = proxyPresence, proxyOwner
	}
	data, err := query(ctx, runner, request{operation: presence})
	if err != nil {
		return peerIdentity{}, err
	}
	present, err := decodeBool(data)
	if err != nil {
		return peerIdentity{}, err
	}
	if !present {
		return peerIdentity{}, nil
	}
	data, err = query(ctx, runner, request{operation: owner})
	if err != nil {
		return peerIdentity{}, err
	}
	name, err := decodeString(data)
	if err != nil || !uniqueName(name) {
		return peerIdentity{}, errors.New("passive service owner is not a supported unique name")
	}
	data, err = query(ctx, runner, request{operation: peerPID, peer: name})
	if err != nil {
		return peerIdentity{}, err
	}
	pid, err := decodePID(data)
	if err != nil {
		return peerIdentity{}, err
	}
	return peerIdentity{present: true, owner: name, pid: pid}, nil
}

// readBoot checks identifiers supplied by the reader on both sides of all bus
// activity; even injected diagnostics cannot turn malformed IDs into evidence.
func readBoot(ctx context.Context, reader bootReader) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	value, err := reader.read(ctx)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !bootIDText(value) {
		return "", errors.New("passive sensor boot identifier is malformed")
	}
	return value, nil
}

// collect brackets a single diagnostic snapshot with repeated boot, bus,
// manager and proxy identities. It does not assert atomic process attestation.
func collect(ctx context.Context, runner commandRunner, reader bootReader) (*Snapshot, error) {
	if ctx == nil || runner == nil || reader == nil {
		return nil, errors.New("passive sensor observation requires context and bounded readers")
	}
	ctx, cancel := context.WithTimeout(ctx, maximumObservation)
	defer cancel()
	boot, err := readBoot(ctx, reader)
	if err != nil {
		return nil, err
	}
	data, err := query(ctx, runner, request{operation: readBusID})
	if err != nil {
		return nil, err
	}
	bus, err := decodeString(data)
	if err != nil || !hexText(bus, 32) {
		return nil, errors.New("passive sensor bus identifier is malformed")
	}
	manager, err := identity(ctx, runner, false)
	if err != nil {
		return nil, err
	}
	if !manager.present {
		return nil, errors.New("passive sensor service manager has no observed owner")
	}
	data, err = query(ctx, runner, request{operation: loadedUnits, peer: manager.owner})
	if err != nil {
		return nil, err
	}
	units, err := decodeUnits(data)
	if err != nil {
		return nil, err
	}
	proxy, err := identity(ctx, runner, true)
	if err != nil {
		return nil, err
	}
	managerAfter, err := identity(ctx, runner, false)
	if err != nil {
		return nil, err
	}
	proxyAfter, err := identity(ctx, runner, true)
	if err != nil {
		return nil, err
	}
	data, err = query(ctx, runner, request{operation: readBusID})
	if err != nil {
		return nil, err
	}
	busAfter, err := decodeString(data)
	if err != nil {
		return nil, err
	}
	bootAfter, err := readBoot(ctx, reader)
	if err != nil {
		return nil, err
	}
	if boot != bootAfter || bus != busAfter || manager != managerAfter || proxy != proxyAfter {
		return nil, errors.New("passive sensor observation changed during identity bracketing")
	}
	return &Snapshot{facts: Facts{BusID: bus, BootID: boot, ManagerOwner: manager.owner, ManagerPID: manager.pid, ProxyOwner: proxy.owner, ProxyPID: proxy.pid, Units: append([]LoadedUnit(nil), units...)}, valid: true}, nil
}
