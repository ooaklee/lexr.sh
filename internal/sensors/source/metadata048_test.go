package source

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestMetadata048CompiledAuthority keeps both source policies closed and
// defensive without changing the existing default composition identity.
func TestMetadata048CompiledAuthority(t *testing.T) {
	oldBefore := ApprovedMetadata()
	want := append([]File(nil), oldBefore...)
	want[1] = File{Path: "libssc/SOURCE.json", SHA256: "a4f8b204fae4fed651d0a4f01c6825e23b1f52f29f576ba1a7d80e28a3db01ab", Bytes: 15731, GitMode: "100644"}
	want[3] = File{Path: "SOURCE.sha256", SHA256: "79d220b28c676b1f0174d29beaf84c8697d06a7237b988cf55ac1e33a08eab35", Bytes: 120400, GitMode: "100644"}
	if !reflect.DeepEqual(ApprovedMetadata048(), want) || !reflect.DeepEqual(ApprovedMetadata(), oldBefore) {
		t.Fatal("fixed metadata successor changed its pins or the old default")
	}
	policy := approved048Authority()
	if !policy.metadata048 || policy.commit != Metadata048SourceCommit || Metadata048SourceFileCount != 955 || policy.beforeOpen != nil || policy.beforeCopy != nil || !reflect.DeepEqual([]int{policy.components[0].files, policy.components[1].files, policy.components[2].files}, []int{793, 83, 79}) {
		t.Fatal("metadata successor authority differs")
	}
	policy.components[0].sha256 = "changed"
	metadata := ApprovedMetadata048()
	metadata[0].SHA256 = "changed"
	if !reflect.DeepEqual(ApprovedMetadata048(), want) || approved048Authority().components[0].sha256 == "changed" || approvedAuthority().metadata048 {
		t.Fatal("source policy copies exposed mutable or cross-profile authority")
	}
}

// TestMetadata048RefusesInvalidAndSyntheticSource does not mistake a fixture,
// zero handle or caller's source entries for the compiled successor authority.
func TestMetadata048RefusesInvalidAndSyntheticSource(t *testing.T) {
	for _, captured := range []*Captured{nil, {}, {valid: true, metadata048: true}, {valid: true, composition: Composition{SourceCommit: Metadata048SourceCommit}}} {
		if captured.IsMetadata048() {
			t.Fatal("invalid handle acquired the metadata successor profile")
		}
	}
	if _, err := Capture048(t.TempDir()); err == nil {
		t.Fatal("empty source root acquired successor authority")
	}
	if _, err := Verify048(t.TempDir()); err == nil {
		t.Fatal("empty source root acquired successor verification")
	}
	entries, policy := capturedFixtureEntries(t)
	if _, err := FromEntries048(entries); err == nil {
		t.Fatal("synthetic source metadata supplied successor authority")
	}
	fixture, err := capturedFromEntries(entries, policy)
	if err != nil || fixture.IsMetadata048() {
		t.Fatal("separate synthetic policy acquired the successor profile")
	}
	policy.metadata048 = true
	policy.commit = Metadata048SourceCommit
	fixture, err = capturedFromEntries(entries, policy)
	if err != nil || fixture.IsMetadata048() {
		t.Fatal("synthetic roster acquired the full successor profile")
	}
}

// TestMetadata048OldTreeOptIn confirms that unchanged old source remains valid
// only under the original default, never the new explicit successor wrappers.
func TestMetadata048OldTreeOptIn(t *testing.T) {
	root := os.Getenv("LEXR_TEST_SENSOR_OE_ROOT")
	if root == "" {
		t.Skip("explicit original frozen source root required")
	}
	captured, err := Capture(root)
	if err != nil || captured.IsMetadata048() {
		t.Fatalf("old source lost its separate original identity: %v", err)
	}
	if _, err := Capture048(root); err == nil {
		t.Fatal("successor capture accepted the old source composition")
	}
	if _, err := FromEntries048(captured.Entries()); err == nil {
		t.Fatal("successor copied-source verifier accepted the old composition")
	}
	verified, err := Verify(root)
	if err != nil || !reflect.DeepEqual(verified, captured.Composition()) {
		t.Fatalf("default verification changed its old source policy: %v", err)
	}
	if _, err := Verify048(root); err == nil {
		t.Fatal("successor verifier accepted the old composition")
	}
	parent := snapshotTestParent(t)
	target := filepath.Join(parent, "wrong-successor-snapshot")
	if snapshot, err := StageRepository048(root, target); err == nil || snapshot != nil {
		t.Fatal("successor repository wrapper accepted the old composition")
	}
	if after, err := Verify(root); err != nil || !reflect.DeepEqual(after, verified) {
		t.Fatal("refused successor staging changed the original old source")
	}
}

