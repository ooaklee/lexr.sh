package install

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/platform"
)

// wifiTestTLV creates synthetic API-2 records without including firmware bytes.
func wifiTestTLV(kind uint32, payload []byte) []byte {
	data := make([]byte, 8+(len(payload)+3)&^3)
	binary.LittleEndian.PutUint32(data, kind)
	binary.LittleEndian.PutUint32(data[4:], uint32(len(payload)))
	copy(data[8:], payload)
	return data
}

// TestWiFiSurfaceGateUsesAnExactOLEDToken excludes the unqualified X1P and
// other Snapdragon machines regardless of compatible-list ordering.
func TestWiFiSurfaceGateUsesAnExactOLEDToken(t *testing.T) {
	for _, test := range []struct {
		data string
		want bool
	}{
		{"microsoft,denali-oled\x00microsoft,denali\x00", true},
		{"qcom,x1e80100\x00microsoft,denali-oled\x00", true},
		{"microsoft,denali-x1p\x00microsoft,denali\x00", false},
		{"microsoft,denali\x00", false},
		{"not-microsoft,denali-oled\x00", false},
		{"microsoft,denali-oled", false},
	} {
		if got := isWiFiSurfaceX1EOLED([]byte(test.data)); got != test.want {
			t.Fatalf("gate(%q)=%v", test.data, got)
		}
	}
}

// prepareWiFiActivationSysfs constructs either recognised driver generation.
// The shared-core ownership link mirrors the split driver's actual sysfs state.
func prepareWiFiActivationSysfs(t *testing.T, driver, module string) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"firmware/devicetree/base/compatible": "microsoft,denali-oled\x00microsoft,denali\x00",
		"module/" + module + "/initstate":     "live\n",
		"module/" + module + "/refcnt":        "0\n",
	}
	for name, data := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeWiFiActivationPCI(t, root, "0004:01:00.0", "0x1107", driver)
	if err := os.MkdirAll(filepath.Join(root, "bus/pci/devices/0004:01:00.0/ieee80211/phy0"), 0o755); err != nil {
		t.Fatal(err)
	}
	driverPath := filepath.Join(root, "bus/pci/drivers", driver)
	if err := os.MkdirAll(driverPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "module/ath12k"), filepath.Join(driverPath, "module")); err != nil {
		t.Fatal(err)
	}
	return root
}

