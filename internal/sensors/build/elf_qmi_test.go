package build

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"fmt"
	"strings"
	"testing"
)

// fixtureQMIContractELF preserves the SSC ABI while modelling an unversioned QMI
// DSO and direct consumers. These are serialised metadata, not native products.
func fixtureQMIContractELF(t *testing.T, name string, index uint16, omitProviderVersym bool, mutate func(*fixtureELFSpec)) []byte {
	t.Helper()
	return fixtureELF(t, name, func(s *fixtureELFSpec) {
		imports := []fixtureSymbol{{"qmi_device_new", "", ""}, {"qmi_message_ssc_control_output_unref", "", ""}}
		switch name {
		case "libqmi-glib.so.5.12.0":
			s.exports = map[string]string{"qmi_device_new": "", "qmi_message_ssc_control_output_unref": ""}
			s.omitVersym = omitProviderVersym
		case "libssc.so.2":
			s.imports = imports
			s.unversionedImportIndex = index
		case "ssccli", "iio-sensor-proxy":
			s.imports = append(s.imports, imports...)
			s.needed = append(s.needed, "libqmi-glib.so.5")
			s.unversionedImportIndex = index
		}
		if mutate != nil {
			mutate(s)
		}
	})
}

// fixtureQMIContractFiles makes every real consumer role traverse the same QMI
// policy; only the unchanged monitor has no private provider imports.
func fixtureQMIContractFiles(t *testing.T, index uint16, omitProviderVersym bool) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	for _, name := range coreELFNames {
		files[name] = fixtureQMIContractELF(t, name, index, omitProviderVersym, nil)
	}
	return files
}

// TestUnversionedQMIFixtureMetadata checks the serialiser itself so a positive
// policy test cannot accidentally rely on an empty-name verdef or verneed.
func TestUnversionedQMIFixtureMetadata(t *testing.T) {
	for _, index := range []uint16{0, 1} {
		for _, omit := range []bool{false, true} {
			t.Run(fmt.Sprintf("import-%d/provider-versym-%t", index, !omit), func(t *testing.T) {
				files := fixtureQMIContractFiles(t, index, omit)
				for _, name := range []string{"libqmi-glib.so.5.12.0", "libssc.so.2"} {
					f, err := elf.NewFile(bytes.NewReader(files[name]))
					if err != nil {
						t.Fatal(err)
					}
					defer f.Close()
					if f.SectionByType(elf.SHT_GNU_VERNEED) != nil {
						t.Fatal("unversioned imports acquired a synthetic version requirement")
					}
					if values, err := f.DynValue(elf.DT_VERNEED); err != nil || len(values) != 0 {
						t.Fatalf("unversioned import requirement tag: %v %v", values, err)
					}
					if name == "libqmi-glib.so.5.12.0" {
						if f.SectionByType(elf.SHT_GNU_VERDEF) != nil {
							t.Fatal("unversioned exports acquired a synthetic version definition")
						}
						if values, err := f.DynValue(elf.DT_VERDEF); err != nil || len(values) != 0 {
							t.Fatalf("unversioned export definition tag: %v %v", values, err)
						}
						if (f.SectionByType(elf.SHT_GNU_VERSYM) == nil) != omit {
							t.Fatal("provider version-symbol table presence differs")
						}
					}
					symbols, err := f.DynamicSymbols()
					if err != nil {
						t.Fatal(err)
					}
					found := 0
					for _, symbol := range symbols {
						if !strings.HasPrefix(symbol.Name, "qmi_") {
							continue
						}
						found++
						if symbol.Version != "" || symbol.Library != "" || elf.ST_BIND(symbol.Info) != elf.STB_GLOBAL || elf.ST_TYPE(symbol.Info) != elf.STT_FUNC || symbol.VersionIndex.IsHidden() {
							t.Fatalf("not an unversioned QMI function: %+v", symbol)
						}
						if name == "libqmi-glib.so.5.12.0" {
							if symbol.Section == elf.SHN_UNDEF || symbol.HasVersion == omit || !omit && symbol.VersionIndex.Index() != 1 {
								t.Fatalf("provider reserved index/definition differs: %+v", symbol)
							}
						} else if symbol.Section != elf.SHN_UNDEF || !symbol.HasVersion || symbol.VersionIndex.Index() != index {
							t.Fatalf("consumer reserved import index differs: %+v", symbol)
						}
					}
					if found != 2 {
						t.Fatalf("QMI fixture symbol count=%d", found)
					}
				}
			})
		}
	}
}

