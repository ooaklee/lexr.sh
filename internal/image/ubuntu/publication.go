package ubuntu

import (
	"os"

	imagepublication "github.com/ooaklee/lexr.sh/internal/image/publication"
)

// publicationIdentity preserves the Ubuntu adapter's internal test contract
// while production publication is shared by every image adapter.
type publicationIdentity struct {
	digest string
	size   int64
}

// noReplacePublisher preserves the adapter's injectable publication boundary.
type noReplacePublisher func(directory *os.File, source, destination string) error

// identifyPublicationBytes maps shared identities into the adapter test shape.
func identifyPublicationBytes(data []byte) publicationIdentity {
	identity := imagepublication.IdentifyBytes(data)
	return publicationIdentity{digest: identity.SHA256, size: identity.Size}
}

// publishImageOutputs routes Ubuntu through the common image transaction.
func publishImageOutputs(
	sourceISO string,
	destinationISO string,
	manifestBytes []byte,
	journalBytes []byte,
	expectedISO publicationIdentity,
	expectedManifest publicationIdentity,
	expectedJournal publicationIdentity,
	publisher noReplacePublisher,
) (string, string, error) {
	var sharedPublisher imagepublication.Publisher
	if publisher != nil {
		sharedPublisher = imagepublication.Publisher(publisher)
	}
	return imagepublication.Publish(
		sourceISO,
		destinationISO,
		manifestBytes,
		journalBytes,
		imagepublication.Identity{SHA256: expectedISO.digest, Size: expectedISO.size},
		imagepublication.Identity{SHA256: expectedManifest.digest, Size: expectedManifest.size},
		imagepublication.Identity{SHA256: expectedJournal.digest, Size: expectedJournal.size},
		sharedPublisher,
	)
}

// publishOutputNoReplace exposes the shared platform primitive to adapter tests.
func publishOutputNoReplace(directory *os.File, source, destination string) error {
	return imagepublication.PublishOutputNoReplace(directory, source, destination)
}

// requireAbsentPublicationPath retains Ubuntu's early preflight call boundary.
func requireAbsentPublicationPath(path, label string) error {
	return imagepublication.RequireAbsentPath(path, label)
}
