package cli

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	lexrconfig "github.com/ooaklee/lexr.sh/internal/config"
)

// TestConfiguredCommandFlags executes the real root pre-run and inspects the
// values a workflow receives, including present zeroes, lists and CLI overrides.
func TestConfiguredCommandFlags(t *testing.T) {
	for _, test := range []struct {
		name, yaml    string
		command, args []string
		want          map[string]string
	}{
		{"catalogue globals", "global: {catalog: custom.json}\ncatalog: {list: {json: true}}", []string{"catalog", "list"}, nil, map[string]string{"catalog": "custom.json", "json": "true"}},
		{"explicit false", "catalog: {list: {json: true}}", []string{"catalog", "list"}, []string{"--json=false"}, map[string]string{"json": "false"}},
		{"image defaults", "image: {create: {catalog_id: elementary-os-8-1-arm64, output: custom.iso, dry_run: true, companion_userspace: [audio, iptsd]}}", []string{"image", "create"}, nil, map[string]string{"output": "custom.iso", "dry-run": "true", "companion-userspace": "[audio,iptsd]"}},
		{"list replaced by CLI", "doctor: {userspace: {feature: [audio, camera], root: /target}}", []string{"doctor", "userspace"}, []string{"--feature", "power"}, map[string]string{"feature": "[power]", "root": "/target"}},
		{"empty list", "userspace: {status: {feature: [], json: true, user_home: ''}}", []string{"userspace", "status"}, nil, map[string]string{"feature": "[]", "json": "true", "user-home": ""}},
		{"null is unset", "kernel: {release: {list: {limit: null}}}", []string{"kernel", "release", "list"}, nil, map[string]string{"limit": "20"}},
		{"explicit zero", "kernel: {release: {list: {limit: 0}}}", []string{"kernel", "release", "list"}, nil, map[string]string{"limit": "0"}},
		{"required inputs", "kernel: {boot: {refresh: {root: /target, abi: 7.2.2-test-qcom-x1e}}}", []string{"kernel", "boot", "refresh"}, nil, map[string]string{"root": "/target", "abi": "7.2.2-test-qcom-x1e"}},
		{"hyphenated command", "kernel: {boot: {register-arch: {arch_root: /arch, esp: /esp, grub_directory: /grub, dry_run: true}}}", []string{"kernel", "boot", "register-arch"}, nil, map[string]string{"arch-root": "/arch", "esp": "/esp", "grub-directory": "/grub"}},
		{"consent cannot come from YAML", "image: {write: {device: /dev/danger, confirm: true}}", []string{"image", "write"}, []string{"test.iso"}, nil},
		{"safety overrides cannot come from YAML", "kernel: {install: {yes: true, force: true, overwrite: true, allow_unverified: true}}", []string{"kernel", "install"}, []string{"bundle"}, nil},
		{"ordinary image write defaults bind", "image: {write: {dry_run: true}}", []string{"image", "write"}, []string{"test.iso"}, map[string]string{"dry-run": "true"}},
		{"ordinary kernel install defaults bind", "kernel: {install: {root: /target, fallback_abi: old-qcom-x1e, package_set: runtime}}", []string{"kernel", "install"}, []string{"bundle"}, map[string]string{"root": "/target", "fallback-abi": "old-qcom-x1e", "package-set": "runtime"}},
		{"kernel local source default binds", "kernel: {build: {source_dir: /kernel/source, dry_run: true}}", []string{"kernel", "build"}, nil, map[string]string{"source-dir": "/kernel/source", "dry-run": "true"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := configFixture(t, "profile: x1e80100-microsoft-denali-oled\n"+test.yaml+"\n")
			root := NewRootCommand(strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
			command, _, err := root.Find(test.command)
			if err != nil {
				t.Fatal(err)
			}
			called := false
			command.RunE = func(command *cobra.Command, _ []string) error {
				called = true
				got := make(map[string]string)
				for name := range test.want {
					got[name] = command.Flags().Lookup(name).Value.String()
				}
				if !reflect.DeepEqual(got, test.want) {
					t.Fatalf("workflow flags = %#v, want %#v", got, test.want)
				}
				return nil
			}
			args := append([]string{"--config", path}, test.command...)
			root.SetArgs(append(args, test.args...))
			err = root.Execute()
			if test.want == nil {
				// Removed YAML keys must be rejected by the strict schema
				// before any workflow could receive them.
				if err == nil || called || !strings.Contains(err.Error(), "field") {
					t.Fatalf("consent YAML accepted: called=%t, error=%v", called, err)
				}
				return
			}
			if err != nil || !called {
				t.Fatalf("workflow called=%t, error=%v", called, err)
			}
		})
	}
}

// TestConfigurationLayeringReachesWorkflow exercises null and list replacement
// across files rather than testing the YAML merge separately from the CLI.
func TestConfigurationLayeringReachesWorkflow(t *testing.T) {
	base := configFixture(t, "profile: x1e80100-microsoft-denali-oled\nuserspace: {status: {root: /base, feature: [audio, camera], json: true}}\n")
	overlay := configFixture(t, "userspace: {status: {root: /overlay, feature: [power], json: false}}\n")
	root := NewRootCommand(strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	command, _, _ := root.Find([]string{"userspace", "status"})
	called := false
	command.RunE = func(command *cobra.Command, _ []string) error {
		called = true
		features, _ := command.Flags().GetStringSlice("feature")
		asJSON, _ := command.Flags().GetBool("json")
		path, _ := command.Flags().GetString("root")
		if !reflect.DeepEqual(features, []string{"power"}) || asJSON || path != "/overlay" {
			t.Fatalf("flags = %v, %t, %s", features, asJSON, path)
		}
		return nil
	}
	root.SetArgs([]string{"--config", base, "userspace", "status", "--config", overlay})
	if err := root.Execute(); err != nil || !called {
		t.Fatalf("called=%t, err=%v", called, err)
	}
}

// TestConfigurationSchemaNamesRealFlags prevents accepted-but-unused defaults
// when a command is moved or renamed independently of the YAML schema.
func TestConfigurationSchemaNamesRealFlags(t *testing.T) {
	root := NewRootCommand(strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	var visit func(reflect.Type, []string)
	visit = func(shape reflect.Type, path []string) {
		for index := 0; index < shape.NumField(); index++ {
			field := shape.Field(index)
			key := field.Tag.Get("yaml")
			if field.Type.Kind() == reflect.Struct {
				visit(field.Type, append(append([]string{}, path...), key))
				continue
			}
			if len(path) == 0 {
				continue
			}
			command := root
			if path[0] != "global" {
				var rest []string
				var err error
				command, rest, err = root.Find(path)
				if err != nil || len(rest) != 0 {
					t.Errorf("config path %s has no command", strings.Join(path, "."))
					continue
				}
			}
			name := strings.ReplaceAll(key, "_", "-")
			flag := command.Flags().Lookup(name)
			if flag == nil {
				flag = command.PersistentFlags().Lookup(name)
			}
			if flag == nil {
				flag = command.InheritedFlags().Lookup(name)
			}
			if flag == nil {
				t.Errorf("config %s.%s has no --%s flag", strings.Join(path, "."), key, name)
				continue
			}
			wantType := field.Type.Kind().String()
			if field.Type.Kind() == reflect.Slice {
				if _, ok := flag.Value.(pflag.SliceValue); !ok {
					t.Errorf("config %s.%s list is not a slice flag", strings.Join(path, "."), key)
				}
			} else if wantType != flag.Value.Type() {
				t.Errorf("config %s.%s type %s differs from flag %s", strings.Join(path, "."), key, wantType, flag.Value.Type())
			}
		}
	}
	visit(reflect.TypeOf(lexrconfig.Config{}), nil)
}
