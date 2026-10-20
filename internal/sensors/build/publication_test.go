package build

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const publicationTestID = "aaaaaaaaaaaaaaaaaaaaaaaa"

func publicationTestParent(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("publication deliberately fails closed outside Linux/Darwin")
	}
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return parent
}

// Synthetic already-verified objects isolate publication's own roster, copy,
// mode and hash checks. They are not ELF/archive verification or native proof.
func publicationTestArtifacts() *VerifiedArtifacts {
	v := &VerifiedArtifacts{valid: true, files: map[string][]byte{}, links: ArtifactLinks()}
	for _, name := range ArtifactFiles() {
		data := []byte("synthetic publication fixture: " + name + "\n")
		v.files[name] = data
		v.artifacts = append(v.artifacts, Artifact{Name: name, Bytes: int64(len(data)), SHA256: publicationSHA256(data), Mode: expectedFiles()[name].mode})
	}
	return v
}

func TestPublicationClosedSetRoundTripAndCollision(t *testing.T) {
	parent := publicationTestParent(t)
	fixture := publicationTestArtifacts()
	receipt := []byte("{\"schema\":\"synthetic-publication-only\"}\n")
	result, err := publishArtifacts(parent, publicationTestID, fixture, receipt)
	if err != nil || !result.Published || !result.Durable || !result.LocationVerified || result.StagingDirectory != "" || result.Directory != filepath.Join(parent, "sensors-"+publicationTestID) || result.ReceiptSHA256 != publicationSHA256(receipt) {
		t.Fatalf("incomplete publication: %+v, %v", result, err)
	}
	entries, err := os.ReadDir(result.Directory)
	if err != nil || len(entries) != 20 {
		t.Fatalf("closed output roster differs: %d, %v", len(entries), err)
	}
	for _, artifact := range fixture.Files() {
		data, err := os.ReadFile(filepath.Join(result.Directory, artifact.Name))
		info, statErr := os.Lstat(filepath.Join(result.Directory, artifact.Name))
		if err != nil || statErr != nil || !info.Mode().IsRegular() || info.Mode().Perm() != os.FileMode(artifact.Mode) || int64(len(data)) != artifact.Bytes || publicationSHA256(data) != artifact.SHA256 {
			t.Fatalf("written artifact differs: %s", artifact.Name)
		}
	}
	for _, link := range ArtifactLinks() {
		target, err := os.Readlink(filepath.Join(result.Directory, link.Name))
		if err != nil || target != link.Target {
			t.Fatalf("written link differs: %s", link.Name)
		}
	}
	data, err := os.ReadFile(filepath.Join(result.Directory, publicationReceiptName))
	if err != nil || string(data) != string(receipt) {
		t.Fatal("receipt bytes differ")
	}
	if info, err := os.Stat(result.Directory); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatal("publication directory is not private")
	}
	second, err := publishArtifacts(parent, publicationTestID, fixture, []byte(`{"different":true}`))
	if err == nil || second.Published || second.StagingDirectory == "" {
		t.Fatalf("existing final output was replaced or partial not retained: %+v, %v", second, err)
	}
	data, err = os.ReadFile(filepath.Join(result.Directory, publicationReceiptName))
	if err != nil || string(data) != string(receipt) {
		t.Fatal("collision changed original receipt")
	}
	if _, err := os.Stat(second.StagingDirectory); err != nil {
		t.Fatal("failed staging directory was deleted")
	}
	third, err := publishArtifacts(parent, publicationTestID, fixture, receipt)
	if err == nil || third.Published || third.StagingDirectory != "" {
		t.Fatal("existing staging directory was reused")
	}
}