// writeWiFiActivationPCI adds a synthetic Qualcomm PCI function and binding.
func writeWiFiActivationPCI(t *testing.T, root, address, product, driver string) {
	t.Helper()
	base := filepath.Join(root, "bus/pci/devices", address)
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{"vendor": "0x17cb\n", "device": product + "\n"} {
		if err := os.WriteFile(filepath.Join(base, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join("../../drivers", driver), filepath.Join(base, "driver")); err != nil {
		t.Fatal(err)
	}
}

// TestWiFiActivationRecognisesDriverGenerations admits the legacy layout but
// rejects the split driver whose reload caused an MHI/QMI kernel Oops on SP11.
func TestWiFiActivationRecognisesDriverGenerations(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ driver, module string }{
		{"ath12k_pci", "ath12k"},
		{"ath12k_wifi7_pci", "ath12k_wifi7"},
	} {
		t.Run(test.driver, func(t *testing.T) {
			root := prepareWiFiActivationSysfs(t, test.driver, test.module)
			commands, err := planWiFiActivationFromSysfs(root)
			if test.driver == "ath12k_wifi7_pci" {
				if err == nil || !strings.Contains(err.Error(), "crashed the kernel") || len(commands) != 0 {
					t.Fatalf("unsafe split driver reload accepted: %v %v", commands, err)
				}
				return
			}
			want := []Command{{Name: "/usr/sbin/modprobe", Args: []string{"-r", test.module}}, {Name: "/usr/sbin/modprobe", Args: []string{test.module}}}
			if err != nil || !reflect.DeepEqual(commands, want) {
				t.Fatalf("commands=%v error=%v", commands, err)
			}
		})
	}
}

// TestWiFiActivationRejectsUnsupportedRuntimeState keeps version-independent
// selection confined to one recognised, loaded radio on the qualified model.
func TestWiFiActivationRejectsUnsupportedRuntimeState(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"unknown-driver", "unbound", "built-in", "module-loading", "wrong-model", "multiple-radios", "shared-driver", "negative-refcount", "failed-probe"} {
		t.Run(name, func(t *testing.T) {
			driver := "ath12k_pci"
			if name == "unknown-driver" {
				driver = "unknown_wifi"
			}
			root := prepareWiFiActivationSysfs(t, driver, "ath12k")
			var err error
			switch name {
			case "unbound":
				err = os.Remove(filepath.Join(root, "bus/pci/devices/0004:01:00.0/driver"))
			case "built-in":
				err = os.Remove(filepath.Join(root, "module/ath12k/initstate"))
			case "module-loading":
				err = os.WriteFile(filepath.Join(root, "module/ath12k/initstate"), []byte("coming\n"), 0o644)
			case "negative-refcount":
				err = os.WriteFile(filepath.Join(root, "module/ath12k/refcnt"), []byte("-1\n"), 0o644)
			case "failed-probe":
				err = os.Remove(filepath.Join(root, "bus/pci/devices/0004:01:00.0/ieee80211/phy0"))
			case "wrong-model":
				err = os.WriteFile(filepath.Join(root, "firmware/devicetree/base/compatible"), []byte("microsoft,denali\x00"), 0o644)
			case "multiple-radios":
				writeWiFiActivationPCI(t, root, "0005:01:00.0", "0x1107", driver)
			case "shared-driver":
				writeWiFiActivationPCI(t, root, "0005:01:00.0", "0x1109", driver)
			}
			if err != nil {
				t.Fatal(err)
			}
			if commands, err := planWiFiActivationFromSysfs(root); err == nil || len(commands) != 0 {
				t.Fatalf("unsupported state accepted: %v %v", commands, err)
			}
		})
	}
	if commands, err := planSurfaceWiFiActivation(t.TempDir()); err == nil || len(commands) != 0 {
		t.Fatal("mounted target accepted for activation")
	}
}

// TestWiFiActivationRejectsAChangedPlan retains installed files while refusing
// stale restart commands if a driver disappears or changes after inspection.
func TestWiFiActivationRejectsAChangedPlan(t *testing.T) {
	for _, disappears := range []bool{false, true} {
		root := prepareWiFiTestRoot(t, wifiTestDatabase(wifiFallbackBoard))
		runner := &fakeRunner{}
		installer := New(runner)
		installer.euid = func() int { return 0 }
		calls := 0
		installer.planWiFiActivation = func(string) ([]Command, error) {
			calls++
			module := "ath12k"
			if calls > 1 {
				if disappears {
					return nil, errors.New("radio disappeared")
				}
				module = "ath12k_wifi7"
			}
			return []Command{{Name: "/usr/sbin/modprobe", Args: []string{"-r", module}}, {Name: "/usr/sbin/modprobe", Args: []string{module}}}, nil
		}
		result, err := installer.WiFi(context.Background(), Options{Root: root, Activate: true})
		if err == nil || !result.FilesInstalled || result.ActivationComplete || result.ActivationError == "" || !result.RebootRequired || len(runner.commands) != 0 {
			t.Fatalf("stale activation: result=%+v commands=%v error=%v", result, runner.commands, err)
		}
	}
}

// wifiTestDatabase constructs an aligned database for structural regression tests.
func wifiTestDatabase(names ...string) []byte {
	data := make([]byte, 20)
	copy(data, "QCA-ATH12K-BOARD\x00")
	for _, name := range names {
		record := wifiTestTLV(0, []byte(name))
		record = append(record, wifiTestTLV(1, []byte("synthetic-board-data"))...)
		data = append(data, wifiTestTLV(0, record)...)
	}
	return data
}