// TestMetadata048TreeOptIn authenticates the complete frozen source bytes and
// copies only into new test-owned scratch. It never builds or contacts a device.
func TestMetadata048TreeOptIn(t *testing.T) {
	root := os.Getenv("LEXR_TEST_SENSOR_METADATA048_OE_ROOT")
	if root == "" {
		t.Skip("explicit frozen metadata successor source root required")
	}
	captured, err := Capture048(root)
	if err != nil {
		t.Fatal(err)
	}
	composition := captured.Composition()
	if !captured.IsMetadata048() || composition.SourceCommit != Metadata048SourceCommit || composition.SourceFileCount != 955 || composition.SourceSymlinkCount != 6 || len(composition.Metadata()) != 4 || len(captured.Entries()) != 965 || !reflect.DeepEqual(composition.Metadata(), ApprovedMetadata048()) {
		t.Fatal("complete successor source composition differs")
	}
	roundTrip, err := FromEntries048(captured.Entries())
	if err != nil || !roundTrip.IsMetadata048() || !reflect.DeepEqual(composition, roundTrip.Composition()) {
		t.Fatalf("successor copied bytes did not round trip: %v", err)
	}
	if _, err := Capture(root); err == nil {
		t.Fatal("default source capture accepted the successor")
	}
	if _, err := FromEntries(captured.Entries()); err == nil {
		t.Fatal("default copied source accepted the successor")
	}
	if verified, err := Verify048(root); err != nil || !reflect.DeepEqual(verified, composition) {
		t.Fatalf("successor verification differs from captured source: %v", err)
	}
	if _, err := Verify(root); err == nil {
		t.Fatal("default verifier accepted the successor")
	}
	staged, err := Stage048(root, filepath.Join(t.TempDir(), "source"))
	if err != nil || !reflect.DeepEqual(composition, staged) {
		t.Fatalf("fresh successor stage differs: %v", err)
	}
	parent := snapshotTestParent(t)
	target := filepath.Join(parent, "metadata048-snapshot")
	restoreSnapshotForTest(t, target)
	snapshot, err := StageRepository048(root, target)
	if err != nil {
		t.Fatalf("fresh successor repository snapshot: %v", err)
	}
	defer snapshot.Close()
	if snapshot.policy.commit != Metadata048SourceCommit || !snapshot.policy.metadata048 || !reflect.DeepEqual(snapshot.Composition(), composition) {
		t.Fatal("successor repository snapshot lost its compiled profile")
	}
	if err := snapshot.Verify(); err != nil {
		t.Fatal(err)
	}
	if verified, err := Verify048(snapshot.Path()); err != nil || !reflect.DeepEqual(verified, composition) {
		t.Fatalf("successor snapshot cannot be independently verified: %v", err)
	}
	if _, err := Verify(snapshot.Path()); err == nil {
		t.Fatal("default verifier accepted the copied successor")
	}
	if duplicate, err := StageRepository048(root, target); err == nil || duplicate != nil {
		t.Fatal("successor wrapper reused an existing snapshot destination")
	}
	wrongTarget := filepath.Join(parent, "wrong-default-snapshot")
	restoreSnapshotForTest(t, wrongTarget)
	if wrong, err := StageRepository(root, wrongTarget); err == nil || wrong != nil {
		t.Fatal("default repository wrapper accepted successor source")
	}
	if after, err := Verify048(root); err != nil || !reflect.DeepEqual(after, composition) {
		t.Fatal("snapshot staging changed the original successor source")
	}
	for _, name := range []string{"libssc/SOURCE.json", "SOURCE.sha256", "libssc/source/LICENSE", "libssc/source/libssc.map"} {
		t.Run(name, func(t *testing.T) {
			entries := captured.Entries()
			found := false
			for n := range entries {
				if entries[n].Path == name {
					entries[n].Data[0] ^= 1
					found = true
				}
			}
			if !found {
				t.Fatal("required source inverse member absent")
			}
			if _, err := FromEntries048(entries); err == nil {
				t.Fatal("changed successor member accepted")
			}
		})
	}
	for _, change := range []func([]Entry) []Entry{
		func(e []Entry) []Entry { return e[:len(e)-1] },
		func(e []Entry) []Entry {
			return append(e, Entry{Path: "unexpected", GitMode: "100644", Data: []byte("x")})
		},
		func(e []Entry) []Entry { e[1] = e[0]; return e },
	} {
		if _, err := FromEntries048(change(captured.Entries())); err == nil {
			t.Fatal("changed successor member closure accepted")
		}
	}
}
