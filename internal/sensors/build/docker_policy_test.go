package build

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/sensors/builderimage"
)

// Policy fixtures use stable source, image and ownership identities without a daemon.
const (
	policyTestToken = "0123456789abcdef01234567"
	policyTestImage = "sha256:" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	policyTestCID   = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
)

// policyTestSpec constructs the accepted creation policy for one fixed build phase.
func policyTestSpec(phase sourcePhase) sourceDockerSpec {
	spec := sourceDockerSpec{Phase: phase, Name: "lexr-sensors-" + string(phase) + "-" + policyTestToken, Token: policyTestToken, ImageID: policyTestImage, VolumeName: sourceVolumePrefix + policyTestToken, Jobs: 3, Env: fixedSourceEnvironment()}
	if phase == sourceBuild {
		spec.SnapshotPath = "/private/owned/source snapshot"
		spec.DaemonSnapshotPath = spec.SnapshotPath
	}
	return spec
}

// policyTestContainer supplies the matching requested and effective container observations.
func policyTestContainer(phase sourcePhase) sourceContainerInspection {
	spec := policyTestSpec(phase)
	c := sourceContainerInspection{ID: policyTestCID, Name: "/" + spec.Name, Image: spec.ImageID}
	c.Config = sourceImageConfig{Image: spec.ImageID, User: "65532:65532", WorkingDir: "/", Entrypoint: []string{"/usr/bin/timeout"}, Cmd: sourceCommand(spec), Env: spec.Env, Labels: map[string]string{sourceOwnerLabel: sourceOwnerValue, sourceTokenLabel: spec.Token, sourceInitRecipeLabel: InitRecipeSHA256(), sourceRecipeLabel: SourceRecipeSHA256(), sourcePhaseLabel: string(phase), "io.lexr.sensors.builder": "dependencies-v1", "io.lexr.sensors.recipe": builderimage.RecipeSHA256()}}
	h := &c.HostConfig
	h.ReadonlyRootfs, h.NetworkMode = true, "none"
	h.PidMode, h.IpcMode, h.CgroupnsMode = "", "private", "private"
	h.PidsLimit, h.Memory, h.NanoCpus = 512, 4<<30, int64(spec.Jobs)*1_000_000_000
	h.CapDrop, h.SecurityOpt = []string{"ALL"}, []string{"no-new-privileges"}
	h.Tmpfs = map[string]string{"/tmp": "rw,nosuid,nodev,size=1g,mode=1777"}
	h.RestartPolicy.Name = "no"
	h.Mounts = []sourceHostMount{{Type: "volume", Source: spec.VolumeName, Target: "/work", VolumeOptions: &sourceVolumeOptions{NoCopy: true}}}
	c.Mounts = []sourceEffectiveMount{{Type: "volume", Name: spec.VolumeName, Source: "/var/lib/docker/volumes/" + spec.VolumeName + "/_data", Destination: "/work", Driver: "local", RW: true}}
	if phase == sourceInit {
		c.Config.User, h.PidsLimit, h.Memory, h.NanoCpus = "0:0", 64, 256<<20, 2_000_000_000
		h.CapAdd = []string{"CHOWN"}
	} else {
		h.Mounts = append(h.Mounts, sourceHostMount{Type: "bind", Source: spec.DaemonSnapshotPath, Target: "/source", ReadOnly: true, BindOptions: &sourceBindOptions{Propagation: "rprivate"}})
		c.Mounts = append(c.Mounts, sourceEffectiveMount{Type: "bind", Source: spec.DaemonSnapshotPath, Destination: "/source", Propagation: "rprivate"})
	}
	c.State.Status = "created"
	return c
}

// TestSourceCreatePolicyHasOnlyFixedPhaseCommands checks immutable entrypoints and isolated phase resources.
func TestSourceCreatePolicyHasOnlyFixedPhaseCommands(t *testing.T) {
	t.Parallel()
	for _, phase := range []sourcePhase{sourceInit, sourceBuild} {
		t.Run(string(phase), func(t *testing.T) {
			spec := policyTestSpec(phase)
			args, err := sourceCreateArgs(spec)
			if err != nil {
				t.Fatal(err)
			}
			user, pids, memory, cpus := "65532:65532", "512", "4g", "3"
			if phase == sourceInit {
				user, pids, memory, cpus = "0:0", "64", "256m", "2"
			}
			want := []string{"create", "--pull=never", "--name", spec.Name, "--platform", "linux/arm64", "--network", "none", "--read-only", "--user", user, "--workdir", "/", "--entrypoint", "/usr/bin/timeout", "--cap-drop", "ALL"}
			if phase == sourceInit {
				want = append(want, "--cap-add", "CHOWN")
			}
			want = append(want, "--security-opt", "no-new-privileges", "--pids-limit", pids, "--memory", memory, "--cpus", cpus, "--ipc", "private", "--cgroupns", "private", "--restart", "no", "--tmpfs", "/tmp:rw,nosuid,nodev,size=1g,mode=1777", "--mount", "type=volume,src="+spec.VolumeName+",dst=/work,volume-nocopy")
			if phase == sourceBuild {
				want = append(want, "--mount", "type=bind,src="+spec.SnapshotPath+",dst=/source,readonly,bind-propagation=rprivate")
			}
			for _, label := range []string{sourceOwnerLabel + "=" + sourceOwnerValue, sourceTokenLabel + "=" + spec.Token, sourceInitRecipeLabel + "=" + InitRecipeSHA256(), sourceRecipeLabel + "=" + SourceRecipeSHA256(), sourcePhaseLabel + "=" + string(phase)} {
				want = append(want, "--label", label)
			}
			for _, value := range spec.Env {
				want = append(want, "--env", value)
			}
			want = append(want, spec.ImageID)
			if phase == sourceInit {
				want = append(want, "--signal=TERM", "--kill-after=5s", "2m", "/bin/bash", "-euo", "pipefail", "-c", InitScript)
			} else {
				want = append(want, "--signal=TERM", "--kill-after=5s", "45m", "/bin/bash", "-euo", "pipefail", "-c", SourceScript, "lexr-sensors-source-build", "3")
			}
			if !reflect.DeepEqual(args, want) {
				t.Fatalf("compiled phase arguments differ:\n%q\nwant:\n%q", args, want)
			}
		})
	}
}

