// Package policy defines immutable userspace release identities shared by
// unprivileged acquisition, image staging, and privileged installation.
package policy

import (
	"errors"
	"fmt"

	userspacerelease "github.com/ooaklee/lexr.sh/internal/userspace/release"
)

const (
	// IPTSDComponent is the sole userspace component currently approved for an
	// offline companion bundle.
	IPTSDComponent = "iptsd-v1"
	// IPTSDRepository is the sole trusted publisher of the pinned IPTSD bundle.
	IPTSDRepository = userspacerelease.DefaultRepository
	// IPTSDTag is the exact immutable release accepted for offline installation.
	IPTSDTag = "sp11-iptsd-v2"
)

// Artifact binds one immutable release filename to its exact digest and byte
// length.
type Artifact struct {
	// Name is the flat release asset filename.
	Name string
	// SHA256 is the lowercase hexadecimal digest of the complete file.
	SHA256 string
	// Size is the exact expected file length in bytes.
	Size int64
}

// Release binds one component to its trusted publisher, tag, and complete
// installable asset set.
type Release struct {
	// Component is the stable userspace catalogue identifier.
	Component string
	// Repository is the exact GitHub owner and repository identity.
	Repository string
	// Tag is the immutable release tag.
	Tag string
	// Artifacts is the complete verified release file set in receipt order.
	Artifacts []Artifact
}

// IPTSDRelease returns the compiled IPTSD release trust contract as fresh data
// so callers cannot widen process-wide policy.
func IPTSDRelease() Release {
	return Release{
		Component:  IPTSDComponent,
		Repository: IPTSDRepository,
		Tag:        IPTSDTag,
		Artifacts: []Artifact{
			{Name: "SHA256SUMS", SHA256: "3910e64748674525b7e1e401aa18fcffd3c3918227086085e665034732f38141", Size: 103},
			{Name: "sp11-iptsd-3.1.0-sp11.2-arm64.tar.xz", SHA256: "c3a6235d7c0a7cc588add7b1f5aab68c6e980b2e03c1e938cc4c5751899719e6", Size: 4375724},
		},
	}
}

// ValidateIdentity checks that a bundle names the exact compiled component,
// publisher, and immutable release tag.
func (contract Release) ValidateIdentity(bundle userspacerelease.Bundle) error {
	if bundle.Component != contract.Component {
		return fmt.Errorf("userspace bundle component is %q, expected %q", bundle.Component, contract.Component)
	}
	if bundle.Repository != contract.Repository {
		return fmt.Errorf("userspace bundle repository is %q, expected %q", bundle.Repository, contract.Repository)
	}
	if bundle.Release != contract.Tag {
		return fmt.Errorf("userspace bundle release is %q, expected %q", bundle.Release, contract.Tag)
	}
	return nil
}

// ValidateArtifacts checks that a verified bundle declares exactly the
// compiled asset filenames, byte lengths, and digests.
func (contract Release) ValidateArtifacts(files []userspacerelease.File) error {
	expected := make(map[string]Artifact, len(contract.Artifacts))
	for _, artifact := range contract.Artifacts {
		if _, duplicate := expected[artifact.Name]; duplicate {
			return fmt.Errorf("compiled userspace release repeats artefact %q", artifact.Name)
		}
		expected[artifact.Name] = artifact
	}
	if len(files) != len(expected) {
		return fmt.Errorf("userspace bundle contains %d files, expected %d", len(files), len(expected))
	}
	seen := make(map[string]bool, len(files))
	for _, file := range files {
		artifact, found := expected[file.Name]
		if !found {
			return fmt.Errorf("userspace bundle contains unexpected file %q", file.Name)
		}
		if seen[file.Name] {
			return fmt.Errorf("userspace bundle repeats file %q", file.Name)
		}
		seen[file.Name] = true
		if !file.Verified {
			return fmt.Errorf("userspace bundle marks %s as unverified", file.Name)
		}
		if file.SHA256 != artifact.SHA256 || file.Size != artifact.Size {
			return fmt.Errorf("userspace bundle metadata disagrees with immutable release metadata for %s", file.Name)
		}
	}
	return nil
}

// ValidateBundle checks one acquired bundle against the complete compiled
// release identity without trusting mutable catalogue metadata.
func (contract Release) ValidateBundle(bundle userspacerelease.Bundle) error {
	if err := contract.ValidateIdentity(bundle); err != nil {
		return err
	}
	return contract.ValidateArtifacts(bundle.Files)
}

// PortableReceipt returns the canonical location-independent receipt bound to
// the complete compiled release identity.
func (contract Release) PortableReceipt() ([]byte, error) {
	if contract.Component == "" || contract.Repository == "" || contract.Tag == "" {
		return nil, errors.New("compiled userspace release identity is incomplete")
	}
	receipt := userspacerelease.Bundle{
		Component: contract.Component, Repository: contract.Repository,
		Release: contract.Tag, Directory: ".",
		Files: make([]userspacerelease.File, len(contract.Artifacts)),
	}
	for index, artifact := range contract.Artifacts {
		receipt.Files[index] = userspacerelease.File{
			Name: artifact.Name, Path: artifact.Name, SHA256: artifact.SHA256,
			Size: artifact.Size, Verified: true,
		}
	}
	return userspacerelease.MarshalPortableReceipt(receipt)
}
