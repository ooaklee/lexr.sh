package image

import (
	imagepublication "github.com/ooaklee/lexr.sh/internal/image/publication"
)

// PublicationIdentity binds one published file to its complete bytes.
type PublicationIdentity = imagepublication.Identity

// IdentifyBytes returns the canonical identity of an in-memory publication.
func IdentifyBytes(data []byte) PublicationIdentity {
	return imagepublication.IdentifyBytes(data)
}

// PublishISOOutputs durably stages and exclusively publishes a manifest,
// journal, and ISO through the shared descriptor-anchored transaction. The ISO
// is published last as the commit marker; existing destinations are untouched.
func PublishISOOutputs(
	sourceISO string,
	destinationISO string,
	manifestBytes []byte,
	journalBytes []byte,
	isoIdentity PublicationIdentity,
) (string, string, error) {
	return imagepublication.Publish(
		sourceISO,
		destinationISO,
		manifestBytes,
		journalBytes,
		isoIdentity,
		imagepublication.IdentifyBytes(manifestBytes),
		imagepublication.IdentifyBytes(journalBytes),
		nil,
	)
}

// RequireAbsentPublication rejects any pre-existing output or sidecar path.
func RequireAbsentPublication(path, label string) error {
	return imagepublication.RequireAbsentPath(path, label)
}
