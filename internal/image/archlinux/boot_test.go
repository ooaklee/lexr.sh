package archlinux

import (
	"strings"
	"testing"
)

// TestLiveBootIdentity rejects values which could escape paths or GRUB syntax.
func TestLiveBootIdentity(t *testing.T) {
	valid := LiveBoot{ABI: "7.2.0-jg-0sp11v23-qcom-x1e", ImageID: "0123456789abcdef0123456789abcdef"}
	for _, mutate := range []func(*LiveBoot){
		func(boot *LiveBoot) { boot.ABI = "../../kernel" },
		func(boot *LiveBoot) { boot.ABI = "kernel\nreboot" },
		func(boot *LiveBoot) { boot.ABI = "kernel;reboot" },
		func(boot *LiveBoot) { boot.ImageID = "" },
		func(boot *LiveBoot) { boot.ImageID = strings.Repeat("a", 33) },
		func(boot *LiveBoot) { boot.ImageID = strings.Repeat("g", 32) },
	} {
		boot := valid
		mutate(&boot)
		if _, err := boot.GRUBConfig(); err == nil {
			t.Errorf("accepted invalid live boot identity: %+v", boot)
		}
		if _, err := boot.BootstrapConfig(); err == nil {
			t.Errorf("bootstrap accepted invalid identity: %+v", boot)
		}
	}
	label, err := valid.Label()
	if err != nil || len(label) > 32 {
		t.Fatalf("invalid ISO label %q: %v", label, err)
	}
	marker, err := valid.Marker()
	if err != nil || !strings.Contains(marker, valid.ImageID) {
		t.Fatalf("marker lost the full image identity: %q, %v", marker, err)
	}
}

// TestLiveBootPairsAllChoices checks every emitted boot entry's media protocol,
// versioned paths and model selection, including diagnostic-only GPU blacklisting.
func TestLiveBootPairsAllChoices(t *testing.T) {
	boot := LiveBoot{ABI: "7.2.0-jg-0sp11v23-qcom-x1e", ImageID: "0123456789abcdef0123456789abcdef"}
	config, err := boot.GRUBConfig()
	if err != nil {
		t.Fatal(err)
	}
	label, _ := boot.Label()
	var kernels, initrds, trees, blacklists int
	for _, line := range strings.Split(config, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "linux":
			kernels++
			if fields[1] != "/arch/aarch64/boot/vmlinuz-"+boot.ABI {
				t.Errorf("kernel is not ABI-bound: %s", line)
			}
			for _, argument := range []string{"arch=aarch64", "archisobasedir=arch", "archisolabel=" + label, "copytoram=n", "checksum=y", "console=tty0"} {
				if !strings.Contains(" "+strings.Join(fields, " ")+" ", " "+argument+" ") {
					t.Errorf("missing live-media argument %q: %s", argument, line)
				}
			}
			if strings.Contains(line, "module_blacklist=msm") {
				blacklists++
				if !strings.Contains(line, "systemd.unit=multi-user.target") {
					t.Error("GPU was disabled outside text diagnostics")
				}
			}
		case "devicetree":
			trees++
			if !strings.HasPrefix(fields[1], "/arch/aarch64/boot/dtb-"+boot.ABI+"/") {
				t.Errorf("DTB is not ABI-bound: %s", line)
			}
		case "initrd":
			initrds++
			if fields[1] != "/arch/aarch64/boot/initramfs-"+boot.ABI+".img" {
				t.Errorf("initrd is not ABI-bound: %s", line)
			}
		}
	}
	if kernels != 4 || initrds != kernels || trees != kernels || blacklists != 1 {
		t.Fatalf("unpaired boot entries or altered graphics policy: kernels=%d initrds=%d trees=%d blacklists=%d", kernels, initrds, trees, blacklists)
	}
	for _, forbidden := range []string{"boot=casper", "rd.live.image", "break=", "qcom_q6v5_pas"} {
		if strings.Contains(config, forbidden) {
			t.Errorf("unrelated or obsolete live argument: %s", forbidden)
		}
	}
}

// TestInstalledBootIsIndependent rejects unsafe targets and verifies installed
// boot never searches for USB media or assumes a generic kernel is a fallback.
func TestInstalledBootIsIndependent(t *testing.T) {
	boot := InstalledBoot{ABI: "7.2.0-jg-0sp11v23-qcom-x1e", RootUUID: "01234567-89ab-cdef-0123-456789abcdef", Device: "surface-pro-11-x1e-oled"}
	config, err := boot.GRUBConfig()
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"archisolabel", "archisobasedir", "copytoram", "checksum=y", "vmlinuz-linux", "/EFI/BOOT", "boot=casper"} {
		if strings.Contains(config, forbidden) {
			t.Errorf("installed boot retained %q", forbidden)
		}
	}
	if strings.Count(config, "root=UUID="+boot.RootUUID) != 2 || !strings.Contains(config, "x1e80100-microsoft-denali-oled.dtb") {
		t.Fatal("installed entries lost their root identity or model")
	}
	boot.Device = "surface-pro-11-x1p-lcd"
	config, err = boot.GRUBConfig()
	if err != nil || !strings.Contains(config, "x1p64100-microsoft-denali.dtb") || strings.Contains(config, "oled.dtb") {
		t.Fatalf("LCD model has an incorrect DTB: %v", err)
	}
	for _, mutate := range []func(*InstalledBoot){
		func(boot *InstalledBoot) { boot.Device = "auto" },
		func(boot *InstalledBoot) { boot.RootUUID = "$(reboot)" },
		func(boot *InstalledBoot) { boot.RootUUID = "" },
		func(boot *InstalledBoot) { boot.ABI = "../other" },
	} {
		invalid := boot
		mutate(&invalid)
		if _, err := invalid.GRUBConfig(); err == nil {
			t.Errorf("accepted unsafe installed boot selection: %+v", invalid)
		}
	}
}
