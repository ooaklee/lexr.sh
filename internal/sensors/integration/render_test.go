package integration

import (
	"bytes"
	"path"
	"reflect"
	"strings"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/sensors/runtimepkg"
	"github.com/ooaklee/lexr.sh/internal/sensors/source"
)

// TestManualUnitGoldenAndClosedDirectives pins the exact static unit and every
// permitted directive independently of the renderer's metadata snapshots.
func TestManualUnitGoldenAndClosedDirectives(t *testing.T) {
	const expectedSHA256 = "2fc2d8ef2e555c65ee168be7d37f9919f0f0cfe22effbb1648849223e0936c2d"
	if got := digest([]byte(renderedUnit)); got != expectedSHA256 {
		t.Fatalf("manual qualification unit golden differs: %s", got)
	}
	wanted := map[string]map[string]string{
		"Unit": {"Description": "Lexr native sensor proxy (qualification only)", "Requires": "dbus.socket", "After": "dbus.socket"},
		"Service": {
			"Type": "exec", "ExecStart": strings.Join(fixedArgv(), " "), "Restart": "no",
			"ProtectSystem": "strict", "ProtectControlGroups": "true", "ProtectHome": "true", "ProtectKernelModules": "true", "PrivateTmp": "true",
			"RestrictAddressFamilies": "AF_UNIX AF_LOCAL AF_NETLINK AF_QIPCRTR", "MemoryDenyWriteExecute": "true", "RestrictRealtime": "true",
		},
	}
	if got := unitDirectives(t, renderedUnit); !reflect.DeepEqual(got, wanted) {
		t.Fatalf("static manual unit directive contract differs: %+v", got)
	}
	for _, forbidden := range []string{"[Install]", "BusName=", "Conflicts=", "ExecStartPre=", "ExecStartPost=", "ExecStop=", "WantedBy=", "Wants=", "--replace", "LD_LIBRARY_PATH", "SYSTEMD_WANTS", "/bin/sh", "Environment="} {
		if strings.Contains(renderedUnit, forbidden) {
			t.Fatalf("manual unit includes an activation, collision or shell hook: %s", forbidden)
		}
	}
	if !strings.HasSuffix(renderedUnit, "\n") || strings.Contains(renderedUnit, "\r") || !reflect.DeepEqual(fixedArgv(), []string{"/lib/ld-linux-aarch64.so.1", "--library-path", "/usr/lib/lexr/sensors/sp11-native-sensors-v1/lib", "/usr/lib/lexr/sensors/sp11-native-sensors-v1/libexec/iio-sensor-proxy"}) {
		t.Fatal("manual unit or fixed loader argv spelling differs")
	}
}

// unitDirectives parses only the fixture's small systemd subset and refuses
// duplicate directives, duplicate sections or unsupported continuations.
func unitDirectives(t *testing.T, data string) map[string]map[string]string {
	t.Helper()
	result := map[string]map[string]string{}
	section := ""
	for _, line := range strings.Split(data, "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSuffix(strings.TrimPrefix(line, "["), "]")
			if result[section] != nil {
				t.Fatal("unit has duplicate section")
			}
			result[section] = map[string]string{}
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found || section == "" || key == "" || value == "" || strings.HasSuffix(line, "\\") {
			t.Fatal("unit directive spelling differs")
		}
		if _, duplicate := result[section][key]; duplicate {
			t.Fatal("unit has duplicate directive")
		}
		result[section][key] = value
	}
	return result
}

