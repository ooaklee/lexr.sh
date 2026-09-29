package cli

import (
	"github.com/ooaklee/lexr.sh/internal/userspace/compatibility"
	"github.com/spf13/cobra"
)

// payloadCompatibilityFlags binds explicit payload evidence for build/release
// commands without inspecting or inheriting the invoking host's identity.
func payloadCompatibilityFlags(command *cobra.Command, target *compatibility.Target, allow *bool, kernel bool) {
	command.Flags().StringVar(&target.Architecture, "target-architecture", "", "declared payload architecture")
	command.Flags().StringVar(&target.DeviceProfile, "target-device-profile", "", "canonical payload device profile ID from lexr profile list")
	command.Flags().StringVar(&target.OSID, "target-os", "", "exact payload os-release ID")
	command.Flags().StringVar(&target.OSVersion, "target-os-version", "", "exact payload os-release VERSION_ID")
	if kernel {
		command.Flags().StringVar(&target.KernelABI, "target-kernel", "", "declared payload kernel ABI")
	}
	command.Flags().BoolVar(allow, "allow-unverified-compatibility", false, "accept unverified compatibility evidence; hard incompatibility and unavailable identity still block")
}
