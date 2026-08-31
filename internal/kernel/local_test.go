package kernel

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// These fixtures describe one internally consistent Surface kernel package pair
// used throughout local bundle discovery tests.
const (
	// testLocalABI is the Surface-specific ABI encoded in both fixture filenames.
	testLocalABI = "7.2.0-jg-0sp11v19-qcom-x1e"
	// testLocalVersion is the matching Debian package version for the fixture ABI.
	testLocalVersion = "7.2.0-jg-0sp11v19"
)

// TestDiscoverLocalBundleWithoutChecksumManifest verifies local discovery picks
// only the Surface runtime pair, calculates metadata, and marks it unverified.
func TestDiscoverLocalBundleWithoutChecksumManifest(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	imageName, modulesName := writeLocalPair(t, directory, testLocalABI, testLocalVersion)
	writeLocalFile(t, filepath.Join(directory, "README.txt"), "ignored")
	writeLocalFile(t, filepath.Join(directory, "linux-headers-"+testLocalABI+"_"+testLocalVersion+"_arm64.deb"), "headers")
	writeLocalFile(t, filepath.Join(directory, "linux-image-6.8.0-generic_6.8.0_arm64.deb"), "generic")

	bundle, err := DiscoverLocalBundle(directory)
	if err != nil {
		t.Fatalf("DiscoverLocalBundle() error = %v", err)
	}

	if bundle.SchemaVersion != BundleSchemaVersion {
		t.Fatalf("SchemaVersion = %d, want %d", bundle.SchemaVersion, BundleSchemaVersion)
	}
	if bundle.Release != localReleasePrefix+testLocalABI {
		t.Fatalf("Release = %q, want %q", bundle.Release, localReleasePrefix+testLocalABI)
	}
	if bundle.Repository != "" {
		t.Fatalf("Repository = %q, want empty for local bundle", bundle.Repository)
	}
	if bundle.ABI != testLocalABI || bundle.Version != testLocalVersion || bundle.Architecture != "arm64" {
		t.Fatalf("derived ABI/version/architecture = %q/%q/%q", bundle.ABI, bundle.Version, bundle.Architecture)
	}
	if len(bundle.DeviceTrees) != 2 {
		t.Fatalf("DeviceTrees length = %d, want the validated Surface Pro 11 set", len(bundle.DeviceTrees))
	}
	if len(bundle.Packages) != 2 {
		t.Fatalf("Packages length = %d, want 2", len(bundle.Packages))
	}

	for _, expectation := range []struct {
		role PackageRole
		name string
		data string
	}{
		{role: RoleImage, name: imageName, data: "image package"},
		{role: RoleModules, name: modulesName, data: "modules package"},
	} {
		pkg, ok := bundle.Package(expectation.role)
		if !ok {
			t.Errorf("Package(%q) not found", expectation.role)
			continue
		}
		if pkg.Name != expectation.name {
			t.Errorf("Package(%q).Name = %q, want %q", expectation.role, pkg.Name, expectation.name)
		}
		if !filepath.IsAbs(pkg.Path) || pkg.Path != filepath.Join(directory, expectation.name) {
			t.Errorf("Package(%q).Path = %q, want absolute discovered path", expectation.role, pkg.Path)
		}
		if pkg.SHA256 != localDigest(expectation.data) {
			t.Errorf("Package(%q).SHA256 = %q, want calculated digest", expectation.role, pkg.SHA256)
		}
		if pkg.Size != int64(len(expectation.data)) {
			t.Errorf("Package(%q).Size = %d, want %d", expectation.role, pkg.Size, len(expectation.data))
		}
		if pkg.Verified {
			t.Errorf("Package(%q).Verified = true without %s", expectation.role, localChecksumManifest)
		}
	}
}

