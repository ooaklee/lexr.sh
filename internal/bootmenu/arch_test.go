package bootmenu

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/platform"
)

// testRootUUID is a synthetic installed root identity, not a device identifier.
const testRootUUID = "12345678-1234-1234-1234-123456789abc"

// testESPUUID is the synthetic GPT identity of the shared ESP.
const testESPUUID = "abcd1234-1234-1234-1234-123456789abc"

// testABI selects fixture boot filenames using the installer ABI contract.
const testABI = "7.2.0-jg-0sp11v23-qcom-x1e"

// testMain models the standard GRUB custom loader and two unrelated OS entries.
const testMain = `menuentry 'Ubuntu' { echo ubuntu; }
menuentry 'Windows' { echo windows; }
### BEGIN /etc/grub.d/41_custom ###
if [ -f  ${config_directory}/custom.cfg ]; then
  source ${config_directory}/custom.cfg
elif [ -z "${config_directory}" -a -f  $prefix/custom.cfg ]; then
  source $prefix/custom.cfg
fi
### END /etc/grub.d/41_custom ###
`

// fixtureRunner supplies mount evidence and records syntax validation boundaries.
type fixtureRunner struct {
	mounts      map[string]mount
	syntaxError bool
	onCheck     func()
	checked     []byte
	commands    []string
}

// Run only admits syntax checking; mutations cannot hide inside test commands.
func (r *fixtureRunner) Run(_ context.Context, c platform.Command) error {
	r.commands = append(r.commands, c.Name)
	if c.Name != "grub-script-check" || len(c.Args) != 0 {
		return errors.New("unexpected mutation command")
	}
	r.checked, _ = io.ReadAll(c.Stdin)
	if r.onCheck != nil {
		r.onCheck()
	}
	if r.syntaxError {
		return errors.New("invalid syntax")
	}
	return nil
}

// Capture supplies mounted identity evidence without touching actual devices.
func (r *fixtureRunner) Capture(_ context.Context, c platform.Command) ([]byte, error) {
	r.commands = append(r.commands, c.Name)
	if c.Name != "findmnt" || len(c.Args) != 5 {
		return nil, errors.New("unexpected inspection")
	}
	m, ok := r.mounts[c.Args[2]]
	if !ok {
		return nil, errors.New("not mounted")
	}
	return json.Marshal(map[string]any{"filesystems": []mount{m}})
}