// These are supplied expected coordinates, not mapping authority. The normal
// controller must obtain the daemon coordinate from its private binding before
// assembling this policy spec; these tests never inspect a real daemon.
func TestSourceMountPolicySeparatesHostAndDaemonCoordinates(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ name, host, daemon string }{
		{"linux-identity", "/private/owned/source snapshot", "/private/owned/source snapshot"},
		{"desktop-derived-full-path", "/Users/tester/owned/source snapshot", "/host_mnt/Users/tester/owned/source snapshot"},
	} {
		t.Run(test.name, func(t *testing.T) {
			spec, c := policyTestSpec(sourceBuild), policyTestContainer(sourceBuild)
			spec.SnapshotPath, spec.DaemonSnapshotPath = test.host, test.daemon
			c.HostConfig.Mounts[1].Source, c.Mounts[1].Source = test.daemon, test.daemon
			if err := validateSourceContainerPolicy(c, spec); err != nil {
				t.Fatalf("exact supplied daemon coordinate refused: %v", err)
			}
			args, err := sourceCreateArgs(spec)
			if err != nil {
				t.Fatal(err)
			}
			want := "type=bind,src=" + test.host + ",dst=/source,readonly,bind-propagation=rprivate"
			if !slices.Contains(args, want) {
				t.Fatal("create did not preserve the exact host coordinate")
			}
			if test.host != test.daemon && slices.Contains(args, "type=bind,src="+test.daemon+",dst=/source,readonly,bind-propagation=rprivate") {
				t.Fatal("daemon inspection coordinate leaked into the client create request")
			}
		})
	}
}

// TestSourceMountPolicyRejectsDaemonCoordinateAliasesAndDivergence refuses inferred or conflicting mount paths.
func TestSourceMountPolicyRejectsDaemonCoordinateAliasesAndDivergence(t *testing.T) {
	t.Parallel()
	spec := policyTestSpec(sourceBuild)
	spec.SnapshotPath = "/Users/tester/owned/source snapshot"
	spec.DaemonSnapshotPath = "/host_mnt" + spec.SnapshotPath
	good := policyTestContainer(sourceBuild)
	good.HostConfig.Mounts[1].Source, good.Mounts[1].Source = spec.DaemonSnapshotPath, spec.DaemonSnapshotPath
	if err := validateSourceContainerPolicy(good, spec); err != nil {
		t.Fatal("daemon-coordinate refusal baseline is not valid", err)
	}
	for name, observed := range map[string]string{
		"empty": "", "untranslated-host": spec.SnapshotPath, "prefix-only": "/host_mnt",
		"double-prefix":  "/host_mnt" + spec.DaemonSnapshotPath,
		"trailing-slash": spec.DaemonSnapshotPath + "/", "sibling-prefix": spec.DaemonSnapshotPath + "-other",
		"descendant": spec.DaemonSnapshotPath + "/nested", "case-alias": strings.ToLower(spec.DaemonSnapshotPath),
		"unclean-alias": "/host_mnt/Users/tester/owned/../owned/source snapshot",
	} {
		for _, coordinate := range []string{"requested", "effective", "both"} {
			t.Run(name+"/"+coordinate, func(t *testing.T) {
				c := clonePolicyContainer(t, good)
				if coordinate != "effective" {
					c.HostConfig.Mounts[1].Source = observed
				}
				if coordinate != "requested" {
					c.Mounts[1].Source = observed
				}
				if err := validateSourceContainerPolicy(c, spec); err == nil {
					t.Fatal("observed source path supplied an unverified alias or disagreed with the exact expected daemon path")
				}
			})
		}
	}
	for name, edit := range map[string]func(*sourceContainerInspection){
		"requested-writable": func(c *sourceContainerInspection) { c.HostConfig.Mounts[1].ReadOnly = false },
		"effective-writable": func(c *sourceContainerInspection) { c.Mounts[1].RW = true },
		"requested-shared":   func(c *sourceContainerInspection) { c.HostConfig.Mounts[1].BindOptions.Propagation = "rshared" },
		"effective-shared":   func(c *sourceContainerInspection) { c.Mounts[1].Propagation = "rshared" },
		"nonrecursive":       func(c *sourceContainerInspection) { c.HostConfig.Mounts[1].BindOptions.NonRecursive = true },
		"duplicate-request": func(c *sourceContainerInspection) {
			c.HostConfig.Mounts = append(c.HostConfig.Mounts, c.HostConfig.Mounts[1])
		},
		"duplicate-effective": func(c *sourceContainerInspection) { c.Mounts = append(c.Mounts, c.Mounts[1]) },
		"work-mode-leak":      func(c *sourceContainerInspection) { c.Mounts[1].Mode = "z" },
	} {
		t.Run(name, func(t *testing.T) {
			c := clonePolicyContainer(t, good)
			edit(&c)
			if err := validateSourceContainerPolicy(c, spec); err == nil {
				t.Fatal("daemon translation weakened the source bind policy")
			}
		})
	}
}

