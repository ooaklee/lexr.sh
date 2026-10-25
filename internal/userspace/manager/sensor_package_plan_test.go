package manager

import (
	"context"
	"strings"
	"testing"

	lexr "github.com/ooaklee/lexr.sh"
	"github.com/ooaklee/lexr.sh/internal/sensors/packageplan"
	"github.com/ooaklee/lexr.sh/internal/userspace/catalog"
)

// TestSensorPackageIdentityDoesNotGrantCatalogueAuthority keeps a reserved
// planning identifier below the shipped and compiled component policies. Even
// the source-only catalogue shape cannot register this future runtime identity.
func TestSensorPackageIdentityDoesNotGrantCatalogueAuthority(t *testing.T) {
	downloader, installer := &fakeDownloader{}, &fakeInstaller{}
	manager := New(catalog.NewLoader(lexr.UserspaceCatalogFS(), "supported-userspace.json"), downloader, nil)
	manager.Installer = installer
	shipped, err := manager.LoadCatalog("")
	if err != nil {
		t.Fatal(err)
	}
	if _, present := shipped.Get(packageplan.ComponentID); present {
		t.Fatal("a package layout registered a shipped runtime component")
	}
	data := []byte(strings.Replace(privateSensorCatalogue, `"id":"native-sensors"`, `"id":"`+packageplan.ComponentID+`"`, 1))
	if _, err := catalog.LoadBytes(data); err != nil {
		t.Fatalf("fixture must reach the compiled authority intersection: %v", err)
	}
	selected := writePrivateCatalogue(t, data)
	if _, err := manager.LoadCatalog(selected); err == nil || !strings.Contains(err.Error(), "has no compiled policy") {
		t.Fatalf("reserved runtime identity became executable policy: %v", err)
	}
	if _, err := manager.Install(context.Background(), InstallRequest{CatalogPath: selected, Selector: packageplan.ComponentID, Root: t.TempDir()}); err == nil {
		t.Fatal("runtime layout identity became installable")
	}
	if len(installer.calls) != 0 || len(downloader.specs) != 0 {
		t.Fatal("unqualified runtime identity reached a downloader or installer")
	}
}