// TestDiscoverLocalBundleWithOptionsIncludesMatchingHeaders verifies complete
// local installs select every exact package for the runtime ABI and version.
func TestDiscoverLocalBundleWithOptionsIncludesMatchingHeaders(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	imageName, modulesName := writeLocalPair(t, directory, testLocalABI, testLocalVersion)
	headersName, commonName := writeLocalHeaderPair(t, directory, testLocalABI, testLocalVersion)
	contents := map[string]string{
		imageName:   "image package",
		modulesName: "modules package",
		headersName: "headers package",
		commonName:  "common headers package",
	}
	manifestLines := make([]string, 0, len(contents))
	for name, content := range contents {
		manifestLines = append(manifestLines, localDigest(content)+"  "+name)
	}
	writeLocalFile(t, filepath.Join(directory, localChecksumManifest), strings.Join(manifestLines, "\n")+"\n")

	bundle, err := DiscoverLocalBundleWithOptions(directory, LocalBundleOptions{PackageSet: LocalPackageSetAll})
	if err != nil {
		t.Fatalf("DiscoverLocalBundleWithOptions() error = %v", err)
	}
	if len(bundle.Packages) != 4 {
		t.Fatalf("Packages length = %d, want 4", len(bundle.Packages))
	}
	for _, role := range []PackageRole{RoleImage, RoleModules, RoleHeaders, RoleCommonHeaders} {
		pkg, ok := bundle.Package(role)
		if !ok {
			t.Errorf("Package(%q) not found", role)
			continue
		}
		if !pkg.Verified {
			t.Errorf("Package(%q).Verified = false with matching %s", role, localChecksumManifest)
		}
	}
}

// TestDiscoverLocalBundleWithOptionsRequiresCoherentHeaders verifies exact
// matching headers are optional as a pair but never partially selected.
func TestDiscoverLocalBundleWithOptionsRequiresCoherentHeaders(t *testing.T) {
	t.Parallel()

	t.Run("matching ABI headers only", func(t *testing.T) {
		t.Parallel()
		directory := t.TempDir()
		writeLocalPair(t, directory, testLocalABI, testLocalVersion)
		headersName, _ := localHeaderPackageNames(testLocalABI, testLocalVersion)
		writeLocalFile(t, filepath.Join(directory, headersName), "headers")

		_, err := DiscoverLocalBundleWithOptions(directory, LocalBundleOptions{PackageSet: LocalPackageSetAll})
		assertLocalErrorContains(t, err, "complete pair", "linux-qcom-x1e-headers")
	})

	t.Run("matching common headers only", func(t *testing.T) {
		t.Parallel()
		directory := t.TempDir()
		writeLocalPair(t, directory, testLocalABI, testLocalVersion)
		_, commonName := localHeaderPackageNames(testLocalABI, testLocalVersion)
		writeLocalFile(t, filepath.Join(directory, commonName), "common headers")

		_, err := DiscoverLocalBundleWithOptions(directory, LocalBundleOptions{PackageSet: LocalPackageSetAll})
		assertLocalErrorContains(t, err, "complete pair", "linux-headers")
	})

	t.Run("both matching headers absent but declared", func(t *testing.T) {
		t.Parallel()
		directory := t.TempDir()
		imageName, modulesName := writeLocalPair(t, directory, testLocalABI, testLocalVersion)
		headersName, commonName := localHeaderPackageNames(testLocalABI, testLocalVersion)
		manifest := fmt.Sprintf(
			"%s  %s\n%s  %s\n%s  %s\n%s  %s\n",
			localDigest("image package"), imageName,
			localDigest("modules package"), modulesName,
			localDigest("headers package"), headersName,
			localDigest("common headers package"), commonName,
		)
		writeLocalFile(t, filepath.Join(directory, localChecksumManifest), manifest)

		_, err := DiscoverLocalBundleWithOptions(directory, LocalBundleOptions{PackageSet: LocalPackageSetAll})
		assertLocalErrorContains(t, err, "complete pair", headersName, commonName)
	})

	t.Run("one matching header declared while both are absent", func(t *testing.T) {
		t.Parallel()
		directory := t.TempDir()
		imageName, modulesName := writeLocalPair(t, directory, testLocalABI, testLocalVersion)
		headersName, commonName := localHeaderPackageNames(testLocalABI, testLocalVersion)
		manifest := fmt.Sprintf(
			"%s  %s\n%s  %s\n%s  %s\n",
			localDigest("image package"), imageName,
			localDigest("modules package"), modulesName,
			localDigest("headers package"), headersName,
		)
		writeLocalFile(t, filepath.Join(directory, localChecksumManifest), manifest)

		_, err := DiscoverLocalBundleWithOptions(directory, LocalBundleOptions{PackageSet: LocalPackageSetAll})
		assertLocalErrorContains(t, err, "complete pair", headersName, commonName)
	})

	t.Run("runtime selection ignores declared headers deliberately", func(t *testing.T) {
		t.Parallel()
		directory := t.TempDir()
		imageName, modulesName := writeLocalPair(t, directory, testLocalABI, testLocalVersion)
		headersName, commonName := localHeaderPackageNames(testLocalABI, testLocalVersion)
		manifest := fmt.Sprintf(
			"%s  %s\n%s  %s\n%s  %s\n%s  %s\n",
			localDigest("image package"), imageName,
			localDigest("modules package"), modulesName,
			localDigest("headers package"), headersName,
			localDigest("common headers package"), commonName,
		)
		writeLocalFile(t, filepath.Join(directory, localChecksumManifest), manifest)

		bundle, err := DiscoverLocalBundleWithOptions(directory, LocalBundleOptions{PackageSet: LocalPackageSetRuntime})
		if err != nil {
			t.Fatalf("DiscoverLocalBundleWithOptions() error = %v", err)
		}
		if len(bundle.Packages) != 2 {
			t.Fatalf("Packages length = %d, want runtime pair", len(bundle.Packages))
		}
	})

	t.Run("other version is unrelated", func(t *testing.T) {
		t.Parallel()
		directory := t.TempDir()
		writeLocalPair(t, directory, testLocalABI, testLocalVersion)
		writeLocalHeaderPair(t, directory, testLocalABI, testLocalVersion+".1")

		bundle, err := DiscoverLocalBundleWithOptions(directory, LocalBundleOptions{PackageSet: LocalPackageSetAll})
		if err != nil {
			t.Fatalf("DiscoverLocalBundleWithOptions() error = %v", err)
		}
		if len(bundle.Packages) != 2 {
			t.Fatalf("Packages length = %d, want runtime pair", len(bundle.Packages))
		}
	})
}

