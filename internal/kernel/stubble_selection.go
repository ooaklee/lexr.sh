package kernel

import (
	"errors"
	"fmt"
)

const (
	// OrderedBundleSchemaVersion retains ordered Stubble inventory without
	// changing the unique-route contract of existing schema-2 manifests.
	OrderedBundleSchemaVersion = 3
	// StubbleInventoryOrderPolicy records observed image order, not a guessed
	// specificity rank for a hardware ID or a hardware qualification result.
	StubbleInventoryOrderPolicy = "stubble-inventory-order-v1"
	// maximumStubbleHWIDRecords bounds records decoded from the embedded table.
	maximumStubbleHWIDRecords = 8192
)

// StubbleHWIDRecord retains one type-1 record in actual .hwids table order.
// A board's CHID priority is evaluated at boot and is not encoded by this UUID.
type StubbleHWIDRecord struct {
	HWID       string `json:"hwid"`
	Compatible string `json:"compatible"`
}

// StubbleSelection binds the complete optional inventory to observed image
// bytes. PayloadOrder is the physical .dtbauto order, not device-name order.
// Shared optional routes remain observations, never support claims.
type StubbleSelection struct {
	Policy          string              `json:"policy"`
	ImageSHA256     string              `json:"image_sha256"`
	HWIDTableSHA256 string              `json:"hwid_table_sha256"`
	PayloadOrder    []string            `json:"payload_order"`
	HWIDRecords     []StubbleHWIDRecord `json:"hwid_records"`
}

// ValidateSchemaVersion preserves schema 2 and explicitly opts ordered
// inventories into schema 3. Relabeling either contract is rejected.
func (bundle Bundle) ValidateSchemaVersion() error {
	ordered := bundle.DTBSelectionProvenance != nil && bundle.DTBSelectionProvenance.StubbleSelection != nil
	if bundle.SchemaVersion == BundleSchemaVersion && !ordered {
		return nil
	}
	if bundle.SchemaVersion == OrderedBundleSchemaVersion && ordered && bundle.EffectiveDTBDelivery == DTBDeliveryEmbedded {
		return nil
	}
	return fmt.Errorf("kernel bundle schema %d does not match its unique-route or ordered-inventory contract", bundle.SchemaVersion)
}

// HasSharedSelectionRoutes reports overlap without interpreting it as a
// runtime choice. Callers must still validate every raw record and payload.
func HasSharedSelectionRoutes(trees []DeviceTree) bool {
	seen := make(map[DeviceTreeSelector]string)
	for _, tree := range trees {
		for _, selector := range tree.Selectors {
			if owner, found := seen[selector]; found && owner != tree.Device {
				return true
			}
			seen[selector] = tree.Device
		}
	}
	return false
}

// cloneStubbleSelection preserves observable order while taking ownership of
// the caller's slices. Canonicalisation must never sort either order away.
func cloneStubbleSelection(source *StubbleSelection) *StubbleSelection {
	if source == nil {
		return nil
	}
	value := *source
	value.PayloadOrder = append([]string(nil), source.PayloadOrder...)
	value.HWIDRecords = append([]StubbleHWIDRecord(nil), source.HWIDRecords...)
	return &value
}

// validateStubbleSelection permits overlap only among explicitly optional
// payloads. Required devices retain the conservative globally-unique route
// guard; this policy does not guess a board-specific winner from sorted UUIDs.
func validateStubbleSelection(provenance *DTBSelectionProvenance, trees []DeviceTree) error {
	selection := provenance.StubbleSelection
	if selection == nil {
		return nil
	}
	if provenance.Tool != "stubble" || provenance.MachDBSHA256 != "" || selection.Policy != StubbleInventoryOrderPolicy {
		return errors.New("unsupported ordered Stubble inventory policy or machdb selection")
	}
	if !validSHA256(selection.ImageSHA256) || !validSHA256(selection.HWIDTableSHA256) {
		return errors.New("ordered Stubble inventory requires image and HWID table digests")
	}
	if len(selection.PayloadOrder) != len(trees) || len(trees) == 0 || len(trees) > maximumBundleDeviceTrees {
		return errors.New("ordered Stubble inventory must name every embedded payload exactly once")
	}
	byDevice := make(map[string]DeviceTree, len(trees))
	for _, tree := range trees {
		if tree.EmbeddedMatches != 1 || len(tree.CompatibleStrings) == 0 {
			return errors.New("ordered Stubble inventory requires one attributable payload per device")
		}
		byDevice[tree.Device] = tree
	}
	seen := make(map[string]bool, len(trees))
	firstCompatible := make(map[string]string, len(trees))
	for _, device := range selection.PayloadOrder {
		tree, found := byDevice[device]
		if !found || seen[device] {
			return errors.New("ordered Stubble inventory contains an unknown or duplicate payload")
		}
		seen[device] = true
		compatible := tree.CompatibleStrings[0]
		if _, found := firstCompatible[compatible]; !found {
			firstCompatible[compatible] = device
		}
	}
	for _, tree := range trees {
		if tree.Required && firstCompatible[tree.CompatibleStrings[0]] != tree.Device {
			return fmt.Errorf("required device tree %s is shadowed by an earlier primary-compatible payload", tree.Device)
		}
	}
	if len(selection.HWIDRecords) == 0 || len(selection.HWIDRecords) > maximumStubbleHWIDRecords {
		return errors.New("ordered Stubble inventory has an invalid HWID record count")
	}
	observed := make(map[string]map[string]bool)
	routeCompatibles := make(map[string]map[string]bool)
	for _, record := range selection.HWIDRecords {
		if !canonicalUUIDExpression.MatchString(record.HWID) || record.HWID == "00000000-0000-0000-0000-000000000000" || !compatibleExpression.MatchString(record.Compatible) {
			return errors.New("ordered Stubble inventory contains an invalid HWID record")
		}
		if observed[record.Compatible] == nil {
			observed[record.Compatible] = make(map[string]bool)
		}
		observed[record.Compatible][record.HWID] = true
		if routeCompatibles[record.HWID] == nil {
			routeCompatibles[record.HWID] = make(map[string]bool)
		}
		routeCompatibles[record.HWID][record.Compatible] = true
	}
	for _, evidence := range provenance.Selections {
		expected := make(map[string]bool)
		for _, record := range evidence.Records {
			if record.Source != "hwids" {
				return errors.New("ordered Stubble inventory supports HWID records only")
			}
			for _, hwid := range record.HWIDs {
				expected[hwid] = true
			}
		}
		tree, found := byDevice[evidence.Device]
		if !found || len(tree.CompatibleStrings) == 0 {
			return errors.New("ordered Stubble evidence names an unknown device")
		}
		actual := observed[tree.CompatibleStrings[0]]
		if len(expected) != len(actual) {
			return fmt.Errorf("ordered Stubble HWID table disagrees with raw evidence for %s", tree.Device)
		}
		for hwid := range expected {
			if !actual[hwid] {
				return fmt.Errorf("ordered Stubble HWID table disagrees with raw evidence for %s", tree.Device)
			}
			if tree.Required && (len(routeCompatibles[hwid]) != 1 || !routeCompatibles[hwid][tree.CompatibleStrings[0]]) {
				return fmt.Errorf("required device tree %s shares an encoded HWID route with another compatible", tree.Device)
			}
		}
	}
	return nil
}