// TestSourceCreateRefusesUnvalidatedSpec prevents container creation from incomplete policy inputs.
func TestSourceCreateRefusesUnvalidatedSpec(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		edit func(*sourceDockerSpec)
	}{
		{"unknown-phase", func(s *sourceDockerSpec) { s.Phase = "run" }},
		{"token", func(s *sourceDockerSpec) { s.Token = strings.Repeat("A", 24) }},
		{"name", func(s *sourceDockerSpec) { s.Name = "foreign" }},
		{"image-tag", func(s *sourceDockerSpec) { s.ImageID = "ubuntu:26.04" }},
		{"volume", func(s *sourceDockerSpec) { s.VolumeName += "-other" }},
		{"zero-jobs", func(s *sourceDockerSpec) { s.Jobs = 0 }},
		{"too-many-jobs", func(s *sourceDockerSpec) { s.Jobs = 9 }},
		{"unknown-env", func(s *sourceDockerSpec) { s.Env = append(s.Env, "LD_PRELOAD=/tmp/foreign.so") }},
		{"duplicate-env", func(s *sourceDockerSpec) { s.Env = append(s.Env, s.Env[0]) }},
		{"missing-env", func(s *sourceDockerSpec) { s.Env = s.Env[1:] }},
		{"changed-env", func(s *sourceDockerSpec) { s.Env[0] = "PATH=/tmp" }},
		{"missing-source", func(s *sourceDockerSpec) { s.SnapshotPath = "" }},
		{"relative-source", func(s *sourceDockerSpec) { s.SnapshotPath = "relative" }},
		{"unclean-source", func(s *sourceDockerSpec) { s.SnapshotPath = "/private/a/../source" }},
		{"broad-source", func(s *sourceDockerSpec) { s.SnapshotPath = "/" }},
		{"trailing-slash-source", func(s *sourceDockerSpec) { s.SnapshotPath += "/" }},
		{"comma-source", func(s *sourceDockerSpec) { s.SnapshotPath = "/private/source,readonly=false" }},
		{"quote-source", func(s *sourceDockerSpec) { s.SnapshotPath = "/private/source\"" }},
		{"control-source", func(s *sourceDockerSpec) { s.SnapshotPath = "/private/source\n" }},
		{"invalid-utf8-source", func(s *sourceDockerSpec) { s.SnapshotPath = "/private/" + string([]byte{0xff}) }},
		{"missing-daemon-source", func(s *sourceDockerSpec) { s.DaemonSnapshotPath = "" }},
		{"relative-daemon-source", func(s *sourceDockerSpec) { s.DaemonSnapshotPath = "relative" }},
		{"unclean-daemon-source", func(s *sourceDockerSpec) { s.DaemonSnapshotPath = "/host_mnt/private/a/../source" }},
		{"broad-daemon-source", func(s *sourceDockerSpec) { s.DaemonSnapshotPath = "/" }},
		{"trailing-slash-daemon-source", func(s *sourceDockerSpec) { s.DaemonSnapshotPath += "/" }},
		{"comma-daemon-source", func(s *sourceDockerSpec) { s.DaemonSnapshotPath = "/host_mnt/private/source,readonly=false" }},
		{"quote-daemon-source", func(s *sourceDockerSpec) { s.DaemonSnapshotPath += "\"" }},
		{"control-daemon-source", func(s *sourceDockerSpec) { s.DaemonSnapshotPath += "\n" }},
		{"invalid-utf8-daemon-source", func(s *sourceDockerSpec) { s.DaemonSnapshotPath = "/host_mnt/private/" + string([]byte{0xff}) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := policyTestSpec(sourceBuild)
			test.edit(&spec)
			if args, err := sourceCreateArgs(spec); err == nil || args != nil {
				t.Fatal("unsafe spec produced arguments")
			}
		})
	}
	for _, paths := range [][2]string{{"/private/source", ""}, {"", "/host_mnt/private/source"}, {"/private/source", "/host_mnt/private/source"}} {
		init := policyTestSpec(sourceInit)
		init.SnapshotPath, init.DaemonSnapshotPath = paths[0], paths[1]
		if _, err := sourceCreateArgs(init); err == nil {
			t.Fatal("initialization received a host or daemon source coordinate")
		}
	}
}

// TestSourceInspectionDecodeAndNativeEngine verifies typed inspection and the Linux ARM64 engine gate.
func TestSourceInspectionDecodeAndNativeEngine(t *testing.T) {
	t.Parallel()
	good := `{"ID":"01234567-abcd-0123-abcd-0123456789ab","ServerVersion":"29.1.2","OSType":"linux","Architecture":"aarch64","Labels":["not-native-authority"]}`
	if id, version, err := validateSourceEngine([]byte(good)); err != nil || id != "01234567-abcd-0123-abcd-0123456789ab" || version != "29.1.2" {
		t.Fatalf("valid native daemon refused: %q %q %v", id, version, err)
	}
	for _, raw := range []string{"", "null", "[]", good + `{}`, strings.Replace(good, `"linux"`, `"windows"`, 1), strings.Replace(good, `"aarch64"`, `"amd64"`, 1), strings.Replace(good, `"29.1.2"`, `"not-a-version"`, 1), strings.Replace(good, `"ID":`, `"ID":"duplicate","ID":`, 1), strings.Replace(good, `"Labels":["not-native-authority"]`, `"nested":{"x":1,"x":2}`, 1), strings.Repeat(" ", 256<<10) + good, string([]byte{'{', '"', 0xff, '"', ':', '1', '}'})} {
		if _, _, err := validateSourceEngine([]byte(raw)); err == nil {
			t.Fatalf("invalid daemon/JSON accepted: %.120q", raw)
		}
	}
}