// TestDiscoverLocalBundleManifestPackageSet verifies a Lexr-emitted bundle
// declaration distinguishes an intentional runtime download from a complete
// native build without weakening explicit runtime selection.
func TestDiscoverLocalBundleManifestPackageSet(t *testing.T) {
	t.Parallel()

	t.Run("downloaded runtime manifest ignores undeployed header assets", func(t *testing.T) {
		t.Parallel()
		directory := t.TempDir()
		writeLocalPair(t, directory, testLocalABI, testLocalVersion)
		writeLocalCompleteChecksumManifest(t, directory, testLocalABI, testLocalVersion)
		writeLocalBundleManifest(t, directory, testLocalABI, testLocalVersion, false)

		bundle, err := DiscoverLocalBundleWithOptions(directory, LocalBundleOptions{PackageSet: LocalPackageSetAll})
		if err != nil {
			t.Fatalf("DiscoverLocalBundleWithOptions() error = %v", err)
		}
		if len(bundle.Packages) != 2 {
			t.Fatalf("Packages length = %d, want intentional runtime pair", len(bundle.Packages))
		}
		for _, item := range bundle.Packages {
			if !item.Verified {
				t.Errorf("runtime package %s was not checksum verified", item.Name)
			}
		}
	})

	t.Run("complete manifest remains fail closed when headers vanish", func(t *testing.T) {
		t.Parallel()
		directory := t.TempDir()
		writeLocalPair(t, directory, testLocalABI, testLocalVersion)
		writeLocalCompleteChecksumManifest(t, directory, testLocalABI, testLocalVersion)
		writeLocalBundleManifest(t, directory, testLocalABI, testLocalVersion, true)
		headersName, commonName := localHeaderPackageNames(testLocalABI, testLocalVersion)

		_, err := DiscoverLocalBundleWithOptions(directory, LocalBundleOptions{PackageSet: LocalPackageSetAll})
		assertLocalErrorContains(t, err, "complete pair", headersName, commonName)
	})

	t.Run("runtime override accepts a complete manifest without local headers", func(t *testing.T) {
		t.Parallel()
		directory := t.TempDir()
		writeLocalPair(t, directory, testLocalABI, testLocalVersion)
		writeLocalCompleteChecksumManifest(t, directory, testLocalABI, testLocalVersion)
		writeLocalBundleManifest(t, directory, testLocalABI, testLocalVersion, true)

		bundle, err := DiscoverLocalBundleWithOptions(directory, LocalBundleOptions{PackageSet: LocalPackageSetRuntime})
		if err != nil {
			t.Fatalf("DiscoverLocalBundleWithOptions() error = %v", err)
		}
		if len(bundle.Packages) != 2 {
			t.Fatalf("Packages length = %d, want explicit runtime pair", len(bundle.Packages))
		}
	})

	t.Run("runtime manifest bounds stray matching headers", func(t *testing.T) {
		t.Parallel()
		directory := t.TempDir()
		writeLocalPair(t, directory, testLocalABI, testLocalVersion)
		writeLocalHeaderPair(t, directory, testLocalABI, testLocalVersion)
		writeLocalCompleteChecksumManifest(t, directory, testLocalABI, testLocalVersion)
		writeLocalBundleManifest(t, directory, testLocalABI, testLocalVersion, false)

		bundle, err := DiscoverLocalBundleWithOptions(directory, LocalBundleOptions{PackageSet: LocalPackageSetAll})
		if err != nil {
			t.Fatalf("DiscoverLocalBundleWithOptions() error = %v", err)
		}
		if len(bundle.Packages) != 2 {
			t.Fatalf("Packages length = %d, want manifest-bound runtime pair", len(bundle.Packages))
		}
	})
}

