package install

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestAudioPackagedSelector exercises the real four-file transaction against
// Arch's ALSA link, including dry-run and preservation of the shared profile.
func TestAudioPackagedSelector(t *testing.T) {
	originalSpec := audioSpec
	t.Cleanup(func() { audioSpec = originalSpec })
	bundle, spec := makeBundle(t, AudioComponent, "sp11-audio-v19c", map[string][]byte{
		"X1E80100-Microsoft-Surface-Pro-11-tplg.bin": []byte("topology"),
		"MICROSOFT-Surface-Pro-11in.conf":            []byte("card"),
		"SP11-HiFi.conf":                             []byte("verb"),
		"x1e80100.conf":                              []byte("surface lookup"),
	})
	audioSpec = spec
	root := t.TempDir()
	selector := filepath.Join(root, audioSelectorPath)
	referent := filepath.Join(root, "usr/share/alsa/ucm2/Qualcomm/x1e80100/x1e80100.conf")
	for _, path := range []string{selector, referent} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(referent, []byte("shared Qualcomm profile"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(audioSelectorLink, selector); err != nil {
		t.Fatal(err)
	}
	installer := New(&fakeRunner{})
	installer.euid = func() int { return 501 }
	dryRun, err := installer.Audio(context.Background(), Options{BundleDir: bundle, Root: root, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(dryRun.Files) != 4 || dryRun.Files[3].Action != "replace-link" {
		t.Fatalf("unexpected plan: %+v", dryRun)
	}
	if link, err := os.Readlink(selector); err != nil || link != audioSelectorLink {
		t.Fatalf("dry-run changed link: %q, %v", link, err)
	}
	if _, err := os.Stat(dryRun.BackupDirectory); !os.IsNotExist(err) {
		t.Fatalf("dry-run created backup: %v", err)
	}
	installer.euid = func() int { return 0 }
	result, err := installer.Audio(context.Background(), Options{BundleDir: bundle, Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(selector); err != nil || string(got) != "surface lookup" {
		t.Fatalf("selector = %q, %v", got, err)
	}
	if info, err := os.Lstat(selector); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("selector not regular: %v", err)
	}
	if link, err := os.Readlink(result.Files[3].Backup); err != nil || link != audioSelectorLink {
		t.Fatalf("backup lost link type: %q, %v", link, err)
	}
	if got, err := os.ReadFile(referent); err != nil || string(got) != "shared Qualcomm profile" {
		t.Fatalf("shared profile changed: %q, %v", got, err)
	}
}

// TestAudioSelectorRefusesOtherLinks keeps the exception limited to the exact
// packaged link and does not permit redirected selectors or other audio files.
func TestAudioSelectorRefusesOtherLinks(t *testing.T) {
	for _, link := range []string{"/etc/passwd", "../../../../../../../../outside", "../../Qualcomm/x1e80100/other.conf"} {
		t.Run(link, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, audioSelectorPath)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(link, path); err != nil {
				t.Fatal(err)
			}
			if _, err := resolveAudioTarget(root, audioSelectorPath); err == nil {
				t.Fatal("unexpected selector link accepted")
			}
		})
	}
	root := t.TempDir()
	other := "usr/share/alsa/ucm2/Qualcomm/x1e80100/SP11-HiFi.conf"
	path := filepath.Join(root, other)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(audioSelectorLink, path); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveAudioTarget(root, other); err == nil {
		t.Fatal("non-selector link accepted")
	}
}

// TestAudioSelectorRollbackAndPublicationFailures covers restoring the link
// after a later transaction failure, corrupt input and a changed planned link.
func TestAudioSelectorRollbackAndPublicationFailures(t *testing.T) {
	for _, scenario := range []string{"rollback", "corrupt-source", "changed-link", "changed-installed-file"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "payload")
			if err := os.WriteFile(source, []byte("surface lookup"), 0o644); err != nil {
				t.Fatal(err)
			}
			digest, info, err := hashRegularNoFollow(source)
			if err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(root, "selector")
			if err := os.Symlink(audioSelectorLink, target); err != nil {
				t.Fatal(err)
			}
			original, err := os.Lstat(target)
			if err != nil {
				t.Fatal(err)
			}
			plan := audioChangePlan{change: FileChange{Source: source, Target: target, Backup: filepath.Join(root, "backup/selector"), Replaced: true}, sourceDigest: digest, sourceSize: info.Size(), mode: 0o644, originalLink: audioSelectorLink, originalInfo: original}
			if err := backupAudioSelector(plan); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "corrupt-source":
				if err := os.WriteFile(source, []byte("bad payload"), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := publishAudioTarget(plan); err == nil {
					t.Fatal("corrupt source published")
				}
			case "changed-link":
				// Keep the old inode allocated so this deterministically tests
				// a different inode even when the new link has identical text.
				if err := os.Rename(target, target+".old"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(audioSelectorLink, target); err != nil {
					t.Fatal(err)
				}
				if err := publishAudioTarget(plan); err == nil {
					t.Fatal("changed link overwritten")
				}
			default:
				if err := publishAudioTarget(plan); err != nil {
					t.Fatal(err)
				}
				if scenario == "changed-installed-file" {
					if err := os.WriteFile(target, []byte("subsequent edit"), 0o644); err != nil {
						t.Fatal(err)
					}
					if err := rollbackAudio([]audioChangePlan{plan}); err == nil {
						t.Fatal("changed file overwritten by rollback")
					}
					if got, _ := os.ReadFile(target); string(got) != "subsequent edit" {
						t.Fatalf("edit lost: %q", got)
					}
					return
				}
				if err := rollbackAudio([]audioChangePlan{plan}); err != nil {
					t.Fatal(err)
				}
			}
			if link, err := os.Readlink(target); err != nil || link != audioSelectorLink {
				t.Fatalf("original link lost: %q, %v", link, err)
			}
		})
	}
}
