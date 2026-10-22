package build

import (
	"fmt"
	"strings"
	"testing"
)

// mountTestRoot supplies a fixed root mount for synthetic Linux mountinfo tables.
const mountTestRoot = "1 1 8:1 / / rw,relatime - ext4 /dev/root rw\n"

// mountTestRecord formats one controlled mountinfo record without consulting the host.
func mountTestRecord(id int, device, root, mountpoint, optional string) string {
	escape := strings.NewReplacer(`\`, `\134`, " ", `\040`, "\t", `\011`, "\n", `\012`)
	if optional != "" {
		optional = " " + optional
	}
	return fmt.Sprintf("%d 1 %s %s %s rw%s - ext4 /dev/fixture rw\n", id, device, escape.Replace(root), escape.Replace(mountpoint), optional)
}

// TestBuildMountTopologyAllowsDistinctCoordinates admits nonoverlapping source and output mappings.
func TestBuildMountTopologyAllowsDistinctCoordinates(t *testing.T) {
	for _, test := range []struct {
		name, data string
		paths      []string
	}{
		{"ordinary", mountTestRoot, []string{"/source", "/work", "/output"}},
		{"component-boundary", mountTestRoot, []string{"/source", "/source-other", "/output"}},
		{"unrelated-bind", mountTestRoot + mountTestRecord(2, "8:1", "/elsewhere", "/alias", ""), []string{"/source", "/alias/work", "/output"}},
		{"partial-root-prefix", mountTestRoot + mountTestRecord(2, "8:1", "/source-other", "/alias", ""), []string{"/source", "/alias/work", "/output"}},
		{"different-device", mountTestRoot + mountTestRecord(2, "8:2", "/source", "/alias", ""), []string{"/source", "/alias", "/output"}},
		{"deepest-mount", mountTestRoot + mountTestRecord(2, "8:1", "/source/inside", "/alias", "") + mountTestRecord(3, "8:2", "/", "/alias/deep", ""), []string{"/source", "/alias/deep/work", "/output"}},
		{"btrfs-distinct-subvolumes", mountTestRecord(1, "0:42", "/@source", "/source", "") + mountTestRecord(2, "0:42", "/@work", "/work", "") + mountTestRecord(3, "0:42", "/@output", "/output", ""), []string{"/source/job", "/work/job", "/output/job"}},
		{"escaped-spaces", mountTestRoot + mountTestRecord(2, "8:1", "/some source", "/source copy", ""), []string{"/source copy/job", "/work", "/output"}},
		{"escaped-specials", mountTestRoot + mountTestRecord(2, "8:1", "/literal\\tab\tline\n", "/copy\\tab\tline\n", ""), []string{"/copy\\tab\tline\n/job", "/work", "/output"}},
		{"propagation-tags", mountTestRoot + mountTestRecord(2, "8:2", "/", "/source", "shared:2 master:3 propagate_from:4"), []string{"/source", "/work", "/output"}},
		{"unbindable", mountTestRoot + mountTestRecord(2, "8:2", "/", "/source", "unbindable"), []string{"/source", "/work", "/output"}},
		{"unknown-optional-tag", mountTestRoot + mountTestRecord(2, "8:2", "/", "/source", "future_tag:opaque"), []string{"/source", "/work", "/output"}},
		{"unrelated-stack", mountTestRoot + mountTestRecord(2, "8:2", "/", "/unused", "") + mountTestRecord(3, "8:3", "/", "/unused", ""), []string{"/source", "/work", "/output"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := validateBuildMountTopology([]byte(test.data), test.paths); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestBuildMountTopologyRejectsDirectDescendantAndReverseAliases refuses overlapping backing locations.
func TestBuildMountTopologyRejectsDirectDescendantAndReverseAliases(t *testing.T) {
	for _, test := range []struct {
		name, data string
		paths      []string
	}{
		{"identical", mountTestRoot, []string{"/source", "/source", "/output"}},
		{"ordinary-descendant", mountTestRoot, []string{"/source", "/source/work", "/output"}},
		{"direct-bind", mountTestRoot + mountTestRecord(2, "8:1", "/source", "/alias", ""), []string{"/source", "/alias", "/output"}},
		{"descendant-bind", mountTestRoot + mountTestRecord(2, "8:1", "/source/private", "/alias", ""), []string{"/source", "/alias/job", "/output"}},
		{"reverse-descendant", mountTestRoot + mountTestRecord(2, "8:1", "/source", "/alias", ""), []string{"/alias/job", "/source", "/output"}},
		{"work-output-overlap", mountTestRoot + mountTestRecord(2, "8:1", "/work", "/alias", ""), []string{"/source", "/work", "/alias/output"}},
		{"escaped-alias", mountTestRoot + mountTestRecord(2, "8:1", "/source with space/inside", "/alias", ""), []string{"/source with space", "/alias/job", "/output"}},
		{"root-bind", mountTestRoot + mountTestRecord(2, "8:1", "/", "/alias", ""), []string{"/source", "/alias/source/job", "/output"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := validateBuildMountTopology([]byte(test.data), test.paths); err == nil || !strings.Contains(err.Error(), "overlap") {
				t.Fatalf("physical overlap did not fail at overlap gate: %v", err)
			}
		})
	}
}

// TestBuildMountTopologyRejectsAmbiguousOrMissingMappings requires one decisive mount identity per selection.
func TestBuildMountTopologyRejectsAmbiguousOrMissingMappings(t *testing.T) {
	paths := []string{"/source", "/alias/deep/work", "/output"}
	for name, data := range map[string]string{
		"deepest-stack":      mountTestRoot + mountTestRecord(2, "8:2", "/", "/alias/deep", "") + mountTestRecord(3, "8:3", "/", "/alias/deep", ""),
		"hidden-child-stack": mountTestRoot + mountTestRecord(2, "8:2", "/old", "/alias", "") + mountTestRecord(3, "8:3", "/hidden", "/alias/deep", "") + mountTestRecord(4, "8:4", "/new", "/alias", ""),
		"root-stack":         mountTestRoot + mountTestRecord(2, "8:2", "/", "/", ""),
		"missing-output":     mountTestRecord(1, "8:1", "/source", "/source", "") + mountTestRecord(2, "8:1", "/work", "/alias/deep", ""),
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateBuildMountTopology([]byte(data), paths); err == nil {
				t.Fatal("ambiguous or unmapped selected path accepted")
			}
		})
	}
}

// TestBuildMountTopologyRefusesCorruptAndTruncatedTables preserves strict mountinfo parsing boundaries.
func TestBuildMountTopologyRefusesCorruptAndTruncatedTables(t *testing.T) {
	for name, data := range map[string]string{
		"empty": "", "truncated": strings.TrimSuffix(mountTestRoot, "\n"), "blank-record": mountTestRoot + "\n",
		"bad-id": strings.Replace(mountTestRoot, "1 1 ", "0 1 ", 1), "id-overflow": strings.Replace(mountTestRoot, "1 1 ", "18446744073709551616 1 ", 1),
		"bad-parent": strings.Replace(mountTestRoot, "1 1 ", "1 +1 ", 1), "duplicate-id": mountTestRoot + mountTestRecord(1, "8:2", "/", "/other", ""),
		"bad-device": strings.Replace(mountTestRoot, "8:1", "08:1", 1), "device-overflow": strings.Replace(mountTestRoot, "8:1", "4294967296:1", 1),
		"extra-device-part": strings.Replace(mountTestRoot, "8:1", "8:1:2", 1), "negative-device": strings.Replace(mountTestRoot, "8:1", "-1:1", 1),
		"relative-root": strings.Replace(mountTestRoot, " / / ", " relative / ", 1), "unclean-root": strings.Replace(mountTestRoot, " / / ", " /a/../b / ", 1),
		"unclean-mountpoint": strings.Replace(mountTestRoot, " / / ", " / // ", 1), "trailing-mount-slash": mountTestRecord(2, "8:1", "/", "/mount/", ""),
		"bad-escape": strings.Replace(mountTestRoot, " / / ", ` /bad\041 / `, 1), "truncated-escape": strings.Replace(mountTestRoot, " / / ", ` /bad\04 / `, 1),
		"nul-escape": strings.Replace(mountTestRoot, " / / ", ` /bad\000 / `, 1), "literal-tab": strings.Replace(mountTestRoot, " / / ", " /bad\tpath / ", 1),
		"missing-separator": strings.Replace(mountTestRoot, " - ", " ", 1), "missing-source": strings.Replace(mountTestRoot, " /dev/root rw", " rw", 1),
		"extra-tail": strings.TrimSuffix(mountTestRoot, "\n") + " extra\n", "duplicate-separator": strings.Replace(mountTestRoot, " /dev/root ", " - ", 1),
		"empty-field": strings.Replace(mountTestRoot, " - ", "  - ", 1), "bad-access": strings.Replace(mountTestRoot, "rw,relatime", "relatime", 1),
		"bad-tag":      mountTestRoot + mountTestRecord(2, "8:2", "/", "/source", "shared:0"),
		"repeated-tag": mountTestRoot + mountTestRecord(2, "8:2", "/", "/source", "shared:1 shared:2"),
		"tag-format":   mountTestRoot + mountTestRecord(2, "8:2", "/", "/source", "unbindable:1"),
		"invalid-utf8": mountTestRoot + string([]byte{0xff}) + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateBuildMountTopology([]byte(data), []string{"/source", "/work", "/output"}); err == nil {
				t.Fatal("corrupt/truncated mountinfo accepted")
			}
		})
	}
}

// TestBuildMountTopologyEnforcesBoundsAndCanonicalSelections bounds the table and refuses ambiguous paths.
func TestBuildMountTopologyEnforcesBoundsAndCanonicalSelections(t *testing.T) {
	paths := []string{"/source", "/work", "/output"}
	var table strings.Builder
	table.WriteString(mountTestRoot)
	for id := 2; id <= maximumBuildMountInfoRecords; id++ {
		table.WriteString(mountTestRecord(id, "8:1", "/unrelated", fmt.Sprintf("/unused%d", id), ""))
	}
	if err := validateBuildMountTopology([]byte(table.String()), paths); err != nil {
		t.Fatalf("exact record bound refused: %v", err)
	}
	table.WriteString(mountTestRecord(maximumBuildMountInfoRecords+1, "8:1", "/unrelated", "/one-more", ""))
	if err := validateBuildMountTopology([]byte(table.String()), paths); err == nil {
		t.Fatal("record limit was not enforced")
	}
	if err := validateBuildMountTopology([]byte(strings.Repeat("x", maximumBuildMountInfoBytes)+"\n"), paths); err == nil {
		t.Fatal("byte limit was not enforced")
	}
	for _, selected := range [][]string{nil, {"/source", "/work"}, {"/source", "/work", "/output", "/extra"}, {"relative", "/work", "/output"}, {"/source/../else", "/work", "/output"}, {"/source/", "/work", "/output"}, {"//source", "/work", "/output"}, {"/source\x00", "/work", "/output"}} {
		if err := validateBuildMountTopology([]byte(mountTestRoot), selected); err == nil {
			t.Fatal("missing/noncanonical selected paths accepted")
		}
	}
}