// TestSourceInspectionAcceptsCanonicalDockerShapeWithOptionalNulls permits only documented nullable observations.
func TestSourceInspectionAcceptsCanonicalDockerShapeWithOptionalNulls(t *testing.T) {
	t.Parallel()
	raw := editPolicyJSON(t, func(root map[string]any) {
		config := policyJSONObject(t, root, "Config")
		labels := policyJSONObject(t, config, "Labels")
		labels["State"], labels["state"] = "arbitrary-map-key", "distinct-map-key"
		labels["Id"], labels["id"] = "also-arbitrary", "also-distinct"
		// Known maps, slices and pointers may legitimately be null. They do
		// not assert a primitive observation and remain subject to policy.
		config["Volumes"], config["ExposedPorts"], config["Healthcheck"] = nil, nil, nil
		host := policyJSONObject(t, root, "HostConfig")
		host["CapAdd"], host["Devices"], host["DeviceRequests"] = nil, nil, nil
		mounts := host["Mounts"].([]any)
		bind := policyJSONObject(t, mounts[1].(map[string]any), "BindOptions")
		// These false fields are omitted by Docker's mount-option encoding.
		for _, name := range []string{"NonRecursive", "CreateMountpoint", "ReadOnlyNonRecursive", "ReadOnlyForceRecursive"} {
			delete(bind, name)
		}
		root["FutureDockerExtension"] = map[string]any{"Id": nil, "id": true, "State": map[string]any{"Status": true, "status": false}}
	})
	var c sourceContainerInspection
	if err := decodeSourceInspection(raw, &c); err != nil {
		t.Fatal("canonical Docker-shaped object refused optional nulls/map keys/extensions", err)
	}
	spec := policyTestSpec(sourceBuild)
	if err := exactContainerOwnership(c, spec, policyTestCID); err != nil {
		t.Fatal(err)
	}
	if err := validateSourceContainerPolicy(c, spec); err != nil {
		t.Fatal("canonical decoded container no longer passes fixed policy", err)
	}
	if _, err := observedSourceTerminal(c); err != nil {
		t.Fatal(err)
	}
}

// TestSourceInspectionRequiresPrimitiveObservations rejects missing or wrongly typed security-relevant fields.
func TestSourceInspectionRequiresPrimitiveObservations(t *testing.T) {
	t.Parallel()
	for section, names := range map[string][]string{
		"State":      {"Status", "Running", "Paused", "Restarting", "Dead", "OOMKilled", "Pid", "ExitCode", "Error", "StartedAt", "FinishedAt"},
		"HostConfig": {"Privileged", "ReadonlyRootfs", "AutoRemove"},
	} {
		for _, name := range names {
			for _, action := range []string{"missing", "null"} {
				t.Run(section+"/"+name+"/"+action, func(t *testing.T) {
					raw := editPolicyJSON(t, func(root map[string]any) {
						fields := policyJSONObject(t, root, section)
						if action == "missing" {
							delete(fields, name)
						} else {
							fields[name] = nil
						}
					})
					var c sourceContainerInspection
					if err := decodeSourceInspection(raw, &c); err == nil {
						t.Fatal("absent/null primitive observation was defaulted to authority")
					}
				})
			}
		}
		for _, action := range []string{"missing", "null"} {
			t.Run(section+"/"+action, func(t *testing.T) {
				raw := editPolicyJSON(t, func(root map[string]any) {
					if action == "missing" {
						delete(root, section)
					} else {
						root[section] = nil
					}
				})
				var c sourceContainerInspection
				if err := decodeSourceInspection(raw, &c); err == nil {
					t.Fatal("absent/null required inspection section accepted")
				}
			})
		}
	}
	for name, edit := range map[string]func(map[string]any){
		"string": func(root map[string]any) { policyJSONObject(t, root, "Config")["User"] = nil },
		"number": func(root map[string]any) { policyJSONObject(t, root, "HostConfig")["PidsLimit"] = nil },
		"optional-struct-number": func(root map[string]any) {
			policyJSONObject(t, root, "Config")["Healthcheck"] = map[string]any{"Retries": nil}
		},
		"effective-mount-bool": func(root map[string]any) { root["Mounts"].([]any)[0].(map[string]any)["RW"] = nil },
		"requested-mount-bool": func(root map[string]any) {
			mount := policyJSONObject(t, root, "HostConfig")["Mounts"].([]any)[0].(map[string]any)
			policyJSONObject(t, mount, "VolumeOptions")["NoCopy"] = nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			var c sourceContainerInspection
			if err := decodeSourceInspection(editPolicyJSON(t, edit), &c); err == nil {
				t.Fatal("explicit null primitive field accepted")
			}
		})
	}
}

// TestSourceInspectionRejectsKnownStructCaseAliases prevents case-insensitive JSON decoding from hiding ambiguity.
func TestSourceInspectionRejectsKnownStructCaseAliases(t *testing.T) {
	t.Parallel()
	for name, edit := range map[string]func(map[string]any){
		"tagged-id":       func(root map[string]any) { root["id"] = root["Id"] },
		"tagged-id-alone": func(root map[string]any) { root["ID"] = root["Id"]; delete(root, "Id") },
		"state":           func(root map[string]any) { root["state"] = root["State"] },
		"status":          func(root map[string]any) { policyJSONObject(t, root, "State")["status"] = "exited" },
		"running":         func(root map[string]any) { policyJSONObject(t, root, "State")["running"] = false },
		"host-primitive":  func(root map[string]any) { policyJSONObject(t, root, "HostConfig")["privileged"] = false },
		"config":          func(root map[string]any) { policyJSONObject(t, root, "Config")["user"] = "65532:65532" },
		"nested-pointer": func(root map[string]any) {
			policyJSONObject(t, root, "Config")["Healthcheck"] = map[string]any{"Test": []any{}, "test": []any{}}
		},
		"slice-struct": func(root map[string]any) {
			mount := policyJSONObject(t, root, "HostConfig")["Mounts"].([]any)[1].(map[string]any)
			mount["readonly"] = true
		},
		"nested-mount-options": func(root map[string]any) {
			mount := policyJSONObject(t, root, "HostConfig")["Mounts"].([]any)[1].(map[string]any)
			policyJSONObject(t, mount, "BindOptions")["propagation"] = "rprivate"
		},
	} {
		t.Run(name, func(t *testing.T) {
			var c sourceContainerInspection
			if err := decodeSourceInspection(editPolicyJSON(t, edit), &c); err == nil {
				t.Fatal("noncanonical/casefold-aliased known struct field accepted")
			}
		})
	}
	var image sourceImageInspection
	for _, raw := range []string{`{"Id":"x","OS":"linux"}`, `{"Id":"x","Os":null}`, `{"Id":"x","Config":{"User":null}}`} {
		if err := decodeSourceInspection([]byte(raw), &image); err == nil {
			t.Fatalf("image field ambiguity/null accepted: %s", raw)
		}
	}
	for _, raw := range []string{
		`{"ID":"01234567-abcd-0123-abcd-0123456789ab","id":"different","ServerVersion":"29.1.2","OSType":"linux","Architecture":"arm64"}`,
		`{"ID":"01234567-abcd-0123-abcd-0123456789ab","ServerVersion":null,"OSType":"linux","Architecture":"arm64"}`,
	} {
		if _, _, err := validateSourceEngine([]byte(raw)); err == nil {
			t.Fatalf("engine field ambiguity/null accepted: %s", raw)
		}
	}
}

