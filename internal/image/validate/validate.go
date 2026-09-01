// Package validate routes generated-image validation to the adapter declared
// by the image's strict embedded manifest.
package validate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	imagecontract "github.com/ooaklee/lexr.sh/internal/image"
	"github.com/ooaklee/lexr.sh/internal/image/fedora"
	"github.com/ooaklee/lexr.sh/internal/image/ubuntu"
	"github.com/ooaklee/lexr.sh/internal/platform"
)

const (
	// maximumRoutingImageBytes matches the existing generated-ISO validation
	// boundary and rejects devices, pipes, and unexpectedly large inputs before
	// invoking container tooling.
	maximumRoutingImageBytes int64 = 64 << 30
)

// adapterValidator is the common structural-validation contract implemented by
// each distribution adapter.
type adapterValidator interface {
	// Validate proves the requested image satisfies one adapter's complete
	// structural boot contract.
	Validate(context.Context, string) (imagecontract.ValidationReport, error)
}

// adapterFactory constructs one adapter around the same Docker boundary used
// to inspect the routing manifest.
type adapterFactory func(*platform.Docker) adapterValidator

// routedImage binds the strictly decoded routing decision to the exact
// manifest bytes extracted from the generated ISO.
type routedImage struct {
	Manifest       imagecontract.Manifest
	ManifestSHA256 string
	ManifestSize   int64
	Cleanup        func() error
}

// manifestExtractor is an internal test seam around bounded xorriso extraction.
type manifestExtractor func(context.Context, *platform.Docker, string) (routedImage, error)

// Validator inspects the bounded embedded manifest and delegates complete
// structural validation to one explicitly supported distribution adapter.
type Validator struct {
	docker        *platform.Docker
	extract       manifestExtractor
	ubuntuFactory adapterFactory
	fedoraFactory adapterFactory
}

// NewValidator creates a generated-image validator and supplies the standard
// Docker runner when none is injected.
func NewValidator(docker *platform.Docker) *Validator {
	if docker == nil {
		docker = platform.NewDocker(nil)
	}
	return &Validator{
		docker:  docker,
		extract: extractRoutingManifest,
		ubuntuFactory: func(docker *platform.Docker) adapterValidator {
			return ubuntu.NewValidator(docker)
		},
		fedoraFactory: func(docker *platform.Docker) adapterValidator {
			return fedora.NewValidator(docker)
		},
	}
}

// Validate extracts only the bounded routing manifest, rejects every adapter
// outside the compiled allowlist, and invokes that adapter's full validator.
// A successful adapter report must identify the same manifest and source path
// used for routing.
func (v *Validator) Validate(ctx context.Context, isoPath string) (report imagecontract.ValidationReport, resultErr error) {
	if v == nil || v.docker == nil || v.extract == nil {
		return report, errors.New("generated-image validator is not configured")
	}
	absolute, err := filepath.Abs(isoPath)
	if err != nil {
		return report, fmt.Errorf("resolve image path for validator routing: %w", err)
	}
	absolute = filepath.Clean(absolute)

	routed, err := v.extract(ctx, v.docker, absolute)
	if err != nil {
		if routed.Cleanup != nil {
			err = errors.Join(err, routed.Cleanup())
		}
		return imagecontract.ValidationReport{Path: absolute}, fmt.Errorf("inspect generated-image manifest: %w", err)
	}
	if routed.Cleanup != nil {
		defer func() { resultErr = errors.Join(resultErr, routed.Cleanup()) }()
	}
	routingReport := imagecontract.ValidationReport{
		Path:           absolute,
		Layout:         routed.Manifest.Layout,
		Adapter:        routed.Manifest.Adapter,
		ManifestSHA256: routed.ManifestSHA256,
		ManifestSize:   routed.ManifestSize,
		KernelABI:      routed.Manifest.KernelBundle.ABI,
	}
	for _, deviceTree := range routed.Manifest.KernelBundle.DeviceTrees {
		routingReport.DeviceTrees = append(routingReport.DeviceTrees, deviceTree.Device)
	}
	if routed.Manifest.SchemaVersion != imagecontract.ManifestSchemaVersion {
		return routingReport, fmt.Errorf("unsupported image manifest schema %d; expected %d",
			routed.Manifest.SchemaVersion, imagecontract.ManifestSchemaVersion)
	}

	var factory adapterFactory
	switch routed.Manifest.Adapter {
	case ubuntu.AdapterID:
		factory = v.ubuntuFactory
	case fedora.AdapterID:
		factory = v.fedoraFactory
	default:
		return routingReport, fmt.Errorf("unsupported generated-image adapter %q", routed.Manifest.Adapter)
	}
	if factory == nil {
		return routingReport, fmt.Errorf("generated-image adapter %q is not configured", routed.Manifest.Adapter)
	}
	selected := factory(v.docker)
	if selected == nil {
		return routingReport, fmt.Errorf("generated-image adapter %q returned no validator", routed.Manifest.Adapter)
	}

	report, err = selected.Validate(ctx, absolute)
	if err != nil {
		if report.Path == "" {
			report.Path = absolute
		}
		return report, err
	}
	validatedPath, pathErr := filepath.Abs(report.Path)
	if pathErr != nil || filepath.Clean(validatedPath) != absolute {
		return report, errors.Join(errors.New("adapter validation report identifies a different image path"), pathErr)
	}
	if !report.Valid {
		return report, errors.New("adapter validator returned success with an invalid report")
	}
	if report.Adapter != routed.Manifest.Adapter {
		return report, fmt.Errorf("adapter validation report identifies %q, routed manifest identifies %q",
			report.Adapter, routed.Manifest.Adapter)
	}
	if report.ManifestSHA256 != routed.ManifestSHA256 || report.ManifestSize != routed.ManifestSize {
		return report, errors.New("embedded manifest changed between adapter routing and structural validation")
	}
	return report, nil
}

