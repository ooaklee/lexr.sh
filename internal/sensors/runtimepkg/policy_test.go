package runtimepkg

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/sensors/build"
	"github.com/ooaklee/lexr.sh/internal/sensors/source"
	"github.com/ooaklee/lexr.sh/internal/userspace/compatibility"
)

// This is the independently reviewed synthetic qualification fixture, not a
// sensor support declaration. The compiled size/hash remain its authority.
const qualificationFixture = `{
  "schema_version": 1,
  "kind": "lexr.userspace-component-compatibility",
  "component_id": "native-sensors-runtime-v1",
  "release": "sp11-native-sensors-v1",
  "capabilities": [
    "sensors"
  ],
  "description": "Qualification-only software fixture; no tested device support or live installation authority is declared.",
  "lexr": {
    "minimum_inclusive": "0.7.0"
  },
  "targets": [
    {
      "architectures": [
        "arm64"
      ],
      "device_profiles": [
        "x1e80100-microsoft-denali-oled"
      ],
      "operating_systems": [
        {
          "id": "ubuntu",
          "version_ranges": [
            {
              "minimum_inclusive": "26.04",
              "maximum_exclusive": "26.10"
            }
          ],
          "tested_versions": []
        }
      ],
      "kernels": [
        {
          "patch_line": "7.2.9",
          "platform_flavour": "qcom-x1e",
          "scope": "sp11",
          "abi_generation": {
            "minimum_inclusive": 2,
            "maximum_exclusive": 3
          }
        }
      ]
    }
  ]
}
`

// qualificationTarget returns only the narrow synthetic admission tuple, not
// an asserted tested target or discovered functional minimum kernel.
func qualificationTarget() compatibility.Target {
	return compatibility.Target{LexrVersion: "0.7.0", Architecture: "arm64", DeviceProfile: "x1e80100-microsoft-denali-oled", OSID: "ubuntu", OSVersion: "26.04", KernelABI: "7.2.9-jg-0sp11v2-qcom-x1e"}
}

// TestIndependentQualificationPinAndUnknownEvidence binds the fixture bytes and
// preserves hard tuple bounds without manufacturing support evidence.
func TestIndependentQualificationPinAndUnknownEvidence(t *testing.T) {
	m, err := verifyDeclaration([]byte(qualificationFixture))
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(qualificationFixture)) != qualificationDeclarationBytes || digest([]byte(qualificationFixture)) != qualificationDeclarationSHA256 {
		t.Fatal("independent qualification fixture pin changed")
	}
	for _, test := range []struct {
		name   string
		target compatibility.Target
		status string
	}{
		{"qualification-only", qualificationTarget(), compatibility.Unverified},
		{"missing", compatibility.Target{}, compatibility.Unavailable},
		{"wrong-abi", func() compatibility.Target {
			x := qualificationTarget()
			x.KernelABI = "7.2.9-jg-0sp11v3-qcom-x1e"
			return x
		}(), compatibility.Incompatible},
		{"old-abi", func() compatibility.Target {
			x := qualificationTarget()
			x.KernelABI = "7.2.9-jg-0sp11v1-qcom-x1e"
			return x
		}(), compatibility.Incompatible},
		{"wrong-device", func() compatibility.Target {
			x := qualificationTarget()
			x.DeviceProfile = "x1e80100-microsoft-denali-lcd"
			return x
		}(), compatibility.Incompatible},
		{"wrong-os", func() compatibility.Target { x := qualificationTarget(); x.OSVersion = "26.10"; return x }(), compatibility.Incompatible},
		{"wrong-architecture", func() compatibility.Target { x := qualificationTarget(); x.Architecture = "amd64"; return x }(), compatibility.Incompatible},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := compatibility.Evaluate(m, test.target); got.Status != test.status {
				t.Fatalf("status=%s want=%s reasons=%v", got.Status, test.status, got.Reasons)
			}
		})
	}
	for _, altered := range [][]byte{nil, append([]byte(qualificationFixture), ' '), bytes.ReplaceAll([]byte(qualificationFixture), []byte("26.04"), []byte("24.04")), bytes.ReplaceAll([]byte(qualificationFixture), []byte(`"minimum_inclusive": 2`), []byte(`"minimum_inclusive": 1`))} {
		if _, err := verifyDeclaration(altered); err == nil {
			t.Fatal("editable declaration displaced independent qualification pin")
		}
	}
}

// TestInvalidOpaqueHandlesCannotPackageOrAssess ensures zero handles, missing
// source and malformed ELF roles cannot supply qualification authority.
func TestInvalidOpaqueHandlesCannotPackageOrAssess(t *testing.T) {
	for _, test := range []struct {
		a *build.VerifiedArtifacts
		s *source.Captured
	}{{nil, nil}, {&build.VerifiedArtifacts{}, &source.Captured{}}, {nil, &source.Captured{}}, {&build.VerifiedArtifacts{}, nil}} {
		if _, err := Build(test.a, test.s, []byte(qualificationFixture)); err == nil {
			t.Fatal("invalid handles created a qualification package")
		}
	}
	for _, p := range []*VerifiedPackage{nil, {}} {
		if p.Bytes() != nil || p.Files() != nil || p.Links() != nil || p.Declaration() != nil || p.Reference() != (Reference{}) || p.Assess(qualificationTarget()).Status != compatibility.Unavailable {
			t.Fatal("invalid package exposed authority")
		}
	}
	if err := build.ValidateRuntimeELFs(nil); err == nil {
		t.Fatal("empty runtime ELF role map accepted")
	}
	if err := build.ValidateRuntimeELFs(map[string][]byte{"libqmi-glib.so.5.12.0": []byte("x"), "libssc.so.2": []byte("x"), "ssccli": []byte("x"), "iio-sensor-proxy": []byte("x"), "monitor-sensor": []byte("x")}); err == nil {
		t.Fatal("nonELF runtime files accepted")
	}
}

// TestReferenceAndOuterAuthorityRefusals rejects malformed retained identities
// and demonstrates that a correct digest cannot replace the package contract.
func TestReferenceAndOuterAuthorityRefusals(t *testing.T) {
	data := smallArchive(t)
	for _, ref := range []Reference{{}, {Bytes: int64(len(data)), SHA256: "abc"}, {Bytes: int64(len(data) + 1), SHA256: digest(data)}, {Bytes: int64(len(data)), SHA256: "D87954F5A072987AC7503E59ED346CEDEFB63DF1622E357F047CC7571BB4914A"}, {Bytes: MaximumPackageBytes + 1, SHA256: digest(data)}} {
		if _, err := Decode(data, ref); err == nil {
			t.Fatal("invalid byte authority accepted")
		}
	}
	if _, err := Decode(data, reference(data)); err == nil {
		t.Fatal("valid digest replaced closed package contract")
	}
	if Kind == "lexr.userspace-component" || Purpose == "production" {
		t.Fatal("qualification-only identity became a production kind")
	}
	if !reflect.DeepEqual(fixedLinks(), []Link{{"lib/libqmi-glib.so", "libqmi-glib.so.5"}, {"lib/libqmi-glib.so.5", "libqmi-glib.so.5.12.0"}, {"lib/libssc.so", "libssc.so.2"}}) {
		t.Fatal("runtime aliases changed")
	}
}