// editPolicyJSON applies one controlled mutation to a complete synthetic inspection response.
func editPolicyJSON(t *testing.T, edit func(map[string]any)) []byte {
	t.Helper()
	data, err := json.Marshal(policyTestContainer(sourceBuild))
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatal(err)
	}
	edit(object)
	data, err = json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// policyJSONObject selects a required fixture object while reporting an invalid test shape clearly.
func policyJSONObject(t *testing.T, parent map[string]any, key string) map[string]any {
	t.Helper()
	object, ok := parent[key].(map[string]any)
	if !ok {
		t.Fatalf("fixture field %s is not an object", key)
	}
	return object
}

// TestSourceEnvironmentAndPreparedImage binds the command environment to the verified dependency image.
func TestSourceEnvironmentAndPreparedImage(t *testing.T) {
	t.Parallel()
	base := []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "DEBIAN_FRONTEND=noninteractive", "LANG=C.UTF-8", "LC_ALL=C.UTF-8", "TZ=UTC", "PYTHONDONTWRITEBYTECODE=1"}
	env, err := sourceEnvironment(base)
	if err != nil || !reflect.DeepEqual(env, fixedSourceEnvironment()) {
		t.Fatalf("dependency environment not translated to fixed source policy: %v", err)
	}
	env[0] = "mutated"
	if fixedSourceEnvironment()[0] == "mutated" {
		t.Fatal("fixed environment returned mutable authority")
	}
	for _, additional := range []string{"LD_PRELOAD=x", "PYTHONPATH=x", "HTTP_PROXY=x", "PATH=duplicate", "MALFORMED", "HOME=/tmp\nPATH=/tmp", "HOME=" + strings.Repeat("a", 4097), "HOME=" + string([]byte{0xff})} {
		if _, err := sourceEnvironment(append(slices.Clone(base), additional)); err == nil {
			t.Errorf("unreviewed inherited environment accepted: %.80q", additional)
		}
	}
	image := sourceImageInspection{ID: policyTestImage, OS: "linux", Architecture: "arm64", Config: sourceImageConfig{User: "65532:65532", WorkingDir: "/", Entrypoint: []string{"/bin/false"}, Cmd: []string{"lexr-explicit-offline-runner-required"}, Env: base, Labels: map[string]string{"io.lexr.sensors.builder": "dependencies-v1", "io.lexr.sensors.recipe": builderimage.RecipeSHA256()}}}
	if err := validateSourceImage(image, policyTestImage); err != nil {
		t.Fatal(err)
	}
	for _, edit := range []func(*sourceImageInspection){
		func(i *sourceImageInspection) { i.ID = "ubuntu:26.04" },
		func(i *sourceImageInspection) { i.OS = "windows" },
		func(i *sourceImageInspection) { i.Architecture = "amd64" },
		func(i *sourceImageInspection) { i.Config.User = "0:0" },
		func(i *sourceImageInspection) { i.Config.WorkingDir = "/tmp" },
		func(i *sourceImageInspection) { i.Config.Entrypoint = []string{"/bin/bash"} },
		func(i *sourceImageInspection) { i.Config.Cmd = []string{"unreviewed"} },
		func(i *sourceImageInspection) {
			i.Config.Volumes = map[string]json.RawMessage{"/foreign": json.RawMessage(`{}`)}
		},
		func(i *sourceImageInspection) {
			i.Config.ExposedPorts = map[string]json.RawMessage{"80/tcp": json.RawMessage(`{}`)}
		},
		func(i *sourceImageInspection) {
			i.Config.Healthcheck = &sourceHealthcheck{Test: []string{"CMD", "unreviewed"}}
		},
		func(i *sourceImageInspection) { i.Config.Labels = nil },
		func(i *sourceImageInspection) { i.Config.Env = []string{"LD_PRELOAD=x"} },
	} {
		changed := image
		edit(&changed)
		if err := validateSourceImage(changed, policyTestImage); err == nil {
			t.Fatal("changed image policy accepted")
		}
	}
	if err := validateSourceImage(image, "ubuntu:26.04"); err == nil {
		t.Fatal("a tag supplied expected image authority")
	}
}

// TestSourceOwnershipRequiresExactContainerImageAndRecipe refuses borrowed or altered container identities.
func TestSourceOwnershipRequiresExactContainerImageAndRecipe(t *testing.T) {
	t.Parallel()
	for _, phase := range []sourcePhase{sourceInit, sourceBuild} {
		spec, good := policyTestSpec(phase), policyTestContainer(phase)
		if err := exactContainerOwnership(good, spec, policyTestCID); err != nil {
			t.Fatal(err)
		}
		if err := exactContainerOwnership(good, spec, ""); err != nil {
			t.Fatal("the initial bounded create receipt could not be bound", err)
		}
		for _, edit := range []func(*sourceContainerInspection){
			func(c *sourceContainerInspection) { c.ID = strings.Repeat("b", 64) },
			func(c *sourceContainerInspection) { c.Name += "-other" },
			func(c *sourceContainerInspection) { c.Image = "sha256:" + strings.Repeat("b", 64) },
			func(c *sourceContainerInspection) { c.Config.Image = "tag:latest" },
			func(c *sourceContainerInspection) { c.Config.Labels[sourceTokenLabel] = "foreign" },
			func(c *sourceContainerInspection) { c.Config.Labels[sourceInitRecipeLabel] = "foreign" },
			func(c *sourceContainerInspection) { c.Config.Labels[sourceRecipeLabel] = "foreign" },
			func(c *sourceContainerInspection) { c.Config.Labels[sourcePhaseLabel] = "foreign" },
		} {
			changed := clonePolicyContainer(t, good)
			edit(&changed)
			if err := exactContainerOwnership(changed, spec, policyTestCID); err == nil {
				t.Fatal("changed source container ownership accepted")
			}
		}
	}
}

