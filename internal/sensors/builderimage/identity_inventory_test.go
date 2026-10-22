package builderimage

import (
	"archive/tar"
	"context"
	"path"
	"strings"
	"testing"
)

// These controlled Docker-boundary fixtures prove refusal/authority behaviour,
// not Linux account creation, a working private bus or physical sensors.
func TestManagerRejectsUnverifiedBuildIdentityInventory(t *testing.T) {
	const user = "lexr-sensor-build:x:65532:65532:Lexr sensor source builder:/work/job/home:/usr/sbin/nologin\n"
	const group = "lexr-sensor-build:x:65532:\n"
	const identity = "[build-identity]\n" + user + group + "password_status=L\n"
	for _, row := range []struct {
		name string
		edit func(string) string
	}{
		{"missing", func(s string) string { return strings.Replace(s, identity, "", 1) }},
		{"duplicate", func(s string) string { return s + identity }},
		{"uid", func(s string) string {
			return strings.Replace(s, user, strings.Replace(user, ":65532:", ":65531:", 1), 1)
		}},
		{"primary-gid", func(s string) string {
			return strings.Replace(s, user, strings.Replace(user, ":65532:65532:", ":65532:65531:", 1), 1)
		}},
		{"name", func(s string) string {
			return strings.Replace(s, user, strings.Replace(user, "lexr-sensor-build", "other-build", 1), 1)
		}},
		{"gecos", func(s string) string {
			return strings.Replace(s, user, strings.Replace(user, "Lexr sensor source builder", "other description", 1), 1)
		}},
		{"home", func(s string) string {
			return strings.Replace(s, user, strings.Replace(user, "/work/job/home", "/root", 1), 1)
		}},
		{"shell", func(s string) string {
			return strings.Replace(s, user, strings.Replace(user, "/usr/sbin/nologin", "/bin/bash", 1), 1)
		}},
		{"group", func(s string) string { return strings.Replace(s, group, "lexr-sensor-build:x:65531:\n", 1) }},
		{"group-members", func(s string) string { return strings.Replace(s, group, "lexr-sensor-build:x:65532:other\n", 1) }},
		{"unlocked", func(s string) string { return strings.Replace(s, "password_status=L\n", "password_status=P\n", 1) }},
		{"blank-password", func(s string) string { return strings.Replace(s, "password_status=L\n", "password_status=NP\n", 1) }},
	} {
		t.Run(row.name, func(t *testing.T) {
			f := newManagerTestRunner(t)
			original, err := inventoryMember(f.tools, ToolchainInventoryPath)
			if err != nil || strings.Count(original, identity) != 1 {
				t.Fatal("controlled inventory lacks the unique canonical account record", err)
			}
			changed := row.edit(original)
			if changed == original {
				t.Fatal("fixture edit did not change the account record")
			}
			f.tools = managerTestTar(t, []tar.Header{{Name: path.Base(ToolchainInventoryPath), Typeflag: tar.TypeReg, Size: int64(len(changed))}}, [][]byte{[]byte(changed)})
			result, err := newManagerUnderTest(f).Prepare(context.Background(), Request{})
			managerTestNoGrant(t, f, result, err, false)
			if result.Terminal == nil || result.Terminal.ExitCode != 0 || result.Provenance != nil {
				t.Fatal("identity refusal lost stopped terminal evidence or granted provenance")
			}
		})
	}
}
