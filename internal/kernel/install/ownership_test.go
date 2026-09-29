package install

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/bootidentity"
	"github.com/ooaklee/lexr.sh/internal/platform"
)

// writeRawOwnershipGRUB deliberately bypasses the legacy fixture migration.
func writeRawOwnershipGRUB(t *testing.T, root, text string) {
	t.Helper()
	file := filepath.Join(root, "boot/grub/grub.cfg")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// ownershipMenu describes literal selectors, never inferred fixture ownership.
func ownershipMenu(title, bootUUID, rootArgument, abi, initrd string) string {
	return "menuentry '" + title + "' {\n search --no-floppy --fs-uuid --set=root " + bootUUID + "\n" +
		" linux /boot/vmlinuz-" + abi + " " + rootArgument + "\n initrd /boot/" + initrd + "\n}\n"
}

// TestGRUBOwnershipSameABIDistros covers false rejection and false acceptance
// without relying on distribution names or their initramfs naming convention.
func TestGRUBOwnershipSameABIDistros(t *testing.T) {
	for _, tc := range []struct{ title, initrd string }{
		{"Arch Linux ARM", "initramfs-" + fixtureFallbackABI + ".img"},
		{"Fedora", "initramfs-" + fixtureFallbackABI + ".img"},
		{"Ubuntu " + fixtureFallbackABI, "initrd.img-" + fixtureFallbackABI},
		{"Debian " + fixtureFallbackABI, "initrd.img-" + fixtureFallbackABI},
		{"elementary " + fixtureFallbackABI, "initrd.img-" + fixtureFallbackABI},
		{"Unlabelled foreign system", "initrd.img-" + fixtureFallbackABI},
	} {
		t.Run(tc.title, func(t *testing.T) {
			root, _ := fixtureEnvironment(t)
			local := fixtureOwnedGRUB(fixtureGRUB(false))
			foreign := ownershipMenu(tc.title, "foreign-boot", "root=UUID=foreign-root", fixtureFallbackABI, tc.initrd)
			writeRawOwnershipGRUB(t, root, local+foreign)
			before, err := os.ReadFile(filepath.Join(root, "boot/grub/grub.cfg"))
			if err != nil {
				t.Fatal(err)
			}
			evidence, err := verifyFallback(fixtureContext(), root, fixtureFallbackABI)
			if err != nil || evidence.GRUBEntryCount != 1 || evidence.DeviceTreeBoot.GRUBEntryCount != 1 {
				t.Fatalf("foreign entry contaminated local fallback: %+v %v", evidence, err)
			}
			entries, err := InspectGRUB(fixtureContext(), root)
			if err != nil || len(entries) != 2 || entries[1].Ownership != GRUBForeign {
				t.Fatalf("ownership: %+v %v", entries, err)
			}
			if _, _, err := InspectGRUBArtifact(fixtureContext(), root, entries[1], entries[1].Linux[0]); err == nil {
				t.Fatal("foreign kernel was certified using host bytes")
			}
			after, err := os.ReadFile(filepath.Join(root, "boot/grub/grub.cfg"))
			if err != nil || string(before) != string(after) {
				t.Fatal("read-only inspection rewrote foreign menu")
			}
			writeRawOwnershipGRUB(t, root, foreign)
			if _, err := verifyFallback(fixtureContext(), root, fixtureFallbackABI); err == nil {
				t.Fatal("foreign-only entry qualified local fallback")
			}
		})
	}
}

// TestGRUBOwnershipStaticSubset checks both supported generated forms and
// fail-closed treatment of ambiguous or executable selectors.
func TestGRUBOwnershipStaticSubset(t *testing.T) {
	base := ownershipMenu("Ubuntu "+fixtureFallbackABI, "fixture-root", "root=UUID=fixture-root", fixtureFallbackABI, "initrd.img-"+fixtureFallbackABI)
	search := "search --no-floppy --fs-uuid --set=root fixture-root"
	for _, tc := range []struct {
		name, text string
		want       GRUBOwnership
	}{
		{"owned UUID", base, GRUBOwned},
		{"file-scope conditional", "if [ x$feature = xy ]; then\n" + base + "fi\n", GRUBUnresolved},
		{"partition selector", strings.ReplaceAll(base, "root=UUID=fixture-root", "root=PARTUUID=fixture-part"), GRUBOwned},
		{"udev UUID alias", strings.ReplaceAll(base, "root=UUID=fixture-root", "root=/dev/disk/by-uuid/fixture-root"), GRUBOwned},
		{"canonical device", strings.ReplaceAll(base, "root=UUID=fixture-root", "root=/dev/fixture"), GRUBOwned},
		{"unknown device alias", strings.ReplaceAll(base, "root=UUID=fixture-root", "root=/dev/unknown-alias"), GRUBUnresolved},
		{"missing root", strings.ReplaceAll(base, "root=UUID=fixture-root", ""), GRUBUnresolved},
		{"duplicate root", strings.ReplaceAll(base, "root=UUID=fixture-root", "root=UUID=fixture-root root=UUID=foreign-root"), GRUBUnresolved},
		{"dynamic root", strings.ReplaceAll(base, "root=UUID=fixture-root", "root=$linux_root"), GRUBUnresolved},
		{"missing boot selector", strings.ReplaceAll(base, search, ""), GRUBUnresolved},
		{"search only prints results", strings.ReplaceAll(base, search, "search --fs-uuid fixture-root"), GRUBUnresolved},
		{"foreign boot local root", strings.ReplaceAll(base, search, "search --fs-uuid --set=root foreign-boot"), GRUBUnresolved},
		{"source changes before initrd", strings.ReplaceAll(base, " initrd ", " search --fs-uuid --set=root foreign-boot\n initrd "), GRUBUnresolved},
		{"same conditional outcome", strings.ReplaceAll(base, search, "if [ x$feature_platform_search_hint = xy ]; then\n"+search+"\nelse\n"+search+"\nfi"), GRUBOwned},
		{"different conditional outcomes", strings.ReplaceAll(base, search, "if [ x$feature_platform_search_hint = xy ]; then\n"+search+"\nelse\nsearch --fs-uuid --set=root foreign-boot\nfi"), GRUBUnresolved},
		{"unknown helper", strings.ReplaceAll(base, search, search+"\n custom_root_helper"), GRUBUnresolved},
		{"hidden command separator", strings.ReplaceAll(base, search, search+"\n echo ready; set root=(hd1,gpt1)"), GRUBUnresolved},
		{"unterminated stanza", strings.TrimSuffix(base, "}\n"), GRUBUnresolved},
		{"dynamic sourced configuration", strings.ReplaceAll(base, search, search+"\n source /boot/other.cfg"), GRUBUnresolved},
		{"qualified literal variable", strings.ReplaceAll(base, "/boot/", "($root)/boot/"), GRUBOwned},
		{"non-device variable prefix", strings.ReplaceAll(base, "/boot/", "$root/boot/"), GRUBUnresolved},
		{"self assignment", strings.ReplaceAll(base, search, search+"\n set root=$root"), GRUBOwned},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeRawOwnershipGRUB(t, root, tc.text)
			entries, err := InspectGRUB(fixtureContext(), root)
			if err != nil || len(entries) != 1 {
				t.Fatalf("parse: %+v %v", entries, err)
			}
			if entries[0].Ownership != tc.want {
				t.Fatalf("ownership = %s (%s), want %s", entries[0].Ownership, entries[0].OwnershipReason, tc.want)
			}
			if tc.want == GRUBUnresolved {
				if _, err := ownedABIEntries(root, entries, fixtureFallbackABI); err == nil {
					t.Fatal("unresolved required entry accepted")
				}
			}
		})
	}
}