func TestPublicationRejectsInvalidInputsBeforeCreatingOutput(t *testing.T) {
	for _, name := range []string{"nil", "zero", "missing", "extra", "duplicate", "wrong-hash", "wrong-size", "wrong-mode", "changed-data", "missing-data", "missing-link", "duplicate-link", "escaping-link", "bad-id", "empty-id", "array-receipt", "null-receipt", "invalid-receipt", "trailing-receipt", "oversized-receipt"} {
		t.Run(name, func(t *testing.T) {
			parent := publicationTestParent(t)
			v, id, receipt := publicationTestArtifacts(), publicationTestID, []byte(`{}`)
			switch name {
			case "nil":
				v = nil
			case "zero":
				v = &VerifiedArtifacts{}
			case "missing":
				v.artifacts = v.artifacts[1:]
			case "extra":
				v.artifacts = append(v.artifacts, Artifact{Name: "extra"})
			case "duplicate":
				v.artifacts[1] = v.artifacts[0]
			case "wrong-hash":
				v.artifacts[0].SHA256 = strings.Repeat("0", 64)
			case "wrong-size":
				v.artifacts[0].Bytes++
			case "wrong-mode":
				v.artifacts[0].Mode = 0o777
			case "changed-data":
				v.files[v.artifacts[0].Name][0] ^= 1
			case "missing-data":
				delete(v.files, v.artifacts[0].Name)
			case "missing-link":
				v.links = v.links[1:]
			case "duplicate-link":
				v.links[1] = v.links[0]
			case "escaping-link":
				v.links[0].Target = "../outside"
			case "bad-id":
				id = strings.Repeat("A", 24)
			case "empty-id":
				id = ""
			case "array-receipt":
				receipt = []byte(`[]`)
			case "null-receipt":
				receipt = []byte(`null`)
			case "invalid-receipt":
				receipt = []byte(`{`)
			case "trailing-receipt":
				receipt = []byte(`{} {}`)
			case "oversized-receipt":
				receipt = []byte(`{"padding":"` + strings.Repeat("x", 1<<20) + `"}`)
			}
			result, err := publishArtifacts(parent, id, v, receipt)
			if err == nil || result.Published || result.StagingDirectory != "" {
				t.Fatalf("invalid input created output: %+v, %v", result, err)
			}
			entries, err := os.ReadDir(parent)
			if err != nil || len(entries) != 0 {
				t.Fatal("invalid input changed parent")
			}
		})
	}
}

func TestPublicationRejectsNoncanonicalOrSymlinkParent(t *testing.T) {
	base := publicationTestParent(t)
	parent := filepath.Join(base, "actual")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(parent, alias); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{alias, parent + string(filepath.Separator), "relative", filepath.Join(base, "missing")} {
		if result, err := publishArtifacts(path, publicationTestID, publicationTestArtifacts(), []byte(`{}`)); err == nil || result.Published {
			t.Fatalf("unsafe parent accepted: %q", path)
		}
	}
}

func TestPublicationRetainsPartialOnWriteOrDirectorySyncFailure(t *testing.T) {
	for _, phase := range []string{"file", "staging", "parent"} {
		t.Run(phase, func(t *testing.T) {
			parent := publicationTestParent(t)
			hooks := publicationHooks{}
			if phase == "file" {
				hooks.syncFile = func(*os.File) error { return errors.New("injected file sync failure") }
			} else {
				hooks.syncDirectory = func(f *os.File, current string) error {
					if current == phase {
						return errors.New("injected " + current + " sync failure")
					}
					return f.Sync()
				}
			}
			result, err := publishArtifactsWithHooks(parent, publicationTestID, publicationTestArtifacts(), []byte(`{}`), hooks)
			if err == nil || !strings.Contains(err.Error(), "sync failure") || result.Durable {
				t.Fatalf("sync failure not represented: %+v, %v", result, err)
			}
			if phase == "parent" {
				if !result.Published || !result.LocationVerified || result.Directory == "" || result.StagingDirectory != "" {
					t.Fatalf("post-rename failure falsely denied existing publication: %+v", result)
				}
				if _, err := os.Stat(result.Directory); err != nil {
					t.Fatal("published directory disappeared")
				}
			} else if result.Published || result.LocationVerified || result.StagingDirectory == "" {
				t.Fatalf("pre-rename failure flags differ: %+v", result)
			} else if _, err := os.Stat(result.StagingDirectory); err != nil {
				t.Fatal("failed partial was not retained")
			}
		})
	}
}

