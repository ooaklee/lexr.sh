package debianlive

import (
	"os"
	"os/exec"
	"testing"
)

// TestUnpackInitramfsLayoutsIntegration uses real CPIO and unmkinitramfs tools
// to verify flat archives, ordered overlays and rejection of ambiguous roots.
// All generated files remain inside the disposable container.
func TestUnpackInitramfsLayoutsIntegration(t *testing.T) {
	if os.Getenv("LEXR_DOCKER_INTEGRATION") != "1" {
		t.Skip("set LEXR_DOCKER_INTEGRATION=1 for archive-layout integration")
	}
	image := os.Getenv("LEXR_TEST_TOOLS_IMAGE")
	if image == "" {
		t.Skip("set LEXR_TEST_TOOLS_IMAGE to the prepared Lexr tools image")
	}
	script := unpackInitramfsScript + `set -o pipefail
mkdir -p /tmp/root/usr/bin /tmp/early
printf '#!/bin/sh\n' > /tmp/root/init
printf 'live-main' > /tmp/root/usr/bin/live-boot
printf 'early-layer' > /tmp/early/sentinel
(cd /tmp/root && find . -print0 | cpio --null -o --format=newc | gzip -n) > /tmp/flat.img
unpack_image /tmp/flat.img /tmp/flat
test "$(cat /tmp/flat/main/usr/bin/live-boot)" = live-main
(cd /tmp/early && find . -print0 | cpio --null -o --format=newc) > /tmp/early.cpio
cat /tmp/early.cpio /tmp/flat.img > /tmp/multiple.img
unpack_image /tmp/multiple.img /tmp/multiple
test "$(cat /tmp/multiple/early/sentinel)" = early-layer
test "$(cat /tmp/multiple/main/usr/bin/live-boot)" = live-main
mkdir /tmp/root/main
(cd /tmp/root && find . -print0 | cpio --null -o --format=newc | gzip -n) > /tmp/ambiguous.img
# A subprocess with errexit must stop rather than silently nest an existing main.
export -f unpack_image
if bash -ceu 'unpack_image /tmp/ambiguous.img /tmp/ambiguous'; then
 echo 'ambiguous single-archive root was accepted' >&2
 exit 1
fi
`
	command := exec.CommandContext(t.Context(), "docker", "run", "--rm", "--network", "none", image, "bash", "-ceu", script)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("archive layouts: %v\n%s", err, output)
	}
}
