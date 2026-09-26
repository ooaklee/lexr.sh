// Package lexr exposes build-time assets shared by the CLI entrypoints.
package lexr

import (
	"embed"
	"io/fs"
)

// embeddedCatalog contains the catalogue shipped with this version of the CLI.
//
//go:embed supported-isos.json
var embeddedCatalog embed.FS

// embeddedUserspaceCatalog contains the audited userspace component catalogue.
//
//go:embed supported-userspace.json
var embeddedUserspaceCatalog embed.FS

// CatalogFS returns the read-only filesystem containing supported-isos.json.
func CatalogFS() fs.FS {
	return embeddedCatalog
}

// UserspaceCatalogFS returns the read-only filesystem containing
// supported-userspace.json.
func UserspaceCatalogFS() fs.FS {
	return embeddedUserspaceCatalog
}

// embeddedProjectTerms keeps generated support packages on the repository's
// single legal-document authority.
//
//go:embed LICENSE NOTICE
var embeddedProjectTerms embed.FS

// ProjectTermsFS returns the complete Apache-2.0 licence and project notice.
func ProjectTermsFS() fs.FS { return embeddedProjectTerms }