// TestDiscoverLocalBundleRejectsUnsafeBundleManifest keeps the optional local
// package-set authority bounded, strict, regular, and semantically coherent.
func TestDiscoverLocalBundleRejectsUnsafeBundleManifest(t *testing.T) {
	t.Parallel()

	t.Run("malformed JSON", func(t *testing.T) {
		t.Parallel()
		directory := t.TempDir()
		writeLocalPair(t, directory, testLocalABI, testLocalVersion)
		writeLocalFile(t, filepath.Join(directory, localBundleManifest), "{not-json")

		_, err := DiscoverLocalBundleWithOptions(directory, LocalBundleOptions{PackageSet: LocalPackageSetAll})
		assertLocalErrorContains(t, err, localBundleManifest, "decode")
	})

	t.Run("unknown field", func(t *testing.T) {
		t.Parallel()
		directory := t.TempDir()
		writeLocalPair(t, directory, testLocalABI, testLocalVersion)
		writeLocalFile(t, filepath.Join(directory, localBundleManifest), `{"schema_version":1,"unknown":true}`)

		_, err := DiscoverLocalBundleWithOptions(directory, LocalBundleOptions{PackageSet: LocalPackageSetAll})
		assertLocalErrorContains(t, err, localBundleManifest, "unknown field")
	})

	t.Run("trailing JSON value", func(t *testing.T) {
		t.Parallel()
		directory := t.TempDir()
		writeLocalPair(t, directory, testLocalABI, testLocalVersion)
		writeLocalBundleManifest(t, directory, testLocalABI, testLocalVersion, false)
		file, err := os.OpenFile(filepath.Join(directory, localBundleManifest), os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.WriteString("{}\n"); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}

		_, err = DiscoverLocalBundleWithOptions(directory, LocalBundleOptions{PackageSet: LocalPackageSetAll})
		assertLocalErrorContains(t, err, localBundleManifest, "trailing")
	})

	t.Run("oversized", func(t *testing.T) {
		t.Parallel()
		directory := t.TempDir()
		writeLocalPair(t, directory, testLocalABI, testLocalVersion)
		writeLocalFile(t, filepath.Join(directory, localBundleManifest), strings.Repeat("x", int(maximumLocalBundleManifestBytes)+1))

		_, err := DiscoverLocalBundleWithOptions(directory, LocalBundleOptions{PackageSet: LocalPackageSetAll})
		assertLocalErrorContains(t, err, localBundleManifest, "no larger")
	})

	t.Run("directory", func(t *testing.T) {
		t.Parallel()
		directory := t.TempDir()
		writeLocalPair(t, directory, testLocalABI, testLocalVersion)
		if err := os.Mkdir(filepath.Join(directory, localBundleManifest), 0o700); err != nil {
			t.Fatal(err)
		}

		_, err := DiscoverLocalBundleWithOptions(directory, LocalBundleOptions{PackageSet: LocalPackageSetAll})
		assertLocalErrorContains(t, err, localBundleManifest, "regular file")
	})

	t.Run("symbolic link", func(t *testing.T) {
		t.Parallel()
		if runtime.GOOS == "windows" {
			t.Skip("symbolic link creation is not reliably available on Windows")
		}
		directory := t.TempDir()
		writeLocalPair(t, directory, testLocalABI, testLocalVersion)
		target := filepath.Join(directory, "bundle-target.json")
		writeLocalFile(t, target, "{}")
		if err := os.Symlink(target, filepath.Join(directory, localBundleManifest)); err != nil {
			t.Fatal(err)
		}

		_, err := DiscoverLocalBundleWithOptions(directory, LocalBundleOptions{PackageSet: LocalPackageSetAll})
		assertLocalErrorContains(t, err, localBundleManifest, "symbolic link")
	})

	t.Run("runtime identity mismatch", func(t *testing.T) {
		t.Parallel()
		directory := t.TempDir()
		writeLocalPair(t, directory, testLocalABI, testLocalVersion)
		writeLocalBundleManifest(t, directory, "7.3.0-other-qcom-x1e", "7.3.0-other", false)

		_, err := DiscoverLocalBundleWithOptions(directory, LocalBundleOptions{PackageSet: LocalPackageSetAll})
		assertLocalErrorContains(t, err, localBundleManifest, "does not match runtime ABI")
	})

	t.Run("package bytes disagree without checksum manifest", func(t *testing.T) {
		t.Parallel()
		directory := t.TempDir()
		imageName, _ := writeLocalPair(t, directory, testLocalABI, testLocalVersion)
		writeLocalBundleManifest(t, directory, testLocalABI, testLocalVersion, false)
		writeLocalFile(t, filepath.Join(directory, imageName), "modified image package")

		_, err := DiscoverLocalBundleWithOptions(directory, LocalBundleOptions{PackageSet: LocalPackageSetAll})
		assertLocalErrorContains(t, err, imageName, localBundleManifest, "bytes disagree")
	})
}

