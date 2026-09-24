package hostcap

import "testing"

// TestRequirementEvaluate verifies combined operating-system and architecture policy.
func TestRequirementEvaluate(t *testing.T) {
	t.Parallel()
	requirement := Requirement{
		Operation:        "native camera build",
		OperatingSystems: []string{"linux"},
		Architectures:    []string{"arm64"},
	}

	if availability := requirement.Evaluate(Host{GOOS: "linux", GOARCH: "arm64"}); !availability.Executable || availability.ExecutionBlocker != "" {
		t.Fatalf("expected Linux arm64 to be executable: %#v", availability)
	}

	availability := requirement.Evaluate(Host{GOOS: "darwin", GOARCH: "arm64"})
	want := "native camera build requires operating system linux and architecture arm64; this binary reports darwin/arm64"
	if availability.Executable || availability.ExecutionBlocker != want {
		t.Fatalf("unexpected unsupported-host decision: %#v", availability)
	}
}

// TestRequirementSupportsAlternativeOperatingSystems verifies ordered alternatives.
func TestRequirementSupportsAlternativeOperatingSystems(t *testing.T) {
	t.Parallel()
	requirement := Requirement{
		Operation:        "release publication",
		OperatingSystems: []string{"linux", "darwin"},
	}
	if availability := requirement.Evaluate(Host{GOOS: "darwin", GOARCH: "amd64"}); !availability.Executable {
		t.Fatalf("expected Darwin to be accepted: %#v", availability)
	}
	want := "release publication requires operating system linux or darwin; this binary reports windows/amd64"
	if availability := requirement.Evaluate(Host{GOOS: "windows", GOARCH: "amd64"}); availability.Executable || availability.ExecutionBlocker != want {
		t.Fatalf("unexpected Windows decision: %#v", availability)
	}
}

// TestUnconstrainedRequirementAcceptsHost verifies empty allow-lists mean any host.
func TestUnconstrainedRequirementAcceptsHost(t *testing.T) {
	t.Parallel()
	if availability := (Requirement{}).Evaluate(Host{GOOS: "plan9", GOARCH: "mips"}); !availability.Executable || availability.ExecutionBlocker != "" {
		t.Fatalf("expected unconstrained requirement to be executable: %#v", availability)
	}
}

// TestRequirementRejectsIncompleteHost verifies injected host identities fail closed.
func TestRequirementRejectsIncompleteHost(t *testing.T) {
	t.Parallel()
	availability := (Requirement{Operation: "publication"}).Evaluate(Host{GOOS: "linux"})
	if availability.Executable || availability.ExecutionBlocker != "publication cannot evaluate an incomplete host identity" {
		t.Fatalf("unexpected incomplete-host decision: %#v", availability)
	}
}
