package bootdoctor

import (
	"context"
	"strings"

	"github.com/ooaklee/lexr.sh/internal/bootidentity"
)

// doctorContext supplies synthetic mounted identity, explicitly and only for
// portable fixtures; unmounted production roots cannot qualify boot evidence.
func doctorContext() context.Context {
	return bootidentity.WithResolver(context.Background(), func(_ context.Context, root string) (bootidentity.Identity, error) {
		fs := bootidentity.Filesystem{UUID: "fixture-root", PartUUID: "fixture-part", Device: "/dev/fixture", FSType: "ext4", FSRoot: "/", Mountpoint: root}
		return bootidentity.Identity{Root: fs, Boot: fs}, nil
	})
}

// doctorOwnedGRUB migrates historical rootless artefact fixtures. Ownership
// regression tests write raw configuration instead of using this helper.
func doctorOwnedGRUB(content string) string {
	lines := strings.Split(content, "\n")
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "linux ") || strings.HasPrefix(trimmed, "linuxefi ") {
			line = strings.ReplaceAll(line, "root=UUID=private", "root=UUID=fixture-root")
			if !strings.Contains(line, "root=") {
				line += " root=UUID=fixture-root"
			}
		}
		result = append(result, line)
		if strings.HasPrefix(trimmed, "menuentry ") {
			result = append(result, " search --fs-uuid --set=root fixture-root")
		}
	}
	return strings.Join(result, "\n")
}
