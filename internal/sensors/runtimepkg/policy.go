package runtimepkg

import (
	"github.com/ooaklee/lexr.sh/internal/sensors/packageplan"
	"github.com/ooaklee/lexr.sh/internal/userspace/compatibility"
)

// This separately reviewed fixture is synthetic qualification admission only.
// It lives outside the production declaration path. These constants are the
// authority; editable package metadata and fixture files cannot replace them.
const (
	qualificationDeclarationBytes  = int64(1074)
	qualificationDeclarationSHA256 = "cc8008d0880da88eee7e961efb0aca13946d24a2f84133d97375f30ef128aeb3"
)

// verifyDeclaration authenticates the separately reviewed qualification fixture
// before the compatibility decoder can interpret any of its target claims.
func verifyDeclaration(data []byte) (*compatibility.Manifest, error) {
	return compatibility.Verify(data, compatibility.Reference{Size: qualificationDeclarationBytes, SHA256: qualificationDeclarationSHA256}, packageplan.ComponentID, packageplan.ProposedRelease)
}
