package bootidentity

import (
	"bytes"
	"errors"
)

// Btrfs-progs emits this exact C-locale line only for default ID 5 (FS_TREE).
// Other IDs, extra lines, whitespace, diagnostics and truncation are not proof.
func btrfsDefaultRootOutput(stdout, stderr []byte, runErr error) bool {
	return runErr == nil && len(stderr) == 0 && bytes.Equal(stdout, []byte("ID 5 (FS_TREE)\n"))
}

// btrfsOutput bounds each command stream without retaining arbitrarily large
// diagnostics. Any overflow is a failed attestation; output is never reported.
type btrfsOutput struct {
	data  []byte
	limit int
}

// Write retains output only while the fixed byte cap is satisfied.
func (b *btrfsOutput) Write(p []byte) (int, error) {
	if len(p) > b.limit-len(b.data) {
		return 0, errors.New("btrfs evidence exceeds its output bound")
	}
	b.data = append(b.data, p...)
	return len(p), nil
}

// Bytes returns the retained, bounded command output.
func (b *btrfsOutput) Bytes() []byte { return b.data }
