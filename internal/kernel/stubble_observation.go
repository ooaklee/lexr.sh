package kernel

import (
	"bytes"
	"crypto/sha256"
	"debug/pe"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"unicode/utf8"
)

const (
	// maximumStubbleImageBytes bounds the complete in-memory EFI observation.
	maximumStubbleImageBytes = 512 << 20
	// maximumStubbleDTBBytes bounds each virtual device-tree payload.
	maximumStubbleDTBBytes = 4 << 20
	// maximumStubbleHWIDBytes bounds the complete virtual HWID table.
	maximumStubbleHWIDBytes = 1 << 20
	// maximumStubbleSections bounds the inspected PE section inventory.
	maximumStubbleSections = 4096
	// stubbleHWIDRecordBytes is ukify's packed descriptor/GUID/offset row size.
	stubbleHWIDRecordBytes = 28
)

// ObserveStubbleSelection measures the image's stored payload order and HWID
// table. It does not predict a physical board's CHIDs or claim that optional
// payloads are independently selectable or hardware-qualified.
func ObserveStubbleSelection(image []byte, trees []DeviceTree) (*StubbleSelection, error) {
	if len(image) == 0 || len(image) > maximumStubbleImageBytes || len(trees) == 0 || len(trees) > maximumBundleDeviceTrees {
		return nil, errors.New("Stubble observation has an invalid image or inventory size")
	}
	embedded := make(map[string]DeviceTree, len(trees))
	devices := make(map[string]bool, len(trees))
	for _, tree := range trees {
		if !stableIdentifierExpression.MatchString(tree.Device) || devices[tree.Device] || !validSHA256(tree.SHA256) ||
			tree.EmbeddedMatches < 0 || tree.EmbeddedMatches > 1 || len(tree.CompatibleStrings) == 0 || len(tree.CompatibleStrings) > maximumCompatibleStrings {
			return nil, errors.New("Stubble observation has an invalid device-tree inventory")
		}
		devices[tree.Device] = true
		seen := make(map[string]bool, len(tree.CompatibleStrings))
		for _, compatible := range tree.CompatibleStrings {
			if !compatibleExpression.MatchString(compatible) || seen[compatible] {
				return nil, errors.New("Stubble observation has invalid compatible strings")
			}
			seen[compatible] = true
		}
		if tree.EmbeddedMatches == 1 {
			if _, exists := embedded[tree.SHA256]; exists {
				return nil, errors.New("Stubble observation has non-unique embedded DTB digests")
			}
			embedded[tree.SHA256] = tree
		}
	}
	if len(embedded) == 0 {
		return nil, errors.New("Stubble observation has no declared embedded DTB")
	}
	sections, err := stubblePESections(image)
	if err != nil {
		return nil, err
	}
	result := &StubbleSelection{Policy: StubbleInventoryOrderPolicy, ImageSHA256: stubbleDigest(image)}
	seenPayloads := make(map[string]bool, len(embedded))
	hwidSections := 0
	for _, section := range sections {
		switch section.Name {
		case ".dtbauto":
			if len(result.PayloadOrder) >= maximumBundleDeviceTrees {
				return nil, errors.New("Stubble image has too many embedded DTB sections")
			}
			payload, err := stubbleSectionPayload(image, section, maximumStubbleDTBBytes)
			if err != nil {
				return nil, err
			}
			digest := stubbleDigest(payload)
			tree, exists := embedded[digest]
			if !exists || seenPayloads[digest] {
				return nil, errors.New("Stubble embedded payload is undeclared or duplicated")
			}
			compatibles, err := stubbleFDTCompatibles(payload)
			if err != nil {
				return nil, fmt.Errorf("inspect Stubble DTB %s: %w", tree.Device, err)
			}
			if !reflect.DeepEqual(compatibles, tree.CompatibleStrings) {
				return nil, fmt.Errorf("Stubble DTB %s compatible strings differ from the actual payload", tree.Device)
			}
			seenPayloads[digest] = true
			result.PayloadOrder = append(result.PayloadOrder, tree.Device)
		case ".hwids":
			hwidSections++
			if hwidSections != 1 {
				return nil, errors.New("Stubble image has multiple HWID tables")
			}
			payload, err := stubbleSectionPayload(image, section, maximumStubbleHWIDBytes)
			if err != nil {
				return nil, err
			}
			result.HWIDTableSHA256 = stubbleDigest(payload)
			result.HWIDRecords, err = stubbleHWIDRecords(payload)
			if err != nil {
				return nil, err
			}
		}
	}
	if hwidSections != 1 || len(result.PayloadOrder) != len(embedded) {
		return nil, errors.New("Stubble image lacks its complete embedded inventory or exact HWID table")
	}
	return result, nil
}

