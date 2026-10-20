package build

import (
	"archive/tar"
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestRecipeSourceNamespacePreservesRepresentableNames checks the actual
// destination filesystem rather than assuming that an OS name implies case
// sensitivity. An incapable filesystem must cause an explicit, payload-free
// rejection, not a skip or a falsely successful extraction.
func TestRecipeSourceNamespacePreservesRepresentableNames(t *testing.T) {
	tests := []struct {
		name    string
		members []recipeTarMember
	}{
		{
			name: "ordinary single file",
			members: []recipeTarMember{
				{name: "README", data: []byte("ordinary source\n")},
			},
		},
		{
			name: "kernel case-distinct headers",
			members: []recipeTarMember{
				{name: "include/uapi/linux/netfilter/xt_CONNMARK.h", data: []byte("upper header\n")},
				{name: "include/uapi/linux/netfilter/xt_connmark.h", data: []byte("lower header\n")},
			},
		},
		{
			name: "implicit case-distinct directories",
			members: []recipeTarMember{
				{name: "Dir/a", data: []byte("upper directory\n")},
				{name: "dir/b", data: []byte("lower directory\n")},
			},
		},
		{
			name: "case-distinct file and directory",
			members: []recipeTarMember{
				{name: "Node", data: []byte("regular node\n")},
				{name: "node/child", data: []byte("directory child\n")},
			},
		},
		{
			name: "case-distinct symlink and directory",
			members: []recipeTarMember{
				{name: "target", data: []byte("internal link target\n")},
				{name: "Link", typeflag: tar.TypeSymlink, linkname: "target"},
				{name: "link/child", data: []byte("separate directory child\n")},
			},
		},
		{
			name: "Unicode casefold-distinct names",
			members: []recipeTarMember{
				{name: "Straße.h", data: []byte("sharp s spelling\n")},
				{name: "STRASSE.h", data: []byte("double s spelling\n")},
			},
		},
		{
			name: "Unicode normalization-distinct names",
			members: []recipeTarMember{
				{name: "caf\u00e9.h", data: []byte("composed spelling\n")},
				{name: "cafe\u0301.h", data: []byte("decomposed spelling\n")},
			},
		},
		{
			name: "single normalization-sensitive name",
			members: []recipeTarMember{
				{name: "caf\u00e9.h", data: []byte("exact composed spelling\n")},
			},
		},
		{
			name: "safe internal relative link",
			members: []recipeTarMember{
				{name: "real/file", data: []byte("contained data\n")},
				{name: "links/file", typeflag: tar.TypeSymlink, linkname: "../real/file"},
			},
		},
		{
			name: "safe internal link text remains exact",
			members: []recipeTarMember{
				{name: "real/nested", typeflag: tar.TypeDir},
				{name: "real/file", data: []byte("contained data\n")},
				{name: "link", typeflag: tar.TypeSymlink, linkname: "real/nested/../file"},
			},
		},
		{
			name: "explicit directory before child",
			members: []recipeTarMember{
				{name: "directory", typeflag: tar.TypeDir},
				{name: "directory/file", data: []byte("child data\n")},
			},
		},
		{
			name: "explicit directory after child",
			members: []recipeTarMember{
				{name: "directory/file", data: []byte("child data\n")},
				{name: "directory", typeflag: tar.TypeDir},
			},
		},
		{
			name: "Git-adjacent names are not metadata",
			members: []recipeTarMember{
				{name: ".gitignore", data: []byte("*.o\n")},
				{name: ".github/README", data: []byte("ordinary committed data\n")},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			archive := filepath.Join(root, "source.tar")
			writeRecipeTar(t, archive, test.members)
			destination := filepath.Join(root, "destination")
			if err := os.Mkdir(destination, 0o700); err != nil {
				t.Fatal(err)
			}
			nodes := recipeSourceNamespaceNodes(test.members)
			capable, err := recipeSourceNamespaceCapability(destination, nodes)
			if err != nil {
				t.Fatalf("independent destination capability check: %v", err)
			}
			output, err := executeRecipeLocalSourceExtractor(t, archive, destination, strconv.Itoa(recipeSourceNamespaceFileCount(test.members)))
			if !capable {
				if err == nil || !strings.Contains(string(output), "cannot represent") {
					t.Fatalf("incapable destination error = %v, output %q", err, output)
				}
				assertRecipeSourceNamespaceEmpty(t, destination)
				return
			}
			if err != nil {
				t.Fatalf("extract representable namespace: %v\n%s", err, output)
			}
			assertRecipeSourceNamespaceContents(t, destination, nodes, test.members)
		})
	}
}

// TestRecipeSourceNamespaceRejectsBeforePayload requires complete preflight:
// an invalid later member cannot leave earlier regular-file payload behind.
func TestRecipeSourceNamespaceRejectsBeforePayload(t *testing.T) {
	tests := []struct {
		name     string
		members  []recipeTarMember
		expected string
		want     string
	}{
		{
			name: "duplicate regular file",
			members: []recipeTarMember{
				{name: "duplicate", data: []byte("first\n")},
				{name: "duplicate", data: []byte("second\n")},
			},
			want: "duplicate",
		},
		{
			name: "duplicate directory header",
			members: []recipeTarMember{
				{name: "directory", typeflag: tar.TypeDir},
				{name: "directory", typeflag: tar.TypeDir},
			},
			want: "duplicate",
		},
		{
			name: "regular file before descendant",
			members: []recipeTarMember{
				{name: "node", data: []byte("not a directory\n")},
				{name: "node/child", data: []byte("child\n")},
			},
		},
		{
			name: "regular file after descendant",
			members: []recipeTarMember{
				{name: "node/child", data: []byte("child\n")},
				{name: "node", data: []byte("not a directory\n")},
			},
		},
		{
			name: "symlink before descendant",
			members: []recipeTarMember{
				{name: "link", typeflag: tar.TypeSymlink, linkname: "real"},
				{name: "link/child", data: []byte("child\n")},
			},
		},
		{
			name: "symlink after descendant",
			members: []recipeTarMember{
				{name: "link/child", data: []byte("child\n")},
				{name: "link", typeflag: tar.TypeSymlink, linkname: "real"},
			},
		},
		{
			name: "internal hardlink remains unsupported",
			members: []recipeTarMember{
				{name: "target", data: []byte("internal target\n")},
				{name: "alias", typeflag: tar.TypeLink, linkname: "target"},
			},
			want: "unsupported member type",
		},
		{name: "FIFO remains unsupported", members: []recipeTarMember{{name: "fifo", typeflag: tar.TypeFifo}}, want: "unsupported member type"},
		{name: "traversal", members: []recipeTarMember{{name: "../outside-marker", data: []byte("overwrite\n")}}},
		{name: "absolute path", members: []recipeTarMember{{name: "/absolute-source-path", data: []byte("absolute\n")}}},
		{name: "noncanonical path", members: []recipeTarMember{{name: "nested/../file", data: []byte("noncanonical\n")}}},
		{name: "dot component", members: []recipeTarMember{{name: "nested/./file", data: []byte("dot\n")}}},
		{name: "newline in path", members: []recipeTarMember{{name: "line\nbreak", data: []byte("control\n")}}},
		{name: "tab in path", members: []recipeTarMember{{name: "tab\tname", data: []byte("control\n")}}},
		{name: "DEL in path", members: []recipeTarMember{{name: "delete\u007fname", data: []byte("control\n")}}},
		{name: "oversized path", members: []recipeTarMember{{name: strings.Repeat("a", 4097), data: []byte("long\n")}}},
		{name: "Git metadata at root", members: []recipeTarMember{{name: ".git/config", data: []byte("metadata\n")}}},
		{name: "casefold Git metadata", members: []recipeTarMember{{name: ".GIT/config", data: []byte("metadata\n")}}},
		{name: "nested casefold Git metadata", members: []recipeTarMember{{name: "nested/.GiT/config", data: []byte("metadata\n")}}},
		{name: "Git metadata regular member", members: []recipeTarMember{{name: ".git", data: []byte("metadata\n")}}},
		{name: "escaping symbolic link", members: []recipeTarMember{{name: "link", typeflag: tar.TypeSymlink, linkname: "../../outside-marker"}}},
		{name: "absolute symbolic link", members: []recipeTarMember{{name: "link", typeflag: tar.TypeSymlink, linkname: "/outside-marker"}}},
		{name: "newline in symbolic link", members: []recipeTarMember{{name: "link", typeflag: tar.TypeSymlink, linkname: "line\nbreak"}}},
		{name: "wrong file count", members: []recipeTarMember{{name: "file", data: []byte("counted\n")}}, expected: "3", want: "file count"},
		{name: "malformed file count", members: []recipeTarMember{{name: "file", data: []byte("counted\n")}}, expected: "not-a-number"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			outside := filepath.Join(root, "outside-marker")
			if err := os.WriteFile(outside, []byte("preserve outside\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			// This leading member detects accidental extraction while validating.
			members := append([]recipeTarMember{{name: "early-payload", data: []byte("must not be extracted\n")}}, test.members...)
			archive := filepath.Join(root, "source.tar")
			writeRecipeTar(t, archive, members)
			destination := filepath.Join(root, "destination")
			if err := os.Mkdir(destination, 0o700); err != nil {
				t.Fatal(err)
			}
			expected := test.expected
			if expected == "" {
				expected = strconv.Itoa(recipeSourceNamespaceFileCount(members))
			}
			output, err := executeRecipeLocalSourceExtractor(t, archive, destination, expected)
			if err == nil || (test.want != "" && !strings.Contains(string(output), test.want)) {
				t.Fatalf("invalid namespace error = %v, output %q", err, output)
			}
			assertRecipeSourceNamespaceEmpty(t, destination)
			contents, err := os.ReadFile(outside)
			if err != nil || string(contents) != "preserve outside\n" {
				t.Fatalf("outside marker = %q, %v", contents, err)
			}
		})
	}
}

// TestRecipeSourceNamespaceRejectsFinalEscapingLinkChains covers both orders:
// lexical normalization of a/.. is inside, but a -> . makes it escape after
// the complete set of links exists. No claim of preflight rejection is made.
func TestRecipeSourceNamespaceRejectsFinalEscapingLinkChains(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		t.Run(fmt.Sprintf("reverse=%t", reverse), func(t *testing.T) {
			root := t.TempDir()
			destination := filepath.Join(root, "destination")
			if err := os.Mkdir(destination, 0o700); err != nil {
				t.Fatal(err)
			}
			members := []recipeTarMember{
				{name: "a", typeflag: tar.TypeSymlink, linkname: "."},
				{name: "b", typeflag: tar.TypeSymlink, linkname: "a/.."},
			}
			if reverse {
				members[0], members[1] = members[1], members[0]
			}
			archive := filepath.Join(root, "source.tar")
			writeRecipeTar(t, archive, members)
			output, err := executeRecipeLocalSourceExtractor(t, archive, destination, "2")
			if err == nil {
				t.Fatalf("final escaping link chain accepted: %s", output)
			}
		})
	}
}

// TestRecipeSourceNamespaceRejectsNonemptyDestination ensures the probe and
// extractor cannot overwrite pre-existing destination data.
func TestRecipeSourceNamespaceRejectsNonemptyDestination(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "source.tar")
	writeRecipeTar(t, archive, []recipeTarMember{{name: "payload", data: []byte("new data\n")}})
	destination := filepath.Join(root, "destination")
	if err := os.Mkdir(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(destination, "keep")
	if err := os.WriteFile(marker, []byte("existing data\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	output, err := executeRecipeLocalSourceExtractor(t, archive, destination, "1")
	if err == nil {
		t.Fatalf("nonempty destination accepted: %s", output)
	}
	entries, err := os.ReadDir(destination)
	if err != nil || len(entries) != 1 || entries[0].Name() != "keep" {
		t.Fatalf("destination entries = %v, %v", entries, err)
	}
	contents, err := os.ReadFile(marker)
	if err != nil || string(contents) != "existing data\n" {
		t.Fatalf("existing marker = %q, %v", contents, err)
	}
}

// TestRecipeSourceNamespaceProbeFailuresArePayloadFree injects failures only
// through a Python preamble in this test process. The production extractor is
// otherwise copied byte-for-byte from its recipe markers; there is no public
// bypass, environment switch or production fault-injection argument.
func TestRecipeSourceNamespaceProbeFailuresArePayloadFree(t *testing.T) {
	tests := []struct {
		name     string
		preamble string
		want     string
	}{
		{
			name: "temporary namespace I/O error",
			preamble: `import tempfile
def injected_probe_failure(*args, **kwargs):
    raise OSError("injected namespace probe I/O failure")
tempfile.TemporaryDirectory = injected_probe_failure
`,
			want: "injected namespace probe I/O failure",
		},
		{
			name: "exclusive creation observes an alias",
			preamble: `import builtins
import errno
import os
original_namespace_open = builtins.open
def injected_namespace_alias(filename, mode="r", *args, **kwargs):
    if "x" in mode and os.path.basename(os.fspath(filename)) == "beta":
        raise FileExistsError(errno.EEXIST, "injected namespace alias", filename)
    return original_namespace_open(filename, mode, *args, **kwargs)
builtins.open = injected_namespace_alias
`,
			want: "cannot represent",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			archive := filepath.Join(root, "source.tar")
			writeRecipeTar(t, archive, []recipeTarMember{
				{name: "alpha", data: []byte("alpha payload\n")},
				{name: "beta", data: []byte("beta payload\n")},
			})
			destination := filepath.Join(root, "destination")
			if err := os.Mkdir(destination, 0o700); err != nil {
				t.Fatal(err)
			}
			output, err := executeRecipeSourceNamespaceWithPreamble(t, test.preamble, archive, destination, "2")
			if err == nil || !strings.Contains(string(output), test.want) {
				t.Fatalf("injected probe error = %v, output %q", err, output)
			}
			assertRecipeSourceNamespaceEmpty(t, destination)
		})
	}
}

// recipeSourceNamespaceNodes expands implicit parents without folding or
// normalizing spelling. It is used only for the valid positive fixtures.
func recipeSourceNamespaceNodes(members []recipeTarMember) map[string]bool {
	nodes := make(map[string]bool)
	for _, member := range members {
		nodes[member.name] = member.typeflag == tar.TypeDir
		for parent := path.Dir(member.name); parent != "."; parent = path.Dir(parent) {
			nodes[parent] = true
		}
	}
	return nodes
}

// recipeSourceNamespaceCapability independently checks actual filesystem
// round-tripping with exclusive creation and exact readdir spellings, not a
// Unicode casefold approximation. All link names are regular sentinels here.
func recipeSourceNamespaceCapability(destination string, nodes map[string]bool) (capable bool, resultErr error) {
	probe, err := os.MkdirTemp(destination, ".test-namespace-capability-")
	if err != nil {
		return false, err
	}
	defer func() {
		if err := os.RemoveAll(probe); err != nil && resultErr == nil {
			resultErr = err
		}
	}()
	names := make([]string, 0, len(nodes))
	for name := range nodes {
		names = append(names, name)
	}
	sort.Slice(names, func(left, right int) bool {
		leftDepth, rightDepth := strings.Count(names[left], "/"), strings.Count(names[right], "/")
		if leftDepth != rightDepth {
			return leftDepth < rightDepth
		}
		return names[left] < names[right]
	})
	var identities []fs.FileInfo
	for _, name := range names {
		filename := filepath.Join(probe, filepath.FromSlash(name))
		if nodes[name] {
			err = os.Mkdir(filename, 0o700)
		} else {
			var file *os.File
			file, err = os.OpenFile(filename, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			if err == nil {
				err = file.Close()
			}
		}
		if os.IsExist(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		entries, err := os.ReadDir(filepath.Dir(filename))
		if err != nil {
			return false, err
		}
		exact := false
		for _, entry := range entries {
			if entry.Name() == path.Base(name) {
				exact = true
			}
		}
		if !exact {
			return false, nil
		}
		identity, err := os.Lstat(filename)
		if err != nil {
			return false, err
		}
		for _, previous := range identities {
			if os.SameFile(previous, identity) {
				return false, nil
			}
		}
		identities = append(identities, identity)
	}
	return true, nil
}

// assertRecipeSourceNamespaceContents proves all payloads, link targets and
// exact explicit/implicit path spellings survived, without following symlinks.
func assertRecipeSourceNamespaceContents(t *testing.T, destination string, nodes map[string]bool, members []recipeTarMember) {
	t.Helper()
	actual := make(map[string]bool)
	if err := filepath.WalkDir(destination, func(filename string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if filename == destination {
			return nil
		}
		relative, err := filepath.Rel(destination, filename)
		if err != nil {
			return err
		}
		actual[filepath.ToSlash(relative)] = entry.IsDir()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(actual) != len(nodes) {
		t.Fatalf("extracted namespace = %#v; want %#v", actual, nodes)
	}
	for name, directory := range nodes {
		actualDirectory, exists := actual[name]
		if !exists || actualDirectory != directory {
			t.Fatalf("extracted path %q directory=%t exists=%t; want directory=%t", name, actualDirectory, exists, directory)
		}
	}
	for _, member := range members {
		filename := filepath.Join(destination, filepath.FromSlash(member.name))
		if member.typeflag == tar.TypeDir {
			continue
		}
		if member.typeflag == tar.TypeSymlink {
			target, err := os.Readlink(filename)
			if err != nil || target != member.linkname {
				t.Fatalf("link %q target = %q, %v; want %q", member.name, target, err, member.linkname)
			}
			continue
		}
		contents, err := os.ReadFile(filename)
		if err != nil || string(contents) != string(member.data) {
			t.Fatalf("file %q = %q, %v; want %q", member.name, contents, err, member.data)
		}
	}
}

// recipeSourceNamespaceFileCount excludes directory headers from the source identity count.
func recipeSourceNamespaceFileCount(members []recipeTarMember) int {
	count := 0
	for _, member := range members {
		if member.typeflag == 0 || member.typeflag == tar.TypeReg || member.typeflag == tar.TypeRegA || member.typeflag == tar.TypeSymlink {
			count++
		}
	}
	return count
}

// assertRecipeSourceNamespaceEmpty checks that preflight did not extract payloads.
func assertRecipeSourceNamespaceEmpty(t *testing.T, destination string) {
	t.Helper()
	entries, err := os.ReadDir(destination)
	if err != nil || len(entries) != 0 {
		t.Fatalf("rejected extraction left destination entries %v, %v", entries, err)
	}
}

// executeRecipeSourceNamespaceWithPreamble has one explicit test-only seam:
// prepend standard-library mocks before the unchanged embedded extractor.
func executeRecipeSourceNamespaceWithPreamble(t *testing.T, preamble string, arguments ...string) ([]byte, error) {
	t.Helper()
	const startMarker = "<<'PY_LOCAL_SOURCE'\n"
	const endMarker = "\nPY_LOCAL_SOURCE\n"
	if strings.Count(containerRecipe, startMarker) != 1 || strings.Count(containerRecipe, endMarker) != 1 {
		t.Fatal("local source extractor markers are not unique")
	}
	start := strings.Index(containerRecipe, startMarker) + len(startMarker)
	end := strings.Index(containerRecipe[start:], endMarker)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "python3", append([]string{"-"}, arguments...)...)
	command.Stdin = strings.NewReader(preamble + "\n" + containerRecipe[start:start+end])
	return command.CombinedOutput()
}
