package install

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/ooaklee/lexr.sh/internal/bootidentity"
)

// errSharedBootWrite protects physical files, not globally unique ABI names.
var errSharedBootWrite = errors.New("another or unresolved installation references boot files this transaction would change; separate the owning boot paths before installing")

// inspectBootWriteScope checks predictable conflicts in the existing menu.
// It never mounts a foreign root, updates another distro, or executes os-prober.
// Final inspection is still required after package-driven menu regeneration.
func inspectBootWriteScope(ctx context.Context, root, abi string, fallback *FallbackBindingPlan) (bool, error) {
	identity, err := bootidentity.Resolve(ctx, root)
	if err != nil {
		return false, err
	}
	entries, err := InspectGRUB(ctx, root)
	if err != nil {
		return false, err
	}
	paths := map[string]bool{}
	for _, name := range []string{"vmlinuz-", "initrd.img-", "System.map-", "config-", "dtb-"} {
		paths["boot/"+name+abi] = true
	}
	if fallback != nil && fallback.Create {
		relative, err := filepath.Rel(root, fallback.Destination)
		if err != nil {
			return false, err
		}
		paths[filepath.ToSlash(relative)] = true
	}
	multiboot := false
	for _, entry := range entries {
		if entry.Ownership == GRUBOwned {
			continue
		}
		multiboot = true
		for _, tokens := range [][]GRUBPathToken{entry.Linux, entry.Initrd, entry.DeviceTrees} {
			for _, token := range tokens {
				relative, mapped := mapBootToken(root, identity.Boot, token)
				if mapped && (paths[relative] || strings.HasPrefix(relative, "boot/dtbs/"+abi+"/")) {
					return true, errSharedBootWrite
				}
			}
		}
	}
	// A generator may discover foreign entries only after package mutation.
	// Surface that limitation in the plan without disabling discovery.
	prober := filepath.Join(root, "etc/grub.d/30_os-prober")
	if info, err := os.Lstat(prober); err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
		multiboot = true
	}
	return multiboot, nil
}