// TestNativeELFUnversionedQMIContractAcceptsBothReservedImports covers the two
// observed/allowed unversioned indices without relaxing the versioned SSC ABI.
func TestNativeELFUnversionedQMIContractAcceptsBothReservedImports(t *testing.T) {
	for _, index := range []uint16{0, 1} {
		for _, omit := range []bool{false, true} {
			t.Run(fmt.Sprintf("import-%d/provider-versym-%t", index, !omit), func(t *testing.T) {
				if err := validateNativeELFs(fixtureQMIContractFiles(t, index, omit)); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

// TestNativeELFUnversionedQMIAllowsNamedSystemImports distinguishes a provider's
// libc version requirements from forbidden named versions on its own exports.
func TestNativeELFUnversionedQMIAllowsNamedSystemImports(t *testing.T) {
	for _, index := range []uint16{0, 1} {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			files := fixtureQMIContractFiles(t, index, false)
			files["libqmi-glib.so.5.12.0"] = fixtureQMIContractELF(t, "libqmi-glib.so.5.12.0", index, false, func(s *fixtureELFSpec) {
				s.imports = []fixtureSymbol{{"memcpy", "GLIBC_2.17", "libc.so.6"}}
			})
			if err := validateNativeELFs(files); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// The QMI-only helper understands an absent version table, but no supported
// consumer role may lose its SSC definitions/import versions as a consequence.
func TestNativeELFUnversionedQMIKeepsConsumerVersionsRequired(t *testing.T) {
	for _, name := range []string{"libssc.so.2", "ssccli", "iio-sensor-proxy"} {
		t.Run(name, func(t *testing.T) {
			files := fixtureQMIContractFiles(t, 0, false)
			files[name] = fixtureQMIContractELF(t, name, 0, false, func(s *fixtureELFSpec) { s.omitVersym = true })
			want := "required provider import is unsatisfied"
			if name == "libssc.so.2" {
				want = "SSC public symbol is not a default defined function"
			}
			if err := validateNativeELFs(files); err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("wanted %q, got %v", want, err)
			}
		})
	}
}

// Give a hidden definition the same name as a public one. The existing public
// export-map duplicate guard ignores hidden entries, so this must reach the
// new exact-one-definition check rather than pass via the public function.
func TestNativeELFUnversionedQMIDuplicateDefinitionRefused(t *testing.T) {
	const name = "libqmi-glib.so.5.12.0"
	files := fixtureQMIContractFiles(t, 0, false)
	data := fixtureQMIContractELF(t, name, 0, false, func(s *fixtureELFSpec) {
		s.exports["qmi_duplicate_slot"] = ""
		s.privateSymbol = "qmi_duplicate_slot"
	})
	f, err := elf.NewFile(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	symbols, err := f.DynamicSymbols()
	if err != nil {
		t.Fatal(err)
	}
	target, duplicate := -1, -1
	for i, s := range symbols {
		switch s.Name {
		case "qmi_device_new":
			target = i
		case "qmi_duplicate_slot":
			duplicate = i
		}
	}
	if target < 0 || duplicate < 0 {
		t.Fatal("fixture lacks its two definition slots")
	}
	dynsym := f.SectionByType(elf.SHT_DYNSYM)
	// DynamicSymbols omits the initial undefined entry; ELF64 symbols are 24B.
	nameIndex := binary.LittleEndian.Uint32(data[dynsym.Offset+uint64(target+1)*24:])
	binary.LittleEndian.PutUint32(data[dynsym.Offset+uint64(duplicate+1)*24:], nameIndex)
	object, err := inspectNativeELF(name, data)
	if err != nil || len(object.qmiExports["qmi_device_new"]) != 2 {
		t.Fatalf("fixture must reach the duplicate-definition policy: %v", err)
	}
	files[name] = data
	if err := validateNativeELFs(files); err == nil || !strings.Contains(err.Error(), "required provider import is unsatisfied") {
		t.Fatalf("duplicate definition did not fail provider matching: %v", err)
	}
}

// TestNativeELFUnversionedQMIRefusesChangedContracts keeps the new exception
// narrow; every mutation must reach its intended existing/new refusal category.
func TestNativeELFUnversionedQMIRefusesChangedContracts(t *testing.T) {
	cases := []struct {
		name, artifact, want string
		mutate               func(*fixtureELFSpec)
	}{
		{"missing-export", "libqmi-glib.so.5.12.0", "required provider import is unsatisfied", func(s *fixtureELFSpec) { delete(s.exports, "qmi_device_new") }},
		{"wrong-soname", "libqmi-glib.so.5.12.0", "SONAME differs", func(s *fixtureELFSpec) { s.soname = "libqmi-glib.so.4" }},
		{"missing-direct-needed", "libssc.so.2", "required provider dependency differs", func(s *fixtureELFSpec) { s.needed = []string{"libc.so.6"} }},
		{"transitive-only-cli-provider", "ssccli", "required provider import is unsatisfied", func(s *fixtureELFSpec) { s.needed = []string{"libc.so.6", "libssc.so.2"} }},
		{"versioned-provider-empty-import", "libqmi-glib.so.5.12.0", "required provider import is unsatisfied", func(s *fixtureELFSpec) {
			s.exports["qmi_device_new"] = "LIBQMI_GLIB_1.0"
			s.exports["qmi_message_ssc_control_output_unref"] = "LIBQMI_GLIB_1.0"
		}},
		{"absent-versym-named-definition", "libqmi-glib.so.5.12.0", "required provider import is unsatisfied", func(s *fixtureELFSpec) { s.omitVersym = true; s.exports["qmi_other"] = "LIBQMI_GLIB_1.0" }},
		{"absent-versym-named-requirement", "libqmi-glib.so.5.12.0", "required provider import is unsatisfied", func(s *fixtureELFSpec) {
			s.omitVersym = true
			s.imports = []fixtureSymbol{{"memcpy", "GLIBC_2.17", "libc.so.6"}}
		}},
		{"named-import-unversioned-provider", "libssc.so.2", "required provider import is unsatisfied", func(s *fixtureELFSpec) {
			s.imports[0].version = "LIBQMI_GLIB_FUTURE"
			s.imports[0].library = "libqmi-glib.so.5"
		}},
		{"wrong-named-library", "libssc.so.2", "required provider import is unsatisfied", func(s *fixtureELFSpec) {
			s.imports[0].version = "LIBQMI_GLIB_1.0"
			s.imports[0].library = "libqmi-glib.so.4"
		}},
		{"mixed-qmi-abi", "libqmi-glib.so.5.12.0", "required provider import is unsatisfied", func(s *fixtureELFSpec) { s.exports["qmi_other"] = "LIBQMI_GLIB_1.0" }},
		{"named-non-qmi-export", "libqmi-glib.so.5.12.0", "required provider import is unsatisfied", func(s *fixtureELFSpec) { s.exports["other_private_export"] = "OTHER_1.0" }},
		{"hidden-provider-version", "libqmi-glib.so.5.12.0", "required provider import is unsatisfied", func(s *fixtureELFSpec) { s.hiddenSymbol = "qmi_device_new" }},
		{"hidden-provider-visibility", "libqmi-glib.so.5.12.0", "required provider import is unsatisfied", func(s *fixtureELFSpec) { s.privateSymbol = "qmi_device_new" }},
		{"object-provider", "libqmi-glib.so.5.12.0", "required provider import is unsatisfied", func(s *fixtureELFSpec) { s.objectSymbol = "qmi_device_new" }},
		{"weak-provider", "libqmi-glib.so.5.12.0", "required provider import is unsatisfied", func(s *fixtureELFSpec) { s.weakSymbol = "qmi_device_new" }},
		{"local-provider", "libqmi-glib.so.5.12.0", "required provider import is unsatisfied", func(s *fixtureELFSpec) { s.localSymbol = "qmi_device_new" }},
		{"undefined-provider", "libqmi-glib.so.5.12.0", "required provider import is unsatisfied", func(s *fixtureELFSpec) { s.undefinedSymbol = "qmi_device_new" }},
		{"non-executable-provider", "libqmi-glib.so.5.12.0", "required provider import is unsatisfied", func(s *fixtureELFSpec) { s.nonExecutableSymbol = "qmi_device_new" }},
		{"local-export-version-index", "libqmi-glib.so.5.12.0", "required provider import is unsatisfied", func(s *fixtureELFSpec) { s.versionIndices = map[string]uint16{"qmi_device_new": 0} }},
		{"unresolved-export-version-index", "libqmi-glib.so.5.12.0", "required provider import is unsatisfied", func(s *fixtureELFSpec) { s.versionIndices = map[string]uint16{"qmi_device_new": 42} }},
		{"unused-unresolved-export-version-index", "libqmi-glib.so.5.12.0", "required provider import is unsatisfied", func(s *fixtureELFSpec) {
			s.exports["qmi_uncalled"] = ""
			s.versionIndices = map[string]uint16{"qmi_uncalled": 42}
		}},
		{"hidden-import-version-index", "libssc.so.2", "required provider import is unsatisfied", func(s *fixtureELFSpec) { s.versionIndices = map[string]uint16{"qmi_device_new": 0x8001} }},
		{"unresolved-import-version-index", "libssc.so.2", "required provider import is unsatisfied", func(s *fixtureELFSpec) { s.versionIndices = map[string]uint16{"qmi_device_new": 42} }},
		{"object-import", "libssc.so.2", "required provider import is unsatisfied", func(s *fixtureELFSpec) { s.objectSymbol = "qmi_device_new" }},
		{"weak-import", "libssc.so.2", "required provider import is unsatisfied", func(s *fixtureELFSpec) { s.weakSymbol = "qmi_device_new" }},
		{"local-import", "libssc.so.2", "required provider import is unsatisfied", func(s *fixtureELFSpec) { s.localSymbol = "qmi_device_new" }},
		{"hidden-import-visibility", "libssc.so.2", "required provider import is unsatisfied", func(s *fixtureELFSpec) { s.privateSymbol = "qmi_device_new" }},
		{"ssc-public-version-stays-required", "libssc.so.2", "SSC public export/version missing or changed", func(s *fixtureELFSpec) { s.exports["ssc_sensor_sample_copy"] = "" }},
		{"ssc-import-version-stays-required", "ssccli", "required provider import is unsatisfied", func(s *fixtureELFSpec) { s.imports[0].version = ""; s.imports[0].library = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			files := fixtureQMIContractFiles(t, 0, false)
			files[tc.artifact] = fixtureQMIContractELF(t, tc.artifact, 0, false, tc.mutate)
			if err := validateNativeELFs(files); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("wanted %q, got %v", tc.want, err)
			}
		})
	}
}

// fixtureQMIContractArchive refreshes every identity after substituting current
// unversioned QMI metadata, retaining the existing test-only licence authority.
func fixtureQMIContractArchive(t *testing.T, index uint16, omitProviderVersym bool) (map[string][]byte, map[string]fileRule) {
	t.Helper()
	files, rules := artifactFixture(t)
	for name, data := range fixtureQMIContractFiles(t, index, omitProviderVersym) {
		files[name] = data
	}
	rewriteFixtureReport(t, files, nil)
	return files, rules
}

// TestArtifactUnversionedQMIAcceptance exercises the unchanged archive,
// identity, loader, direct dependency and sealed-result route together.
func TestArtifactUnversionedQMIAcceptance(t *testing.T) {
	for _, index := range []uint16{0, 1} {
		for _, omit := range []bool{false, true} {
			t.Run(fmt.Sprintf("import-%d/provider-versym-%t", index, !omit), func(t *testing.T) {
				files, rules := fixtureQMIContractArchive(t, index, omit)
				v, err := decodeDockerCopy(wrapFixtureCopy(t, writeFixtureTar(t, fixtureMembers(files))), 2, rules)
				if err != nil {
					t.Fatal(err)
				}
				if len(v.Files()) != 16 || len(v.Links()) != 3 || v.Report().Jobs != 2 {
					t.Fatal("unversioned QMI archive lost a required product, alias or job identity")
				}
			})
		}
	}
}

// TestArtifactUnversionedQMIKeepsIdentityAndLoaderRefusals proves that allowing
// this ABI form does not bypass provider hashes or the observed private route.
func TestArtifactUnversionedQMIKeepsIdentityAndLoaderRefusals(t *testing.T) {
	for _, kind := range []string{"member-identity", "provider-identity", "qmi-origin", "ssc-origin", "missing-qmi-loader"} {
		t.Run(kind, func(t *testing.T) {
			files, rules := fixtureQMIContractArchive(t, 0, false)
			want := ""
			switch kind {
			case "member-identity":
				data := files["libqmi-glib.so.5.12.0"]
				shoff := binary.LittleEndian.Uint64(data[40:48])
				text := binary.LittleEndian.Uint64(data[shoff+64+24:])
				data[text] ^= 1
				want = "reported product identity differs"
			case "provider-identity":
				rewriteFixtureReport(t, files, func(r *BuildReport) { r.Providers[0].SHA256 = strings.Repeat("0", 64) })
				want = "reported private provider differs"
			case "qmi-origin", "ssc-origin":
				soname := "libqmi-glib.so.5"
				if kind == "ssc-origin" {
					soname = "libssc.so.2"
				}
				rewriteLoaderFixture(t, files, func(raw map[string]string) {
					raw["ssccli"] = strings.Replace(raw["ssccli"], ProviderDirectory+"/lib/"+soname, "/usr/lib/"+soname, 1)
				})
				want = "raw loader origins differ"
			case "missing-qmi-loader":
				rewriteLoaderFixture(t, files, func(raw map[string]string) {
					row := "\tlibqmi-glib.so.5 => " + ProviderDirectory + "/lib/libqmi-glib.so.5 (0x2345)\n"
					if strings.Count(raw["libssc.so.2"], row) != 1 {
						t.Fatal("QMI loader row is not unique")
					}
					raw["libssc.so.2"] = strings.Replace(raw["libssc.so.2"], row, "", 1)
				})
				want = "raw loader origins differ"
			}
			if _, err := decodeArtifactArchive(writeFixtureTar(t, fixtureMembers(files)), 2, rules); err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("wanted %q, got %v", want, err)
			}
		})
	}
}