// VerifyStubbleSelection compares all claimed order/table evidence with freshly
// observed bytes; matching sets with a different stored order are not equal.
func VerifyStubbleSelection(image []byte, trees []DeviceTree, want *StubbleSelection) error {
	if want == nil {
		return errors.New("Stubble selection evidence is missing")
	}
	got, err := ObserveStubbleSelection(image, trees)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(got, want) {
		return errors.New("Stubble selection evidence differs from the actual image")
	}
	return nil
}

// stubbleDigest encodes the exact byte digest without canonicalising content.
func stubbleDigest(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

// stubblePESection retains only the inline name and byte ranges needed for
// observation. Symbol, string and relocation table contents are never decoded.
type stubblePESection struct {
	Name        string
	VirtualSize uint32
	Size        uint32
	Offset      uint32
}

// stubblePESections admits a bounded DOS/PE32+/EFI envelope before observing
// section headers. Unlike debug/pe.NewFile, it never expands unused COFF tables
// or repeatedly decodes relocation entries supplied by an untrusted image.
func stubblePESections(image []byte) ([]stubblePESection, error) {
	bad := func() ([]stubblePESection, error) {
		return nil, errors.New("Stubble image has an invalid bounded AArch64 EFI envelope")
	}
	if len(image) < 64 || len(image) > maximumStubbleImageBytes || !bytes.Equal(image[:2], []byte("MZ")) {
		return bad()
	}
	peOffset := uint64(binary.LittleEndian.Uint32(image[0x3c:]))
	if peOffset < 64 || peOffset+24 > uint64(len(image)) || !bytes.Equal(image[peOffset:peOffset+4], []byte{'P', 'E', 0, 0}) {
		return bad()
	}
	coff := image[peOffset+4 : peOffset+24]
	count, optionalSize := uint64(binary.LittleEndian.Uint16(coff[2:])), uint64(binary.LittleEndian.Uint16(coff[16:]))
	sectionOffset := peOffset + 24 + optionalSize
	if binary.LittleEndian.Uint16(coff) != pe.IMAGE_FILE_MACHINE_ARM64 || count == 0 || count > maximumStubbleSections ||
		optionalSize < 112 || sectionOffset+count*40 > uint64(len(image)) {
		return bad()
	}
	optional := image[peOffset+24 : sectionOffset]
	entry := binary.LittleEndian.Uint32(optional[16:])
	imageSize, headersSize := binary.LittleEndian.Uint32(optional[56:]), binary.LittleEndian.Uint32(optional[60:])
	directories := uint64(binary.LittleEndian.Uint32(optional[108:]))
	if binary.LittleEndian.Uint16(optional) != 0x20b || binary.LittleEndian.Uint16(optional[68:]) != pe.IMAGE_SUBSYSTEM_EFI_APPLICATION ||
		entry == 0 || entry >= imageSize || imageSize > maximumStubbleImageBytes || headersSize == 0 ||
		uint64(headersSize) > uint64(len(image)) || headersSize > imageSize || sectionOffset+count*40 > uint64(headersSize) ||
		112+directories*8 != optionalSize {
		return bad()
	}
	// Validate declared metadata extents with arithmetic only. A valid bounded
	// table may exist, but it cannot cause allocations or work proportional to
	// its symbol or relocation count in this observer.
	symbolOffset, symbols := uint64(binary.LittleEndian.Uint32(coff[8:])), uint64(binary.LittleEndian.Uint32(coff[12:]))
	if symbolOffset == 0 && symbols != 0 {
		return bad()
	}
	if symbolOffset != 0 {
		stringOffset := symbolOffset + symbols*18
		if symbolOffset < uint64(headersSize) || stringOffset+4 > uint64(len(image)) {
			return bad()
		}
		stringBytes := uint64(binary.LittleEndian.Uint32(image[stringOffset:]))
		if stringBytes < 4 || stringOffset+stringBytes > uint64(len(image)) {
			return bad()
		}
	}
	sections := make([]stubblePESection, 0, int(count))
	for index := uint64(0); index < count; index++ {
		header := image[sectionOffset+index*40 : sectionOffset+(index+1)*40]
		relocationOffset, relocations := uint64(binary.LittleEndian.Uint32(header[24:])), uint64(binary.LittleEndian.Uint16(header[32:]))
		if relocations != 0 && (relocationOffset < uint64(headersSize) || relocationOffset+relocations*10 > uint64(len(image))) {
			return bad()
		}
		name := header[:8]
		if end := bytes.IndexByte(name, 0); end >= 0 {
			name = name[:end]
		}
		rawOffset := binary.LittleEndian.Uint32(header[20:])
		if (string(name) == ".dtbauto" || string(name) == ".hwids") && rawOffset < headersSize {
			return bad()
		}
		sections = append(sections, stubblePESection{Name: string(name), VirtualSize: binary.LittleEndian.Uint32(header[8:]),
			Size: binary.LittleEndian.Uint32(header[16:]), Offset: rawOffset})
	}
	return sections, nil
}

// The virtual payload excludes file-alignment padding, matching Stubble's
// VirtualSize input and the producer's byte-identical DTB attribution.
func stubbleSectionPayload(image []byte, section stubblePESection, maximum uint32) ([]byte, error) {
	if section.VirtualSize == 0 || section.VirtualSize > maximum || section.VirtualSize > section.Size ||
		uint64(section.Offset)+uint64(section.Size) > uint64(len(image)) {
		return nil, fmt.Errorf("Stubble %s section has an invalid bounded payload", section.Name)
	}
	return image[uint64(section.Offset) : uint64(section.Offset)+uint64(section.VirtualSize)], nil
}

// stubbleHWIDRecords decodes ukify's complete 28-byte record stream and string
// table. Type-2 firmware records are checked but do not participate in the
// devicetree matcher; duplicate type-1 routes retain their actual stored order.
func stubbleHWIDRecords(table []byte) ([]StubbleHWIDRecord, error) {
	if len(table) < stubbleHWIDRecordBytes*2 || len(table) > maximumStubbleHWIDBytes {
		return nil, errors.New("Stubble HWID table has an invalid bounded size")
	}
	type row struct {
		typeID      uint32
		guid        []byte
		nameOffset  uint32
		valueOffset uint32
	}
	var rows []row
	stringsOffset := 0
	for offset := 0; offset+stubbleHWIDRecordBytes <= len(table); offset += stubbleHWIDRecordBytes {
		value := table[offset : offset+stubbleHWIDRecordBytes]
		descriptor := binary.LittleEndian.Uint32(value)
		if descriptor == 0 {
			if !bytes.Equal(value, make([]byte, stubbleHWIDRecordBytes)) {
				return nil, errors.New("Stubble HWID terminator is not a null record")
			}
			stringsOffset = offset + stubbleHWIDRecordBytes
			break
		}
		if descriptor != 0x1000001c && descriptor != 0x2000001c || len(rows) >= maximumStubbleHWIDRecords {
			return nil, errors.New("Stubble HWID table has an unsupported descriptor or too many records")
		}
		rows = append(rows, row{descriptor >> 28, value[4:20], binary.LittleEndian.Uint32(value[20:]), binary.LittleEndian.Uint32(value[24:])})
	}
	if stringsOffset == 0 || len(rows) == 0 || stringsOffset >= len(table) {
		return nil, errors.New("Stubble HWID table lacks bounded records, terminator, or strings")
	}
	stringsByOffset := make(map[uint32]string)
	for offset := stringsOffset; offset < len(table); {
		if len(stringsByOffset) >= maximumStubbleHWIDRecords*2 {
			return nil, errors.New("Stubble HWID string table has too many entries")
		}
		end := bytes.IndexByte(table[offset:], 0)
		if end <= 0 || end > 512 {
			return nil, errors.New("Stubble HWID string table is malformed")
		}
		value := string(table[offset : offset+end])
		if !validSelectorValue(value) || !utf8.ValidString(value) {
			return nil, errors.New("Stubble HWID string table has invalid text")
		}
		stringsByOffset[uint32(offset)] = value
		offset += end + 1
	}
	var result []StubbleHWIDRecord
	for _, row := range rows {
		_, nameOK := stringsByOffset[row.nameOffset]
		value, valueOK := stringsByOffset[row.valueOffset]
		if !nameOK || !valueOK {
			return nil, errors.New("Stubble HWID record has an invalid string offset")
		}
		if row.typeID == 1 {
			if !compatibleExpression.MatchString(value) {
				return nil, errors.New("Stubble HWID record has an invalid compatible")
			}
			guid := fmt.Sprintf("%08x-%04x-%04x-%x-%x", binary.LittleEndian.Uint32(row.guid[:4]), binary.LittleEndian.Uint16(row.guid[4:6]),
				binary.LittleEndian.Uint16(row.guid[6:8]), row.guid[8:10], row.guid[10:16])
			result = append(result, StubbleHWIDRecord{HWID: guid, Compatible: value})
		}
	}
	if len(result) == 0 {
		return nil, errors.New("Stubble HWID table has no devicetree record")
	}
	return result, nil
}

// stubbleFDTCompatibles reads the root property while checking the bounded FDT
// token stream. Secondary strings retain their actual order, not a sorted set.
func stubbleFDTCompatibles(payload []byte) ([]string, error) {
	bad := func() ([]string, error) { return nil, errors.New("malformed bounded FDT or root compatible property") }
	if len(payload) < 40 || len(payload) > maximumStubbleDTBBytes || binary.BigEndian.Uint32(payload) != 0xd00dfeed {
		return bad()
	}
	total := uint64(binary.BigEndian.Uint32(payload[4:]))
	structureOffset, stringsOffset := uint64(binary.BigEndian.Uint32(payload[8:])), uint64(binary.BigEndian.Uint32(payload[12:]))
	reserveOffset := uint64(binary.BigEndian.Uint32(payload[16:]))
	version, compatibleVersion := binary.BigEndian.Uint32(payload[20:]), binary.BigEndian.Uint32(payload[24:])
	stringsSize, structureSize := uint64(binary.BigEndian.Uint32(payload[32:])), uint64(binary.BigEndian.Uint32(payload[36:]))
	// Future versions may retain the version-17 layout through last_comp_version;
	// accepting that declared compatibility follows the kernel's libfdt policy.
	if total < 40 || total != uint64(len(payload)) || version < 17 || compatibleVersion > 17 ||
		structureOffset < 40 || structureOffset%4 != 0 || structureSize%4 != 0 || structureSize == 0 ||
		stringsOffset < 40 || structureOffset+structureSize > total || stringsOffset+stringsSize > total ||
		structureOffset < stringsOffset+stringsSize && stringsOffset < structureOffset+structureSize ||
		reserveOffset < 40 || reserveOffset%8 != 0 || reserveOffset+16 > total {
		return bad()
	}
	reserveEnd := reserveOffset
	for {
		if reserveEnd+16 > total || reserveEnd < structureOffset+structureSize && structureOffset < reserveEnd+16 ||
			reserveEnd < stringsOffset+stringsSize && stringsOffset < reserveEnd+16 {
			return bad()
		}
		zero := binary.BigEndian.Uint64(payload[reserveEnd:]) == 0 && binary.BigEndian.Uint64(payload[reserveEnd+8:]) == 0
		reserveEnd += 16
		if zero {
			break
		}
	}
	structure := payload[structureOffset : structureOffset+structureSize]
	stringsBlock := payload[stringsOffset : stringsOffset+stringsSize]
	depth, cursor := 0, 0
	var hasChildren [256]bool
	rootSeen, rootClosed := false, false
	var compatibles []string
	for cursor+4 <= len(structure) {
		token := binary.BigEndian.Uint32(structure[cursor:])
		cursor += 4
		switch token {
		case 1: // FDT_BEGIN_NODE
			if rootClosed || depth >= 256 {
				return bad()
			}
			end := bytes.IndexByte(structure[cursor:], 0)
			if end < 0 || end > 255 || !utf8.Valid(structure[cursor:cursor+end]) || depth == 0 && (rootSeen || end != 0) {
				return bad()
			}
			cursor = (cursor + end + 1 + 3) &^ 3
			if cursor > len(structure) {
				return bad()
			}
			if depth > 0 {
				hasChildren[depth-1] = true
			}
			hasChildren[depth] = false
			rootSeen = true
			depth++
		case 2: // FDT_END_NODE
			if depth == 0 {
				return bad()
			}
			depth--
			rootClosed = depth == 0
		case 3: // FDT_PROP
			if depth == 0 || hasChildren[depth-1] || cursor+8 > len(structure) {
				return bad()
			}
			length, nameOffset := uint64(binary.BigEndian.Uint32(structure[cursor:])), uint64(binary.BigEndian.Uint32(structure[cursor+4:]))
			cursor += 8
			if uint64(cursor)+length > uint64(len(structure)) || nameOffset >= uint64(len(stringsBlock)) {
				return bad()
			}
			nameWindow := stringsBlock[nameOffset:]
			if len(nameWindow) > 256 {
				nameWindow = nameWindow[:256]
			}
			nameEnd := bytes.IndexByte(nameWindow, 0)
			if nameEnd <= 0 {
				return bad()
			}
			if depth == 1 && string(stringsBlock[nameOffset:nameOffset+uint64(nameEnd)]) == "compatible" {
				if compatibles != nil || length == 0 {
					return bad()
				}
				value := structure[cursor : uint64(cursor)+length]
				if value[len(value)-1] != 0 {
					return bad()
				}
				seen := make(map[string]bool)
				for _, item := range bytes.Split(value[:len(value)-1], []byte{0}) {
					compatible := string(item)
					if !compatibleExpression.MatchString(compatible) || seen[compatible] || len(compatibles) >= maximumCompatibleStrings {
						return bad()
					}
					seen[compatible] = true
					compatibles = append(compatibles, compatible)
				}
			}
			cursor = (cursor + int(length) + 3) &^ 3
			if cursor > len(structure) {
				return bad()
			}
		case 4: // FDT_NOP
		case 9: // FDT_END
			if !rootClosed || depth != 0 || len(compatibles) == 0 || cursor != len(structure) {
				return bad()
			}
			return compatibles, nil
		default:
			return bad()
		}
	}
	return bad()
}
