package source

import (
	"bytes"
	"reflect"
	"testing"
)

// capturedFixtureEntries copies the isolated fixture through the real source
// capture path while retaining its separate synthetic metadata authority.
func capturedFixtureEntries(t *testing.T) ([]Entry, authority) {
	t.Helper()
	f := newFixture(t)
	s, err := capture(f.root, f.policy)
	if err != nil {
		t.Fatal(err)
	}
	var entries []Entry
	for _, file := range s.files {
		entries = append(entries, Entry{Path: file.identity.Path, Data: file.data, GitMode: file.identity.GitMode})
	}
	for _, link := range s.composition.links {
		entries = append(entries, Entry{Path: link.Path, GitMode: "120000", LinkTarget: link.Target})
	}
	return entries, f.policy
}

// TestCapturedCopiedSourceClosureAndDefensiveCopies checks complete source
// replay and refuses mutation through input buffers or returned metadata.
func TestCapturedCopiedSourceClosureAndDefensiveCopies(t *testing.T) {
	entries, policy := capturedFixtureEntries(t)
	captured, err := capturedFromEntries(entries, policy)
	if err != nil {
		t.Fatal(err)
	}
	before := captured.Entries()
	for n := range entries {
		if len(entries[n].Data) != 0 {
			entries[n].Data[0] ^= 1
		}
		entries[n].Path = "changed"
	}
	if !reflect.DeepEqual(before, captured.Entries()) {
		t.Fatal("captured source retained caller-owned buffers")
	}
	copy := captured.Entries()
	for n := range copy {
		if len(copy[n].Data) != 0 {
			copy[n].Data[0] ^= 1
		}
		copy[n].LinkTarget = "changed"
	}
	if !reflect.DeepEqual(before, captured.Entries()) {
		t.Fatal("source accessor exposed retained buffers")
	}
	composition := captured.Composition()
	composition.files[0].Path = "changed"
	composition.metadata[0].SHA256 = "changed"
	composition.links[0].Target = "changed"
	if reflect.DeepEqual(composition, captured.Composition()) {
		t.Fatal("composition accessor exposed retained slices")
	}
	if _, err := capturedFromEntries(captured.Entries(), policy); err != nil {
		t.Fatalf("copied authenticated source did not round trip: %v", err)
	}
	if _, err := FromEntries(captured.Entries()); err == nil {
		t.Fatal("public copied-source verifier accepted synthetic metadata authority")
	}
}

// TestCapturedEntriesRefuseAlteration covers missing, extra, changed and unsafe
// copied members without allowing editable metadata to supply authority.
func TestCapturedEntriesRefuseAlteration(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func([]Entry) []Entry
	}{
		{"missing", func(e []Entry) []Entry { return e[:len(e)-1] }},
		{"extra", func(e []Entry) []Entry {
			return append(e, Entry{Path: "unexpected", Data: []byte("x"), GitMode: "100644"})
		}},
		{"duplicate", func(e []Entry) []Entry { e[1] = e[0]; return e }},
		{"traversal", func(e []Entry) []Entry { e[0].Path = "../escape"; return e }},
		{"absolute", func(e []Entry) []Entry { e[0].Path = "/escape"; return e }},
		{"backslash", func(e []Entry) []Entry { e[0].Path = `bad\path`; return e }},
		{"metadata-bytes", func(e []Entry) []Entry {
			for n := range e {
				if e[n].Path == "libqmi/SOURCE.json" {
					e[n].Data[0] ^= 1
				}
			}
			return e
		}},
		{"raw-bytes", func(e []Entry) []Entry {
			for n := range e {
				if e[n].Path == "libqmi/source/data.c" {
					e[n].Data[0] ^= 1
				}
			}
			return e
		}},
		{"raw-size", func(e []Entry) []Entry {
			for n := range e {
				if e[n].Path == "libqmi/source/data.c" {
					e[n].Data = append(e[n].Data, 1)
				}
			}
			return e
		}},
		{"raw-mode", func(e []Entry) []Entry {
			for n := range e {
				if e[n].Path == "libqmi/source/data.c" {
					e[n].GitMode = "100755"
				}
			}
			return e
		}},
		{"checksum", func(e []Entry) []Entry {
			for n := range e {
				if e[n].Path == "SOURCE.sha256" {
					e[n].Data[0] ^= 1
				}
			}
			return e
		}},
		{"link-target", func(e []Entry) []Entry {
			for n := range e {
				if e[n].GitMode == "120000" {
					e[n].LinkTarget = "wrong"
				}
			}
			return e
		}},
		{"link-data", func(e []Entry) []Entry {
			for n := range e {
				if e[n].GitMode == "120000" {
					e[n].Data = []byte("bad")
				}
			}
			return e
		}},
		{"link-kind", func(e []Entry) []Entry {
			for n := range e {
				if e[n].GitMode == "120000" {
					e[n].GitMode = "100644"
					e[n].LinkTarget = ""
				}
			}
			return e
		}},
		{"undeclared-safe-name", func(e []Entry) []Entry { e[0].Path = "libqmi/source/extra"; return e }},
		{"special-mode", func(e []Entry) []Entry { e[0].GitMode = "104644"; return e }},
	} {
		t.Run(test.name, func(t *testing.T) {
			entries, policy := capturedFixtureEntries(t)
			if _, err := capturedFromEntries(test.change(entries), policy); err == nil {
				t.Fatal("altered copied source was accepted")
			}
		})
	}
}

// TestCapturedInvalidHandlesHaveNoBytes ensures zero or empty inputs cannot
// manufacture authenticated composition bytes or lineage metadata.
func TestCapturedInvalidHandlesHaveNoBytes(t *testing.T) {
	for _, captured := range []*Captured{nil, {}} {
		if captured.Entries() != nil || !reflect.DeepEqual(captured.Composition(), Composition{}) {
			t.Fatal("invalid source handle exposed source authority")
		}
	}
	if _, err := FromEntries(nil); err == nil {
		t.Fatal("empty copied source accepted")
	}
	if _, err := Capture(t.TempDir()); err == nil {
		t.Fatal("empty source root accepted")
	}
	entries, policy := capturedFixtureEntries(t)
	c, err := capturedFromEntries(entries, policy)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range c.Entries() {
		if entry.GitMode == "120000" && !bytes.Equal(entry.Data, nil) {
			t.Fatal("link retained file content")
		}
	}
}
