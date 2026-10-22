package build

import "errors"

// LegacySchemaVersion identifies the exact pre-manifest native build contract.
// New builds always produce SchemaVersion; legacy reading does not retrofit
// compatibility or authorise a new manifest-aware release.
const LegacySchemaVersion = 1

// validateReceiptSchema keeps legacy and manifest-aware authority disjoint.
func validateReceiptSchema(receipt BundleReceipt) error {
	switch receipt.SchemaVersion {
	case LegacySchemaVersion:
		if receipt.Compatibility != nil {
			return errors.New("legacy camera receipt cannot contain compatibility metadata")
		}
	case SchemaVersion:
		if receipt.Compatibility == nil {
			return errors.New("camera build omits compatibility authority")
		}
	default:
		return errors.New("unsupported camera build receipt schema")
	}
	return nil
}
