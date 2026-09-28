package install

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/ooaklee/lexr.sh/internal/bootidentity"
)

// fixtureContext explicitly supplies mounted identity for portable temporary
// roots. Production never infers identity from a rootless fixture or manifest.
func fixtureContext() context.Context {
	return bootidentity.WithResolver(context.Background(), func(_ context.Context, root string) (bootidentity.Identity, error) {
		fs := bootidentity.Filesystem{UUID: "fixture-root", PartUUID: "fixture-part", Device: "/dev/fixture", FSType: "ext4", FSRoot: "/", Mountpoint: root}
		return bootidentity.Identity{Root: fs, Boot: fs}, nil
	})
}

// fixtureSeparateBootContext represents a distinct mounted boot filesystem.
func fixtureSeparateBootContext() context.Context {
	return bootidentity.WithResolver(context.Background(), func(_ context.Context, root string) (bootidentity.Identity, error) {
		fs := bootidentity.Filesystem{UUID: "fixture-root", FSType: "ext4", FSRoot: "/", Mountpoint: root}
		boot := bootidentity.Filesystem{UUID: "fixture-boot", FSType: "ext4", FSRoot: "/", Mountpoint: filepath.Join(root, "boot")}
		return bootidentity.Identity{Root: fs, Boot: boot}, nil
	})
}

// fixtureOwnedGRUB adds explicit ownership to historical artefact-only fixtures.
// New ownership tests write raw configuration and must not use this migration.
func fixtureOwnedGRUB(content string) string {
	lines := strings.Split(content, "\n")
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "linux ") || strings.HasPrefix(trimmed, "linuxefi ") {
			line = strings.ReplaceAll(line, "root=fixture", "root=UUID=fixture-root")
			if !strings.Contains(line, "root=") {
				line += " root=UUID=fixture-root"
			}
		}
		line = strings.ReplaceAll(line, "devicetree /dtb-", "devicetree /boot/dtb-")
		line = strings.ReplaceAll(line, "devicetree /sp11-denali.dtb", "devicetree /boot/sp11-denali.dtb")
		result = append(result, line)
		if strings.HasPrefix(trimmed, "menuentry ") {
			result = append(result, " search --fs-uuid --set=root fixture-root")
		}
	}
	return strings.Join(result, "\n")
}
