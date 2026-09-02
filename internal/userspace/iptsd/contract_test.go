package iptsd

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestPinnedReleaseFixture validates the complete published release when its
// securely extracted root is supplied by an integration test environment.
func TestPinnedReleaseFixture(t *testing.T) {
	root := os.Getenv("LEXR_TEST_IPTSD_RELEASE_ROOT")
	if root == "" {
		t.Skip("set LEXR_TEST_IPTSD_RELEASE_ROOT to an extracted sp11-iptsd-v1 root")
	}
	release, err := ValidateRelease(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(release.Files) != 28 {
		t.Fatalf("install files = %d, want 28", len(release.Files))
	}
	for _, file := range release.Files {
		if file.Size <= 0 || len(file.SHA256) != 64 || filepath.IsAbs(file.Target) {
			t.Fatalf("invalid install file: %+v", file)
		}
	}
}

// TestFedoraPackageSourceFixture validates the enriched v2 archive profile and
// proves its exact OE template is rendered from authenticated provenance.
func TestFedoraPackageSourceFixture(t *testing.T) {
	root := os.Getenv("LEXR_TEST_FEDORA_IPTSD_RELEASE_ROOT")
	if root == "" {
		t.Skip("set LEXR_TEST_FEDORA_IPTSD_RELEASE_ROOT to an extracted sp11-iptsd-v2 root")
	}
	source, err := ValidateFedoraPackageSource(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(source.Template) != int(fedoraRPMSpecTemplate.size) || source.SourceDateEpoch <= 0 {
		t.Fatalf("Fedora package source identity = template %d bytes, epoch %d", len(source.Template), source.SourceDateEpoch)
	}
	rendered := string(source.RenderedSpec)
	for _, required := range []string{
		"Name:           lexr-sp11-iptsd",
		"Version:        " + Version,
		"export SOURCE_DATE_EPOCH=" + strconv.FormatInt(source.SourceDateEpoch, 10),
		"%changelog",
	} {
		if !strings.Contains(rendered, required) {
			t.Errorf("rendered Fedora RPM spec omits %q", required)
		}
	}
	for _, unresolved := range []string{"@IPTSD_VERSION@", "@SOURCE_DATE_EPOCH@", "@CHANGELOG_DATE@"} {
		if strings.Contains(rendered, unresolved) {
			t.Errorf("rendered Fedora RPM spec retains %s", unresolved)
		}
	}
	mutated := append([]byte(nil), source.Template...)
	mutated[len(mutated)-1] ^= 1
	if _, err := RenderFedoraRPMSpec(mutated, source.SourceDateEpoch); err == nil {
		t.Fatal("unreviewed Fedora RPM template mutation passed rendering")
	}
}

// TestFedoraPackageSourceRejectsLegacyProfile proves portable compatibility
// does not let the narrower v1 integration authorise a Fedora RPM build.
func TestFedoraPackageSourceRejectsLegacyProfile(t *testing.T) {
	root := os.Getenv("LEXR_TEST_FEDORA_IPTSD_RELEASE_ROOT")
	if root == "" {
		t.Skip("set LEXR_TEST_FEDORA_IPTSD_RELEASE_ROOT to an extracted sp11-iptsd-v2 root")
	}
	legacyRoot := copyTree(t, root)
	integrationRoot := filepath.Join(legacyRoot, filepath.FromSlash(IntegrationRelative))
	if err := os.RemoveAll(filepath.Join(integrationRoot, "packaging", "fedora")); err != nil {
		t.Fatal(err)
	}
	readme, err := os.ReadFile(filepath.Join("testdata", "sp11-iptsd-lexr-readme.fixture"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(integrationRoot, "README.md"), readme, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateRelease(legacyRoot); err != nil {
		t.Fatalf("legacy portable profile no longer validates: %v", err)
	}
	if _, err := ValidateFedoraPackageSource(legacyRoot); err == nil {
		t.Fatal("legacy portable profile authorised a Fedora RPM build")
	}
}

// TestSourceDateEpochValueRejectsNonCanonicalValues keeps archive-controlled
// provenance from becoming syntax or ambiguous metadata in the rendered spec.
func TestSourceDateEpochValueRejectsNonCanonicalValues(t *testing.T) {
	if got, err := sourceDateEpochValue(map[string]string{"SOURCE_DATE_EPOCH": "1767049547"}); err != nil || got != 1767049547 {
		t.Fatalf("canonical source epoch = %d, %v", got, err)
	}
	for _, raw := range []string{"", "0", "-1", "+1", "01", " 1", "1 ", "9223372036854775808"} {
		t.Run(strconv.Quote(raw), func(t *testing.T) {
			if _, err := sourceDateEpochValue(map[string]string{"SOURCE_DATE_EPOCH": raw}); err == nil {
				t.Fatalf("non-canonical SOURCE_DATE_EPOCH %q passed validation", raw)
			}
		})
	}
}

// TestCurrentLexrDocumentationIdentityRemainsInstallable proves that the
// current Lexr-era guidance matches the primary compiled contract.
func TestCurrentLexrDocumentationIdentityRemainsInstallable(t *testing.T) {
	testDocumentationIdentity(t, "sp11-iptsd-lexr-readme.fixture", "af2f4657a15125a1288f2453162954bbf3ee958c2935935d28302e503a45fdf3", 3540)
}

// TestPublishedV1DocumentationAlternativeRemainsInstallable proves that the
// immutable release's historical README identity remains accepted.
func TestPublishedV1DocumentationAlternativeRemainsInstallable(t *testing.T) {
	testDocumentationIdentity(t, "sp11-iptsd-v1-readme.fixture", "69a92f448f64f3d16b59770869bb7a5411470dd153770765fce97f68c41bd687", 3204)
}

// testDocumentationIdentity proves that one reviewed README is accepted,
// propagated into the copy plan, and does not admit an unreviewed mutation.
func testDocumentationIdentity(t *testing.T, fixtureName, expectedDigest string, expectedSize int64) {
	t.Helper()
	documentation, err := os.ReadFile(filepath.Join("testdata", fixtureName))
	if err != nil {
		t.Fatal(err)
	}
	integrationRoot := t.TempDir()
	readmePath := filepath.Join(integrationRoot, "README.md")
	if err := os.WriteFile(readmePath, documentation, 0o644); err != nil {
		t.Fatal(err)
	}

	current := integrationFileSpecification(t, "README.md")
	alternatives := append([]fileSpec{current}, legacyIntegrationAlternatives["README.md"]...)
	matched, err := validateFileAlternatives(readmePath, alternatives)
	if err != nil {
		t.Fatalf("validate reviewed documentation fixture: %v", err)
	}
	if matched.sha256 != expectedDigest || matched.size != expectedSize {
		t.Fatalf("matched README identity = %+v", matched)
	}
	manifest := map[string]fileSpec{"README.md": matched}
	planned := integrationInstallFile(integrationRoot, manifest, "README.md", "usr/local/share/doc/sp11-iptsd/README.md", 0o644)
	if planned.SHA256 != matched.sha256 || planned.Size != matched.size {
		t.Fatalf("planned README identity = %s/%d, want %s/%d", planned.SHA256, planned.Size, matched.sha256, matched.size)
	}

	if err := os.WriteFile(readmePath, append(documentation, '!'), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := validateFileAlternatives(readmePath, alternatives); err == nil || !strings.Contains(err.Error(), "approved contract") {
		t.Fatalf("unreviewed README error = %v", err)
	}
}

// integrationFileSpecification returns one compiled integration identity so
// focused fixture tests do not depend on the separate OE repository checkout.
func integrationFileSpecification(t *testing.T, name string) fileSpec {
	t.Helper()
	for _, specification := range releaseIntegrationFiles {
		if specification.path == name {
			return specification
		}
	}
	t.Fatalf("compiled IPTSD integration omits %q", name)
	return fileSpec{}
}

// TestPayloadRejectsMutationAndUnexpectedShape verifies that binary changes,
// extra files, and payload links cannot survive the fixed checksum authority.
func TestPayloadRejectsMutationAndUnexpectedShape(t *testing.T) {
	fixture := os.Getenv("LEXR_TEST_IPTSD_RELEASE_ROOT")
	if fixture == "" {
		t.Skip("set LEXR_TEST_IPTSD_RELEASE_ROOT to exercise payload mutations")
	}
	source := filepath.Join(fixture, filepath.FromSlash(PayloadRelative))
	integration := filepath.Join(fixture, filepath.FromSlash(IntegrationRelative))
	t.Run("mutated binary", func(t *testing.T) {
		root := copyTree(t, source)
		path := filepath.Join(root, "bin", "sp11-iptsd")
		file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte{0}); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := ValidatePayload(root, integration); err == nil {
			t.Fatal("mutated binary passed validation")
		}
	})
	t.Run("unexpected file", func(t *testing.T) {
		root := copyTree(t, source)
		if err := os.WriteFile(filepath.Join(root, "unexpected"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := ValidatePayload(root, integration); err == nil || !strings.Contains(err.Error(), "unexpected") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("symlinked licence", func(t *testing.T) {
		root := copyTree(t, source)
		path := filepath.Join(root, "licenses", "LICENSE.iptsd")
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("LICENSE.integration", path); err != nil {
			t.Fatal(err)
		}
		if _, err := ValidatePayload(root, integration); err == nil || !strings.Contains(err.Error(), "symbolic link") {
			t.Fatalf("error = %v", err)
		}
	})
}

// TestPayloadManifestRejectsHostilePathsAndDuplicates verifies the parser's
// canonical relative-path and unique-entry boundaries independently of hashes.
func TestPayloadManifestRejectsHostilePathsAndDuplicates(t *testing.T) {
	digest := hex.EncodeToString(make([]byte, 32))
	for name, data := range map[string]string{
		"traversal": digest + "  ./../escape\n",
		"absolute":  digest + "  .//escape\n",
		"duplicate": digest + "  ./one\n" + digest + "  ./one\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parsePayloadManifest([]byte(data)); err == nil {
				t.Fatal("hostile payload manifest passed")
			}
		})
	}
}

// TestAArch64ELFRejectsWrongMachineAndLinks verifies architecture and no-link
// executable validation before binaries can enter an installation plan.
func TestAArch64ELFRejectsWrongMachineAndLinks(t *testing.T) {
	header := make([]byte, 20)
	copy(header, []byte{0x7f, 'E', 'L', 'F', 2, 1})
	header[18] = 62
	path := filepath.Join(t.TempDir(), "binary")
	if err := os.WriteFile(path, header, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := validateAArch64ELF(path); err == nil || !strings.Contains(err.Error(), "AArch64") {
		t.Fatalf("error = %v", err)
	}
	link := filepath.Join(filepath.Dir(path), "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if err := validateAArch64ELF(link); err == nil || !strings.Contains(err.Error(), "non-symlink") {
		t.Fatalf("error = %v", err)
	}
}

// TestIntegrationRejectsLinksAndMutation verifies that released human text and
// templates cannot be substituted after the compiled contract is reviewed.
func TestIntegrationRejectsLinksAndMutation(t *testing.T) {
	fixture := os.Getenv("LEXR_TEST_IPTSD_RELEASE_ROOT")
	if fixture == "" {
		t.Skip("set LEXR_TEST_IPTSD_RELEASE_ROOT to exercise release mutations")
	}
	source := filepath.Join(fixture, filepath.FromSlash(IntegrationRelative))
	t.Run("mutated template", func(t *testing.T) {
		root := copyTree(t, source)
		path := filepath.Join(root, "packaging", "sp11-iptsd@.service.in")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(data, '#'), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := ValidateIntegration(root); err == nil || !strings.Contains(err.Error(), "size") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("symlinked config", func(t *testing.T) {
		root := copyTree(t, source)
		path := filepath.Join(root, "config", "surface-pro-11-0c80.conf")
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("surface-pro-11-0c83.conf", path); err != nil {
			t.Fatal(err)
		}
		if err := ValidateIntegration(root); err == nil || !strings.Contains(err.Error(), "symbolic link") {
			t.Fatalf("error = %v", err)
		}
	})
}

// copyTree copies a private test fixture without retaining symbolic links.
func copyTree(t *testing.T, source string) string {
	t.Helper()
	destination := t.TempDir()
	err := filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil || relative == "." {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.Mkdir(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
	if err != nil {
		t.Fatal(err)
	}
	return destination
}
