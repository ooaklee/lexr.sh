package source

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
)

// Entry is one copied source-composition member. Regular files have Data and a
// GitMode of 100644 or 100755. Links have GitMode 120000, an exact relative
// LinkTarget, and no Data. Paths are relative to the approved sensor composition.
// An Entry alone is not authenticated source authority.
type Entry struct {
	Path       string
	Data       []byte
	GitMode    string
	LinkTarget string
}

// Captured retains authenticated public source bytes. Its zero value has no
// authority. It describes an approved composition lineage, not an observed Git
// checkout, a successful native producer, redistribution permission or support.
type Captured struct {
	entries     []Entry
	composition Composition
	valid       bool
	metadata048 bool
}

// Capture reads the same fixed metadata, source inventory and descriptor-checked
// bytes as Verify. It does not run Git or a producer and never writes the root.
func Capture(repositoryRoot string) (*Captured, error) {
	return captureComposition(repositoryRoot, approvedAuthority())
}

// Capture048 authenticates only the separately frozen metadata successor.
// Like Capture, it establishes source lineage, not observed producer execution.
func Capture048(repositoryRoot string) (*Captured, error) {
	return captureComposition(repositoryRoot, approved048Authority())
}

// captureComposition retains captured bytes under one private compiled policy.
func captureComposition(repositoryRoot string, policy authority) (*Captured, error) {
	s, err := capture(repositoryRoot, policy)
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(s.files)+len(s.composition.links))
	for _, file := range s.files {
		entries = append(entries, Entry{Path: file.identity.Path, Data: file.data, GitMode: file.identity.GitMode})
	}
	for _, link := range s.composition.links {
		entries = append(entries, Entry{Path: link.Path, GitMode: "120000", LinkTarget: link.Target})
	}
	return capturedFromEntries(entries, policy)
}

// FromEntries authenticates a complete copied composition against independently
// compiled metadata identities and their closed inventories. Caller-provided
// metadata never supplies authority. No archive is extracted to a filesystem.
func FromEntries(entries []Entry) (*Captured, error) {
	return capturedFromEntries(entries, approvedAuthority())
}

// FromEntries048 authenticates a complete copied metadata successor against its
// compiled inventories. Copied manifests and callers cannot select authority.
func FromEntries048(entries []Entry) (*Captured, error) {
	return capturedFromEntries(entries, approved048Authority())
}

