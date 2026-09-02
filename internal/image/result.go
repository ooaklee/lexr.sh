package image

// Result identifies every durable artefact produced by a successful image
// adapter and any diagnostic workspace explicitly retained by the caller.
type Result struct {
	// OutputISO is the absolute path of the atomically published image.
	OutputISO string
	// ManifestPath is the sidecar provenance manifest path.
	ManifestPath string
	// JournalPath is the durable operation checkpoint journal path.
	JournalPath string
	// SHA256 is the digest of the published image.
	SHA256 string
	// Size is the published image length in bytes.
	Size int64
	// WorkspacePath is populated only when host diagnostics were retained.
	WorkspacePath string
	// WorkspaceVolume is populated only when the case-sensitive Docker work
	// volume was retained for diagnostics.
	WorkspaceVolume string
	// CompanionBundle repeats the single manifest's optional support inventory
	// so command callers can report its inclusion and licence status directly.
	CompanionBundle CompanionBundleRecord
}