// extractRoutingManifest uses Docker-isolated xorriso to extract exactly the
// reserved manifest member. A process file-size limit bounds hostile ISO member
// contents before they can be written to the private host workspace.
func extractRoutingManifest(ctx context.Context, docker *platform.Docker, isoPath string) (result routedImage, resultErr error) {
	if docker == nil || docker.Runner == nil {
		return result, errors.New("Docker boundary is nil")
	}
	absolute, source, identity, err := openRoutingImage(isoPath)
	if err != nil {
		return result, err
	}
	defer func() { resultErr = errors.Join(resultErr, source.Close()) }()

	if err := docker.Check(ctx); err != nil {
		return result, err
	}
	toolsImage, err := docker.EnsureToolsImage(ctx)
	if err != nil {
		return result, err
	}
	workspace, err := os.MkdirTemp(filepath.Dir(absolute), ".lexr-route-")
	if err != nil {
		return result, fmt.Errorf("create manifest-routing workspace: %w", err)
	}
	keepWorkspace := false
	defer func() {
		if !keepWorkspace {
			resultErr = errors.Join(resultErr, os.RemoveAll(workspace))
		}
	}()

	// POSIX sh and the Ubuntu tooling image express ulimit -f in 512-byte
	// blocks. DecodeManifest independently enforces the exact byte ceiling.
	fileLimitBlocks := (int64(imagecontract.MaximumManifestSize) + 511) / 512
	extractScript := `umask 077
ulimit -f "$1"
xorriso -osirrox on -indev /input.iso -extract /sp11/lexr-manifest.json /work/manifest.json
chmod 0644 /work/manifest.json
`
	if err := runManifestExtraction(ctx, docker, toolsImage, absolute, workspace,
		extractScript, strconv.FormatInt(fileLimitBlocks, 10)); err != nil {
		return result, fmt.Errorf("extract /sp11/lexr-manifest.json: %w", err)
	}
	if err := verifyRoutingImageIdentity(absolute, source, identity); err != nil {
		return result, err
	}
	manifestBytes, err := readRoutingManifest(filepath.Join(workspace, "manifest.json"))
	if err != nil {
		return result, err
	}
	manifest, err := imagecontract.DecodeManifest(bytes.NewReader(manifestBytes))
	if err != nil {
		return result, err
	}
	digest := sha256.Sum256(manifestBytes)
	result = routedImage{
		Manifest:       manifest,
		ManifestSHA256: fmt.Sprintf("%x", digest),
		ManifestSize:   int64(len(manifestBytes)),
		Cleanup: func() error {
			return os.RemoveAll(workspace)
		},
	}
	keepWorkspace = true
	return result, nil
}