// capturedFromEntries checks copied bytes against the selected compiled policy;
// only private tests can select a synthetic authority.
func capturedFromEntries(entries []Entry, policy authority) (*Captured, error) {
	wantCount := 1
	for _, pin := range policy.components {
		wantCount += 1 + pin.files + pin.links
	}
	if len(entries) != wantCount {
		return nil, errors.New("copied source composition member count differs")
	}
	// Clone before validation: retaining caller-owned buffers would allow an
	// authenticated handle's bytes to change after this function returns.
	byPath := make(map[string]Entry, len(entries))
	var total int64
	for _, entry := range entries {
		if !safeRelative(entry.Path) || len(entry.Data) > int(maximumFileBytes) {
			return nil, errors.New("copied source entry is unsafe or oversized")
		}
		if _, duplicate := byPath[entry.Path]; duplicate {
			return nil, errors.New("copied source contains a duplicate member")
		}
		if entry.GitMode == "120000" {
			if len(entry.Data) != 0 || !safeRelative(entry.LinkTarget) {
				return nil, errors.New("copied source link has data or unsafe target")
			}
		} else if (entry.GitMode != "100644" && entry.GitMode != "100755") || entry.LinkTarget != "" {
			return nil, errors.New("copied source regular entry has invalid mode or target")
		}
		total += int64(len(entry.Data))
		if total > maximumSourceBytes+(1<<20) {
			return nil, errors.New("copied source exceeds its compiled byte bound")
		}
		entry.Data = append([]byte(nil), entry.Data...)
		byPath[entry.Path] = entry
	}
	composition := Composition{SourceCommit: policy.commit}
	seen := make(map[string]bool, len(entries))
	verify := func(expected File) (Entry, error) {
		entry, found := byPath[expected.Path]
		digest := sha256.Sum256(entry.Data)
		if !found || entry.LinkTarget != "" || entry.GitMode != expected.GitMode || int64(len(entry.Data)) != expected.Bytes || hex.EncodeToString(digest[:]) != expected.SHA256 {
			return Entry{}, fmt.Errorf("copied source identity differs: %s", expected.Path)
		}
		seen[expected.Path] = true
		return entry, nil
	}
	for _, pin := range policy.components {
		metadata := File{Path: path.Join(pin.name, "SOURCE.json"), SHA256: pin.sha256, Bytes: pin.bytes, GitMode: "100644"}
		entry, err := verify(metadata)
		if err != nil {
			return nil, err
		}
		var inv inventory
		if err := json.Unmarshal(entry.Data, &inv); err != nil {
			return nil, fmt.Errorf("parse authenticated copied inventory: %w", err)
		}
		if inv.Schema != "oe-sensor-source-inventory-v1" || inv.Component != pin.name || inv.Status != "experimental-source-only" || inv.InventoryRoot != "source" || len(inv.Files) != pin.files || len(inv.Symlinks) != pin.links {
			return nil, errors.New("copied authenticated source inventory contract differs")
		}
		if _, err := inventoryNodes(inv); err != nil {
			return nil, err
		}
		composition.metadata = append(composition.metadata, metadata)
		for _, name := range sortedKeys(inv.Files) {
			identity := inv.Files[name]
			if identity.Bytes > maximumSourceBytes-composition.SourceBytes {
				return nil, errors.New("copied raw source exceeds its compiled byte bound")
			}
			file := File{Path: path.Join(pin.name, "source", name), SHA256: identity.SHA256, Bytes: identity.Bytes, GitMode: identity.GitMode}
			if _, err := verify(file); err != nil {
				return nil, err
			}
			composition.SourceBytes += file.Bytes
			composition.files = append(composition.files, file)
		}
		for _, name := range sortedKeys(inv.Symlinks) {
			full := path.Join(pin.name, "source", name)
			link, found := byPath[full]
			if !found || link.GitMode != "120000" || len(link.Data) != 0 || link.LinkTarget != inv.Symlinks[name] {
				return nil, fmt.Errorf("copied source link differs: %s", full)
			}
			seen[full] = true
			composition.links = append(composition.links, Symlink{Path: full, Target: link.LinkTarget})
		}
	}
	checksums := File{Path: policy.checksums.name, SHA256: policy.checksums.sha256, Bytes: policy.checksums.bytes, GitMode: "100644"}
	if checksums.Path != "SOURCE.sha256" {
		return nil, errors.New("compiled source checksum name differs")
	}
	if _, err := verify(checksums); err != nil {
		return nil, err
	}
	composition.metadata = append(composition.metadata, checksums)
	if len(seen) != len(byPath) {
		return nil, errors.New("copied source contains undeclared members")
	}
	composition.SourceFileCount = len(composition.files)
	composition.SourceSymlinkCount = len(composition.links)
	result := &Captured{composition: composition, valid: true, metadata048: policy.metadata048}
	for _, entry := range byPath {
		result.entries = append(result.entries, entry)
	}
	sort.Slice(result.entries, func(i, j int) bool { return result.entries[i].Path < result.entries[j].Path })
	return result, nil
}

// IsMetadata048 identifies an authenticated closed successor handle. The zero
// value, old composition and private synthetic test policies grant no such claim.
func (c *Captured) IsMetadata048() bool {
	return c != nil && c.valid && c.metadata048 && c.composition.SourceCommit == Metadata048SourceCommit && c.composition.SourceFileCount == Metadata048SourceFileCount && c.composition.SourceSymlinkCount == 6 && len(c.composition.metadata) == 4
}

// Entries returns defensive copies. Invalid and zero handles return nil.
func (c *Captured) Entries() []Entry {
	if c == nil || !c.valid {
		return nil
	}
	result := make([]Entry, len(c.entries))
	for n, entry := range c.entries {
		entry.Data = append([]byte(nil), entry.Data...)
		result[n] = entry
	}
	return result
}

// Composition returns defensive metadata for the captured composition lineage.
func (c *Captured) Composition() Composition {
	if c == nil || !c.valid {
		return Composition{}
	}
	result := c.composition
	result.files = result.SourceFiles()
	result.metadata = result.Metadata()
	result.links = result.Symlinks()
	return result
}
