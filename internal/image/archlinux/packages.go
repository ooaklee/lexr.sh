package archlinux

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"

	"github.com/ooaklee/lexr.sh/internal/artifact"
	imagecontract "github.com/ooaklee/lexr.sh/internal/image"
)

// packageLockJSON records reviewed package bytes, not a mutable repository query.
//
//go:embed packages.lock.json
var packageLockJSON []byte

// sha256Pattern restricts content identities used in cache filenames.
var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// packageNamePattern restricts native pacman package names in fixed build inputs.
var packageNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9+_.-]{0,127}$`)

// lockedPackage binds a reviewed archive and detached signature to its source.
type lockedPackage struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	URL       string `json:"url"`
	SHA256    string `json:"sha256"`
	Size      int64  `json:"size_bytes"`
	Signature string `json:"signature_base64"`
}

// lockedPackages rejects incomplete, duplicate or unsafe compiled package inputs.
func lockedPackages() ([]lockedPackage, error) {
	decoder := json.NewDecoder(bytes.NewReader(packageLockJSON))
	decoder.DisallowUnknownFields()
	var entries []lockedPackage
	if err := decoder.Decode(&entries); err != nil {
		return nil, err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, errors.New("Arch package lock has trailing JSON")
	}
	if len(entries) == 0 || len(entries) > 1024 {
		return nil, errors.New("Arch package lock is empty or oversized")
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		u, err := url.Parse(entry.URL)
		sig, sigErr := base64.StdEncoding.DecodeString(entry.Signature)
		if err != nil || u.Scheme != "https" || u.Hostname() != "ca.us.mirror.archlinuxarm.org" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !sha256Pattern.MatchString(entry.SHA256) || !packageNamePattern.MatchString(entry.Name) || entry.Version == "" || entry.Size <= 0 || entry.Size > 1<<30 || sigErr != nil || len(sig) < 64 || len(sig) > 8192 || seen[entry.Name] {
			return nil, fmt.Errorf("invalid or repeated locked Arch package %q", entry.Name)
		}
		seen[entry.Name] = true
	}
	for _, name := range []string{"grub", "mkinitcpio-archiso", "linux-firmware-qcom", "networkmanager", "wireless-regdb"} {
		if !seen[name] {
			return nil, fmt.Errorf("Arch package lock is missing %s", name)
		}
	}
	return entries, nil
}

// stagePackages verifies content-addressed cache entries and copies private
// snapshots before pacman receives the package paths in an offline container.
func (r *Remasterer) stagePackages(ctx context.Context, workspace, cache string) error {
	entries, err := lockedPackages()
	if err != nil {
		return err
	}
	if cache == "" {
		cache, err = os.UserCacheDir()
		if err != nil {
			return err
		}
		cache = filepath.Join(cache, "lexr")
	}
	cache = filepath.Join(cache, "arch-packages")
	if err = os.MkdirAll(filepath.Join(workspace, "packages"), 0755); err != nil {
		return err
	}
	for index, entry := range entries {
		fmt.Fprintf(r.Out, "Arch package %d/%d: %s %s\n", index+1, len(entries), entry.Name, entry.Version)
		filename := entry.SHA256 + ".pkg.tar.xz"
		cached, err := r.Artifacts.Acquire(ctx, artifact.Source{Location: entry.URL, ExpectedSHA256: entry.SHA256}, filepath.Join(cache, filename))
		if err != nil {
			return fmt.Errorf("acquire %s: %w", entry.Name, err)
		}
		if cached.Size != entry.Size {
			return fmt.Errorf("locked package %s has unexpected size", entry.Name)
		}
		digest, size, err := imagecontract.SnapshotFile(ctx, cached.Path, filepath.Join(workspace, "packages", filename), 1<<30, nil)
		if err != nil {
			return err
		}
		if digest != entry.SHA256 || size != entry.Size {
			return fmt.Errorf("package %s changed during staging", entry.Name)
		}
		sig, _ := base64.StdEncoding.DecodeString(entry.Signature)
		if err = os.WriteFile(filepath.Join(workspace, "packages", filename+".sig"), sig, 0644); err != nil {
			return err
		}
	}
	return os.WriteFile(filepath.Join(workspace, "packages.lock.json"), packageLockJSON, 0644)
}
