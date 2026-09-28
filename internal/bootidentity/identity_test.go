package bootidentity

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// TestPrivateIdentityCannotBeSerialised prevents accidental receipt expansion.
func TestPrivateIdentityCannotBeSerialised(t *testing.T) {
	fs := Filesystem{UUID: "private-root", Device: "/dev/private-device"}
	for _, value := range []any{fs, &fs, Identity{Root: fs, Boot: fs}} {
		if data, err := json.Marshal(value); err == nil || strings.Contains(err.Error(), "private-root") || strings.Contains(string(data), "private-root") {
			t.Fatalf("private identity serialisation: %s %v", data, err)
		}
	}
}

// TestResolvePinsIdentity refuses stale evidence while preserving cancellation.
func TestResolvePinsIdentity(t *testing.T) {
	fs := Filesystem{UUID: "fixture-root", FSType: "ext4", FSRoot: "/", Mountpoint: "/"}
	current := Identity{Root: fs, Boot: fs}
	ctx := WithResolver(context.Background(), func(context.Context, string) (Identity, error) { return current, nil })
	pinned := WithExpected(ctx, current)
	if _, err := Resolve(pinned, "/"); err != nil {
		t.Fatal(err)
	}
	current.Boot.UUID = "private-different-boot"
	if _, err := Resolve(pinned, "/"); err == nil || strings.Contains(err.Error(), "private-different-boot") {
		t.Fatalf("changed/private identity: %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := Resolve(cancelled, "/"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

// TestIdentityValidationRejectsUnsupportedViews prevents offline directories
// and nested ESP paths from masquerading as the selected root or boot view.
func TestIdentityValidationRejectsUnsupportedViews(t *testing.T) {
	for _, tc := range []struct {
		name       string
		root, boot Filesystem
	}{
		{"no identifiers", Filesystem{FSRoot: "/", Mountpoint: "/"}, Filesystem{FSRoot: "/", Mountpoint: "/"}},
		{"wrong root", Filesystem{UUID: "x", FSRoot: "/", Mountpoint: "/other"}, Filesystem{UUID: "x", FSRoot: "/", Mountpoint: "/"}},
		{"ESP view", Filesystem{UUID: "x", FSRoot: "/", Mountpoint: "/"}, Filesystem{UUID: "y", FSRoot: "/", Mountpoint: "/boot/efi"}},
		{"unclean subvolume", Filesystem{UUID: "x", FSRoot: "/a/../b", Mountpoint: "/"}, Filesystem{UUID: "y", FSRoot: "/", Mountpoint: "/boot"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := WithResolver(context.Background(), func(context.Context, string) (Identity, error) { return Identity{Root: tc.root, Boot: tc.boot}, nil })
			if _, err := Resolve(ctx, "/"); err == nil {
				t.Fatal("unsupported view accepted")
			}
		})
	}
}
