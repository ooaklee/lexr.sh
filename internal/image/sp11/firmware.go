package sp11

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	imagecontract "github.com/ooaklee/lexr.sh/internal/image"
	"github.com/ooaklee/lexr.sh/internal/platform"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// firmwareRevision pins upstream linux-firmware 20260221 to its immutable commit.
const firmwareRevision = "599764611a8ac213c6aa6dad17c941c2f46b53cb"

// firmwareBaseURL supplies redistributable GPU data and its required notices.
const firmwareBaseURL = "https://gitlab.com/kernel-firmware/linux-firmware/-/raw/" + firmwareRevision + "/"

// FirmwareInput identifies one data-only input. Neither executable helpers nor
// private platform firmware can be requested through this closed list.
type FirmwareInput struct {
	Path   string `json:"upstream_path"`
	File   string `json:"media_file"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// firmwareInputs includes the complete redistribution licence and notices.
var firmwareInputs = []FirmwareInput{
	{Path: "WHENCE", File: "WHENCE", SHA256: "f347586920c214293245169711f81ef837d9c37410a04bddb44a583e2883c2f9", Size: 381885},
	{Path: "LICENSE.qcom", File: "LICENSE.qcom.txt", SHA256: "be904cd28cb292b80cdb6cf412ab0d9159d431671e987ad433c1f62e0988a9bc", Size: 13962},
	{Path: "qcom/NOTICE.txt", File: "qcom_NOTICE.txt", SHA256: "fa43e1b9a13b341a07adca9dbe73d0f9072d7966fdfe811c01f0dd2872d7309a", Size: 23966},
	{Path: "qcom/gen70500_gmu.bin", File: "qcom_gen70500_gmu.bin", SHA256: "5dfba247d548cabcb892ffa716e8dc82a345fd54b5dbae46ba523230e7ae37dd", Size: 81312},
	{Path: "qcom/gen70500_sqe.fw", File: "qcom_gen70500_sqe.fw", SHA256: "05ae89e6dea62268cec3f4abb5d7d6db2c95270ff105fd056f77262e39d2e527", Size: 77332},
}

// firmwareProvenance describes every extra input alongside its original licence.
func firmwareProvenance() ([]byte, error) {
	return json.MarshalIndent(struct {
		Repository string          `json:"repository"`
		Revision   string          `json:"revision"`
		Files      []FirmwareInput `json:"files"`
	}{"https://gitlab.com/kernel-firmware/linux-firmware", firmwareRevision, firmwareInputs}, "", "  ")
}

// downloadFirmwareInput bounds time and bytes before checking the full digest.
func downloadFirmwareInput(ctx context.Context, client *http.Client, input FirmwareInput) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, firmwareBaseURL+input.Path, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Request.URL.Scheme != "https" {
		return nil, fmt.Errorf("firmware download failed: %s", response.Status)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, input.Size+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != input.Size || fmt.Sprintf("%x", sha256.Sum256(data)) != input.SHA256 {
		return nil, fmt.Errorf("upstream firmware input %s differs from pinned bytes", input.Path)
	}
	return data, nil
}

// PrepareGPUFirmware adds the two public GPU files absent from this source and
// preserves their complete upstream provenance, licence and notices in both
// the medium and installer root. It never reads firmware from the host.
func PrepareGPUFirmware(ctx context.Context, docker *platform.Docker, toolsImage, workspace, volume string) error {
	directory := filepath.Join(workspace, "sp11", "firmware")
	if err := os.MkdirAll(directory, 0755); err != nil {
		return err
	}
	for _, input := range firmwareInputs {
		data, err := downloadFirmwareInput(ctx, http.DefaultClient, input)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(directory, input.File), data, 0644); err != nil {
			return err
		}
	}
	receipt, err := firmwareProvenance()
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(directory, "provenance.json"), receipt, 0644); err != nil {
		return err
	}
	const script = `root=/linux-work/rootfs
for component in usr usr/lib usr/lib/firmware usr/lib/firmware/qcom; do
    test -d "$root/$component" && test ! -L "$root/$component"
done
for name in gen70500_gmu.bin gen70500_sqe.fw; do
    target="$root/usr/lib/firmware/qcom/$name"
    for suffix in '' .xz .zst; do
        test ! -e "$target$suffix" && test ! -L "$target$suffix"
    done
    install -m 0644 "/work/sp11/firmware/qcom_$name" "$target"
done
install -d -m 0755 "$root/usr/share/lexr/firmware"
cp /work/sp11/firmware/* "$root/usr/share/lexr/firmware/"
`
	if err := docker.RunInWorkspaceVolume(ctx, toolsImage, workspace, volume, "bash", "-ceu", script); err != nil {
		return fmt.Errorf("stage missing SP11 GPU firmware: %w", err)
	}
	return nil
}

// ValidateGPUFirmwareDirectory checks every expected data file and its provenance.
func ValidateGPUFirmwareDirectory(workspace, relative string) error {
	for _, input := range firmwareInputs {
		data, err := imagecontract.ReadBoundedExtractedFile(workspace, filepath.ToSlash(filepath.Join(relative, input.File)), input.Size)
		if err != nil {
			return err
		}
		if int64(len(data)) != input.Size || fmt.Sprintf("%x", sha256.Sum256(data)) != input.SHA256 {
			return fmt.Errorf("supplemental GPU firmware input %s differs", input.File)
		}
	}
	expected, err := firmwareProvenance()
	if err != nil {
		return err
	}
	actual, err := imagecontract.ReadBoundedExtractedFile(workspace, relative+"/provenance.json", 1<<20)
	if err != nil {
		return err
	}
	if string(actual) != string(expected) {
		return errors.New("SP11 GPU firmware provenance differs from the pinned input set")
	}
	return nil
}

// ValidateFirmwareCopies checks every loader search location and CPIO section
// for a prepared file. Unrelated source firmware remains permitted, while an
// override or stale duplicate cannot replace the bytes validation attests to.
func ValidateFirmwareCopies(root string, sections []string, abi, firmware, digest string, size int64) error {
	found := false
	for _, section := range sections {
		for _, prefix := range []string{"updates/" + abi, "updates", abi, ""} {
			for _, suffix := range []string{"", ".xz", ".zst"} {
				relative := filepath.ToSlash(filepath.Join(section, "usr/lib/firmware", prefix, firmware+suffix))
				data, err := imagecontract.ReadBoundedExtractedFile(root, relative, 16<<20)
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				if err != nil {
					return err
				}
				if suffix != "" || int64(len(data)) != size || fmt.Sprintf("%x", sha256.Sum256(data)) != digest {
					return fmt.Errorf("prepared firmware has an unverified override at %s", relative)
				}
				found = true
			}
		}
	}
	if !found {
		return fmt.Errorf("prepared firmware %s is absent", firmware)
	}
	return nil
}

// SupplementalGPUFirmwareInputs returns a copy of the pinned data and notice inventory.
func SupplementalGPUFirmwareInputs() []FirmwareInput {
	return append([]FirmwareInput(nil), firmwareInputs...)
}
