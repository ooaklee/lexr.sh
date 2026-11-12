// Package serviceprobe reads a closed set of passive service-manager facts.
// Its snapshots are diagnostics, not process attestation, lifecycle.Backend
// observations, service mutation authority or installation approval.
package serviceprobe

import (
	"errors"
	"time"
)

// Compiled names and budgets exclude caller-selected buses, unit patterns,
// executables, methods and operation timeouts from the public observer API.
const (
	vendorUnit          = "iio-sensor-proxy.service"
	nativeUnit          = "lexr-native-sensors.service"
	managerName         = "org.freedesktop.systemd1"
	proxyName           = "net.hadess.SensorProxy"
	busName             = "org.freedesktop.DBus"
	busPath             = "/org/freedesktop/DBus"
	managerPath         = "/org/freedesktop/systemd1"
	maximumStdout       = int64(64 << 10)
	maximumStderr       = int64(8 << 10)
	maximumCallDuration = 3 * time.Second
	maximumObservation  = 20 * time.Second
	processWaitDelay    = 250 * time.Millisecond
)

// LoadedUnit is the exact a(ssssssouso) manager tuple, not an effective unit
// definition or evidence that its executable, process or files are trusted.
type LoadedUnit struct {
	Name        string
	Description string
	LoadState   string
	ActiveState string
	SubState    string
	Following   string
	ObjectPath  string
	JobID       uint32
	JobType     string
	JobPath     string
}

// Facts reports only identities consistently bracketed by passive queries.
// PIDs are bus-reported credentials, not independently attested process epochs.
// Empty ProxyOwner and zero ProxyPID mean explicitly reobserved name absence.
type Facts struct {
	BusID        string
	BootID       string
	ManagerOwner string
	ManagerPID   uint32
	ProxyOwner   string
	ProxyPID     uint32
	Units        []LoadedUnit
}

// Snapshot is opaque and zero-invalid. It cannot be decoded from a receipt or
// supplied to the fixture lifecycle engine as mutation authority.
type Snapshot struct {
	facts Facts
	valid bool
}

// Facts returns defensive diagnostic copies and refuses an invalid handle.
func (s *Snapshot) Facts() (Facts, error) {
	if s == nil || !s.valid {
		return Facts{}, errors.New("passive sensor service snapshot is invalid")
	}
	result := s.facts
	result.Units = append([]LoadedUnit(nil), result.Units...)
	return result, nil
}