// TestDiscoverLocalBundleWithOptionsRejectsUnknownPackageSet keeps local
// selection a closed policy instead of accepting caller-supplied expressions.
func TestDiscoverLocalBundleWithOptionsRejectsUnknownPackageSet(t *testing.T) {
	t.Parallel()

	_, err := DiscoverLocalBundleWithOptions(t.TempDir(), LocalBundleOptions{PackageSet: LocalPackageSet("linux-.*")})
	assertLocalErrorContains(t, err, "package set", "all", "runtime")
}

// TestDiscoverLocalBundleVerifiesChecksumManifest verifies both selected runtime
// packages become verified when SHA256SUMS contains matching digests.
func TestDiscoverLocalBundleVerifiesChecksumManifest(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	imageName, modulesName := writeLocalPair(t, directory, testLocalABI, testLocalVersion)
	manifest := fmt.Sprintf(
		"%s  %s\n%s *%s\n%s  unrelated-release-note.txt\n",
		strings.ToUpper(localDigest("image package")),
		imageName,
		localDigest("modules package"),
		modulesName,
		strings.Repeat("c", 64),
	)
	writeLocalFile(t, filepath.Join(directory, localChecksumManifest), manifest)

	bundle, err := DiscoverLocalBundle(directory)
	if err != nil {
		t.Fatalf("DiscoverLocalBundle() error = %v", err)
	}
	for _, role := range []PackageRole{RoleImage, RoleModules} {
		pkg, ok := bundle.Package(role)
		if !ok {
			t.Fatalf("Package(%q) not found", role)
		}
		if !pkg.Verified {
			t.Errorf("Package(%q).Verified = false with matching %s", role, localChecksumManifest)
		}
	}
}