// TestSelectWiFiBoard checks exact selectors, native preference and malformed
// nested lengths rather than accepting the first plausible board entry.
func TestSelectWiFiBoard(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		data     []byte
		selected string
	}{
		{"fallback", wifiTestDatabase(wifiFallbackBoard), wifiFallbackBoard},
		{"native-first", wifiTestDatabase(wifiSurfaceBoard, wifiFallbackBoard), wifiSurfaceBoard},
		{"native-last", wifiTestDatabase(wifiFallbackBoard, wifiSurfaceBoard), wifiSurfaceBoard},
		{"wrong-subsystem", wifiTestDatabase(strings.Replace(wifiFallbackBoard, "3378", "3379", 1)), ""},
		{"duplicates", wifiTestDatabase(wifiFallbackBoard, wifiFallbackBoard), ""},
		{"truncated", wifiTestDatabase(wifiFallbackBoard)[:30], ""},
		{"empty", nil, ""},
		{"bad-magic", append([]byte("BAD"), wifiTestDatabase(wifiFallbackBoard)[3:]...), ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			selection, err := selectWiFiBoard(test.data)
			if test.selected == "" {
				if err == nil {
					t.Fatal("malformed or unsupported database accepted")
				}
				return
			}
			if err != nil || selection.name != test.selected || !bytes.Equal(selection.data, []byte("synthetic-board-data")) {
				t.Fatalf("selection=%v error=%v", selection, err)
			}
		})
	}
}

// FuzzSelectWiFiBoard exercises hostile nested lengths and aligned boundaries.
func FuzzSelectWiFiBoard(f *testing.F) {
	f.Add(wifiTestDatabase(wifiFallbackBoard))
	f.Add([]byte("QCA-ATH12K-BOARD\x00"))
	f.Fuzz(func(t *testing.T, data []byte) {
		selection, err := selectWiFiBoard(data)
		if err == nil && (len(selection.data) == 0 || len(selection.data) > maximumWiFiBoardBytes || selection.name != wifiSurfaceBoard && selection.name != wifiFallbackBoard) {
			t.Fatal("parser returned an invalid selection")
		}
	})
}

// prepareWiFiTestRoot supplies only synthetic distribution input to a target.
func prepareWiFiTestRoot(t *testing.T, database []byte) string {
	t.Helper()
	root := t.TempDir()
	directory := filepath.Join(root, wifiFirmwareDirectory)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "board-2.bin"), database, 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestWiFiInstallationIsOfflineRecoverableAndIdempotent verifies a real derived
