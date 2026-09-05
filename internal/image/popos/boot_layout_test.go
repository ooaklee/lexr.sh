package popos

import (
	"strings"
	"testing"
)

// hybridReportFixture retains the geometry observed in the actual Pop source
// boot prototype after adding a partition-relative ISO and an appended ESP.
const hybridReportFixture = `System area summary: MBR protective-msdos-label cyl-align-off GPT
Partition offset   : 16
GPT type GUID      : 2 28732ac11ff8d211ba4b00a0c93ec93b
GPT start and size : 1 64 5644828
GPT start and size : 2 5644892 8192
El Torito boot img : 1 UEFI y none 0x0000 0x00 8192 1411223
`

// TestParseBootLayoutRequiresSameOpticalAndUSBImage rejects valid-looking
// partition metadata whose actual boot paths disagree or escape the ISO.
func TestParseBootLayoutRequiresSameOpticalAndUSBImage(t *testing.T) {
	const imageSize int64 = 2894462976
	extent, err := parseBootLayout(hybridReportFixture, imageSize)
	if err != nil || extent.offset != 2890184704 || extent.size != 4194304 {
		t.Fatalf("boot extent %#v: %v", extent, err)
	}
	for name, report := range map[string]string{
		"no partition view":      strings.ReplaceAll(hybridReportFixture, "Partition offset   : 16", "Partition offset : 0"),
		"wrong type":             strings.ReplaceAll(hybridReportFixture, efiPartitionType, strings.Repeat("a", 32)),
		"optical kernel differs": strings.ReplaceAll(hybridReportFixture, "8192 1411223", "8192 1411224"),
		"optical size differs":   strings.ReplaceAll(hybridReportFixture, "8192 1411223", "8191 1411223"),
		"overlap":                strings.ReplaceAll(hybridReportFixture, "1 64 5644828", "1 64 5644830"),
		"unbounded":              strings.ReplaceAll(hybridReportFixture, "2 5644892 8192", "2 5644892 9223372036854775807"),
		"third partition":        hybridReportFixture + "GPT start and size : 3 1 1\n",
		"second boot image":      hybridReportFixture + "El Torito boot img : 2 UEFI y none 0x0000 0x00 8192 1411223\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseBootLayout(report, imageSize); err == nil {
				t.Fatal("accepted inconsistent boot geometry")
			}
		})
	}
	if _, err := parseBootLayout(hybridReportFixture, 2000000000); err == nil {
		t.Fatal("accepted truncated image")
	}
}