// TestGRUBOwnershipBtrfs distinguishes installations sharing a filesystem UUID
// through explicit subvolume identity and maps paths through that exact view.
func TestGRUBOwnershipBtrfs(t *testing.T) {
	for _, tc := range []struct {
		name, flags string
		want        GRUBOwnership
	}{
		{"local", "rootflags=subvol=@ubuntu", GRUBOwned},
		{"absolute local", "rootflags=subvol=/@ubuntu", GRUBOwned},
		{"foreign subvolume", "rootflags=subvol=@arch", GRUBForeign},
		{"implicit default", "", GRUBUnresolved},
		{"unsupported numeric subvolume", "rootflags=subvolid=256", GRUBUnresolved},
		{"conflicting selectors", "rootflags=subvol=@ubuntu,subvolid=257", GRUBUnresolved},
		{"unclean subvolume", "rootflags=subvol=@other/../@ubuntu", GRUBUnresolved},
		{"overridden rootflags", "rootflags=subvol=@ubuntu rootflags=rw", GRUBUnresolved},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, _ := fixtureEnvironment(t)
			ctx := bootidentity.WithResolver(context.Background(), func(context.Context, string) (bootidentity.Identity, error) {
				fs := bootidentity.Filesystem{UUID: "fixture-root", FSType: "btrfs", FSRoot: "/@ubuntu", Mountpoint: root}
				return bootidentity.Identity{Root: fs, Boot: fs}, nil
			})
			text := ownershipMenu("Ubuntu", "fixture-root", "root=UUID=fixture-root "+tc.flags, fixtureFallbackABI, "initrd.img-"+fixtureFallbackABI)
			text = strings.ReplaceAll(text, "/boot/", "/@ubuntu/boot/")
			writeRawOwnershipGRUB(t, root, text)
			entries, err := InspectGRUB(ctx, root)
			if err != nil || len(entries) != 1 || entries[0].Ownership != tc.want {
				t.Fatalf("ownership: %+v %v", entries, err)
			}
			if tc.want == GRUBOwned {
				if err := VerifyGRUBEntryABIArtifacts(ctx, root, entries[0], fixtureFallbackABI); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

// TestGRUBOwnershipSeparateBootRejectsGuessedPath ensures a wrong /boot prefix
// on a separate filesystem cannot pass by silently trying another host path.
func TestGRUBOwnershipSeparateBootRejectsGuessedPath(t *testing.T) {
	root, _ := fixtureEnvironment(t)
	text := ownershipMenu("Ubuntu", "fixture-boot", "root=UUID=fixture-root", fixtureFallbackABI, "initrd.img-"+fixtureFallbackABI)
	writeRawOwnershipGRUB(t, root, text)
	entries, err := InspectGRUB(fixtureSeparateBootContext(), root)
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries: %v %v", entries, err)
	}
	if err := VerifyGRUBEntryABIArtifacts(fixtureSeparateBootContext(), root, entries[0], fixtureFallbackABI); err == nil {
		t.Fatal("guessed /boot path accepted")
	}
}

// TestGRUBOwnershipPrivacy keeps selectors private while exporting scope.
func TestGRUBOwnershipPrivacy(t *testing.T) {
	root, _ := fixtureEnvironment(t)
	entries, err := InspectGRUB(fixtureContext(), root)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"fixture-root", "fixture-part", "/dev/fixture", "root=UUID"} {
		if strings.Contains(string(data), private) {
			t.Fatalf("private selector exposed: %s", private)
		}
	}
	if !strings.Contains(string(data), "proven-owned") {
		t.Fatal("scope missing from JSON")
	}
}

// TestSharedBootWrites guards physical references even if an ABI is never
// deleted and even when its package records exist only in another root.
func TestSharedBootWrites(t *testing.T) {
	for _, tc := range []struct {
		name, bootUUID, abi string
		wantConflict        bool
	}{
		{"same ABI separate boot", "foreign-boot", fixtureTargetABI, false},
		{"same ABI shared boot", "fixture-root", fixtureTargetABI, true},
		{"retained fallback shared boot", "fixture-root", fixtureFallbackABI, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, bundle := fixtureEnvironment(t)
			foreign := ownershipMenu("Other distro", tc.bootUUID, "root=UUID=foreign-root", tc.abi, "initramfs-"+tc.abi+".img")
			writeRawOwnershipGRUB(t, root, fixtureOwnedGRUB(fixtureGRUB(false))+foreign)
			runner := &fakeRunner{root: root}
			_, err := fixtureManager(runner).Preflight(fixtureContext(), fixtureRequest(root, bundle, true))
			if errors.Is(err, errSharedBootWrite) != tc.wantConflict || (!tc.wantConflict && err != nil) {
				t.Fatalf("conflict = %v, want %t", err, tc.wantConflict)
			}
			if len(runner.runs) != 0 {
				t.Fatal("preflight mutated files")
			}
		})
	}
}