// file transaction, its dry run, backup, receipt and unchanged distribution input.
func TestWiFiInstallationIsOfflineRecoverableAndIdempotent(t *testing.T) {
	database := wifiTestDatabase(wifiFallbackBoard)
	root := prepareWiFiTestRoot(t, database)
	target := filepath.Join(root, wifiFirmwareDirectory, "board.bin")
	if err := os.WriteFile(target, []byte("old-board"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{}
	installer := New(runner)
	installer.euid = func() int { return 501 }
	preview, err := installer.WiFi(context.Background(), Options{Root: root, DryRun: true})
	if err != nil || len(preview.Files) != 1 || preview.Files[0].Action != "replace" {
		t.Fatalf("preview=%+v error=%v", preview, err)
	}
	if _, err := os.Stat(preview.BackupDirectory); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("dry run wrote backup state")
	}
	if _, err := installer.WiFi(context.Background(), Options{Root: root}); err == nil {
		t.Fatal("unprivileged write accepted")
	}
	installer.euid = func() int { return 0 }
	result, err := installer.WiFi(context.Background(), Options{Root: root})
	if err != nil || !result.FilesInstalled || !result.RebootRequired {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	actual, _ := os.ReadFile(target)
	if string(actual) != "synthetic-board-data" {
		t.Fatalf("incorrect derived bytes %q", actual)
	}
	backup, _ := os.ReadFile(result.Files[0].Backup)
	if string(backup) != "old-board" {
		t.Fatal("original not backed up")
	}
	receipt, _ := os.ReadFile(result.Receipt)
	if !bytes.Contains(receipt, []byte("board_sha256")) || !bytes.Contains(receipt, []byte("\"files_installed\": true")) {
		t.Fatal("durable receipt missing provenance/state")
	}
	original, _ := os.ReadFile(filepath.Join(root, wifiFirmwareDirectory, "board-2.bin"))
	if !bytes.Equal(original, database) {
		t.Fatal("distribution database changed")
	}
	result, err = installer.WiFi(context.Background(), Options{Root: root})
	if err != nil || result.Receipt != "" || result.Files[0].Action != "retain" {
		t.Fatalf("repeat was not idempotent: %+v %v", result, err)
	}
	if len(runner.commands) != 0 {
		t.Fatal("offline installation executed a command")
	}
}

// TestWiFiRejectsUnsafeTargetsAndPreservesNativeBoards checks the no-replacement
// boundary and retirement behaviour when upstream gains the exact selector.
func TestWiFiRejectsUnsafeTargetsAndPreservesNativeBoards(t *testing.T) {
	root := prepareWiFiTestRoot(t, wifiTestDatabase(wifiFallbackBoard))
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("untouched"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, wifiFirmwareDirectory, "board.bin")); err != nil {
		t.Fatal(err)
	}
	installer := New(&fakeRunner{})
	installer.euid = func() int { return 0 }
	if _, err := installer.WiFi(context.Background(), Options{Root: root}); err == nil {
		t.Fatal("symlinked target accepted")
	}
	if data, _ := os.ReadFile(outside); string(data) != "untouched" {
		t.Fatal("outside target changed")
	}
	native := prepareWiFiTestRoot(t, wifiTestDatabase(wifiSurfaceBoard, wifiFallbackBoard))
	result, err := installer.WiFi(context.Background(), Options{Root: native})
	if err != nil || result.Files[0].Action != "retain-native-board" {
		t.Fatalf("native result=%+v error=%v", result, err)
	}
	if _, err := os.Stat(filepath.Join(native, wifiFirmwareDirectory, "board.bin")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("native board did not suppress fallback creation")
	}
}

// TestWiFiCompressedInputAndActivationFailure checks bounded host decompression
// and reports an activation failure without hiding successful file publication.
func TestWiFiCompressedInputAndActivationFailure(t *testing.T) {
	root := prepareWiFiTestRoot(t, []byte("compressed-fixture"))
	path := filepath.Join(root, wifiFirmwareDirectory, "board-2.bin")
	if err := os.Rename(path, path+".zst"); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{inspect: func(command platform.Command) error {
		if command.Name == "/usr/bin/zstd" {
			_, err := command.Stdout.Write(wifiTestDatabase(wifiFallbackBoard))
			return err
		}
		return errors.New("simulated radio restart failure")
	}}
	installer := New(runner)
	installer.euid = func() int { return 0 }
	installer.planWiFiActivation = func(string) ([]Command, error) {
		return []Command{{Name: "/usr/sbin/modprobe", Args: []string{"-r", "ath12k_wifi7"}}, {Name: "/usr/sbin/modprobe", Args: []string{"ath12k_wifi7"}}}, nil
	}
	result, err := installer.WiFi(context.Background(), Options{Root: root, Activate: true})
	if err == nil || !result.FilesInstalled || result.ActivationComplete || result.ActivationError == "" {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	if len(runner.commands) != 2 || !reflect.DeepEqual(runner.commands[1].Args, []string{"-r", "ath12k_wifi7"}) {
		t.Fatalf("commands=%v", runner.commands)
	}
	for _, command := range runner.commands {
		if strings.Contains(strings.Join(command.Args, " "), "qcom_q6v5_pas") || strings.Contains(command.Name, "systemctl") {
			t.Fatal("unrelated service or DSP restart")
		}
	}
	var output wifiLimitedOutput
	if _, err := output.Write(make([]byte, maximumWiFiDatabaseBytes+1)); err == nil {
		t.Fatal("oversized decompression accepted")
	}
}
