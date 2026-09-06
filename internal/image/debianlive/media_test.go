package debianlive

import (
	"strings"
	"testing"

	imagecontract "github.com/ooaklee/lexr.sh/internal/image"
)

// validUUID is a canonical generated UUID used across the contract tests.
const validUUID = "c5ef1897-ed0f-490e-b6d8-961dc41124b2"

// alternativeUUID is a second canonical UUID used to build mismatch cases.
const alternativeUUID = "7bda4398-0498-4564-acfe-e90dcc1c75f2"

// validMediumArtifact returns a well-formed artefact record for the medium
// identity marker.
func validMediumArtifact() imagecontract.ArtifactRecord {
	return imagecontract.ArtifactRecord{
		Path: mediumIdentityPath, SHA256: strings.Repeat("a", 64), Size: 37,
	}
}

// mustBuildContract builds the reference contract or fails the test.
func mustBuildContract(t *testing.T) mediaContract {
	t.Helper()
	contract, err := newDirectHybrid([]byte(validUUID + "\n"))
	if err != nil {
		t.Fatalf("newDirectHybrid() error = %v", err)
	}
	return contract
}

// mustBuildRecord builds the reference discovery record or fails the test.
func mustBuildRecord(t *testing.T) imagecontract.MediaDiscoveryRecord {
	t.Helper()
	record, err := mustBuildContract(t).discoveryRecord(validMediumArtifact())
	if err != nil {
		t.Fatalf("discoveryRecord() error = %v", err)
	}
	return record
}

// TestNewDirectHybridBuildsLiveBootContract verifies a generated UUID expands
// to the exact direct-media identity paths understood by live-boot.
func TestNewDirectHybridBuildsLiveBootContract(t *testing.T) {
	t.Parallel()

	contract := mustBuildContract(t)
	if contract.Strategy != directHybridStrategy || contract.Protocol != liveBootProtocol ||
		contract.MediumPath != mediumIdentityPath || contract.InitramfsPath != initramfsIdentityPath ||
		contract.UUID != validUUID {
		t.Fatalf("newDirectHybrid() = %#v", contract)
	}
	if err := contract.validate(); err != nil {
		t.Fatalf("mediaContract.validate() error = %v", err)
	}
}

// TestParseUUIDRejectsUnsafeValues verifies malformed, non-canonical, and
// multi-value identities cannot enter ISO paths or discovery metadata.
func TestParseUUIDRejectsUnsafeValues(t *testing.T) {
	t.Parallel()

	for _, value := range []string{
		"",
		" " + validUUID,
		validUUID + " ",
		validUUID + "\r\n",
		validUUID + "\n\n",
		strings.ToUpper(validUUID),
		"c5ef1897-ed0f-490e-b6d8-961dc41124b",
		"c5ef1897-ed0f-490e-b6d8-961dc41124bg",
		validUUID + "\n" + alternativeUUID,
		"../../etc/passwd",
	} {
		if _, err := parseUUID([]byte(value)); err == nil {
			t.Errorf("parseUUID(%q) succeeded, want error", value)
		}
	}
}

// TestParseUUIDAcceptsUUIDgenOutput verifies the uuidgen trailing newline is
// accepted exactly once.
func TestParseUUIDAcceptsUUIDgenOutput(t *testing.T) {
	t.Parallel()

	for _, value := range []string{validUUID, validUUID + "\n"} {
		got, err := parseUUID([]byte(value))
		if err != nil || got != validUUID {
			t.Errorf("parseUUID(%q) = %q, %v", value, got, err)
		}
	}
}

// TestMatchesComparesLiveBootIdentity verifies matching identity files pair
// into the contract and mismatched identities are rejected, mirroring
// live-boot's exact-content matches_uuid comparison.
func TestMatchesComparesLiveBootIdentity(t *testing.T) {
	t.Parallel()

	contract, err := matches([]byte(validUUID+"\n"), []byte(validUUID))
	if err != nil || contract != mustBuildContract(t) {
		t.Fatalf("matches() = %#v, %v", contract, err)
	}
	if _, err := matches([]byte(alternativeUUID+"\n"), []byte(validUUID+"\n")); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("matches() mismatch error = %v", err)
	}
	if _, err := matches([]byte("C5EF1897-ED0F-490E-B6D8-961DC41124B2"), []byte(validUUID)); err == nil {
		t.Fatal("matches() accepted an uppercase medium UUID")
	}
}

// TestDiscoveryRecordRoundTrip verifies debianlive owns the translation between
// its strict identity pairing and the distribution-neutral manifest evidence
// model, including the boot-arguments evidence.
func TestDiscoveryRecordRoundTrip(t *testing.T) {
	t.Parallel()

	contract := mustBuildContract(t)
	record, err := contract.discoveryRecord(validMediumArtifact())
	if err != nil {
		t.Fatalf("discoveryRecord() error = %v", err)
	}
	if len(record.Evidence) != 3 {
		t.Fatalf("discoveryRecord() produced %d evidence entries, want 3", len(record.Evidence))
	}
	if record.Evidence[2].Role != bootArgumentsRole || record.Evidence[2].Value != requiredBootArguments ||
		record.Evidence[2].Path != "" || record.Evidence[2].Scope != kernelCommandLineScope {
		t.Fatalf("boot-arguments evidence = %#v", record.Evidence[2])
	}
	gotContract, gotMarker, err := fromDiscoveryRecord(record)
	if err != nil {
		t.Fatalf("fromDiscoveryRecord() error = %v", err)
	}
	marker := validMediumArtifact()
	if gotContract != contract || gotMarker != marker {
		t.Fatalf("round trip = %#v, %#v; want %#v, %#v", gotContract, gotMarker, contract, marker)
	}
}