// TestDiscoverLocalBundleRejectsPackageSelectionProblems exercises missing,
// ambiguous, mismatched, generic, and non-file runtime package candidates.
func TestDiscoverLocalBundleRejectsPackageSelectionProblems(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(*testing.T, string)
		want  []string
	}{
		{
			name: "missing image",
			setup: func(t *testing.T, directory string) {
				writeLocalRuntimePackage(t, directory, RoleModules, testLocalABI, testLocalVersion, "modules")
			},
			want: []string{"exactly one", "linux-image", "found none"},
		},
		{
			name: "missing modules",
			setup: func(t *testing.T, directory string) {
				writeLocalRuntimePackage(t, directory, RoleImage, testLocalABI, testLocalVersion, "image")
			},
			want: []string{"exactly one", "linux-modules", "found none"},
		},
		{
			name: "generic ARM packages are not Surface packages",
			setup: func(t *testing.T, directory string) {
				writeLocalRuntimePackage(t, directory, RoleImage, "6.8.0-generic", "6.8.0", "image")
				writeLocalRuntimePackage(t, directory, RoleModules, "6.8.0-generic", "6.8.0", "modules")
			},
			want: []string{"Surface Pro 11", "linux-image", "found none"},
		},
		{
			name: "ambiguous images",
			setup: func(t *testing.T, directory string) {
				writeLocalPair(t, directory, testLocalABI, testLocalVersion)
				writeLocalRuntimePackage(t, directory, RoleImage, "7.3.0-sp11-qcom-x1e", "7.3.0-sp11", "second image")
			},
			want: []string{"ambiguous", "linux-image", "7.2.0", "7.3.0"},
		},
		{
			name: "ambiguous modules",
			setup: func(t *testing.T, directory string) {
				writeLocalPair(t, directory, testLocalABI, testLocalVersion)
				writeLocalRuntimePackage(t, directory, RoleModules, "7.3.0-sp11-qcom-x1e", "7.3.0-sp11", "second modules")
			},
			want: []string{"ambiguous", "linux-modules", "7.2.0", "7.3.0"},
		},
		{
			name: "ABI mismatch",
			setup: func(t *testing.T, directory string) {
				writeLocalRuntimePackage(t, directory, RoleImage, testLocalABI, testLocalVersion, "image")
				writeLocalRuntimePackage(t, directory, RoleModules, "7.2.1-jg-0sp11v20-qcom-x1e", testLocalVersion, "modules")
			},
			want: []string{"ABI mismatch", testLocalABI, "7.2.1-jg-0sp11v20-qcom-x1e"},
		},
		{
			name: "package version mismatch",
			setup: func(t *testing.T, directory string) {
				writeLocalRuntimePackage(t, directory, RoleImage, testLocalABI, testLocalVersion, "image")
				writeLocalRuntimePackage(t, directory, RoleModules, testLocalABI, testLocalVersion+".1", "modules")
			},
			want: []string{"package version mismatch", testLocalVersion, testLocalVersion + ".1"},
		},
		{
			name: "candidate directory is not a package",
			setup: func(t *testing.T, directory string) {
				imageName, _ := localPackageNames(testLocalABI, testLocalVersion)
				if err := os.Mkdir(filepath.Join(directory, imageName), 0o755); err != nil {
					t.Fatalf("os.Mkdir(candidate) error = %v", err)
				}
				writeLocalRuntimePackage(t, directory, RoleModules, testLocalABI, testLocalVersion, "modules")
			},
			want: []string{"kernel package", "is not a regular file"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			directory := t.TempDir()
			test.setup(t, directory)
			_, err := DiscoverLocalBundle(directory)
			assertLocalErrorContains(t, err, test.want...)
		})
	}
}

// TestDiscoverLocalBundleRejectsSymbolicLinkPackage verifies local discovery
// will not trust a runtime package whose path can be redirected after inspection.
func TestDiscoverLocalBundleRejectsSymbolicLinkPackage(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("symbolic link creation is not reliably available on Windows")
	}

	directory := t.TempDir()
	imageName, _ := localPackageNames(testLocalABI, testLocalVersion)
	target := filepath.Join(directory, "image-target")
	writeLocalFile(t, target, "image")
	if err := os.Symlink(target, filepath.Join(directory, imageName)); err != nil {
		t.Fatalf("os.Symlink(package) error = %v", err)
	}
	writeLocalRuntimePackage(t, directory, RoleModules, testLocalABI, testLocalVersion, "modules")

	_, err := DiscoverLocalBundle(directory)
	assertLocalErrorContains(t, err, imageName, "symbolic link")
}

