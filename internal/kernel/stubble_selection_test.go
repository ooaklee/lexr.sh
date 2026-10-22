package kernel

import (
	"reflect"
	"strings"
	"testing"
)

// orderedSelectionOptions is a synthetic declaration, not observed hardware
// evidence. The two optional LCD payloads deliberately share all their routes.
func orderedSelectionOptions() BundleOptions {
	options := validBundleOptions(RequestedBootImageModeStubble, DTBDeliveryEmbedded, false)
	options.DeviceTrees[1].Required = false
	alias := CloneDeviceTrees(options.DeviceTrees[1:])[0]
	alias.Device, alias.Basename = "optional-lcd-alias", "alias.dtb"
	alias.Path = "usr/lib/firmware/" + testBundleABI + "/device-tree/qcom/alias.dtb"
	alias.SHA256 = strings.Repeat("8", 64)
	options.DeviceTrees = append(options.DeviceTrees, alias)
	options.EmbeddedDTBCount = 3
	options.DTBSelectionProvenance.Selections = append(options.DTBSelectionProvenance.Selections, DeviceTreeSelectionEvidence{
		Device: alias.Device, Records: []DTBSelectionRecord{{Source: "hwids", Compatible: "microsoft,denali-x1p", HWIDs: []string{"22222222-2222-5222-8222-222222222222"}}},
	})
	options.DTBSelectionProvenance.StubbleSelection = &StubbleSelection{
		Policy: StubbleInventoryOrderPolicy, ImageSHA256: strings.Repeat("5", 64), HWIDTableSHA256: strings.Repeat("6", 64),
		PayloadOrder: []string{alias.Device, "surface-pro-11-x1e-oled", "surface-pro-11-x1p-lcd"},
		HWIDRecords: []StubbleHWIDRecord{
			{HWID: "22222222-2222-5222-8222-222222222222", Compatible: "microsoft,denali-x1p"},
			{HWID: "11111111-1111-5111-8111-111111111111", Compatible: "microsoft,denali"},
		},
	}
	return options
}

// TestOrderedStubbleInventoryPreservesOptionalOverlapAndExplicitClaims keeps
// every payload/record while advertising only the unambiguous required model.
func TestOrderedStubbleInventoryPreservesOptionalOverlapAndExplicitClaims(t *testing.T) {
	options := orderedSelectionOptions()
	wantOrder := append([]string(nil), options.DTBSelectionProvenance.StubbleSelection.PayloadOrder...)
	wantRecords := append([]StubbleHWIDRecord(nil), options.DTBSelectionProvenance.StubbleSelection.HWIDRecords...)
	bundle, err := NewBundle(options)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.SchemaVersion != OrderedBundleSchemaVersion || bundle.ValidateSchemaVersion() != nil || len(bundle.DeviceTrees) != 3 || len(bundle.DTBSelectionProvenance.Selections) != 3 {
		t.Fatalf("incomplete ordered declaration: %+v", bundle)
	}
	if !reflect.DeepEqual(bundle.BootPlatforms(), []string{"surface-pro-11-x1e-oled"}) {
		t.Fatalf("optional payloads became support claims: %v", bundle.BootPlatforms())
	}
	selection := bundle.DTBSelectionProvenance.StubbleSelection
	if !reflect.DeepEqual(selection.PayloadOrder, wantOrder) || !reflect.DeepEqual(selection.HWIDRecords, wantRecords) {
		t.Fatal("canonicalization sorted away measured order")
	}
	options.DTBSelectionProvenance.StubbleSelection.PayloadOrder[0] = "changed"
	options.DTBSelectionProvenance.StubbleSelection.HWIDRecords[0].Compatible = "changed"
	if !reflect.DeepEqual(selection.PayloadOrder, wantOrder) || !reflect.DeepEqual(selection.HWIDRecords, wantRecords) {
		t.Fatal("ordered evidence was not deeply owned")
	}
}