// TestInstallRegenerationDiscoversForeignSameABI exercises the original
// preflight-success/post-hook-failure boundary, plus recovery from a real error.
func TestInstallRegenerationDiscoversForeignSameABI(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "rollback"}[fail], func(t *testing.T) {
			root, bundle := fixtureEnvironment(t)
			foreign := ownershipMenu("Arch Linux ARM", "foreign-boot", "root=UUID=foreign-root", fixtureFallbackABI, "initramfs-"+fixtureFallbackABI+".img")
			labelled := "'Ubuntu " + fixtureFallbackABI + "'"
			recovery := strings.Replace(fixtureGRUB(false), labelled, "'Ubuntu "+fixtureFallbackABI+" (recovery mode)'", 1)
			shortcut := strings.Replace(fixtureGRUB(false), labelled, "'Ubuntu'", 1)
			writeRawOwnershipGRUB(t, root, fixtureOwnedGRUB(shortcut+fixtureGRUB(false)+recovery))
			original, err := os.ReadFile(filepath.Join(root, "boot/grub/grub.cfg"))
			if err != nil {
				t.Fatal(err)
			}
			runner := &fakeRunner{root: root}
			runner.runHook = func(_ context.Context, command platform.Command) error {
				switch {
				case slicesContain(command.Args, "--install"):
					if err := installFixtureTarget(root); err != nil {
						return err
					}
					writeFixtureFile(t, filepath.Join(root, "boot/initrd.img-"+fixtureTargetABI), "target initramfs")
					// Stock GRUB promotes the generic shortcut to the new kernel;
					// the fallback retains its labelled normal and recovery pair.
					promoted := strings.ReplaceAll(shortcut, fixtureFallbackABI, fixtureTargetABI)
					text := fixtureOwnedGRUB(promoted+fixtureGRUB(true)+recovery) + foreign
					if fail {
						text = strings.ReplaceAll(text, " devicetree /boot/dtb-"+fixtureTargetABI+"\n", "")
					}
					writeRawOwnershipGRUB(t, root, text)
				case slicesContain(command.Args, "--purge"):
					return removeFixtureTarget(root)
				}
				return nil
			}
			manager := fixtureManager(runner)
			manager.effectiveUID = func() int { return 0 }
			receipt, err := manager.Install(fixtureContext(), fixtureRequest(root, bundle, false))
			if fail {
				if err == nil || receipt.Rollback == nil || receipt.Rollback.Error != "" || !receipt.Rollback.GRUBRestored {
					t.Fatalf("rollback: %+v %v", receipt.Rollback, err)
				}
				restored, err := os.ReadFile(filepath.Join(root, "boot/grub/grub.cfg"))
				if err != nil || string(restored) != string(original) {
					t.Fatal("original menu not restored")
				}
			} else {
				if err != nil || !receipt.RebootRequired || receipt.Installed == nil {
					t.Fatalf("install failed: %v", err)
				}
				generated, err := os.ReadFile(filepath.Join(root, "boot/grub/grub.cfg"))
				if err != nil || !strings.Contains(string(generated), foreign) {
					t.Fatal("foreign menu entry was removed or changed")
				}
				fallback, err := verifyFallback(fixtureContext(), root, fixtureFallbackABI)
				if err != nil || fallback.DeviceTreeBoot.NormalGRUBEntryCount != 1 || fallback.DeviceTreeBoot.RecoveryGRUBEntryCount != 1 {
					t.Fatalf("fallback normal/recovery pair not preserved: %v", err)
				}
			}
		})
	}
}