// TestDiscoverLocalBundleChecksumManifestValidation verifies malformed, unsafe,
// incomplete, duplicated, and mismatching SHA256SUMS entries are rejected.
func TestDiscoverLocalBundleChecksumManifestValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		manifest func(string, string) string
		want     []string
	}{
		{
			name: "empty",
			manifest: func(_, _ string) string {
				return ""
			},
			want: []string{localChecksumManifest, "empty"},
		},
		{
			name: "malformed line",
			manifest: func(_, _ string) string {
				return "only-one-field\n"
			},
			want: []string{"SHA256SUMS:1", "expected '<sha256>  <filename>'"},
		},
		{
			name: "short digest",
			manifest: func(_, imageName string) string {
				return "abcd  " + imageName + "\n"
			},
			want: []string{"SHA256SUMS:1", "64 hexadecimal"},
		},
		{
			name: "non hexadecimal digest",
			manifest: func(_, imageName string) string {
				return strings.Repeat("z", 64) + "  " + imageName + "\n"
			},
			want: []string{"SHA256SUMS:1", "invalid SHA-256"},
		},
		{
			name: "unsafe parent path",
			manifest: func(_, _ string) string {
				return strings.Repeat("a", 64) + "  ../outside.deb\n"
			},
			want: []string{"SHA256SUMS:1", "unsafe filename"},
		},
		{
			name: "unsafe cross-platform path",
			manifest: func(_, _ string) string {
				return strings.Repeat("a", 64) + "  nested\\outside.deb\n"
			},
			want: []string{"SHA256SUMS:1", "unsafe filename"},
		},
		{
			name: "duplicate entry",
			manifest: func(_, imageName string) string {
				return strings.Repeat("a", 64) + "  " + imageName + "\n" +
					strings.Repeat("b", 64) + "  " + imageName + "\n"
			},
			want: []string{"SHA256SUMS:2", "duplicate entry"},
		},
		{
			name: "missing image coverage",
			manifest: func(modulesName, _ string) string {
				return localDigest("modules package") + "  " + modulesName + "\n"
			},
			want: []string{localChecksumManifest, "does not cover", "linux-image"},
		},
		{
			name: "image checksum mismatch",
			manifest: func(modulesName, imageName string) string {
				return strings.Repeat("0", 64) + "  " + imageName + "\n" +
					localDigest("modules package") + "  " + modulesName + "\n"
			},
			want: []string{"SHA-256 mismatch", "linux-image", "expected", "got"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			directory := t.TempDir()
			imageName, modulesName := writeLocalPair(t, directory, testLocalABI, testLocalVersion)
			writeLocalFile(t, filepath.Join(directory, localChecksumManifest), test.manifest(modulesName, imageName))

			_, err := DiscoverLocalBundle(directory)
			assertLocalErrorContains(t, err, test.want...)
		})
	}
}

// TestDiscoverLocalBundleRejectsInvalidChecksumManifestFile verifies SHA256SUMS
// must itself be a regular file rather than a directory or symbolic link.
func TestDiscoverLocalBundleRejectsInvalidChecksumManifestFile(t *testing.T) {
	t.Parallel()

	t.Run("directory", func(t *testing.T) {
		t.Parallel()

		directory := t.TempDir()
		writeLocalPair(t, directory, testLocalABI, testLocalVersion)
		if err := os.Mkdir(filepath.Join(directory, localChecksumManifest), 0o755); err != nil {
			t.Fatalf("os.Mkdir(SHA256SUMS) error = %v", err)
		}
		_, err := DiscoverLocalBundle(directory)
		assertLocalErrorContains(t, err, localChecksumManifest, "not a regular file")
	})

	t.Run("symbolic link", func(t *testing.T) {
		t.Parallel()
		if runtime.GOOS == "windows" {
			t.Skip("symbolic link creation is not reliably available on Windows")
		}

		directory := t.TempDir()
		writeLocalPair(t, directory, testLocalABI, testLocalVersion)
		target := filepath.Join(directory, "checksums-target")
		writeLocalFile(t, target, "not trusted")
		if err := os.Symlink(target, filepath.Join(directory, localChecksumManifest)); err != nil {
			t.Fatalf("os.Symlink(SHA256SUMS) error = %v", err)
		}
		_, err := DiscoverLocalBundle(directory)
		assertLocalErrorContains(t, err, localChecksumManifest, "symbolic link")
	})
}

// TestDiscoverLocalBundleDirectoryErrors verifies empty, missing, and regular-file
// inputs produce clear errors before package discovery begins.
func TestDiscoverLocalBundleDirectoryErrors(t *testing.T) {
	t.Parallel()

	t.Run("empty", func(t *testing.T) {
		t.Parallel()
		_, err := DiscoverLocalBundle("\t")
		assertLocalErrorContains(t, err, "directory is required")
	})

	t.Run("missing", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "missing")
		_, err := DiscoverLocalBundle(path)
		assertLocalErrorContains(t, err, "inspect directory", "missing")
	})

	t.Run("regular file", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "kernel.deb")
		writeLocalFile(t, path, "not a directory")
		_, err := DiscoverLocalBundle(path)
		assertLocalErrorContains(t, err, "is not a directory")
	})
}

// writeLocalPair creates the matching image and modules fixtures required for a
// valid locally discovered kernel bundle.
func writeLocalPair(t *testing.T, directory, abi, version string) (string, string) {
	t.Helper()

	imageName := writeLocalRuntimePackage(t, directory, RoleImage, abi, version, "image package")
	modulesName := writeLocalRuntimePackage(t, directory, RoleModules, abi, version, "modules package")
	return imageName, modulesName
}

