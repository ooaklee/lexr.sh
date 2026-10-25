package build

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"strings"
	"testing"
)

// fixtureELFs048 contains serialised, nonexecuted metadata for the successor.
// It is not a native producer result or evidence of running target programs.
func fixtureELFs048(t *testing.T) map[string][]byte {
	t.Helper()
	files := fixtureELFs(t)
	files["libssc.so.2"] = fixtureELF048(t, nil)
	return files
}

// fixtureELF048 extends only the library's exact metadata ABI in an old fixture.
func fixtureELF048(t *testing.T, mutate func(*fixtureELFSpec)) []byte {
	t.Helper()
	data := fixtureELF(t, "libssc.so.2", func(spec *fixtureELFSpec) {
		spec.exports = sscPublicVersions048()
		spec.exports["LIBSSC_0.4.8"] = "LIBSSC_0.4.8"
		if mutate != nil {
			mutate(spec)
		}
	})
	// The unchanged original fixture deliberately recognises only old nodes.
	// Normalise the new node's synthetic symbol shape, not production bytes.
	object, err := elf.NewFile(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer object.Close()
	symbols, err := object.DynamicSymbols()
	if err != nil {
		t.Fatal(err)
	}
	table := object.SectionByType(elf.SHT_DYNSYM)
	for index, symbol := range symbols {
		if symbol.Name == "LIBSSC_0.4.8" {
			entry := data[int(table.Offset)+(index+1)*24 : int(table.Offset)+(index+2)*24]
			entry[4] = byte(elf.STB_GLOBAL)<<4 | byte(elf.STT_OBJECT)
			binary.LittleEndian.PutUint16(entry[6:], uint16(elf.SHN_ABS))
			binary.LittleEndian.PutUint64(entry[8:], 0)
			binary.LittleEndian.PutUint64(entry[16:], 0)
		}
	}
	return data
}

// TestMetadata048ABIIsExactAndDefensive retains all original assignments and
// admits exactly ten additional functions under one additional version node.
func TestMetadata048ABIIsExactAndDefensive(t *testing.T) {
	old, successor := sscPublicVersions(), sscPublicVersions048()
	if len(old) != 97 || len(successor) != 107 {
		t.Fatal("fixed public ABI counts differ")
	}
	for name, version := range old {
		if successor[name] != version {
			t.Fatalf("original public assignment changed: %s", name)
		}
	}
	added := 0
	for name, version := range successor {
		if _, retained := old[name]; !retained {
			added++
			if version != "LIBSSC_0.4.8" {
				t.Fatal("metadata successor added a symbol under another node")
			}
		}
	}
	if added != 10 || sscVersionNode("LIBSSC_0.4.8") {
		t.Fatal("successor broadened the default version-node policy")
	}
	for _, name := range strings.Fields(`ssc_sensor_metadata_snapshot_get_type ssc_sensor_metadata_snapshot_copy ssc_sensor_metadata_snapshot_free ssc_sensor_metadata_snapshot_get_uid_high ssc_sensor_metadata_snapshot_get_uid_low ssc_sensor_metadata_snapshot_get_attribute ssc_sensor_metadata_snapshot_get_matrix_state ssc_sensor_metadata_snapshot_get_matrix_report ssc_sensor_metadata_snapshot_get_effective_matrix ssc_sensor_dup_metadata_snapshot`) {
		if successor[name] != "LIBSSC_0.4.8" {
			t.Fatalf("declared metadata function missing or changed: %s", name)
		}
	}
	delete(successor, "ssc_sensor_dup_metadata_snapshot")
	if len(sscPublicVersions048()) != 107 || len(sscPublicVersions()) != 97 {
		t.Fatal("public ABI copies changed compiled authority")
	}
	if _, _, err := nativeSSCABI(0); err == nil {
		t.Fatal("unknown profile selected an ABI")
	}
	if _, _, err := nativeSSCABI(nativeSSCProfile(255)); err == nil {
		t.Fatal("unrecognised profile selected an ABI")
	}
}

// TestMetadata048ELFProfilesAreSeparate checks both public fixed entry points
// without changing the accepted original ABI or combining their permissions.
func TestMetadata048ELFProfilesAreSeparate(t *testing.T) {
	old, successor := fixtureELFs(t), fixtureELFs048(t)
	if err := ValidateRuntimeELFs(old); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRuntimeELFs048(successor); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRuntimeELFs(successor); err == nil {
		t.Fatal("default ABI accepted metadata successor exports")
	}
	if err := ValidateRuntimeELFs048(old); err == nil {
		t.Fatal("metadata successor accepted the old incomplete ABI")
	}
	if _, err := inspectNativeELF("libssc.so.2", successor["libssc.so.2"]); err == nil {
		t.Fatal("default inspector accepted the successor version node")
	}
	if _, err := inspectNativeELF048("libssc.so.2", successor["libssc.so.2"]); err != nil {
		t.Fatal(err)
	}
	if err := validateNativeELFsWithProfile(successor, 0); err == nil {
		t.Fatal("unknown native profile was accepted")
	}
	for _, changed := range []map[string][]byte{nil, {"libssc.so.2": successor["libssc.so.2"]}} {
		if err := ValidateRuntimeELFs048(changed); err == nil {
			t.Fatal("successor accepted a missing runtime role")
		}
	}
	successor["unexpected"] = []byte("x")
	if err := ValidateRuntimeELFs048(successor); err == nil {
		t.Fatal("successor accepted an extra runtime role")
	}
}

// TestMetadata048ELFRefusesChangedSymbolsAndProviders applies the unchanged
// structural/provider gates to each adversarial serialised successor fixture.
func TestMetadata048ELFRefusesChangedSymbolsAndProviders(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*fixtureELFSpec)
	}{
		{"missing-metadata", func(s *fixtureELFSpec) { delete(s.exports, "ssc_sensor_metadata_snapshot_copy") }},
		{"missing-node", func(s *fixtureELFSpec) { delete(s.exports, "LIBSSC_0.4.8") }},
		{"changed-metadata-node", func(s *fixtureELFSpec) { s.exports["ssc_sensor_metadata_snapshot_copy"] = "LIBSSC_0.4.7" }},
		{"changed-old-node", func(s *fixtureELFSpec) { s.exports["ssc_sensor_sample_copy"] = "LIBSSC_0.4.8" }},
		{"extra-private-export", func(s *fixtureELFSpec) { s.exports["ssc_client_metadata_secret"] = "LIBSSC_0.4.8" }},
		{"unknown-node", func(s *fixtureELFSpec) { s.exports["LIBSSC_0.4.9"] = "LIBSSC_0.4.9" }},
		{"hidden-metadata", func(s *fixtureELFSpec) { s.hiddenSymbol = "ssc_sensor_metadata_snapshot_copy" }},
		{"weak-metadata", func(s *fixtureELFSpec) { s.weakSymbol = "ssc_sensor_metadata_snapshot_copy" }},
		{"weak-retained-public", func(s *fixtureELFSpec) { s.weakSymbol = "ssc_sensor_sample_copy" }},
		{"object-metadata", func(s *fixtureELFSpec) { s.objectSymbol = "ssc_sensor_metadata_snapshot_copy" }},
		{"hidden-node", func(s *fixtureELFSpec) { s.hiddenSymbol = "LIBSSC_0.4.8" }},
		{"wrong-soname", func(s *fixtureELFSpec) { s.soname = "libssc.so.3" }},
		{"wrong-machine", func(s *fixtureELFSpec) { s.machine = elf.EM_X86_64 }},
		{"runpath", func(s *fixtureELFSpec) { s.extraTag = elf.DT_RUNPATH; s.extraValue = "$ORIGIN" }},
		{"wrong-qmi-provider", func(s *fixtureELFSpec) { s.imports[0].library = "libqmi-glib.so.4" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			files := fixtureELFs048(t)
			files["libssc.so.2"] = fixtureELF048(t, test.mutate)
			if err := ValidateRuntimeELFs048(files); err == nil {
				t.Fatal("changed metadata successor ELF was accepted")
			}
		})
	}
	files := fixtureELFs048(t)
	files["ssccli"] = fixtureELF(t, "ssccli", func(s *fixtureELFSpec) {
		s.imports = []fixtureSymbol{{"ssc_sensor_metadata_snapshot_get_type", "LIBSSC_0.4.8", "libssc.so.2"}}
	})
	if err := ValidateRuntimeELFs048(files); err != nil {
		t.Fatalf("exact new-version import was refused: %v", err)
	}
	files["ssccli"] = fixtureELF(t, "ssccli", func(s *fixtureELFSpec) {
		s.imports = []fixtureSymbol{{"ssc_sensor_metadata_snapshot_get_type", "LIBSSC_0.4.7", "libssc.so.2"}}
	})
	if err := ValidateRuntimeELFs048(files); err == nil {
		t.Fatal("metadata import resolved to the wrong symbol version")
	}
}

// TestMetadata048StrongBindingDoesNotChangeLegacyDefault records the existing
// default's weak-function limitation while requiring strong successor exports.
func TestMetadata048StrongBindingDoesNotChangeLegacyDefault(t *testing.T) {
	old := fixtureELFs(t)
	old["libssc.so.2"] = fixtureELF(t, "libssc.so.2", func(spec *fixtureELFSpec) {
		spec.weakSymbol = "ssc_sensor_sample_copy"
	})
	if err := ValidateRuntimeELFs(old); err != nil {
		t.Fatalf("successor work changed the original default's binding semantics: %v", err)
	}
	successor := fixtureELFs048(t)
	successor["libssc.so.2"] = fixtureELF048(t, func(spec *fixtureELFSpec) {
		spec.weakSymbol = "ssc_sensor_sample_copy"
	})
	if err := ValidateRuntimeELFs048(successor); err == nil {
		t.Fatal("successor accepted a weak retained public function")
	}
}
