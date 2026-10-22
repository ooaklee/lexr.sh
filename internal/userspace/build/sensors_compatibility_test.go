package build

import (
	"context"
	"testing"
)

// TestSensorAdapterRejectsDeploymentCompatibilityFields ensures an unqualified
// source build cannot silently consume a deployment tuple or evidence override.
func TestSensorAdapterRejectsDeploymentCompatibilityFields(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*Request)
	}{
		{"lexr-version", func(r *Request) { r.PayloadTarget.LexrVersion = "0.7.0" }},
		{"architecture", func(r *Request) { r.PayloadTarget.Architecture = "arm64" }},
		{"device-profile", func(r *Request) { r.PayloadTarget.DeviceProfile = "x1e80100-microsoft-denali-oled" }},
		{"os-id", func(r *Request) { r.PayloadTarget.OSID = "ubuntu" }},
		{"os-version", func(r *Request) { r.PayloadTarget.OSVersion = "26.04" }},
		{"kernel-abi", func(r *Request) { r.PayloadTarget.KernelABI = "7.2.9-jg-0sp11v2-qcom-x1e" }},
		{"unverified-override", func(r *Request) { r.AllowUnverifiedCompatibility = true }},
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
				t.Fatalf("deployment metadata reached a source builder: %v, %+v", err, native.requests)
			}
		})
	}
}
