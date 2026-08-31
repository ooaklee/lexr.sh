package version

import (
	"runtime/debug"
	"testing"
)

// TestResolveIgnoresAutomaticVCSIdentity verifies that a containing
// superproject cannot become Lexr's reported source provenance.
func TestResolveIgnoresAutomaticVCSIdentity(t *testing.T) {
	info := &debug.BuildInfo{
		Main: debug.Module{Version: "(devel)"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "cccccccccccccccccccccccccccccccccccccccc"},
			{Key: "vcs.time", Value: "2026-08-31T07:47:44Z"},
			{Key: "vcs.modified", Value: "false"},
		},
	}

	gotVersion, gotCommit, gotDate := resolve("dev", "unknown", "unknown", info)
	if gotVersion != "dev" || gotCommit != "unknown" || gotDate != "unknown" {
		t.Fatalf("resolve() = %q, %q, %q; want safe development defaults", gotVersion, gotCommit, gotDate)
	}
}

// TestResolveUsesOnlyTheMainModuleVersion verifies that the module system may
// improve a development version without authorising automatic VCS provenance.
func TestResolveUsesOnlyTheMainModuleVersion(t *testing.T) {
	info := &debug.BuildInfo{
		Main: debug.Module{Version: "v0.0.0-20260831074412-39ba8053de7f"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "39ba8053de7ff016aa4274deb29895a00f7a2d3c"},
			{Key: "vcs.time", Value: "2026-08-31T07:44:12Z"},
		},
	}

	gotVersion, gotCommit, gotDate := resolve("dev", "unknown", "unknown", info)
	if gotVersion != info.Main.Version || gotCommit != "unknown" || gotDate != "unknown" {
		t.Fatalf("resolve() = %q, %q, %q; want module version with unknown VCS identity", gotVersion, gotCommit, gotDate)
	}
}

// TestResolvePreservesLinkerMetadata verifies that release and source-build
// linker values remain authoritative over all automatic build information.
func TestResolvePreservesLinkerMetadata(t *testing.T) {
	info := &debug.BuildInfo{Main: debug.Module{Version: "v9.9.9"}}
	gotVersion, gotCommit, gotDate := resolve(
		"1.2.3",
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"2026-08-31T08:00:00Z",
		info,
	)
	if gotVersion != "1.2.3" ||
		gotCommit != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" ||
		gotDate != "2026-08-31T08:00:00Z" {
		t.Fatalf("resolve() replaced linker metadata: %q, %q, %q", gotVersion, gotCommit, gotDate)
	}
}