// fixture creates separate roots and a receipt-bound ARM64 loader.
func fixture(t *testing.T) (ArchOptions, *fixtureRunner) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	o := ArchOptions{ArchRoot: filepath.Join(base, "arch"), ESP: filepath.Join(base, "esp"), GRUBDirectory: filepath.Join(base, "ubuntu/boot/grub")}
	loader := make([]byte, 128)
	copy(loader, "MZ")
	loader[60] = 80
	copy(loader[80:], []byte{'P', 'E', 0, 0, 0x64, 0xaa})
	sum := sha256.Sum256(loader)
	raw, _ := json.Marshal(receipt{Schema: 1, ABI: testABI, RootUUID: testRootUUID, ESPPartUUID: testESPUUID, GRUBSHA256: hex.EncodeToString(sum[:])})
	for name, data := range map[string][]byte{
		filepath.Join(o.ArchRoot, "etc/lexr/install-receipt.json"):  raw,
		filepath.Join(o.ArchRoot, "boot/vmlinuz-"+testABI):          []byte("kernel"),
		filepath.Join(o.ArchRoot, "boot/initramfs-"+testABI+".img"): []byte("initramfs"),
		filepath.Join(o.ESP, loaderPath):                            loader,
		filepath.Join(o.GRUBDirectory, "grub.cfg"):                  []byte(testMain),
	} {
		if err := os.MkdirAll(filepath.Dir(name), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	r := &fixtureRunner{mounts: map[string]mount{
		o.ArchRoot:      {Target: o.ArchRoot, Source: "/dev/test7", FSType: "ext4", UUID: testRootUUID},
		o.ESP:           {Target: o.ESP, Source: "/dev/test1", FSType: "vfat", UUID: "6AD5-53A7", PartUUID: testESPUUID},
		o.GRUBDirectory: {Target: filepath.Dir(o.GRUBDirectory), Source: "/dev/test6", FSType: "ext4", UUID: "other-boot"},
	}}
	return o, r
}

// TestRegistrationPreservesOtherMenus covers preview purity, exact-byte backup,
// repeat application and preserving both primary config and loader bytes.
func TestRegistrationPreservesOtherMenus(t *testing.T) {
	for _, existing := range []string{"", "# Existing user menu\nmenuentry 'Recovery' { echo recovery; }"} {
		t.Run(existing, func(t *testing.T) {
			o, r := fixture(t)
			custom := filepath.Join(o.GRUBDirectory, "custom.cfg")
			if existing != "" {
				if err := os.WriteFile(custom, []byte(existing), 0640); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadDir(o.GRUBDirectory)
			o.DryRun = true
			preview, err := RegisterArch(context.Background(), o, r)
			if err != nil {
				t.Fatal(err)
			}
			after, _ := os.ReadDir(o.GRUBDirectory)
			if len(after) != len(before) {
				t.Fatal("dry-run created files")
			}
			if !strings.Contains(preview.Entry, "chainloader ($lexr_arch_esp)/EFI/LexrArch/grubaa64.efi") || strings.Contains(preview.Entry, "devicetree") {
				t.Fatal(preview.Entry)
			}
			o.DryRun = false
			applied, err := RegisterArch(context.Background(), o, r)
			if err != nil {
				t.Fatal(err)
			}
			data, _ := os.ReadFile(custom)
			if !bytes.HasPrefix(data, []byte(existing)) || !bytes.Contains(data, []byte(preview.Entry)) {
				t.Fatalf("lost existing contents: %s", data)
			}
			if existing != "" {
				backup, _ := os.ReadFile(applied.Backup)
				if string(backup) != existing {
					t.Fatal("backup changed")
				}
				info, _ := os.Stat(custom)
				if info.Mode().Perm() != 0640 {
					t.Fatal("lost mode")
				}
			}
			repeat, err := RegisterArch(context.Background(), o, r)
			if err != nil || !repeat.AlreadyPresent {
				t.Fatalf("repeat: %v %+v", err, repeat)
			}
			repeated, _ := os.ReadFile(custom)
			if !bytes.Equal(data, repeated) {
				t.Fatal("repeat duplicated entry")
			}
			main, _ := os.ReadFile(filepath.Join(o.GRUBDirectory, "grub.cfg"))
			if string(main) != testMain {
				t.Fatal("regenerated menu")
			}
		})
	}
}

// TestInvalidInputsNeverWrite rejects wrong mounts, changed loaders, unknown
// inclusion, recursion, link escapes and invalid pre-existing menu syntax.
func TestInvalidInputsNeverWrite(t *testing.T) {
	cases := map[string]func(*testing.T, *ArchOptions, *fixtureRunner){
		"unmounted root": func(t *testing.T, o *ArchOptions, r *fixtureRunner) { delete(r.mounts, o.ArchRoot) },
		"wrong root": func(t *testing.T, o *ArchOptions, r *fixtureRunner) {
			m := r.mounts[o.ArchRoot]
			m.UUID = "wrong"
			r.mounts[o.ArchRoot] = m
		},
		"wrong esp": func(t *testing.T, o *ArchOptions, r *fixtureRunner) {
			m := r.mounts[o.ESP]
			m.PartUUID = testRootUUID
			r.mounts[o.ESP] = m
		},
		"self chain": func(t *testing.T, o *ArchOptions, r *fixtureRunner) {
			m := r.mounts[o.GRUBDirectory]
			m.UUID = testRootUUID
			r.mounts[o.GRUBDirectory] = m
		},
		"changed loader": func(t *testing.T, o *ArchOptions, r *fixtureRunner) {
			os.WriteFile(filepath.Join(o.ESP, loaderPath), []byte("changed"), 0644)
		},
		"missing initramfs": func(t *testing.T, o *ArchOptions, r *fixtureRunner) {
			os.Remove(filepath.Join(o.ArchRoot, "boot/initramfs-"+testABI+".img"))
		},
		"unknown inclusion": func(t *testing.T, o *ArchOptions, r *fixtureRunner) {
			os.WriteFile(filepath.Join(o.GRUBDirectory, "grub.cfg"), []byte("# source ${config_directory}/custom.cfg\n"), 0644)
		},
		"invalid syntax": func(t *testing.T, o *ArchOptions, r *fixtureRunner) { r.syntaxError = true },
		"conflicting entry": func(t *testing.T, o *ArchOptions, r *fixtureRunner) {
			os.WriteFile(filepath.Join(o.GRUBDirectory, "custom.cfg"), []byte("# lexr-arch-installed\n"), 0644)
		},
		"custom link": func(t *testing.T, o *ArchOptions, r *fixtureRunner) {
			os.Symlink("grub.cfg", filepath.Join(o.GRUBDirectory, "custom.cfg"))
		},
		"directory link": func(t *testing.T, o *ArchOptions, r *fixtureRunner) {
			old := o.GRUBDirectory
			os.Rename(old, old+"-original")
			os.Symlink(old+"-original", old)
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			o, r := fixture(t)
			change(t, &o, r)
			before, _ := os.ReadDir(o.GRUBDirectory)
			if _, err := RegisterArch(context.Background(), o, r); err == nil {
				t.Fatal("accepted invalid input")
			}
			after, _ := os.ReadDir(o.GRUBDirectory)
			if len(before) != len(after) {
				t.Fatal("wrote files before refusing")
			}
		})
	}
}

// TestChangesDuringPreviewArePreserved models another writer or changed mount
// at the command boundary and refuses to overwrite the newer state.
func TestChangesDuringPreviewArePreserved(t *testing.T) {
	for _, name := range []string{"custom", "main", "mount"} {
		t.Run(name, func(t *testing.T) {
			o, r := fixture(t)
			r.onCheck = func() {
				switch name {
				case "custom":
					os.WriteFile(filepath.Join(o.GRUBDirectory, "custom.cfg"), []byte("other writer\n"), 0644)
				case "main":
					os.WriteFile(filepath.Join(o.GRUBDirectory, "grub.cfg"), []byte("other main\n"), 0644)
				case "mount":
					delete(r.mounts, o.ESP)
				}
			}
			if _, err := RegisterArch(context.Background(), o, r); err == nil {
				t.Fatal("accepted changed state")
			}
			data, _ := os.ReadFile(filepath.Join(o.GRUBDirectory, "custom.cfg"))
			if bytes.Contains(data, []byte(entryID)) {
				t.Fatal("overwrote concurrent change")
			}
		})
	}
}