// TestFallbackComparisonPreservesEvidence allows one generic shortcut to move
// without relaxing recovery cardinality or any safety-critical byte comparison.
func TestFallbackComparisonPreservesEvidence(t *testing.T) {
	root, _ := fixtureEnvironment(t)
	before, err := verifyFallback(fixtureContext(), root, fixtureFallbackABI)
	if err != nil {
		t.Fatal(err)
	}
	before.DeviceTreeBoot.GRUBEntryCount = 3
	before.DeviceTreeBoot.NormalGRUBEntryCount = 2
	before.DeviceTreeBoot.RecoveryGRUBEntryCount = 1
	if err := fallbackUnchanged(before, before); err != nil {
		t.Fatalf("unchanged evidence rejected: %v", err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*BootEvidence)
		valid  bool
	}{
		{"shortcut moved", func(*BootEvidence) {}, true},
		{"recovery lost", func(e *BootEvidence) {
			e.DeviceTreeBoot.RecoveryGRUBEntryCount = 0
			e.DeviceTreeBoot.GRUBEntryCount = 1
		}, false},
		{"recovery added", func(e *BootEvidence) {
			e.DeviceTreeBoot.RecoveryGRUBEntryCount = 2
			e.DeviceTreeBoot.GRUBEntryCount = 3
		}, false},
		{"extra aliases", func(e *BootEvidence) { e.DeviceTreeBoot.NormalGRUBEntryCount = 3; e.DeviceTreeBoot.GRUBEntryCount = 4 }, false},
		{"inconsistent total", func(e *BootEvidence) { e.DeviceTreeBoot.GRUBEntryCount = 3 }, false},
		{"all normal bindings lost", func(e *BootEvidence) { e.DeviceTreeBoot.NormalGRUBEntryCount = 0; e.DeviceTreeBoot.GRUBEntryCount = 1 }, false},
		{"labelled normal missing", func(e *BootEvidence) { e.GRUBEntryCount = 0 }, false},
		{"duplicate labelled normal", func(e *BootEvidence) { e.GRUBEntryCount = 2 }, false},
		{"kernel changed", func(e *BootEvidence) { e.KernelImage.SHA256 = "changed" }, false},
		{"initramfs changed", func(e *BootEvidence) { e.Initramfs.SHA256 = "changed" }, false},
		{"module index changed", func(e *BootEvidence) { e.ModulesDependencyIndex.SHA256 = "changed" }, false},
		{"DTB changed", func(e *BootEvidence) { e.DeviceTreeBoot.SHA256 = "changed" }, false},
		{"DTB set changed", func(e *BootEvidence) { e.DeviceTreeBoot.SHA256s = []string{"changed"} }, false},
		{"delivery changed", func(e *BootEvidence) { e.DeviceTreeBoot.Mode = DeviceTreeBootEmbedded }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			after := before
			after.DeviceTreeBoot.GRUBEntryCount = 2
			after.DeviceTreeBoot.NormalGRUBEntryCount = 1
			tc.mutate(&after)
			if err := fallbackPreservedAfterInstall(before, after); (err == nil) != tc.valid {
				t.Fatalf("comparison accepted=%t want=%t: %v", err == nil, tc.valid, err)
			}
			if tc.valid {
				if err := fallbackPreservedAfterInstall(after, before); err == nil {
					t.Fatal("unexplained shortcut growth accepted")
				}
				if err := fallbackUnchanged(before, after); err == nil {
					t.Fatal("pre-mutation or rollback comparison accepted changed counts")
				}
			}
		})
	}
}

