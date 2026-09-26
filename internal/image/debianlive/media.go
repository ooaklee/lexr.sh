// Package debianlive implements Debian live ARM64 media-source identity and
// the live-boot media contract.
package debianlive

import (
	"errors"
	"fmt"

	imagecontract "github.com/ooaklee/lexr.sh/internal/image"
)

const (
	// directHybridStrategy identifies an ISO written directly to removable media.
	directHybridStrategy = "direct-hybrid"
	// liveBootProtocol identifies the live-boot implementation.
	liveBootProtocol = "live-boot"
	// mediumIdentityPath is the ISO member live-boot compares while scanning media.
	mediumIdentityPath = ".disk/live-uuid"
	// initramfsIdentityPath is the generated initramfs member holding the same UUID.
	initramfsIdentityPath = "conf/uuid.conf"
	// mediumIdentityRole identifies the evidence read from the direct ISO.
	mediumIdentityRole = "medium-identity"
	// initramfsIdentityRole identifies the evidence embedded by the live hooks.
	initramfsIdentityRole = "initramfs-identity"
	// bootArgumentsRole identifies the kernel command line selectors that
	// live-boot needs to rediscover the medium.
	bootArgumentsRole = "boot-arguments"
	// isoFilesystemScope identifies evidence stored in the ISO 9660 filesystem.
	isoFilesystemScope = "iso-filesystem"
	// initramfsScope identifies evidence stored inside the live initramfs.
	initramfsScope = "initramfs"
	// kernelCommandLineScope identifies evidence observed in the boot command line.
	kernelCommandLineScope = "kernel-command-line"
	// liveMediaPath is live-boot's inspected payload directory.
	liveMediaPath = "live"
	// requiredBootArguments supplies the discovery selectors in the generated
	// SP11 menu; the source menu uses live-boot's default /live directory.
	requiredBootArguments = "boot=live components live-media-path=/live"
)

// mediaContract records how a direct Debian live ISO binds its generated
// initramfs to the physical medium that contains the live filesystem.
type mediaContract struct {
	// Strategy identifies the outer media-discovery layout.
	Strategy string
	// Protocol identifies the distribution live-boot implementation.
	Protocol string
	// MediumPath is the ISO-relative identity marker examined by live-boot.
	MediumPath string
	// InitramfsPath is the initramfs-relative identity consumed by live-boot.
	InitramfsPath string
	// UUID is the canonical identity that must be equal at both paths.
	UUID string
}

// newDirectHybrid parses a generated initramfs UUID and returns the complete
// direct-media live-boot contract that must be written into the ISO.
func newDirectHybrid(data []byte) (mediaContract, error) {
	uuid, err := parseUUID(data)
	if err != nil {
		return mediaContract{}, err
	}
	return mediaContract{
		Strategy:      directHybridStrategy,
		Protocol:      liveBootProtocol,
		MediumPath:    mediumIdentityPath,
		InitramfsPath: initramfsIdentityPath,
		UUID:          uuid,
	}, nil
}

// parseUUID accepts one canonical lowercase UUID and rejects additional data,
// uppercase spellings, path-like values, and malformed live-boot identities.
// Exactly one optional trailing newline is allowed, matching uuidgen output.
func parseUUID(data []byte) (string, error) {
	if len(data) == 37 && data[36] == '\n' {
		data = data[:36]
	}
	value := string(data)
	if value == "" {
		return "", errors.New("live-boot UUID is empty")
	}
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return "", fmt.Errorf("live-boot UUID %q is not canonical", value)
	}
	for index, character := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			continue
		}
		if character < '0' || character > '9' && character < 'a' || character > 'f' {
			return "", fmt.Errorf("live-boot UUID %q is not lowercase hexadecimal", value)
		}
	}
	return value, nil
}

// validate verifies that every field of a live-boot contract describes the
// supported direct-hybrid identity pairing.
func (c mediaContract) validate() error {
	if c.Strategy != directHybridStrategy {
		return fmt.Errorf("unsupported live-boot media strategy %q", c.Strategy)
	}
	if c.Protocol != liveBootProtocol {
		return fmt.Errorf("unsupported Debian live-boot protocol %q", c.Protocol)
	}
	if c.MediumPath != mediumIdentityPath || c.InitramfsPath != initramfsIdentityPath {
		return errors.New("live-boot identity paths do not match the direct-hybrid contract")
	}
	if _, err := parseUUID([]byte(c.UUID)); err != nil {
		return err
	}
	return nil
}

// discoveryRecord converts a valid live-boot contract and its immutable medium
// marker into the generic evidence shape shared by all image adapters. The
// boot-arguments evidence documents the discovery selectors live-boot needs on
// the kernel command line.
func (c mediaContract) discoveryRecord(medium imagecontract.ArtifactRecord) (imagecontract.MediaDiscoveryRecord, error) {
	if err := c.validate(); err != nil {
		return imagecontract.MediaDiscoveryRecord{}, err
	}
	if medium.Path != mediumIdentityPath {
		return imagecontract.MediaDiscoveryRecord{}, fmt.Errorf("live-boot medium artifact path is %q, expected %q", medium.Path, mediumIdentityPath)
	}
	if err := imagecontract.ValidateArtifactRecord(medium); err != nil {
		return imagecontract.MediaDiscoveryRecord{}, fmt.Errorf("validate live-boot medium artifact: %w", err)
	}
	return imagecontract.MediaDiscoveryRecord{
		Strategy: c.Strategy,
		Protocol: c.Protocol,
		Evidence: []imagecontract.MediaDiscoveryEvidence{
			{
				Role: mediumIdentityRole, Scope: isoFilesystemScope,
				Path: c.MediumPath, Value: c.UUID, Artifact: &medium,
			},
			{
				Role: initramfsIdentityRole, Scope: initramfsScope,
				Path: c.InitramfsPath, Value: c.UUID,
			},
			{
				Role: bootArgumentsRole, Scope: kernelCommandLineScope,
				Path: "", Value: requiredBootArguments,
			},
		},
	}, nil
}

