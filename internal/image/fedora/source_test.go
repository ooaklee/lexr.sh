package fedora

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFedoraSourceLayoutFixtures covers observed and unsupported source layout variants.
func TestFedoraSourceLayoutFixtures(t *testing.T) {
	read := func(name string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join("testdata", "fedora-44", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	pvd, stub, grub := read("pvd.txt"), read("esp-grub.cfg"), read("grub.cfg")
	for _, variant := range []struct{ name, pvd, stub, grub, marker string }{
		{"publisher", pvd, stub, grub, "/boot/0x503d6c7e"},
		{"new marker", pvd, strings.ReplaceAll(stub, "0x503d6c7e", "0x0123abcd"), strings.ReplaceAll(grub, "0x503d6c7e", "0x0123abcd"), "/boot/0x0123abcd"},
		{"CRLF", strings.ReplaceAll(pvd, "\n", "\r\n"), strings.ReplaceAll(stub, "\n", "\r\n"), strings.ReplaceAll(grub, "\n", "\r\n"), "/boot/0x503d6c7e"},
	} {
		t.Run(variant.name, func(t *testing.T) {
			layout, err := discoverSourceLayout(variant.pvd, variant.stub, variant.grub)
			if err != nil || layout != (sourceLayout{SourceVolumeID, variant.marker}) {
				t.Fatalf("layout = %#v, error = %v", layout, err)
			}
		})
	}
	for _, invalid := range []struct{ name, pvd, stub, grub string }{
		{"label prefix", strings.ReplaceAll(pvd, SourceVolumeID, SourceVolumeID+"0"), stub, grub},
		{"uninspected release", strings.ReplaceAll(pvd, "44", "45"), stub, grub},
		{"ambiguous volume", pvd + pvd, stub, grub},
		{"wrong live label", pvd, stub, strings.ReplaceAll(grub, SourceVolumeID, "Fedora-other")},
		{"duplicate root", pvd, stub, strings.ReplaceAll(grub, "rd.live.image", "rd.live.image root=live:CDLABEL="+SourceVolumeID)},
		{"wrong ESP marker", pvd, strings.ReplaceAll(stub, "0x503d6c7e", "0x0123abcd"), grub},
		{"extra ESP command", pvd, stub + "source /other.cfg\n", grub},
		{"unsafe marker", pvd, strings.ReplaceAll(stub, "0x503d6c7e", "../other"), grub},
		{"unknown kernel path", pvd, stub, strings.ReplaceAll(grub, "/loader/linux", "/loader/other")},
		{"unknown initrd path", pvd, stub, strings.ReplaceAll(grub, "/loader/initrd", "/loader/other")},
		{"missing live protocol", pvd, stub, strings.ReplaceAll(grub, "rd.live.image", "")},
	} {
		t.Run(invalid.name, func(t *testing.T) {
			if _, err := discoverSourceLayout(invalid.pvd, invalid.stub, invalid.grub); err == nil {
				t.Fatal("unsupported or contradictory layout was accepted")
			}
		})
	}
}