func TestPublicationRechecksStagedBytesModesLinksAndRoster(t *testing.T) {
	for _, kind := range []string{"bytes", "mode", "link", "symlink-file", "hardlink-file", "extra"} {
		t.Run(kind, func(t *testing.T) {
			parent := publicationTestParent(t)
			stage := filepath.Join(parent, ".sensors-"+publicationTestID+".partial")
			hooks := publicationHooks{beforeRename: func() error {
				file := filepath.Join(stage, ArtifactFiles()[0])
				switch kind {
				case "bytes":
					data, err := os.ReadFile(file)
					if err != nil {
						return err
					}
					data[0] ^= 1
					return os.WriteFile(file, data, 0o644)
				case "mode":
					return os.Chmod(file, 0o600)
				case "link":
					name := filepath.Join(stage, ArtifactLinks()[0].Name)
					if err := os.Rename(name, filepath.Join(parent, "retained-link")); err != nil {
						return err
					}
					return os.Symlink("../outside", name)
				case "symlink-file", "hardlink-file":
					saved := filepath.Join(parent, "retained-file")
					if err := os.Rename(file, saved); err != nil {
						return err
					}
					if kind == "symlink-file" {
						return os.Symlink(saved, file)
					}
					return os.Link(saved, file)
				default:
					return os.WriteFile(filepath.Join(stage, "extra"), []byte("extra"), 0o600)
				}
			}}
			result, err := publishArtifactsWithHooks(parent, publicationTestID, publicationTestArtifacts(), []byte(`{}`), hooks)
			if err == nil || result.Published || result.StagingDirectory != stage {
				t.Fatalf("staged mutation was published: %+v, %v", result, err)
			}
			if _, err := os.Stat(stage); err != nil {
				t.Fatal("mutated partial was deleted")
			}
		})
	}
}

func TestPublicationParentReplacementBeforeAndAfterRename(t *testing.T) {
	for _, timing := range []string{"before", "after"} {
		t.Run(timing, func(t *testing.T) {
			base := publicationTestParent(t)
			parent, moved := filepath.Join(base, "parent"), filepath.Join(base, "retained-parent")
			if err := os.Mkdir(parent, 0o700); err != nil {
				t.Fatal(err)
			}
			replace := func() error {
				if err := os.Rename(parent, moved); err != nil {
					return err
				}
				if err := os.Mkdir(parent, 0o700); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(parent, "sentinel"), []byte("replacement untouched"), 0o600)
			}
			hooks := publicationHooks{}
			if timing == "before" {
				hooks.beforeRename = replace
			} else {
				hooks.afterRename = replace
			}
			result, err := publishArtifactsWithHooks(parent, publicationTestID, publicationTestArtifacts(), []byte(`{}`), hooks)
			if err == nil || !strings.Contains(err.Error(), "parent pathname changed") || result.LocationVerified || result.Durable || result.Published != (timing == "after") {
				t.Fatalf("parent replacement outcome differs: %+v, %v", result, err)
			}
			entries, err := os.ReadDir(parent)
			data, readErr := os.ReadFile(filepath.Join(parent, "sentinel"))
			if err != nil || readErr != nil || len(entries) != 1 || string(data) != "replacement untouched" {
				t.Fatal("replacement parent was mutated")
			}
			ownedName := ".sensors-" + publicationTestID + ".partial"
			if timing == "after" {
				ownedName = "sensors-" + publicationTestID
			}
			if _, err := os.Stat(filepath.Join(moved, ownedName)); err != nil {
				t.Fatal("owned retained output missing")
			}
		})
	}
}

func TestPublicationCapturesCallerReceiptBytes(t *testing.T) {
	parent := publicationTestParent(t)
	receipt := []byte(`{"captured":true}`)
	expected := append([]byte(nil), receipt...)
	hooks := publicationHooks{beforeRename: func() error {
		copy(receipt, []byte(strings.Repeat("x", len(receipt))))
		return nil
	}}
	result, err := publishArtifactsWithHooks(parent, publicationTestID, publicationTestArtifacts(), receipt, hooks)
	if err != nil || result.ReceiptSHA256 != publicationSHA256(expected) {
		t.Fatalf("receipt metadata used later caller mutation: %+v, %v", result, err)
	}
	data, err := os.ReadFile(filepath.Join(result.Directory, publicationReceiptName))
	if err != nil || string(data) != string(expected) {
		t.Fatal("captured receipt bytes were not published")
	}
}
