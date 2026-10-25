package runtimepkg

import (
	"archive/tar"
	"bytes"
	"testing"
	"time"
)

// smallArchive creates a deterministic three-member USTAR golden fixture.
func smallArchive(t *testing.T) []byte {
	t.Helper()
	data, err := writeArchive([]archiveEntry{{Path: "lib/provider.so.1", Data: []byte("fixed provider\n"), Mode: 0o755}, {Path: "bin/client", Data: []byte("fixed client\n"), Mode: 0o755}, {Path: "lib/provider.so", LinkTarget: "provider.so.1", Mode: 0o777}}, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// rawArchive builds controlled negative fixtures without bypassing the decoder
// under test or changing production archive policy.
func rawArchive(t *testing.T, alter func([]*tar.Header, [][]byte) ([]*tar.Header, [][]byte)) []byte {
	t.Helper()
	headers := []*tar.Header{
		{Name: "bin/client", Size: 13, Mode: 0o755, Typeflag: tar.TypeReg, Format: tar.FormatUSTAR, ModTime: time.Unix(0, 0)},
		{Name: "lib/provider.so", Linkname: "provider.so.1", Mode: 0o777, Typeflag: tar.TypeSymlink, Format: tar.FormatUSTAR, ModTime: time.Unix(0, 0)},
		{Name: "lib/provider.so.1", Size: 15, Mode: 0o755, Typeflag: tar.TypeReg, Format: tar.FormatUSTAR, ModTime: time.Unix(0, 0)},
	}
	payloads := [][]byte{[]byte("fixed client\n"), nil, []byte("fixed provider\n")}
	headers, payloads = alter(headers, payloads)
	var b bytes.Buffer
	w := tar.NewWriter(&b)
	for n, h := range headers {
		if err := w.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(payloads[n]); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// TestCanonicalUSTARGoldenAndRoundTrip pins header spelling, member order,
// padding and end records independently of a successful decoder round trip.
func TestCanonicalUSTARGoldenAndRoundTrip(t *testing.T) {
	data := smallArchive(t)
	const expected = "d87954f5a072987ac7503e59ed346cedefb63df1622e357f047cc7571bb4914a"
	if got := digest(data); got != expected {
		t.Fatalf("canonical archive golden differs: %s", got)
	}
	entries, err := readArchive(data, 1<<20, 1<<10, 3)
	if err != nil || len(entries) != 3 || entries[0].Path != "bin/client" || entries[1].LinkTarget != "provider.so.1" {
		t.Fatalf("canonical archive did not round trip: %v", err)
	}
	second, err := writeArchive(entries, 1<<20)
	if err != nil || !bytes.Equal(data, second) {
		t.Fatal("canonical archive output is not deterministic")
	}
}

// TestCanonicalUSTARRefusesHeaderAndClosureChanges rejects alternate ownership,
// modes, path spellings, types, archive formats and end-record encodings.
func TestCanonicalUSTARRefusesHeaderAndClosureChanges(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func([]*tar.Header, [][]byte) ([]*tar.Header, [][]byte)
	}{
		{"owner", func(h []*tar.Header, d [][]byte) ([]*tar.Header, [][]byte) { h[0].Uid = 1; return h, d }},
		{"group", func(h []*tar.Header, d [][]byte) ([]*tar.Header, [][]byte) { h[0].Gid = 1; return h, d }},
		{"owner-name", func(h []*tar.Header, d [][]byte) ([]*tar.Header, [][]byte) { h[0].Uname = "host"; return h, d }},
		{"group-name", func(h []*tar.Header, d [][]byte) ([]*tar.Header, [][]byte) { h[0].Gname = "host"; return h, d }},
		{"mtime", func(h []*tar.Header, d [][]byte) ([]*tar.Header, [][]byte) {
			h[0].ModTime = time.Unix(1, 0)
			return h, d
		}},
		{"mode", func(h []*tar.Header, d [][]byte) ([]*tar.Header, [][]byte) { h[0].Mode = 0o4755; return h, d }},
		{"link-mode", func(h []*tar.Header, d [][]byte) ([]*tar.Header, [][]byte) { h[1].Mode = 0o755; return h, d }},
		{"traversal", func(h []*tar.Header, d [][]byte) ([]*tar.Header, [][]byte) { h[0].Name = "../client"; return h, d }},
		{"absolute", func(h []*tar.Header, d [][]byte) ([]*tar.Header, [][]byte) { h[0].Name = "/client"; return h, d }},
		{"backslash", func(h []*tar.Header, d [][]byte) ([]*tar.Header, [][]byte) { h[0].Name = `bin\client`; return h, d }},
		{"duplicate", func(h []*tar.Header, d [][]byte) ([]*tar.Header, [][]byte) { return append(h, h[0]), append(d, d[0]) }},
		{"order", func(h []*tar.Header, d [][]byte) ([]*tar.Header, [][]byte) {
			h[0], h[2] = h[2], h[0]
			d[0], d[2] = d[2], d[0]
			return h, d
		}},
		{"directory", func(h []*tar.Header, d [][]byte) ([]*tar.Header, [][]byte) {
			h[0].Typeflag = tar.TypeDir
			h[0].Size = 0
			d[0] = nil
			return h, d
		}},
		{"hardlink", func(h []*tar.Header, d [][]byte) ([]*tar.Header, [][]byte) {
			h[0].Typeflag = tar.TypeLink
			h[0].Size = 0
			h[0].Linkname = "lib/provider.so.1"
			d[0] = nil
			return h, d
		}},
		{"device", func(h []*tar.Header, d [][]byte) ([]*tar.Header, [][]byte) {
			h[0].Typeflag = tar.TypeChar
			h[0].Size = 0
			h[0].Devmajor = 1
			d[0] = nil
			return h, d
		}},
		{"pax", func(h []*tar.Header, d [][]byte) ([]*tar.Header, [][]byte) {
			h[0].Format = tar.FormatPAX
			h[0].PAXRecords = map[string]string{"comment": "extra"}
			return h, d
		}},
		{"gnu", func(h []*tar.Header, d [][]byte) ([]*tar.Header, [][]byte) { h[0].Format = tar.FormatGNU; return h, d }},
		{"absolute-link", func(h []*tar.Header, d [][]byte) ([]*tar.Header, [][]byte) { h[1].Linkname = "/provider"; return h, d }},
		{"traversal-link", func(h []*tar.Header, d [][]byte) ([]*tar.Header, [][]byte) {
			h[1].Linkname = "../provider"
			return h, d
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := readArchive(rawArchive(t, test.edit), 1<<20, 1<<10, 4); err == nil {
				t.Fatal("noncanonical archive header was accepted")
			}
		})
	}
	for _, test := range []struct {
		name string
		data []byte
	}{
		{"one-end-block", smallArchive(t)[:len(smallArchive(t))-512]},
		{"truncated", smallArchive(t)[:600]},
		{"trailing-zero", append(smallArchive(t), make([]byte, 512)...)},
		{"trailing-data", append(smallArchive(t), bytes.Repeat([]byte{'x'}, 512)...)},
		{"empty", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := readArchive(test.data, 1<<20, 1<<10, 4); err == nil {
				t.Fatal("noncanonical archive end/size was accepted")
			}
		})
	}
}

// TestCanonicalUSTARBoundsAndProducerRefusals checks allocation bounds and
// ensures the producer cannot emit unsafe or noncanonical member descriptions.
func TestCanonicalUSTARBoundsAndProducerRefusals(t *testing.T) {
	data := smallArchive(t)
	for _, limits := range []struct {
		outer, member int64
		count         int
	}{{int64(len(data) - 1), 1 << 10, 3}, {1 << 20, 1, 3}, {1 << 20, 1 << 10, 2}} {
		if _, err := readArchive(data, limits.outer, limits.member, limits.count); err == nil {
			t.Fatal("archive bound was ignored")
		}
	}
	for _, entries := range [][]archiveEntry{
		{{Path: "../bad", Mode: 0o644}},
		{{Path: "same", Mode: 0o644}, {Path: "same", Mode: 0o644}},
		{{Path: "bad", Mode: 0o600}},
		{{Path: "bad", Mode: 0o777, LinkTarget: "../escape"}},
		{{Path: "bad", Mode: 0o777, LinkTarget: "other", Data: []byte("extra")}},
	} {
		if _, err := writeArchive(entries, 1<<20); err == nil {
			t.Fatal("noncanonical archive input was accepted")
		}
	}
	if _, err := writeArchive([]archiveEntry{{Path: "member", Mode: 0o644, Data: make([]byte, 2048)}}, 1024); err == nil {
		t.Fatal("archive output bound was ignored")
	}
}