// TestSourceContainerPolicyRefusesEscalations rejects added privileges, devices and mutable isolation settings.
func TestSourceContainerPolicyRefusesEscalations(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		edit func(*sourceContainerInspection)
	}{
		{"privileged", func(c *sourceContainerInspection) { c.HostConfig.Privileged = true }},
		{"writable-root", func(c *sourceContainerInspection) { c.HostConfig.ReadonlyRootfs = false }},
		{"autoremove", func(c *sourceContainerInspection) { c.HostConfig.AutoRemove = true }},
		{"host-network", func(c *sourceContainerInspection) { c.HostConfig.NetworkMode = "host" }},
		{"host-bind", func(c *sourceContainerInspection) { c.HostConfig.Binds = []string{"/:/host"} }},
		{"foreign-volume", func(c *sourceContainerInspection) { c.HostConfig.VolumesFrom = []string{"foreign"} }},
		{"device", func(c *sourceContainerInspection) { c.HostConfig.Devices = []json.RawMessage{json.RawMessage(`{}`)} }},
		{"device-request", func(c *sourceContainerInspection) {
			c.HostConfig.DeviceRequests = []json.RawMessage{json.RawMessage(`{}`)}
		}},
		{"device-rule", func(c *sourceContainerInspection) { c.HostConfig.DeviceCgroupRules = []string{"a *:* rwm"} }},
		{"implicit-volume", func(c *sourceContainerInspection) {
			c.Config.Volumes = map[string]json.RawMessage{"/foreign": json.RawMessage(`{}`)}
		}},
		{"host-pid", func(c *sourceContainerInspection) { c.HostConfig.PidMode = "host" }},
		{"host-ipc", func(c *sourceContainerInspection) { c.HostConfig.IpcMode = "host" }},
		{"host-cgroup", func(c *sourceContainerInspection) { c.HostConfig.CgroupnsMode = "host" }},
		{"host-uts", func(c *sourceContainerInspection) { c.HostConfig.UTSMode = "host" }},
		{"host-userns", func(c *sourceContainerInspection) { c.HostConfig.UsernsMode = "host" }},
		{"added-group", func(c *sourceContainerInspection) { c.HostConfig.GroupAdd = []string{"0"} }},
		{"link", func(c *sourceContainerInspection) { c.HostConfig.Links = []string{"foreign"} }},
		{"extra-host", func(c *sourceContainerInspection) { c.HostConfig.ExtraHosts = []string{"host:host-gateway"} }},
		{"dns", func(c *sourceContainerInspection) { c.HostConfig.Dns = []string{"1.1.1.1"} }},
		{"port", func(c *sourceContainerInspection) {
			c.HostConfig.PortBindings = map[string]json.RawMessage{"80/tcp": json.RawMessage(`[]`)}
		}},
		{"publish-ports", func(c *sourceContainerInspection) { c.HostConfig.PublishAllPorts = true }},
		{"sysctl", func(c *sourceContainerInspection) {
			c.HostConfig.Sysctls = map[string]string{"kernel.domainname": "foreign"}
		}},
		{"no-pid-limit", func(c *sourceContainerInspection) { c.HostConfig.PidsLimit = 0 }},
		{"memory", func(c *sourceContainerInspection) { c.HostConfig.Memory++ }},
		{"cpu", func(c *sourceContainerInspection) { c.HostConfig.NanoCpus++ }},
		{"restart-policy", func(c *sourceContainerInspection) { c.HostConfig.RestartPolicy.Name = "always" }},
		{"restarted", func(c *sourceContainerInspection) { c.RestartCount = 1 }},
		{"capability", func(c *sourceContainerInspection) { c.HostConfig.CapAdd = append(c.HostConfig.CapAdd, "SYS_ADMIN") }},
		{"cap-drop", func(c *sourceContainerInspection) { c.HostConfig.CapDrop = nil }},
		{"nnp-off", func(c *sourceContainerInspection) { c.HostConfig.SecurityOpt = []string{"no-new-privileges=false"} }},
		{"unconfined", func(c *sourceContainerInspection) {
			c.HostConfig.SecurityOpt = append(c.HostConfig.SecurityOpt, "seccomp=unconfined")
		}},
		{"tmpfs-exec-options", func(c *sourceContainerInspection) { c.HostConfig.Tmpfs["/tmp"] = "rw,size=1g" }},
		{"extra-tmpfs", func(c *sourceContainerInspection) { c.HostConfig.Tmpfs["/foreign"] = "rw" }},
		{"wrong-user", func(c *sourceContainerInspection) { c.Config.User = "1000:1000" }},
		{"working-directory", func(c *sourceContainerInspection) { c.Config.WorkingDir = "/work" }},
		{"entrypoint", func(c *sourceContainerInspection) { c.Config.Entrypoint = []string{"/bin/bash"} }},
		{"command", func(c *sourceContainerInspection) { c.Config.Cmd[0] = "foreign" }},
		{"environment", func(c *sourceContainerInspection) { c.Config.Env = append(c.Config.Env, "LD_PRELOAD=/tmp/a") }},
		{"tty", func(c *sourceContainerInspection) { c.Config.Tty = true }},
		{"healthcheck", func(c *sourceContainerInspection) {
			c.Config.Healthcheck = &sourceHealthcheck{Test: []string{"CMD", "foreign"}}
		}},
	}
	for _, phase := range []sourcePhase{sourceInit, sourceBuild} {
		for _, test := range tests {
			t.Run(string(phase)+"/"+test.name, func(t *testing.T) {
				c, spec := policyTestContainer(phase), policyTestSpec(phase)
				test.edit(&c)
				if err := validateSourceContainerPolicy(c, spec); err == nil {
					t.Fatal("container escalation accepted")
				}
			})
		}
	}
}