// TestFromDiscoveryRecordRejectsUnexpectedEvidence proves an adapter cannot
// silently accept Casper values, duplicated evidence, unsupported roles, wrong
// boot arguments, misplaced artefacts, or invalid artefact records.
func TestFromDiscoveryRecordRejectsUnexpectedEvidence(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name   string
		mutate func(record imagecontract.MediaDiscoveryRecord) imagecontract.MediaDiscoveryRecord
	}{
		{
			name: "casper protocol",
			mutate: func(record imagecontract.MediaDiscoveryRecord) imagecontract.MediaDiscoveryRecord {
				record.Protocol = "casper"
				return record
			},
		},
		{
			name: "casper strategy label",
			mutate: func(record imagecontract.MediaDiscoveryRecord) imagecontract.MediaDiscoveryRecord {
				record.Strategy = "casper-hybrid"
				return record
			},
		},
		{
			name: "casper marker path",
			mutate: func(record imagecontract.MediaDiscoveryRecord) imagecontract.MediaDiscoveryRecord {
				record.Evidence[0].Path = ".disk/casper-uuid-generic"
				return record
			},
		},
		{
			name: "duplicate medium identity",
			mutate: func(record imagecontract.MediaDiscoveryRecord) imagecontract.MediaDiscoveryRecord {
				record.Evidence = append(record.Evidence, record.Evidence[0])
				return record
			},
		},
		{
			name: "duplicate boot arguments",
			mutate: func(record imagecontract.MediaDiscoveryRecord) imagecontract.MediaDiscoveryRecord {
				record.Evidence = append(record.Evidence, record.Evidence[2])
				return record
			},
		},
		{
			name: "unsupported extra evidence role",
			mutate: func(record imagecontract.MediaDiscoveryRecord) imagecontract.MediaDiscoveryRecord {
				record.Evidence = append(record.Evidence, imagecontract.MediaDiscoveryEvidence{Role: "filesystem-label"})
				return record
			},
		},
		{
			name: "missing boot arguments evidence",
			mutate: func(record imagecontract.MediaDiscoveryRecord) imagecontract.MediaDiscoveryRecord {
				record.Evidence = record.Evidence[:2]
				return record
			},
		},
		{
			name: "wrong boot arguments value",
			mutate: func(record imagecontract.MediaDiscoveryRecord) imagecontract.MediaDiscoveryRecord {
				record.Evidence[2].Value = "boot=casper components"
				return record
			},
		},
		{
			name: "artifact on initramfs evidence",
			mutate: func(record imagecontract.MediaDiscoveryRecord) imagecontract.MediaDiscoveryRecord {
				artifact := validMediumArtifact()
				record.Evidence[1].Artifact = &artifact
				return record
			},
		},
		{
			name: "missing medium artifact",
			mutate: func(record imagecontract.MediaDiscoveryRecord) imagecontract.MediaDiscoveryRecord {
				record.Evidence[0].Artifact = nil
				return record
			},
		},
		{
			name: "identity values disagree",
			mutate: func(record imagecontract.MediaDiscoveryRecord) imagecontract.MediaDiscoveryRecord {
				record.Evidence[1].Value = alternativeUUID
				return record
			},
		},
		{
			name: "uppercase medium UUID",
			mutate: func(record imagecontract.MediaDiscoveryRecord) imagecontract.MediaDiscoveryRecord {
				record.Evidence[0].Value = strings.ToUpper(validUUID)
				return record
			},
		},
		{
			name: "traversal medium UUID",
			mutate: func(record imagecontract.MediaDiscoveryRecord) imagecontract.MediaDiscoveryRecord {
				record.Evidence[0].Value = "../../etc/passwd"
				record.Evidence[1].Value = "../../etc/passwd"
				return record
			},
		},
		{
			name: "negative-size medium artifact",
			mutate: func(record imagecontract.MediaDiscoveryRecord) imagecontract.MediaDiscoveryRecord {
				record.Evidence[0].Artifact.Size = -1
				return record
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			record := mustBuildRecord(t)
			if _, _, err := fromDiscoveryRecord(tt.mutate(record)); err == nil {
				t.Fatal("fromDiscoveryRecord() accepted invalid evidence")
			}
		})
	}
}

// TestDiscoveryRecordRejectsWrongArtifactPath verifies the medium artefact must
// name the live-boot identity marker.
func TestDiscoveryRecordRejectsWrongArtifactPath(t *testing.T) {
	t.Parallel()

	artifact := validMediumArtifact()
	artifact.Path = "conf/uuid.conf"
	if _, err := mustBuildContract(t).discoveryRecord(artifact); err == nil {
		t.Fatal("discoveryRecord() accepted an initramfs-path artifact")
	}
}
