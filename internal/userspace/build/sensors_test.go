package build

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"

	sensorbuild "github.com/ooaklee/lexr.sh/internal/sensors/build"
)

type recordingSensorBuilder struct {
	requests []sensorbuild.Request
	contexts []context.Context
	result   sensorbuild.Result
	err      error
}

func (builder *recordingSensorBuilder) Run(ctx context.Context, request sensorbuild.Request) (sensorbuild.Result, error) {
	builder.contexts = append(builder.contexts, ctx)
	builder.requests = append(builder.requests, request)
	return builder.result, builder.err
}

// A discovery marker deliberately contains no authenticated inventory. Tests
// of typed dispatch replace only the private native-manager interface; the
// production manager's authentication is tested separately below.
func sensorAdapterRepository(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	marker := filepath.Join(root, filepath.FromSlash(sensorSourceInventoryMarker))
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte("discovery-only fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func sensorAdapterRequest(t *testing.T) Request {
	t.Helper()
	return Request{Component: ComponentSensors, RepositoryRoot: sensorAdapterRepository(t),
		SensorWorkParent: t.TempDir(), OutputDirectory: t.TempDir(), Jobs: 4}
}

func TestSensorAdapterUsesTypedNativeRequestAndResult(t *testing.T) {
	runner := &recordingRunner{}
	manager := New(runner)
	native := &recordingSensorBuilder{result: sensorbuild.Result{
		BuildID: "fixture-build", WorkDirectory: "retained-work",
		Source: sensorbuild.PhaseResult{Name: "source-fixture", TerminalUnknown: true, Stderr: "retained native diagnostics"},
	}}
	manager.sensorBuilder = native
	request := sensorAdapterRequest(t)
	request.DryRun = true
	type contextKey struct{}
	ctx := context.WithValue(context.Background(), contextKey{}, "preserved")
	result, err := manager.RunWithResult(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	wanted := sensorbuild.Request{RepositoryRoot: request.RepositoryRoot, WorkParent: request.SensorWorkParent,
		OutputParent: request.OutputDirectory, Jobs: request.Jobs, DryRun: true}
	if len(native.requests) != 1 || native.requests[0] != wanted || native.contexts[0] != ctx {
		t.Fatalf("native request/context = %+v/%+v, want %+v", native.requests, native.contexts, wanted)
	}
	if result.Component != ComponentSensors || result.Camera != nil || result.Sensors == nil || !reflect.DeepEqual(*result.Sensors, native.result) {
		t.Fatalf("typed sensor result = %+v", result)
	}
	if len(runner.commands) != 0 {
		t.Fatal("sensor adapter invoked the generic command runner")
	}
	data, err := json.Marshal(result)
	if err != nil || !strings.Contains(string(data), `"component":"sensors"`) || !strings.Contains(string(data), `"sensors":`) || strings.Contains(string(data), `"camera":`) {
		t.Fatalf("typed structured result = %s, %v", data, err)
	}
}

func TestSensorAdapterPreservesPartialResultAndOriginalError(t *testing.T) {
	wantErr := errors.New("native source terminal remains uncertain")
	native := &recordingSensorBuilder{err: wantErr, result: sensorbuild.Result{
		WorkDirectory: "retained-failed-work", VolumeName: "retained-owned-volume",
		Source: sensorbuild.PhaseResult{ContainerID: "exact-owned-container", TerminalUnknown: true, RecoveryError: "saved uncertainty"},
	}}
	manager := New(&recordingRunner{})
	manager.sensorBuilder = native
	result, err := manager.RunWithResult(context.Background(), sensorAdapterRequest(t))
	if err != wantErr || !errors.Is(err, wantErr) || result.Component != ComponentSensors || result.Sensors == nil || !reflect.DeepEqual(*result.Sensors, native.result) {
		t.Fatalf("partial result/error = %+v / %v", result, err)
	}
}

func TestSensorAdapterRunAlsoDelegatesOnlyTheTypedWorkflow(t *testing.T) {
	native := &recordingSensorBuilder{}
	manager := New(&recordingRunner{})
	manager.sensorBuilder = native
	if err := manager.Run(context.Background(), sensorAdapterRequest(t)); err != nil || len(native.requests) != 1 {
		t.Fatalf("Run did not delegate: %v, %+v", err, native.requests)
	}
}

func TestSensorAdapterPreservesNativeZeroAndMaximumJobs(t *testing.T) {
	for _, jobs := range []int{0, 1, 8} {
		t.Run(strconv.Itoa(jobs), func(t *testing.T) {
			native := &recordingSensorBuilder{}
			manager := New(&recordingRunner{})
			manager.sensorBuilder = native
			request := sensorAdapterRequest(t)
			request.Jobs = jobs
			if _, err := manager.RunWithResult(context.Background(), request); err != nil || len(native.requests) != 1 || native.requests[0].Jobs != jobs {
				t.Fatalf("native jobs were defaulted/changed: %v, %+v", err, native.requests)
			}
		})
	}
}

func TestSensorAdapterRejectsCrossComponentOptionsBeforeDelegation(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*Request)
	}{
		{"image", func(r *Request) { r.Image = "ubuntu:26.04" }},
		{"work-volume", func(r *Request) { r.WorkVolume = "existing-volume" }},
		{"minimum-free-positive", func(r *Request) { r.MinimumFreeGiB = 12 }},
		{"minimum-free-negative", func(r *Request) { r.MinimumFreeGiB = -1 }},
		{"no-pull", func(r *Request) { r.NoPull = true }},
		{"negative-jobs", func(r *Request) { r.Jobs = -1 }},
		{"nine-jobs", func(r *Request) { r.Jobs = 9 }},
		{"legacy-maximum-jobs", func(r *Request) { r.Jobs = 64 }},
		{"too-many-jobs", func(r *Request) { r.Jobs = 65 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &recordingRunner{}
			native := &recordingSensorBuilder{}
			manager := New(runner)
			manager.sensorBuilder = native
			request := sensorAdapterRequest(t)
			request.DryRun = true
			test.edit(&request)
			if _, err := manager.RunWithResult(context.Background(), request); err == nil || len(native.requests) != 0 || len(runner.commands) != 0 {
				t.Fatalf("invalid option reached a builder: %v, %+v", err, native.requests)
			}
		})
	}
}