// writeLocalHeaderPair creates the exact ABI-specific and common development
// header fixtures for one runtime ABI and Debian version.
func writeLocalHeaderPair(t *testing.T, directory, abi, version string) (string, string) {
	t.Helper()

	headersName, commonName := localHeaderPackageNames(abi, version)
	writeLocalFile(t, filepath.Join(directory, headersName), "headers package")
	writeLocalFile(t, filepath.Join(directory, commonName), "common headers package")
	return headersName, commonName
}

// writeLocalCompleteChecksumManifest records the complete four-package release
// checksum set whether or not every package was downloaded into directory.
func writeLocalCompleteChecksumManifest(t *testing.T, directory, abi, version string) {
	t.Helper()
	imageName, modulesName := localPackageNames(abi, version)
	headersName, commonName := localHeaderPackageNames(abi, version)
	manifest := fmt.Sprintf(
		"%s  %s\n%s  %s\n%s  %s\n%s  %s\n",
		localDigest("image package"), imageName,
		localDigest("modules package"), modulesName,
		localDigest("headers package"), headersName,
		localDigest("common headers package"), commonName,
	)
	writeLocalFile(t, filepath.Join(directory, localChecksumManifest), manifest)
}

// writeLocalBundleManifest emits the exact two- or four-package declaration
// used by native builds and downloaded bundles.
func writeLocalBundleManifest(t *testing.T, directory, abi, version string, includeHeaders bool) {
	t.Helper()
	imageName, modulesName := localPackageNames(abi, version)
	packages := []Package{
		{Role: RoleImage, Name: imageName, SHA256: localDigest("image package"), Size: int64(len("image package")), Verified: true},
		{Role: RoleModules, Name: modulesName, SHA256: localDigest("modules package"), Size: int64(len("modules package")), Verified: true},
	}
	if includeHeaders {
		headersName, commonName := localHeaderPackageNames(abi, version)
		packages = append(packages,
			Package{Role: RoleHeaders, Name: headersName, SHA256: localDigest("headers package"), Size: int64(len("headers package")), Verified: true},
			Package{Role: RoleCommonHeaders, Name: commonName, SHA256: localDigest("common headers package"), Size: int64(len("common headers package")), Verified: true},
		)
	}
	bundle, err := NewBundle("fixture", "https://example.invalid/kernel.git", packages)
	if err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	if err := bundle.WriteJSON(&output); err != nil {
		t.Fatal(err)
	}
	writeLocalFile(t, filepath.Join(directory, localBundleManifest), output.String())
}

// writeLocalRuntimePackage creates one role-specific Debian package fixture and
// returns the generated package filename.
func writeLocalRuntimePackage(t *testing.T, directory string, role PackageRole, abi, version, content string) string {
	t.Helper()

	imageName, modulesName := localPackageNames(abi, version)
	name := imageName
	if role == RoleModules {
		name = modulesName
	}
	writeLocalFile(t, filepath.Join(directory, name), content)
	return name
}

// localPackageNames returns the image and modules filenames for a test ABI and
// Debian package version.
func localPackageNames(abi, version string) (string, string) {
	return "linux-image-" + abi + "_" + version + "_arm64.deb",
		"linux-modules-" + abi + "_" + version + "_arm64.deb"
}

// localHeaderPackageNames returns the flavour and common header filenames for a
// test ABI and Debian package version.
func localHeaderPackageNames(abi, version string) (string, string) {
	base := strings.TrimSuffix(abi, surfaceABISuffix)
	return "linux-headers-" + abi + "_" + version + "_arm64.deb",
		"linux-qcom-x1e-headers-" + base + "_" + version + "_all.deb"
}

// writeLocalFile writes a private regular-file fixture and fails the current test
// immediately if the filesystem setup cannot be completed.
func writeLocalFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", path, err)
	}
}

// localDigest returns the lowercase SHA-256 used in checksum expectations.
func localDigest(content string) string {
	digest := sha256.Sum256([]byte(content))
	return hex.EncodeToString(digest[:])
}

// assertLocalErrorContains requires a local discovery failure to include every
// supplied fragment so its diagnostic remains actionable.
func assertLocalErrorContains(t *testing.T, err error, values ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want text %v", values)
	}
	for _, value := range values {
		if !strings.Contains(err.Error(), value) {
			t.Errorf("error = %q, want text %q", err, value)
		}
	}
}
