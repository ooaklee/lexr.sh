// Package hostcap describes static host requirements for operations that are
// wholly unavailable on some compiled targets. Dynamic prerequisites remain
// owned by the domain that can evaluate them accurately.
package hostcap

import (
	"fmt"
	"runtime"
	"strings"
)

// Host is the complete operating-system and architecture identity of one binary.
type Host struct {
	GOOS   string
	GOARCH string
}

// Current returns the platform reported by the Go runtime.
func Current() Host {
	return Host{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
}

// Requirement is the complete static host policy for one operation. An empty
// operating-system or architecture list accepts every value in that dimension.
type Requirement struct {
	Operation        string
	OperatingSystems []string
	Architectures    []string
}

// Availability records whether an operation may execute on a host.
type Availability struct {
	Executable       bool
	ExecutionBlocker string
}

// Evaluate applies one static requirement and fails closed for an incomplete
// injected host identity.
func (requirement Requirement) Evaluate(host Host) Availability {
	operation := strings.TrimSpace(requirement.Operation)
	if operation == "" {
		operation = "operation"
	}
	if strings.TrimSpace(host.GOOS) == "" || strings.TrimSpace(host.GOARCH) == "" {
		return Availability{ExecutionBlocker: operation + " cannot evaluate an incomplete host identity"}
	}
	if contains(requirement.OperatingSystems, host.GOOS) && contains(requirement.Architectures, host.GOARCH) {
		return Availability{Executable: true}
	}

	constraints := make([]string, 0, 2)
	if len(requirement.OperatingSystems) != 0 {
		constraints = append(constraints, "operating system "+joinAlternatives(requirement.OperatingSystems))
	}
	if len(requirement.Architectures) != 0 {
		constraints = append(constraints, "architecture "+joinAlternatives(requirement.Architectures))
	}
	required := "a supported host"
	if len(constraints) != 0 {
		required = strings.Join(constraints, " and ")
	}
	return Availability{
		Executable:       false,
		ExecutionBlocker: fmt.Sprintf("%s requires %s; this binary reports %s/%s", operation, required, host.GOOS, host.GOARCH),
	}
}

// contains reports whether a value satisfies an empty-or-explicit allow-list.
func contains(allowed []string, value string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, candidate := range allowed {
		if candidate == value {
			return true
		}
	}
	return false
}

// joinAlternatives renders a stable human-readable allow-list.
func joinAlternatives(values []string) string {
	return strings.Join(values, " or ")
}