func TestSensorAdapterRequiresExplicitExistingParentsWithoutCreatingThem(t *testing.T) {
	for _, parent := range []string{"work", "output"} {
		for _, kind := range []string{"empty", "whitespace", "missing", "regular-file", "broken-link"} {
			t.Run(parent+"/"+kind, func(t *testing.T) {
				request := sensorAdapterRequest(t)
				value := ""
				missing := filepath.Join(t.TempDir(), "must-remain-absent")
				switch kind {
				case "whitespace":
					value = " \t"
				case "missing":
					value = missing
				case "regular-file":
					value = filepath.Join(t.TempDir(), "file")
					if err := os.WriteFile(value, []byte("fixture"), 0o600); err != nil {
						t.Fatal(err)
					}
				case "broken-link":
					value = filepath.Join(t.TempDir(), "link")
					if err := os.Symlink(missing, value); err != nil {
						t.Fatal(err)
					}
				}
				if parent == "work" {
					request.SensorWorkParent = value
				} else {
					request.OutputDirectory = value
				}
				runner, native := &recordingRunner{}, &recordingSensorBuilder{}
				manager := New(runner)
				manager.sensorBuilder = native
				if _, err := manager.RunWithResult(context.Background(), request); err == nil || len(native.requests) != 0 || len(runner.commands) != 0 {
					t.Fatalf("invalid parent reached a builder: %v", err)
				}
				if _, err := os.Lstat(missing); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("adapter created or changed a missing parent: %v", err)
				}
			})
		}
	}
}

func TestSensorAdapterWorkParentCannotLeakIntoExistingComponents(t *testing.T) {
	for _, component := range []Component{ComponentIPTSD, ComponentCamera} {
		t.Run(string(component), func(t *testing.T) {
			runner, native, camera := &recordingRunner{}, &recordingSensorBuilder{}, &recordingCameraBuilder{}
			manager := New(runner)
			manager.sensorBuilder, manager.cameraBuilder = native, camera
			_, err := manager.RunWithResult(context.Background(), Request{Component: component, RepositoryRoot: fakeRepository(t), SensorWorkParent: t.TempDir()})
			if err == nil || !strings.Contains(err.Error(), "sensor-work-parent") || len(native.requests) != 0 || len(camera.requests) != 0 || len(runner.commands) != 0 {
				t.Fatalf("sensor option leaked into %s: %v", component, err)
			}
		})
	}
}

