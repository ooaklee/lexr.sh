package popos

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// efiPartitionType is xorriso's on-disk byte-order spelling of the ESP GUID.
const efiPartitionType = "28732ac11ff8d211ba4b00a0c93ec93b"

// gptExtentPattern extracts numbered GPT extents in units of 512-byte sectors.
var gptExtentPattern = regexp.MustCompile(`(?m)^GPT start and size\s*:\s*(\d+)\s+(\d+)\s+(\d+)\s*$`)

// elToritoPattern accepts one non-emulated UEFI image in xorriso's report.
var elToritoPattern = regexp.MustCompile(`(?m)^El Torito boot img\s*:\s*1\s+UEFI\s+y\s+none\s+0x0000\s+0x00\s+(\d+)\s+(\d+)\s*$`)

// bootExtent identifies the actual FAT bytes shared by USB and El Torito boot.
type bootExtent struct {
	offset int64
	size   int64
}

// parseBootLayout requires a partition-relative ISO view and one appended ESP,
// with the optical boot catalogue pointing at exactly those same FAT bytes.
func parseBootLayout(report string, imageSize int64) (bootExtent, error) {
	if imageSize <= 0 || imageSize > maximumISOBytes || imageSize%512 != 0 ||
		!strings.Contains(report, "System area summary: MBR protective-msdos-label cyl-align-off GPT") ||
		!regexp.MustCompile(`(?m)^Partition offset\s*:\s*16\s*$`).MatchString(report) ||
		!regexp.MustCompile(`(?m)^GPT type GUID\s*:\s*2\s+`+efiPartitionType+`\s*$`).MatchString(report) {
		return bootExtent{}, errors.New("Pop output lacks its partition-relative ISO and GPT EFI partition")
	}
	matches := gptExtentPattern.FindAllStringSubmatch(report, -1)
	if len(matches) != 2 || matches[0][1] != "1" || matches[1][1] != "2" {
		return bootExtent{}, errors.New("Pop output has unexpected or ambiguous GPT partitions")
	}
	var extents [2]bootExtent
	for index, match := range matches {
		start, startErr := strconv.ParseInt(match[2], 10, 64)
		sectors, sizeErr := strconv.ParseInt(match[3], 10, 64)
		if startErr != nil || sizeErr != nil || start < 1 || sectors < 1 || start > imageSize/512 || sectors > imageSize/512-start {
			return bootExtent{}, fmt.Errorf("GPT partition %d lies outside the image", index+1)
		}
		extents[index] = bootExtent{offset: start * 512, size: sectors * 512}
	}
	iso, esp := extents[0], extents[1]
	if iso.offset != 32768 || iso.offset+iso.size != esp.offset || esp.offset%2048 != 0 || esp.size > 64<<20 {
		return bootExtent{}, errors.New("Pop output has overlapping, unaligned or oversized EFI boot extents")
	}
	boots := elToritoPattern.FindAllStringSubmatch(report, -1)
	if len(boots) != 1 || strings.Count(report, "El Torito boot img") != 1 {
		return bootExtent{}, errors.New("Pop output requires exactly one non-emulated UEFI boot image")
	}
	loadSectors, loadErr := strconv.ParseInt(boots[0][1], 10, 64)
	lba, lbaErr := strconv.ParseInt(boots[0][2], 10, 64)
	if loadErr != nil || lbaErr != nil || loadSectors != esp.size/512 || lba != esp.offset/2048 {
		return bootExtent{}, errors.New("Pop optical and USB boot paths select different EFI images")
	}
	return esp, nil
}
