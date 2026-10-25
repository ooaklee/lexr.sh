package build

import (
	"errors"

	"github.com/ooaklee/lexr.sh/internal/sensors/source"
)

// producerProfile is selected only by a compiled Manager constructor. It is
// never decoded from source metadata, a request, Docker labels or a receipt.
type producerProfile uint8

const (
	producer047 producerProfile = iota
	producer048
)

func (p producerProfile) valid() bool { return p == producer047 || p == producer048 }

func (p producerProfile) sourceCommit() string {
	switch p {
	case producer047:
		return SourceCommit
	case producer048:
		return SourceCommit048
	}
	return ""
}

func (p producerProfile) sourceScript() string {
	switch p {
	case producer047:
		return SourceScript
	case producer048:
		return SourceScript048()
	}
	return ""
}

func (p producerProfile) sourceRecipeSHA256() string {
	switch p {
	case producer047:
		return SourceRecipeSHA256()
	case producer048:
		return SourceRecipeSHA256048()
	}
	return ""
}

func (p producerProfile) verifySource(root string) (source.Composition, error) {
	if !p.valid() {
		return source.Composition{}, errors.New("sensor producer profile is invalid")
	}
	if p == producer048 {
		return source.Verify048(root)
	}
	return source.Verify(root)
}

func (p producerProfile) stageRepository(root, destination string) (*source.RepositorySnapshot, error) {
	if !p.valid() {
		return nil, errors.New("sensor producer profile is invalid")
	}
	if p == producer048 {
		return source.StageRepository048(root, destination)
	}
	return source.StageRepository(root, destination)
}

func (p producerProfile) decodeCopy(data []byte, jobs int) (*VerifiedArtifacts, error) {
	if !p.valid() {
		return nil, errors.New("sensor producer profile is invalid")
	}
	if p == producer048 {
		return DecodeDockerCopy048(data, jobs)
	}
	return DecodeDockerCopy(data, jobs)
}