// TestSourceMountPolicyRequiresRequestedAndEffectiveIdentity checks both mount observation surfaces independently.
func TestSourceMountPolicyRequiresRequestedAndEffectiveIdentity(t *testing.T) {
	t.Parallel()
	for _, phase := range []sourcePhase{sourceInit, sourceBuild} {
		good, spec := policyTestContainer(phase), policyTestSpec(phase)
		if err := validateSourceContainerPolicy(good, spec); err != nil {
			t.Fatal(err)
		}
		good.Mounts = append(good.Mounts, sourceEffectiveMount{Type: "tmpfs", Destination: "/tmp", RW: true})
		if err := validateSourceContainerPolicy(good, spec); err != nil {
			t.Fatal("normal optional effective tmpfs refused", err)
		}
		if err := validateSourceContainerPolicy(clonePolicyContainer(t, good), spec); err != nil {
			t.Fatal("JSON cloning changed the valid refusal-test baseline", err)
		}
		for _, edit := range []func(*sourceContainerInspection){
			func(c *sourceContainerInspection) { c.HostConfig.Mounts = nil },
			func(c *sourceContainerInspection) { c.HostConfig.Mounts[0].VolumeOptions.NoCopy = false },
			func(c *sourceContainerInspection) { c.HostConfig.Mounts[0].Source += "-foreign" },
			func(c *sourceContainerInspection) { c.HostConfig.Mounts[0].ReadOnly = true },
			func(c *sourceContainerInspection) { c.HostConfig.Mounts[0].VolumeOptions.Subpath = "foreign" },
			func(c *sourceContainerInspection) {
				c.HostConfig.Mounts[0].VolumeOptions.DriverConfig = json.RawMessage(`{}`)
			},
			func(c *sourceContainerInspection) {
				c.HostConfig.Mounts = append(c.HostConfig.Mounts, c.HostConfig.Mounts[0])
			},
			func(c *sourceContainerInspection) { c.Mounts = nil },
			func(c *sourceContainerInspection) { c.Mounts[0].Name += "-foreign" },
			func(c *sourceContainerInspection) { c.Mounts[0].Type = "bind" },
			func(c *sourceContainerInspection) { c.Mounts[0].Driver = "nfs" },
			func(c *sourceContainerInspection) { c.Mounts[0].RW = false },
			func(c *sourceContainerInspection) { c.Mounts[0].Source = "relative" },
			func(c *sourceContainerInspection) { c.Mounts = append(c.Mounts, c.Mounts[0]) },
			func(c *sourceContainerInspection) {
				c.Mounts = append(c.Mounts, sourceEffectiveMount{Type: "bind", Source: "/", Destination: "/host", RW: true})
			},
		} {
			changed := clonePolicyContainer(t, good)
			edit(&changed)
			if err := validateSourceContainerPolicy(changed, spec); err == nil {
				t.Fatal("changed workspace mount accepted")
			}
		}
	}
	spec := policyTestSpec(sourceBuild)
	for _, edit := range []func(*sourceContainerInspection){
		func(c *sourceContainerInspection) { c.HostConfig.Mounts[1].ReadOnly = false },
		func(c *sourceContainerInspection) { c.HostConfig.Mounts[1].Source = "/different" },
		func(c *sourceContainerInspection) { c.HostConfig.Mounts[1].BindOptions.Propagation = "rshared" },
		func(c *sourceContainerInspection) { c.HostConfig.Mounts[1].BindOptions.CreateMountpoint = true },
		func(c *sourceContainerInspection) { c.Mounts[1].RW = true },
		func(c *sourceContainerInspection) { c.Mounts[1].Source = "/different" },
		func(c *sourceContainerInspection) { c.Mounts[1].Propagation = "rshared" },
		func(c *sourceContainerInspection) { c.Mounts[1].Mode = "rw" },
	} {
		changed := policyTestContainer(sourceBuild)
		edit(&changed)
		if err := validateSourceContainerPolicy(changed, spec); err == nil {
			t.Fatal("changed source bind accepted")
		}
	}
}