// fromDiscoveryRecord validates generic manifest evidence as the exact direct
// live-boot contract and returns its independently hashed medium marker.
func fromDiscoveryRecord(record imagecontract.MediaDiscoveryRecord) (mediaContract, imagecontract.ArtifactRecord, error) {
	if record.Strategy != directHybridStrategy || record.Protocol != liveBootProtocol {
		return mediaContract{}, imagecontract.ArtifactRecord{}, fmt.Errorf(
			"unsupported live-boot discovery strategy %q and protocol %q", record.Strategy, record.Protocol)
	}
	if len(record.Evidence) != 3 {
		return mediaContract{}, imagecontract.ArtifactRecord{}, fmt.Errorf("live-boot discovery evidence has %d records, expected 3", len(record.Evidence))
	}
	var medium imagecontract.ArtifactRecord
	var mediumValue, initramfsValue string
	seenMedium, seenInitramfs, seenBoot := false, false, false
	for _, evidence := range record.Evidence {
		switch evidence.Role {
		case mediumIdentityRole:
			if seenMedium {
				return mediaContract{}, imagecontract.ArtifactRecord{}, errors.New("duplicate live-boot medium identity evidence")
			}
			if evidence.Scope != isoFilesystemScope || evidence.Path != mediumIdentityPath || evidence.Artifact == nil {
				return mediaContract{}, imagecontract.ArtifactRecord{}, errors.New("live-boot medium identity evidence is incomplete")
			}
			seenMedium = true
			medium = *evidence.Artifact
			mediumValue = evidence.Value
		case initramfsIdentityRole:
			if seenInitramfs {
				return mediaContract{}, imagecontract.ArtifactRecord{}, errors.New("duplicate live-boot initramfs identity evidence")
			}
			if evidence.Scope != initramfsScope || evidence.Path != initramfsIdentityPath || evidence.Artifact != nil {
				return mediaContract{}, imagecontract.ArtifactRecord{}, errors.New("live-boot initramfs identity evidence is incomplete")
			}
			seenInitramfs = true
			initramfsValue = evidence.Value
		case bootArgumentsRole:
			if seenBoot {
				return mediaContract{}, imagecontract.ArtifactRecord{}, errors.New("duplicate live-boot boot-arguments evidence")
			}
			if evidence.Scope != kernelCommandLineScope || evidence.Path != "" || evidence.Artifact != nil {
				return mediaContract{}, imagecontract.ArtifactRecord{}, errors.New("live-boot boot-arguments evidence is incomplete")
			}
			if evidence.Value != requiredBootArguments {
				return mediaContract{}, imagecontract.ArtifactRecord{}, fmt.Errorf("live-boot boot arguments %q do not match the inspected contract", evidence.Value)
			}
			seenBoot = true
		default:
			return mediaContract{}, imagecontract.ArtifactRecord{}, fmt.Errorf("unsupported live-boot discovery evidence role %q", evidence.Role)
		}
	}
	if !seenMedium || !seenInitramfs || !seenBoot {
		return mediaContract{}, imagecontract.ArtifactRecord{}, errors.New("live-boot discovery evidence is missing a required role")
	}
	if mediumValue == "" || initramfsValue == "" || mediumValue != initramfsValue {
		return mediaContract{}, imagecontract.ArtifactRecord{}, errors.New("live-boot discovery evidence identities do not agree")
	}
	contract, err := newDirectHybrid([]byte(mediumValue))
	if err != nil {
		return mediaContract{}, imagecontract.ArtifactRecord{}, err
	}
	if err := contract.validate(); err != nil {
		return mediaContract{}, imagecontract.ArtifactRecord{}, err
	}
	if medium.Path != mediumIdentityPath {
		return mediaContract{}, imagecontract.ArtifactRecord{}, fmt.Errorf("live-boot medium artifact path is %q, expected %q", medium.Path, mediumIdentityPath)
	}
	if err := imagecontract.ValidateArtifactRecord(medium); err != nil {
		return mediaContract{}, imagecontract.ArtifactRecord{}, fmt.Errorf("validate live-boot medium artifact: %w", err)
	}
	return contract, medium, nil
}

// matches reports whether medium and initramfs identity files contain the same
// valid UUID and returns the parsed contract when they agree. live-boot's
// matches_uuid compares file contents by exact string equality; accepting one
// optional trailing newline matches uuidgen output without changing that
// pairing semantics.
func matches(medium, initramfs []byte) (mediaContract, error) {
	mediumUUID, err := parseUUID(medium)
	if err != nil {
		return mediaContract{}, fmt.Errorf("parse live-boot medium UUID: %w", err)
	}
	initramfsUUID, err := parseUUID(initramfs)
	if err != nil {
		return mediaContract{}, fmt.Errorf("parse live-boot initramfs UUID: %w", err)
	}
	if mediumUUID != initramfsUUID {
		return mediaContract{}, fmt.Errorf("live-boot medium UUID %s does not match initramfs UUID %s", mediumUUID, initramfsUUID)
	}
	return newDirectHybrid(initramfs)
}
