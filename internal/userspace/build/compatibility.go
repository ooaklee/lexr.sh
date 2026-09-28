package build

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/ooaklee/lexr.sh/internal/userspace/compatibility"
	"github.com/ooaklee/lexr.sh/internal/userspace/producer"
)

// iptsdBuildCompatibility retains target evidence beside the unchanged closed
// payload. It is build provenance, not installation or redistribution authority.
type iptsdBuildCompatibility struct {
	SchemaVersion   int                     `json:"schema_version"`
	Compatibility   producer.Record         `json:"compatibility"`
	PayloadChecksum compatibility.Reference `json:"payload_checksum"`
}

// writeIPTSDCompatibility checksums the exact declaration and its assessment
// beside the independently validated payload without widening its closed set.
func writeIPTSDCompatibility(directory string, declaration []byte, record producer.Record) error {
	if err := producer.ValidatePublication(declaration, record); err != nil {
		return err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	file, err := compatibility.OpenRegular(root, "stage/SHA256SUMS")
	if err != nil {
		return err
	}
	payload, readErr := io.ReadAll(io.LimitReader(file, compatibility.MaxBytes+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		return errors.Join(readErr, closeErr)
	}
	if len(payload) == 0 || len(payload) > compatibility.MaxBytes {
		return errors.New("invalid bounded IPTSD payload checksum manifest")
	}
	data, err := json.MarshalIndent(iptsdBuildCompatibility{SchemaVersion: 1, Compatibility: record, PayloadChecksum: compatibility.ReferenceFor(payload)}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	files := map[string][]byte{compatibility.Filename: declaration, "iptsd-build-compatibility.json": data}
	identities := map[string]compatibility.Reference{"stage/SHA256SUMS": compatibility.ReferenceFor(payload)}
	for name, data := range files {
		identities[name] = compatibility.ReferenceFor(data)
	}
	names := make([]string, 0, len(identities))
	for name := range identities {
		names = append(names, name)
	}
	sort.Strings(names)
	var checksums strings.Builder
	for _, name := range names {
		fmt.Fprintf(&checksums, "%s  %s\n", identities[name].SHA256, name)
	}
	// Publish the checksum marker last. Readers still need the compiled payload
	// proof and independent source pin; this mutable marker grants no authority.
	for _, name := range []string{compatibility.Filename, "iptsd-build-compatibility.json", "SHA256SUMS"} {
		content := files[name]
		if name == "SHA256SUMS" {
			content = []byte(checksums.String())
		}
		if err := writeRootedBuildFile(root, name, content); err != nil {
			return err
		}
	}
	return nil
}

// writeRootedBuildFile replaces one metadata leaf atomically through an open
// directory handle, so path replacement cannot redirect the write.
func writeRootedBuildFile(root *os.Root, name string, data []byte) error {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	temporary := ".compatibility-" + hex.EncodeToString(nonce[:])
	file, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	defer root.Remove(temporary)
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	return root.Rename(temporary, name)
}