// TestOwnershipRevalidatedBeforeHash refuses entries retained across a remount.
func TestOwnershipRevalidatedBeforeHash(t *testing.T) {
	root, _ := fixtureEnvironment(t)
	fs := bootidentity.Filesystem{UUID: "fixture-root", FSType: "ext4", FSRoot: "/", Mountpoint: root}
	current := bootidentity.Identity{Root: fs, Boot: fs}
	ctx := bootidentity.WithResolver(context.Background(), func(context.Context, string) (bootidentity.Identity, error) { return current, nil })
	entries, err := InspectGRUB(ctx, root)
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries: %v %v", entries, err)
	}
	current.Root.UUID = "changed-root"
	if _, _, err := InspectGRUBArtifact(ctx, root, entries[0], entries[0].Linux[0]); err == nil || !strings.Contains(err.Error(), "changed since preflight") {
		t.Fatalf("stale entry accepted: %v", err)
	}
}

// TestRollbackRefusesNewForeignSharedReference proves a different validation
// error cannot hide the shared-file hazard before automatic package purge.
func TestRollbackRefusesNewForeignSharedReference(t *testing.T) {
	root, bundle := fixtureEnvironment(t)
	foreign := ownershipMenu("Other distro", "fixture-root", "root=UUID=foreign-root", fixtureTargetABI, "initramfs-"+fixtureTargetABI+".img")
	runner := &fakeRunner{root: root}
	purged := false
	runner.runHook = func(_ context.Context, command platform.Command) error {
		switch {
		case slicesContain(command.Args, "--install"):
			if err := installFixtureTarget(root); err != nil {
				return err
			}
			writeFixtureFile(t, filepath.Join(root, "boot/initrd.img-"+fixtureTargetABI), "target initramfs")
			writeFixtureFile(t, filepath.Join(root, "boot/dtb-"+fixtureTargetABI), "wrong DTB")
			writeRawOwnershipGRUB(t, root, fixtureOwnedGRUB(fixtureGRUB(true))+foreign)
		case slicesContain(command.Args, "--purge"):
			purged = true
		}
		return nil
	}
	manager := fixtureManager(runner)
	manager.effectiveUID = func() int { return 0 }
	receipt, err := manager.Install(fixtureContext(), fixtureRequest(root, bundle, false))
	if err == nil || purged || receipt.Rollback == nil || !strings.Contains(receipt.Rollback.Error, "purge skipped") {
		t.Fatalf("unsafe rollback: purged=%t receipt=%+v error=%v", purged, receipt.Rollback, err)
	}
	if _, err := os.Stat(filepath.Join(root, "boot/vmlinuz-"+fixtureTargetABI)); err != nil {
		t.Fatal("shared kernel was removed")
	}
}

