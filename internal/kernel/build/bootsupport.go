package build

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ooaklee/lexr.sh/internal/kernel"
	"github.com/ooaklee/lexr.sh/internal/kernel/bootsupport"
)

const (
	// bootSupportStagingDirectory is the private package-root directory.
	bootSupportStagingDirectory = "boot-support-staging"
	// bootSupportBuildScriptName is the fixed container archive builder.
	bootSupportBuildScriptName = "build-boot-support"
	// bootSupportMaintainer is the public Debian package maintainer identity.
	bootSupportMaintainer = "Leon Silcott <leon@boasi.io>"
)

// prepareBootSupportPackage materialises the reviewed generic package payload
// and returns the pinned-container command which turns it into a Debian archive.
func prepareBootSupportPackage(transaction string, provenance Provenance, containerName string) (Command, error) {
	if provenance.EffectiveDTBDelivery != kernel.DTBDeliveryExternalRequired {
		return Command{}, errors.New("boot-support package creation requires external DTB delivery")
	}
	abi, version, err := generatedKernelIdentity(filepath.Join(transaction, "artifacts"))
	if err != nil {
		return Command{}, err
	}
	platforms := make([]bootsupport.Platform, 0, len(provenance.DeviceTrees))
	firmwarePrefix := "usr/lib/firmware/" + abi + "/device-tree/"
	for _, tree := range provenance.DeviceTrees {
		if !tree.Required {
			continue
		}
		relative, found := strings.CutPrefix(tree.Path, firmwarePrefix)
		if !found {
			return Command{}, fmt.Errorf("device tree %s is outside the generated ABI firmware directory", tree.Device)
		}
		compatibles := make([]string, 0, len(tree.Selectors))
		for _, selector := range tree.Selectors {
			if selector.Kind == kernel.DeviceTreeSelectorCompatible {
				compatibles = append(compatibles, selector.Value)
			}
		}
		platforms = append(platforms, bootsupport.Platform{
			ID:               tree.Device,
			Title:            platformTitle(tree.Device),
			DeviceTreePath:   relative,
			DeviceTreeSHA256: tree.SHA256,
			Compatibles:      compatibles,
		})
	}
	payload, err := bootsupport.Render(bootsupport.Request{
		ABI: abi, Version: version, Maintainer: bootSupportMaintainer, Platforms: platforms,
	})
	if err != nil {
		return Command{}, fmt.Errorf("render kernel boot-support package: %w", err)
	}
	staging := filepath.Join(transaction, bootSupportStagingDirectory)
	if err := materialiseBootSupportPayload(transaction, staging, payload); err != nil {
		return Command{}, err
	}
	script := filepath.Join(transaction, bootSupportBuildScriptName)
	if err := writePrivateBuildFile(transaction, script, 0o700, []byte(bootsupport.DebianPackageBuildScript())); err != nil {
		return Command{}, err
	}
	name := payload.Name + "_" + payload.Version + "_" + payload.Architecture + ".deb"
	if len(name) > maximumPackageNameBytes || !packageFileNameExpression.MatchString(name) {
		return Command{}, fmt.Errorf("generated boot-support package name contains unsupported bytes: %q", name)
	}
	epoch := provenance.CommitTime.Unix()
	if epoch < 0 {
		return Command{}, errors.New("kernel source commit predates the Unix epoch")
	}
	command := bootSupportContainerCommand(transaction, containerName, name, epoch)
	if err := validateCommand(command); err != nil {
		return Command{}, err
	}
	return command, nil
}

// generatedKernelIdentity derives the one image ABI and package version which
// the support package must accompany.
func generatedKernelIdentity(directory string) (string, string, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return "", "", fmt.Errorf("read generated kernel packages for boot support: %w", err)
	}
	var abi, version string
	count := 0
	for _, entry := range entries {
		role, candidateABI, candidateVersion, parseErr := kernel.ParsePackageName(entry.Name())
		if parseErr != nil || role != kernel.RoleImage {
			continue
		}
		abi, version = candidateABI, candidateVersion
		count++
	}
	if count != 1 {
		return "", "", fmt.Errorf("generated kernel packages contain %d image identities; expected exactly one", count)
	}
	return abi, version, nil
}

// platformTitle returns a stable human-readable boot menu label.
func platformTitle(device string) string {
	switch device {
	case "surface-pro-11-x1e-oled":
		return "Surface Pro 11 (X1E OLED)"
	case "surface-pro-11-x1p-lcd":
		return "Surface Pro 11 (X1P LCD)"
	default:
		return device
	}
}

// materialiseBootSupportPayload creates one private package root from the pure
// renderer output.
func materialiseBootSupportPayload(transaction, staging string, payload bootsupport.Payload) error {
	if err := os.Mkdir(staging, 0o755); err != nil {
		return fmt.Errorf("create boot-support staging directory: %w", err)
	}
	for _, item := range payload.Files {
		target := filepath.Join(staging, filepath.FromSlash(item.Path))
		if !pathWithin(staging, target) {
			return fmt.Errorf("boot-support payload path escapes staging: %q", item.Path)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("create boot-support payload directory: %w", err)
		}
		if err := writePrivateBuildFile(transaction, target, item.Mode.Perm(), item.Data); err != nil {
			return err
		}
	}
	return nil
}

// writePrivateBuildFile creates one new regular file inside the transaction.
func writePrivateBuildFile(transaction, target string, mode os.FileMode, data []byte) error {
	if !pathWithin(transaction, target) {
		return fmt.Errorf("private build file escapes transaction: %s", target)
	}
	file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("create private build file %s: %w", filepath.Base(target), err)
	}
	written, writeErr := file.Write(data)
	if writeErr == nil && written != len(data) {
		writeErr = errors.New("short write")
	}
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return fmt.Errorf("write private build file %s: %w", filepath.Base(target), err)
	}
	return nil
}

// pathWithin reports whether candidate remains inside parent after cleaning.
func pathWithin(parent, candidate string) bool {
	relative, err := filepath.Rel(filepath.Clean(parent), filepath.Clean(candidate))
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// bootSupportContainerCommand constructs reproducible archive creation in the
// same pinned ARM64 userspace as the kernel build.
func bootSupportContainerCommand(transaction, containerName, packageName string, epoch int64) Command {
	return Command{Name: dockerCommand, Args: []string{
		"run", "--rm", "--name", containerName,
		"--platform", dockerPlatform,
		"--mount", "type=bind,src=" + transaction + ",dst=/exchange",
		ContainerImage,
		"/bin/sh", "/exchange/" + bootSupportBuildScriptName,
		"/exchange/" + bootSupportStagingDirectory,
		"/exchange/artifacts/" + packageName,
		strconv.FormatInt(epoch, 10),
	}}
}

// previewBootSupportContainerCommand represents conditional raw-image package
// creation without guessing build-derived identities.
func previewBootSupportContainerCommand() Command {
	return bootSupportContainerCommand(
		"<private-transaction-directory>", "<generated-boot-support-container-name>",
		"<derived-boot-support-package-name>", 0,
	)
}