// TestOrderedStubbleInventoryRejectsUnprovedOrRequiredOverlap preserves the
// fail-closed claim gate without inventing board-specific CHID priorities.
func TestOrderedStubbleInventoryRejectsUnprovedOrRequiredOverlap(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*BundleOptions)
	}{
		{"legacy overlaps still rejected", func(o *BundleOptions) { o.DTBSelectionProvenance.StubbleSelection = nil }},
		{"unknown policy", func(o *BundleOptions) { o.DTBSelectionProvenance.StubbleSelection.Policy = "guess-priority" }},
		{"missing image digest", func(o *BundleOptions) { o.DTBSelectionProvenance.StubbleSelection.ImageSHA256 = "" }},
		{"missing table digest", func(o *BundleOptions) { o.DTBSelectionProvenance.StubbleSelection.HWIDTableSHA256 = "" }},
		{"missing ordinal", func(o *BundleOptions) {
			o.DTBSelectionProvenance.StubbleSelection.PayloadOrder = o.DTBSelectionProvenance.StubbleSelection.PayloadOrder[:2]
		}},
		{"duplicate ordinal", func(o *BundleOptions) {
			o.DTBSelectionProvenance.StubbleSelection.PayloadOrder[0] = o.DTBSelectionProvenance.StubbleSelection.PayloadOrder[1]
		}},
		{"unknown payload", func(o *BundleOptions) { o.DTBSelectionProvenance.StubbleSelection.PayloadOrder[0] = "unknown" }},
		{"required shadowed LCD", func(o *BundleOptions) { o.DeviceTrees[1].Required = true }},
		{"required first LCD overlap", func(o *BundleOptions) { o.DeviceTrees[2].Required = true }},
		{"missing raw records", func(o *BundleOptions) { o.DTBSelectionProvenance.StubbleSelection.HWIDRecords = nil }},
		{"table UUID malformed", func(o *BundleOptions) { o.DTBSelectionProvenance.StubbleSelection.HWIDRecords[0].HWID = "not-a-uuid" }},
		{"zero UUID", func(o *BundleOptions) {
			o.DTBSelectionProvenance.StubbleSelection.HWIDRecords[0].HWID = "00000000-0000-0000-0000-000000000000"
		}},
		{"table compatible malformed", func(o *BundleOptions) {
			o.DTBSelectionProvenance.StubbleSelection.HWIDRecords[0].Compatible = "../board"
		}},
		{"table evidence missing", func(o *BundleOptions) {
			o.DTBSelectionProvenance.StubbleSelection.HWIDRecords = o.DTBSelectionProvenance.StubbleSelection.HWIDRecords[:1]
		}},
		{"table evidence changed", func(o *BundleOptions) {
			o.DTBSelectionProvenance.StubbleSelection.HWIDRecords[0].HWID = "33333333-3333-5333-8333-333333333333"
		}},
		{"required route claimed by an unembedded compatible", func(o *BundleOptions) {
			o.DTBSelectionProvenance.StubbleSelection.HWIDRecords = append(o.DTBSelectionProvenance.StubbleSelection.HWIDRecords,
				StubbleHWIDRecord{HWID: "11111111-1111-5111-8111-111111111111", Compatible: "vendor,unembedded"})
		}},
		{"unmodelled machdb", func(o *BundleOptions) { o.DTBSelectionProvenance.MachDBSHA256 = strings.Repeat("7", 64) }},
		{"unsupported tool", func(o *BundleOptions) { o.DTBSelectionProvenance.Tool = "other-stub" }},
		{"table record bound", func(o *BundleOptions) {
			o.DTBSelectionProvenance.StubbleSelection.HWIDRecords = make([]StubbleHWIDRecord, maximumStubbleHWIDRecords+1)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			options := orderedSelectionOptions()
			test.edit(&options)
			if _, err := NewBundle(options); err == nil {
				t.Fatal("invalid declaration accepted")
			}
		})
	}
}

// TestOrderedInventoryRetainsUnembeddedDatabaseRows distinguishes the complete
// table from payload claims without allowing it to shadow a required route.
func TestOrderedInventoryRetainsUnembeddedDatabaseRows(t *testing.T) {
	options := orderedSelectionOptions()
	row := StubbleHWIDRecord{HWID: "33333333-3333-5333-8333-333333333333", Compatible: "vendor,unembedded"}
	selection := options.DTBSelectionProvenance.StubbleSelection
	selection.HWIDRecords = append(selection.HWIDRecords, row)
	bundle, err := NewBundle(options)
	if err != nil {
		t.Fatal(err)
	}
	actual := bundle.DTBSelectionProvenance.StubbleSelection.HWIDRecords
	if len(actual) != 3 || actual[2] != row || !reflect.DeepEqual(bundle.BootPlatforms(), []string{"surface-pro-11-x1e-oled"}) {
		t.Fatal("complete database row was lost or became a support claim")
	}
}

// TestBundleSchemaVersionDoesNotSilentlyReinterpretLegacyContract prevents
// relabeling schema 2 or accepting policy-less schema 3 declarations.
func TestBundleSchemaVersionDoesNotSilentlyReinterpretLegacyContract(t *testing.T) {
	legacy, err := NewBundle(validBundleOptions(RequestedBootImageModeStubble, DTBDeliveryEmbedded, false))
	if err != nil || legacy.ValidateSchemaVersion() != nil {
		t.Fatalf("legacy contract changed: %v", err)
	}
	legacy.SchemaVersion = OrderedBundleSchemaVersion
	if legacy.ValidateSchemaVersion() == nil {
		t.Fatal("policy-less schema 3 accepted")
	}
	ordered, err := NewBundle(orderedSelectionOptions())
	if err != nil {
		t.Fatal(err)
	}
	ordered.SchemaVersion = BundleSchemaVersion
	if ordered.ValidateSchemaVersion() == nil {
		t.Fatal("schema-3 evidence silently interpreted as schema 2")
	}
}