// TestRollbackStopsWhenMountedIdentityChanges ensures recovery never mutates
// a newly substituted root, even after package installation has started.
func TestRollbackStopsWhenMountedIdentityChanges(t *testing.T) {
	root, bundle := fixtureEnvironment(t)
	fs := bootidentity.Filesystem{UUID: "fixture-root", FSType: "ext4", FSRoot: "/", Mountpoint: root}
	current := bootidentity.Identity{Root: fs, Boot: fs}
	ctx := bootidentity.WithResolver(context.Background(), func(context.Context, string) (bootidentity.Identity, error) { return current, nil })
	runner := &fakeRunner{root: root}
	runner.runHook = func(_ context.Context, command platform.Command) error {
		if slicesContain(command.Args, "--install") {
			current.Root.UUID = "replacement-root"
			return errors.New("simulated mount change during package failure")
		}
		t.Fatalf("unexpected recovery mutation: %+v", command)
		return nil
	}
	manager := fixtureManager(runner)
	manager.effectiveUID = func() int { return 0 }
	receipt, err := manager.Install(ctx, fixtureRequest(root, bundle, false))
	if err == nil || receipt.Rollback == nil || len(receipt.Rollback.Commands) != 0 || receipt.Rollback.GRUBRestored || !strings.Contains(receipt.Rollback.Error, "reviewed root") {
		t.Fatalf("mount-change recovery: %+v %v", receipt.Rollback, err)
	}
}
