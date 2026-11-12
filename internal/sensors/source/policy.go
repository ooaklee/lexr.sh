package source

// ApprovedMetadata returns fresh copies of the four compiled source-inventory
// identities. These are required policy inputs, not evidence that a checkout,
// archive or completed producer was authenticated. Paths are relative to the
// fixed userspace/sensors source root; no filesystem access occurs here.
func ApprovedMetadata() []File {
	return metadataForAuthority(approvedAuthority())
}

// ApprovedMetadata048 returns defensive copies of the metadata successor's four
// exact source identities, without authenticating files or granting execution.
func ApprovedMetadata048() []File {
	return metadataForAuthority(approved048Authority())
}

// metadataForAuthority exposes only copied identities from a private policy.
func metadataForAuthority(policy authority) []File {
	metadata := make([]File, 0, len(policy.components)+1)
	for _, pin := range policy.components {
		metadata = append(metadata, File{Path: pin.name + "/SOURCE.json", SHA256: pin.sha256, Bytes: pin.bytes, GitMode: "100644"})
	}
	metadata = append(metadata, File{Path: policy.checksums.name, SHA256: policy.checksums.sha256, Bytes: policy.checksums.bytes, GitMode: "100644"})
	return metadata
}
