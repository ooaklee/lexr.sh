package source

import (
	"reflect"
	"testing"
)

// TestApprovedMetadataRetainsCompiledAuthority binds packaging requirements to
// the source authenticator's complete existing inventory policy, not a lineage
// string or a newly trusted neighbouring manifest.
func TestApprovedMetadataRetainsCompiledAuthority(t *testing.T) {
	want := []File{
		{Path: "libqmi/SOURCE.json", SHA256: "ab6008235d2f691ce7a8fca2d82aa072ea12bc9874f337f0ebbe1effd82f3083", Bytes: 157815, GitMode: "100644"},
		{Path: "libssc/SOURCE.json", SHA256: "6411dd017ba6aa0d177972706158636a2945ae19db808c549785e6ebc1e2f98c", Bytes: 14556, GitMode: "100644"},
		{Path: "iio-sensor-proxy/SOURCE.json", SHA256: "aa9ac39d11cfdb6f9e1306624458e98579e7340c976bbe573cfb5adf105f8303", Bytes: 14377, GitMode: "100644"},
		{Path: "SOURCE.sha256", SHA256: "4fd055d4d21700047d2970fdd0ae5ddf9deda350d4de7cc298a2fc7a64ae796d", Bytes: 119728, GitMode: "100644"},
	}
	got := ApprovedMetadata()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("compiled inventory identities changed: %+v", got)
	}
	got[0].SHA256 = "changed"
	got = append(got, File{Path: "unreviewed"})
	if !reflect.DeepEqual(ApprovedMetadata(), want) {
		t.Fatal("caller mutation changed compiled source authority")
	}
}
