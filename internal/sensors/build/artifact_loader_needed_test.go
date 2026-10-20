package build

import (
	"encoding/json"
	"strings"
	"testing"
)

// These full-archive cases use serialized ELF fixtures, not a native loader or
// target-OS compatibility proof. Every mutation refreshes the product hashes so
// the direct-dependency gate, rather than stale report evidence, must reject it.
func rewriteLoaderFixture(t *testing.T, files map[string][]byte, change func(map[string]string)) {
	t.Helper()
	var raw map[string]string
	if err := json.Unmarshal(files["loader-evidence.txt"], &raw); err != nil {
		t.Fatal(err)
	}
	change(raw)
	data, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	files["loader-evidence.txt"] = append(data, '\n')
	rewriteFixtureReport(t, files, nil)
}

func requireDirectLoaderRefusal(t *testing.T, files map[string][]byte, rules map[string]fileRule, artifact, soname string) {
	t.Helper()
	if err := validateNativeELFs(files); err != nil {
		t.Fatalf("fixture failed the existing ELF policy before loader validation: %v", err)
	}
	rewriteFixtureReport(t, files, nil)
	_, err := decodeArtifactArchive(writeFixtureTar(t, fixtureMembers(files)), 2, rules)
	if err == nil {
		t.Fatalf("accepted %s direct dependency %s absent from its own loader evidence", artifact, soname)
	}
	for _, expected := range []string{"direct ELF dependency is absent from loader evidence", artifact, soname} {
		if !strings.Contains(err.Error(), expected) {
			t.Fatalf("refused for the wrong reason, wanted %q: %v", expected, err)
		}
	}
}

func TestArtifactDirectNeededRequiresOwnLoaderResolution(t *testing.T) {
	for _, name := range coreELFNames {
		t.Run(name, func(t *testing.T) {
			files, rules := artifactFixture(t)
			files[name] = fixtureELF(t, name, func(spec *fixtureELFSpec) {
				spec.needed = append(spec.needed, "libabsent.so.0")
			})
			// Another ELF's loader observation cannot satisfy this one's need.
			other := coreELFNames[0]
			if other == name {
				other = coreELFNames[1]
			}
			rewriteLoaderFixture(t, files, func(raw map[string]string) {
				raw[other] += "libabsent.so.0 => /usr/lib/libabsent.so.0 (0x5678)\n"
			})
			requireDirectLoaderRefusal(t, files, rules, name, "libabsent.so.0")
		})
	}
}

func TestArtifactDirectNeededRejectsMissingOrWrongSONAMERow(t *testing.T) {
	for _, name := range coreELFNames {
		for _, wrongKey := range []bool{false, true} {
			variant := "missing"
			if wrongKey {
				variant = "wrong-key"
			}
			t.Run(name+"/"+variant, func(t *testing.T) {
				files, rules := artifactFixture(t)
				rewriteLoaderFixture(t, files, func(raw map[string]string) {
					row := "\tlibc.so.6 => /lib/aarch64-linux-gnu/libc.so.6 (0x4321)\n"
					replacement := ""
					if wrongKey {
						replacement = strings.Replace(row, "libc.so.6 =>", "libother.so.6 =>", 1)
					}
					if strings.Count(raw[name], row) != 1 {
						t.Fatal("fixture libc row is not unique")
					}
					raw[name] = strings.Replace(raw[name], row, replacement, 1)
				})
				requireDirectLoaderRefusal(t, files, rules, name, "libc.so.6")
			})
		}
	}
}

func TestArtifactVirtualDSODoesNotResolveDirectNeeded(t *testing.T) {
	files, rules := artifactFixture(t)
	files["monitor-sensor"] = fixtureELF(t, "monitor-sensor", func(spec *fixtureELFSpec) {
		spec.needed = append(spec.needed, "linux-vdso.so.1")
	})
	requireDirectLoaderRefusal(t, files, rules, "monitor-sensor", "linux-vdso.so.1")
}

func TestArtifactDirectLoaderNeededUsesFixedAbsoluteSelfRow(t *testing.T) {
	files, rules := artifactFixture(t)
	for _, name := range coreELFNames {
		files[name] = fixtureELF(t, name, func(spec *fixtureELFSpec) {
			spec.needed = append(spec.needed, "ld-linux-aarch64.so.1")
		})
	}
	rewriteFixtureReport(t, files, nil)
	if _, err := decodeArtifactArchive(writeFixtureTar(t, fixtureMembers(files)), 2, rules); err != nil {
		t.Fatalf("legitimate direct loader need with fixed self-row refused: %v", err)
	}
}

func TestArtifactLoaderAllowsResolvedTransitiveSystemExtras(t *testing.T) {
	files, rules := artifactFixture(t)
	rewriteLoaderFixture(t, files, func(raw map[string]string) {
		for _, name := range coreELFNames {
			raw[name] += "libtransitive.so.1 => /usr/lib/libtransitive.so.1 (0x6789)\n"
		}
	})
	if _, err := decodeArtifactArchive(writeFixtureTar(t, fixtureMembers(files)), 2, rules); err != nil {
		t.Fatalf("resolved transitive system dependency refused: %v", err)
	}
}

func TestArtifactLoaderKeepsPrivateProviderOriginChecks(t *testing.T) {
	for _, soname := range []string{"libqmi-glib.so.5", "libssc.so.2"} {
		t.Run(soname, func(t *testing.T) {
			files, rules := artifactFixture(t)
			rewriteLoaderFixture(t, files, func(raw map[string]string) {
				raw["ssccli"] = strings.Replace(raw["ssccli"], ProviderDirectory+"/lib/"+soname, "/usr/lib/"+soname, 1)
			})
			_, err := decodeArtifactArchive(writeFixtureTar(t, fixtureMembers(files)), 2, rules)
			if err == nil || !strings.Contains(err.Error(), "raw loader origins differ") {
				t.Fatalf("private provider origin mismatch was not refused: %v", err)
			}
		})
	}
}
