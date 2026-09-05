package ubuntu

import (
	"strings"
	"testing"
)

// TestLiveKernelArgumentsAreIndependentOfFirmware proves each Surface entry
// carries the required options even when SMBIOS or GRUB variables are absent.
func TestLiveKernelArgumentsAreIndependentOfFirmware(t *testing.T) {
	config := grubConfig(installedTestABI)
	if err := validateLiveKernelArguments(config); err != nil {
		t.Fatal(err)
	}
	for _, dependency := range []string{"smbios", "$cmdline", "$proc_version"} {
		if strings.Contains(config, dependency) {
			t.Fatalf("Surface menu still depends on %q", dependency)
		}
	}
	if !strings.Contains(config, "if [ $lockdown != \"y\" ]; then\n    cutmem") {
		t.Fatal("Surface memory workaround lost its lockdown guard")
	}
}

// TestLiveKernelArgumentsRejectMissingOptions checks every required argument
// on both desktop and diagnostic entries, including misleading global values.
func TestLiveKernelArgumentsRejectMissingOptions(t *testing.T) {
	for _, argument := range strings.Fields(liveKernelArguments) {
		for _, diagnostics := range []bool{false, true} {
			t.Run(argument+"/diagnostics="+map[bool]string{false: "false", true: "true"}[diagnostics], func(t *testing.T) {
				config := grubConfig(installedTestABI)
				lines := strings.Split(config, "\n")
				for i, line := range lines {
					if strings.HasPrefix(strings.TrimSpace(line), "linux ") &&
						strings.Contains(line, "systemd.unit=multi-user.target") == diagnostics {
						lines[i] = strings.Replace(line, " "+argument+" ", " ", 1)
						break
					}
				}
				config = "# " + argument + "\nset cmdline=\"" + argument + "\"\n" + strings.Join(lines, "\n")
				if err := validateLiveKernelArguments(config); err == nil || !strings.Contains(err.Error(), argument) {
					t.Fatalf("missing %s error = %v", argument, err)
				}
			})
		}
	}
}

// TestLiveKernelArgumentsRejectNonLiteralMenus prevents an empty menu or the
// former variable-based kernel commands from satisfying the runtime contract.
func TestLiveKernelArgumentsRejectNonLiteralMenus(t *testing.T) {
	for _, config := range []string{
		"# linux /casper/vmlinuz " + liveKernelArguments,
		"set cmdline=\"" + liveKernelArguments + "\"\nlinux /casper/vmlinuz $cmdline",
		"linux /other/vmlinuz " + liveKernelArguments,
		"linux /casper/vmlinuz " + strings.Replace(liveKernelArguments, "clk_ignore_unused", "clk_ignore_unused=0", 1),
	} {
		if err := validateLiveKernelArguments(config); err == nil {
			t.Fatalf("invalid live kernel menu was accepted: %s", config)
		}
	}
}

// TestLiveKernelArgumentsRejectDSPBlacklists reproduces the boot-blocking
// candidate menu, including comma lists and equivalent module-name spelling.
func TestLiveKernelArgumentsRejectDSPBlacklists(t *testing.T) {
	for _, argument := range []string{
		"modprobe.blacklist=qcom_q6v5_pas",
		"modprobe.blacklist=other,qcom-q6v5-pas",
		"module_blacklist=qcom_q6v5_pas,other",
		"blacklist=qcom_q6v5_pas",
		"rd.driver.blacklist=qcom_q6v5_pas",
	} {
		config := strings.Replace(grubConfig(installedTestABI), " --- ", " "+argument+" --- ", 1)
		if err := validateLiveKernelArguments(config); err == nil || !strings.Contains(err.Error(), "DSP") {
			t.Fatalf("blocking argument %q error = %v", argument, err)
		}
	}
	config := grubConfig(installedTestABI)
	if strings.Contains(config, "blacklist") {
		t.Fatal("generated live menu retains a driver blacklist")
	}
	config = strings.Replace(config, " --- ", " modprobe.blacklist=unrelated --- ", 1)
	if err := validateLiveKernelArguments(config); err != nil {
		t.Fatalf("unrelated blacklist rejected: %v", err)
	}
}