func TestSensorAdapterDiscoveryUsesOnlyItsRegularInventoryMarker(t *testing.T) {
	root := sensorAdapterRepository(t)
	got, err := resolveRepositoryRoot(root, ComponentSensors)
	if err != nil || got != root {
		t.Fatalf("sensor-only checkout was not found: %q, %v", got, err)
	}
	if _, err := resolveRepositoryRoot(fakeRepository(t), ComponentSensors); err == nil {
		t.Fatal("IPTSD/camera markers were used as sensor inventory")
	}
	marker := filepath.Join(root, filepath.FromSlash(sensorSourceInventoryMarker))
	target := filepath.Join(root, "marker-target")
	if err := os.Rename(marker, target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, marker); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveRepositoryRoot(root, ComponentSensors); err == nil {
		t.Fatal("symlink inventory marker was accepted by discovery")
	}
}

func TestSensorAdapterDiscoversSourceByWalkingUpward(t *testing.T) {
	root := sensorAdapterRepository(t)
	nested := filepath.Join(root, "nested", "directory")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(nested)
	got, err := resolveRepositoryRoot("", ComponentSensors)
	if err != nil {
		t.Fatalf("upward sensor discovery = %q, %v", got, err)
	}
	wantedInfo, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	gotInfo, err := os.Stat(got)
	if err != nil || !os.SameFile(wantedInfo, gotInfo) {
		t.Fatalf("upward discovery selected another directory: %q, %v", got, err)
	}
}

func TestSensorAdapterDefaultBuilderRetainsNativeAuthenticationAndHostGate(t *testing.T) {
	runner := &recordingRunner{}
	manager := New(runner)
	if _, ok := manager.sensorBuilder.(*sensorbuild.Manager); !ok {
		t.Fatal("production adapter does not own the actual native sensor manager")
	}
	request := sensorAdapterRequest(t)
	request.DryRun = true
	// TempDir's leaf mode is not the native controller's private-work policy.
	// Meet that unchanged gate so this test reaches metadata authentication.
	if err := os.Chmod(request.SensorWorkParent, 0o700); err != nil {
		t.Fatal(err)
	}
	metadata := filepath.Join(request.RepositoryRoot, "userspace", "sensors", "libqmi", "SOURCE.json")
	if err := os.MkdirAll(filepath.Dir(metadata), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(metadata, []byte("unauthenticated source metadata\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := manager.RunWithResult(context.Background(), request)
	if result.Component != ComponentSensors || result.Sensors == nil || result.Sensors.Plan.SourceAuthenticated || len(runner.commands) != 0 {
		t.Fatalf("discovery marker bypassed native authentication: %+v, %v", result, err)
	}
	if runtime.GOARCH == "arm64" && (runtime.GOOS == "linux" || runtime.GOOS == "darwin") {
		if err == nil || !strings.Contains(err.Error(), "libqmi/SOURCE.json") {
			t.Fatalf("native pinned-metadata refusal was bypassed: %+v, %v", result, err)
		}
	} else if err != nil || result.Sensors.Plan.Executable || result.Sensors.Plan.ExecutionBlocker == "" {
		t.Fatalf("native unsupported-host dry-run gate changed: %+v, %v", result, err)
	}
}

func TestSensorAdapterUnavailableBuilderAndContextFailWithoutEffects(t *testing.T) {
	for _, kind := range []string{"unavailable", "nil-context", "canceled-context"} {
		t.Run(kind, func(t *testing.T) {
			runner, native := &recordingRunner{}, &recordingSensorBuilder{}
			manager := New(runner)
			manager.sensorBuilder = native
			ctx := context.Background()
			switch kind {
			case "unavailable":
				manager.sensorBuilder = nil
			case "nil-context":
				ctx = nil
			case "canceled-context":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			_, err := manager.RunWithResult(ctx, sensorAdapterRequest(t))
			if err == nil || len(native.requests) != 0 || len(runner.commands) != 0 {
				t.Fatalf("unavailable sensor capability/context produced effects: %v", err)
			}
			if kind == "canceled-context" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation identity lost: %v", err)
			}
		})
	}
}