// runManifestExtraction mounts the untrusted ISO read-only and exposes only a
// fresh private output directory as writable container state.
func runManifestExtraction(
	ctx context.Context,
	docker *platform.Docker,
	toolsImage string,
	isoPath string,
	workspace string,
	script string,
	fileLimitBlocks string,
) error {
	absoluteWorkspace, err := filepath.Abs(workspace)
	if err != nil {
		return fmt.Errorf("resolve manifest-routing workspace: %w", err)
	}
	return docker.Runner.Run(ctx, platform.Command{
		Name: "docker",
		Args: []string{
			"run", "--rm", "--platform", "linux/arm64",
			"--network", "none", "--read-only", "--cap-drop", "ALL",
			"--security-opt", "no-new-privileges",
			"--tmpfs", "/tmp:rw,noexec,nosuid,nodev,size=16m",
			"--volume", isoPath + ":/input.iso:ro",
			"--volume", absoluteWorkspace + ":/work",
			"--workdir", "/work",
			toolsImage,
			"sh", "-ceu", script, "lexr-manifest-extract", fileLimitBlocks,
		},
	})
}

// routingImageIdentity records the descriptor-backed source inspected before
// xorriso opens the host path through Docker.
type routingImageIdentity struct {
	info os.FileInfo
}

// openRoutingImage rejects symbolic links, special files, empty files, and
// unexpectedly large images while retaining an open descriptor for later
// identity comparison.
func openRoutingImage(isoPath string) (absolute string, source *os.File, identity routingImageIdentity, resultErr error) {
	absolute, err := filepath.Abs(isoPath)
	if err != nil {
		return "", nil, identity, fmt.Errorf("resolve image path: %w", err)
	}
	absolute = filepath.Clean(absolute)
	listed, err := os.Lstat(absolute)
	if err != nil {
		return "", nil, identity, fmt.Errorf("inspect image path: %w", err)
	}
	if listed.Mode()&os.ModeSymlink != 0 || !listed.Mode().IsRegular() || listed.Size() <= 0 || listed.Size() > maximumRoutingImageBytes {
		return "", nil, identity, fmt.Errorf("image path %q is not a bounded non-symbolic-link regular file", absolute)
	}
	source, err = os.Open(absolute)
	if err != nil {
		return "", nil, identity, fmt.Errorf("open image for manifest routing: %w", err)
	}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, source.Close())
		}
	}()
	opened, err := source.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(listed, opened) || opened.Size() != listed.Size() {
		return "", nil, identity, errors.Join(errors.New("image identity changed while opening it for validator routing"), err)
	}
	return absolute, source, routingImageIdentity{info: opened}, nil
}

// verifyRoutingImageIdentity fails if the source path was replaced, resized,
// or visibly modified while xorriso read its manifest.
func verifyRoutingImageIdentity(absolute string, source *os.File, identity routingImageIdentity) error {
	pathInfo, pathErr := os.Lstat(absolute)
	openedInfo, openedErr := source.Stat()
	if pathErr != nil || openedErr != nil {
		return errors.Join(errors.New("inspect image identity after manifest extraction"), pathErr, openedErr)
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.Mode().IsRegular() ||
		!os.SameFile(identity.info, pathInfo) || !os.SameFile(identity.info, openedInfo) ||
		pathInfo.Size() != identity.info.Size() || openedInfo.Size() != identity.info.Size() ||
		!pathInfo.ModTime().Equal(identity.info.ModTime()) || !openedInfo.ModTime().Equal(identity.info.ModTime()) {
		return errors.New("image identity changed while its routing manifest was extracted")
	}
	return nil
}

// readRoutingManifest performs a descriptor-bound bounded read after xorriso's
// process-level output limit has already constrained the extracted file.
func readRoutingManifest(path string) (data []byte, resultErr error) {
	listed, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect extracted image manifest: %w", err)
	}
	if listed.Mode()&os.ModeSymlink != 0 || !listed.Mode().IsRegular() || listed.Size() <= 0 || listed.Size() > int64(imagecontract.MaximumManifestSize) {
		return nil, errors.New("extracted image manifest is not a bounded non-symbolic-link regular file")
	}
	manifest, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open extracted image manifest: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, manifest.Close()) }()
	opened, err := manifest.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(listed, opened) || opened.Size() != listed.Size() {
		return nil, errors.Join(errors.New("extracted image manifest identity changed while opening it"), err)
	}
	data, err = io.ReadAll(io.LimitReader(manifest, int64(imagecontract.MaximumManifestSize)+1))
	if err != nil {
		return nil, fmt.Errorf("read extracted image manifest: %w", err)
	}
	after, statErr := manifest.Stat()
	if statErr != nil || !os.SameFile(opened, after) || after.Size() != opened.Size() || int64(len(data)) != opened.Size() {
		return nil, errors.Join(errors.New("extracted image manifest changed while reading it"), statErr)
	}
	if len(data) > imagecontract.MaximumManifestSize {
		return nil, fmt.Errorf("extracted image manifest exceeds %d bytes", imagecontract.MaximumManifestSize)
	}
	return data, nil
}
