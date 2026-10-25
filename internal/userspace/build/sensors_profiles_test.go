package build

import (
	"context"
	"strings"
	"testing"

	sensorbuild "github.com/ooaklee/lexr.sh/internal/sensors/build"
)

// TestSensorAdapterProfileDispatch is request-bound, never selected from a result.
func TestSensorAdapterProfileDispatch(t *testing.T) {
	for _, selected := range []string{"", SensorSourceLegacy047, SensorSourceMetadata048} {
		t.Run("profile="+selected, func(t *testing.T) {
			legacy, metadata := &recordingSensorBuilder{}, &recordingSensorBuilder{}
			manager := New(&recordingRunner{})
			manager.sensorBuilder, manager.sensorBuilder048 = legacy, metadata
			request := sensorAdapterRequest(t)
			request.SensorSourceProfile = selected
			if _, err := manager.RunWithResult(context.Background(), request); err != nil {
				t.Fatal(err)
			}
			chosen, other := legacy, metadata
			if selected == SensorSourceMetadata048 {
				chosen, other = metadata, legacy
			}
			if len(chosen.requests) != 1 || len(other.requests) != 0 {
				t.Fatalf("selection crossed owners: legacy=%+v successor=%+v", legacy.requests, metadata.requests)
			}
			want := sensorbuild.Request{RepositoryRoot: request.RepositoryRoot, WorkParent: request.SensorWorkParent, OutputParent: request.OutputDirectory, Jobs: request.Jobs}
			if chosen.requests[0] != want {
				t.Fatalf("profile leaked into or changed native request: %+v", chosen.requests[0])
			}
		})
	}
}

// TestSensorAdapterUnknownProfilesStopBeforePathDiscovery tests exact spellings.
func TestSensorAdapterUnknownProfilesStopBeforePathDiscovery(t *testing.T) {
	for _, selected := range []string{"metadata-049", "METADATA-048", "metadata-048 ", "legacy-047,metadata-048", "048", " "} {
		manager := New(&recordingRunner{})
		legacy, metadata := &recordingSensorBuilder{}, &recordingSensorBuilder{}
		manager.sensorBuilder, manager.sensorBuilder048 = legacy, metadata
		request := Request{Component: ComponentSensors, SensorSourceProfile: selected, RepositoryRoot: "/missing", SensorWorkParent: "/missing", OutputDirectory: "/missing"}
		if _, err := manager.RunWithResult(context.Background(), request); err == nil || !strings.Contains(err.Error(), "sensor-source-profile") {
			t.Fatalf("unknown selector reached unrelated path validation: %q %v", selected, err)
		}
		if len(legacy.requests)+len(metadata.requests) != 0 {
			t.Fatal("unknown selector invoked a native manager")
		}
	}
}

// TestSensorAdapterProfileCannotLeakIntoUnrelatedComponents includes the default spelling.
func TestSensorAdapterProfileCannotLeakIntoUnrelatedComponents(t *testing.T) {
	for _, component := range []Component{ComponentIPTSD, ComponentCamera, "unknown"} {
		for _, selected := range []string{SensorSourceLegacy047, SensorSourceMetadata048} {
			manager := New(&recordingRunner{})
			_, err := manager.RunWithResult(context.Background(), Request{Component: component, SensorSourceProfile: selected, RepositoryRoot: "/missing"})
			if err == nil || !strings.Contains(err.Error(), "sensor-source-profile") {
				t.Fatalf("selector reached %s workflow: %v", component, err)
			}
		}
	}
}

// TestSensorAdapterMetadataOwnerMustExist never falls back to its legacy owner.
func TestSensorAdapterMetadataOwnerMustExist(t *testing.T) {
	manager := New(&recordingRunner{})
	legacy := &recordingSensorBuilder{}
	manager.sensorBuilder, manager.sensorBuilder048 = legacy, nil
	request := sensorAdapterRequest(t)
	request.SensorSourceProfile = SensorSourceMetadata048
	if _, err := manager.RunWithResult(context.Background(), request); err == nil || len(legacy.requests) != 0 {
		t.Fatalf("missing successor silently used legacy owner: %v", err)
	}
}

// TestSensorAdapterOwnsTwoSeparateProductionManagers preserves constructor ownership.
func TestSensorAdapterOwnsTwoSeparateProductionManagers(t *testing.T) {
	manager := New(&recordingRunner{})
	legacy, oldOK := manager.sensorBuilder.(*sensorbuild.Manager)
	metadata, nextOK := manager.sensorBuilder048.(*sensorbuild.Manager)
	if !oldOK || !nextOK || legacy == metadata {
		t.Fatal("profiles do not own distinct native controllers")
	}
}