// TestSourceWorkspaceEffectiveModesRemainNarrow refuses widened writable workspace settings.
func TestSourceWorkspaceEffectiveModesRemainNarrow(t *testing.T) {
	t.Parallel()
	for _, phase := range []sourcePhase{sourceInit, sourceBuild} {
		for _, mode := range []string{"", "rw", "z"} {
			t.Run(string(phase)+"/accepted/"+mode, func(t *testing.T) {
				c := policyTestContainer(phase)
				c.Mounts[0].Mode = mode
				if err := validateSourceContainerPolicy(c, policyTestSpec(phase)); err != nil {
					t.Fatalf("owned local RW volume mode %q refused: %v", mode, err)
				}
			})
		}
		for _, mode := range []string{"Z", "ro", "RW", "rw,z", "ro,z", "z,rw", "z,ro", "rw,Z", "ro,Z", "z,Z", "z,z", "z,rprivate", " z", "z "} {
			t.Run(string(phase)+"/refused/"+mode, func(t *testing.T) {
				c := policyTestContainer(phase)
				c.Mounts[0].Mode = mode
				if err := validateSourceContainerPolicy(c, policyTestSpec(phase)); err == nil {
					t.Fatalf("unobserved workspace mode %q accepted", mode)
				}
			})
		}
		for name, edit := range map[string]func(*sourceContainerInspection){
			"effective-bind":         func(c *sourceContainerInspection) { c.Mounts[0].Type = "bind" },
			"effective-tmpfs":        func(c *sourceContainerInspection) { c.Mounts[0].Type = "tmpfs" },
			"foreign-name":           func(c *sourceContainerInspection) { c.Mounts[0].Name += "-foreign" },
			"non-local-driver":       func(c *sourceContainerInspection) { c.Mounts[0].Driver = "nfs" },
			"read-only":              func(c *sourceContainerInspection) { c.Mounts[0].RW = false },
			"noncanonical-source":    func(c *sourceContainerInspection) { c.Mounts[0].Source = "/var/lib/../foreign" },
			"propagating-workspace":  func(c *sourceContainerInspection) { c.Mounts[0].Propagation = "rshared" },
			"copy-enabled":           func(c *sourceContainerInspection) { c.HostConfig.Mounts[0].VolumeOptions.NoCopy = false },
			"requested-bind":         func(c *sourceContainerInspection) { c.HostConfig.Mounts[0].Type = "bind" },
			"requested-foreign":      func(c *sourceContainerInspection) { c.HostConfig.Mounts[0].Source += "-foreign" },
			"requested-read-only":    func(c *sourceContainerInspection) { c.HostConfig.Mounts[0].ReadOnly = true },
			"missing-volume-options": func(c *sourceContainerInspection) { c.HostConfig.Mounts[0].VolumeOptions = nil },
			"volume-subpath":         func(c *sourceContainerInspection) { c.HostConfig.Mounts[0].VolumeOptions.Subpath = "foreign" },
			"volume-driver-config": func(c *sourceContainerInspection) {
				c.HostConfig.Mounts[0].VolumeOptions.DriverConfig = json.RawMessage(`{}`)
			},
			"volume-option-labels": func(c *sourceContainerInspection) {
				c.HostConfig.Mounts[0].VolumeOptions.Labels = map[string]string{"foreign": "option"}
			},
		} {
			t.Run(string(phase)+"/z-identity-refusal/"+name, func(t *testing.T) {
				c := policyTestContainer(phase)
				c.Mounts[0].Mode = "z"
				edit(&c)
				if err := validateSourceContainerPolicy(c, policyTestSpec(phase)); err == nil {
					t.Fatal("z bypassed the exact workspace/requested-volume policy")
				}
			})
		}
		for _, mode := range []string{"z", "Z", "rw,z", "ro,z", "z,rw", "z,ro"} {
			t.Run(string(phase)+"/tmpfs-refusal/"+mode, func(t *testing.T) {
				c := policyTestContainer(phase)
				c.Mounts = append(c.Mounts, sourceEffectiveMount{Type: "tmpfs", Destination: "/tmp", Mode: mode, RW: true})
				if err := validateSourceContainerPolicy(c, policyTestSpec(phase)); err == nil {
					t.Fatalf("workspace compatibility leaked into tmpfs mode %q", mode)
				}
			})
			if phase == sourceBuild {
				t.Run("build/source-bind-refusal/"+mode, func(t *testing.T) {
					c := policyTestContainer(phase)
					c.Mounts[1].Mode = mode
					if err := validateSourceContainerPolicy(c, policyTestSpec(phase)); err == nil {
						t.Fatalf("workspace compatibility leaked into source-bind mode %q", mode)
					}
				})
			}
		}
	}
}

// TestSourceTerminalSeparatesStoppedFromSuccessful distinguishes termination evidence from accepted completion.
func TestSourceTerminalSeparatesStoppedFromSuccessful(t *testing.T) {
	t.Parallel()
	c := policyTestContainer(sourceBuild)
	created, err := observedSourceTerminal(c)
	if err != nil || successfulSourceTerminal(created) {
		t.Fatal("created container was rejected as stopped or promoted to success")
	}
	c.State.Status = "exited"
	c.State.StartedAt, c.State.FinishedAt = "2026-10-20T12:00:00.000000001Z", "2026-10-20T12:00:01Z"
	good, err := observedSourceTerminal(c)
	if err != nil || !successfulSourceTerminal(good) {
		t.Fatal("valid terminal state not accepted", err)
	}
	for _, edit := range []func(*sourceContainerInspection){
		func(c *sourceContainerInspection) { c.ID = "foreign" },
		func(c *sourceContainerInspection) { c.State.Status = "running" },
		func(c *sourceContainerInspection) { c.State.Running = true },
		func(c *sourceContainerInspection) { c.State.Paused = true },
		func(c *sourceContainerInspection) { c.State.Restarting = true },
		func(c *sourceContainerInspection) { c.State.Dead = true },
		func(c *sourceContainerInspection) { c.State.Pid = 42 },
	} {
		changed := c
		edit(&changed)
		if _, err := observedSourceTerminal(changed); err == nil {
			t.Fatal("unverified terminal state accepted")
		}
	}
	for _, edit := range []func(*sourceDockerTerminal){
		func(s *sourceDockerTerminal) { s.Status = "created" },
		func(s *sourceDockerTerminal) { s.ExitCode = 1 },
		func(s *sourceDockerTerminal) { s.OOMKilled = true },
		func(s *sourceDockerTerminal) { s.Error = "failure" },
		func(s *sourceDockerTerminal) { s.StartedAt = "" },
		func(s *sourceDockerTerminal) { s.StartedAt = "0001-01-01T00:00:00Z" },
		func(s *sourceDockerTerminal) { s.FinishedAt = "2026-10-20T11:59:59Z" },
	} {
		changed := good
		edit(&changed)
		if successfulSourceTerminal(changed) {
			t.Fatal("unsuccessful stopped state promoted to success")
		}
	}
}

// clonePolicyContainer isolates nested synthetic observations before refusal mutations.
func clonePolicyContainer(t *testing.T, value sourceContainerInspection) sourceContainerInspection {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var result sourceContainerInspection
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	return result
}