// TestRendererRefusesUnauthenticatedHandles checks that caller-created zero
// runtime or source handles cannot become a rendered integration bundle.
func TestRendererRefusesUnauthenticatedHandles(t *testing.T) {
	for _, input := range []struct {
		pkg *runtimepkg.VerifiedPackage
		src *source.Captured
	}{{nil, nil}, {new(runtimepkg.VerifiedPackage), nil}, {nil, new(source.Captured)}, {new(runtimepkg.VerifiedPackage), new(source.Captured)}} {
		if _, err := Render(input.pkg, input.src); err == nil {
			t.Fatal("zero authenticated handles became integration authority")
		}
	}
	for _, bundle := range []*Bundle{nil, {}} {
		if assets, err := bundle.Assets(); err == nil || assets != nil {
			t.Fatal("invalid integration handle returned passive assets")
		}
		if unit, err := bundle.Unit(); err == nil || unit != nil {
			t.Fatal("invalid integration handle returned a unit")
		}
		if snapshot, err := bundle.Snapshot(); err == nil || !reflect.DeepEqual(snapshot, Snapshot{}) {
			t.Fatal("invalid integration handle returned trusted metadata")
		}
	}
}

// TestBundleAccessorsAreDefensive uses only a private mechanical copy fixture,
// not a rendered or authenticated package, to cover all nested slice copying.
func TestBundleAccessorsAreDefensive(t *testing.T) {
	bundle := &Bundle{valid: true, unit: []byte("fixed unit\n"), assets: []Asset{{Path: "raw/fixture", Data: []byte("source\n"), Mode: 0o644}}, snapshot: Snapshot{Argv: []string{"fixed"}, RawTemplates: []TemplateIdentity{{SourcePath: "fixed"}}, Assets: []AssetIdentity{{Path: "fixed"}}}}
	assets, _ := bundle.Assets()
	unit, _ := bundle.Unit()
	snapshot, _ := bundle.Snapshot()
	assets[0].Data[0] ^= 1
	assets[0].Path = "changed"
	unit[0] ^= 1
	snapshot.Argv[0] = "changed"
	snapshot.RawTemplates[0].SourcePath = "changed"
	snapshot.Assets[0].Path = "changed"
	gotAssets, _ := bundle.Assets()
	gotUnit, _ := bundle.Unit()
	gotSnapshot, _ := bundle.Snapshot()
	if gotAssets[0].Path != "raw/fixture" || !bytes.Equal(gotAssets[0].Data, []byte("source\n")) || !bytes.Equal(gotUnit, []byte("fixed unit\n")) || gotSnapshot.Argv[0] != "fixed" || gotSnapshot.RawTemplates[0].SourcePath != "fixed" || gotSnapshot.Assets[0].Path != "fixed" {
		t.Fatal("integration accessor exposed retained mutable state")
	}
}

// TestCompiledTemplatePolicyIsClosed checks that source inputs and passive
// names are fixed independently of caller paths and global registration paths.
func TestCompiledTemplatePolicyIsClosed(t *testing.T) {
	wanted := map[string]int64{"raw/80-iio-sensor-proxy.rules": 2167, "raw/iio-sensor-proxy.service.in": 435, "raw/net.hadess.SensorProxy.conf.in": 1261, "raw/net.hadess.SensorProxy.policy": 686}
	policies := templatePolicies()
	if len(policies) != len(wanted) {
		t.Fatal("compiled template count differs")
	}
	for _, policy := range policies {
		if wanted[policy.assetPath] != policy.bytes || !strings.HasPrefix(policy.sourcePath, "iio-sensor-proxy/source/data/") || path.IsAbs(policy.assetPath) || path.Clean(policy.assetPath) != policy.assetPath || strings.Contains(policy.assetPath, "..") {
			t.Fatal("template policy escaped its fixed passive namespace")
		}
		if !validReference(runtimepkg.Reference{Bytes: policy.bytes, SHA256: policy.sha256}) {
			t.Fatal("template policy has malformed digest or size")
		}
	}
	policies[0].sourcePath = "changed"
	argv := fixedArgv()
	argv[0] = "changed"
	if templatePolicies()[0].sourcePath == "changed" || fixedArgv()[0] != LoaderPath || UnitName == VendorUnitName {
		t.Fatal("caller mutated fixed policy or unit names collide")
	}
}
