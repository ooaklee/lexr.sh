package producer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/platform"
	"github.com/ooaklee/lexr.sh/internal/userspace/compatibility"
)

// stubRunner is a deterministic command runner for authentication tests.
type stubRunner struct {
	// responses maps an exact command line to captured standard output.
	responses map[string]string
	// failures maps an exact command line to a returned error.
	failures map[string]error
}

// Run always fails because the producer only captures bounded output.
func (runner stubRunner) Run(context.Context, platform.Command) error {
	return errors.New("stub runner only supports capture")
}

// Capture returns the canned response for one exact command line.
func (runner stubRunner) Capture(_ context.Context, command platform.Command) ([]byte, error) {
	key := command.Name + " " + strings.Join(command.Args, " ")
	if err, ok := runner.failures[key]; ok {
		return nil, err
	}
	response, ok := runner.responses[key]
	if !ok {
		return nil, errors.New("unexpected command: " + key)
	}
	return []byte(response), nil
}

// declarationBytes reads one reviewed declaration fixture exactly as written.
func declarationBytes(t *testing.T, component string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "declarations", component, compatibility.Filename))
	if err != nil {
		t.Fatalf("read declaration fixture: %v", err)
	}
	return data
}

// authenticator builds a runner whose clean HEAD serves the exact bytes.
func authenticator(t *testing.T, component string, bytes []byte) stubRunner {
	t.Helper()
	return stubRunner{responses: map[string]string{
		"git -C /oe status --porcelain":                             "",
		"git -C /oe cat-file -s HEAD:" + DeclarationPath(component): strconv.Itoa(len(bytes)),
		"git -C /oe show HEAD:" + DeclarationPath(component):        string(bytes),
	}}
}

// payloadTarget returns one complete evidence-bounded target tuple.
func payloadTarget() compatibility.Target {
	return compatibility.Target{
		Architecture: "arm64", DeviceProfile: "x1e80100-microsoft-denali-oled",
		OSID: "ubuntu", OSVersion: "26.04", KernelABI: "7.2.0-jg-0sp11v19-qcom-x1e",
	}
}

// TestReviewedDeclarationsRequireIndependentPins checks every source copy and
// proves editable metadata cannot widen the compiled authority.
func TestReviewedDeclarationsRequireIndependentPins(t *testing.T) {
	for _, component := range []string{"audio-fullio-v19c", "iptsd-v1", "imx681-libcamera-v1"} {
		policy, err := PolicyFor(component)
		if err != nil {
			t.Fatal(err)
		}
		data := declarationBytes(t, component)
		m, ref, err := VerifyDeclaration(data, policy)
		if err != nil {
			t.Fatal(err)
		}
		if ref != compatibility.ReferenceFor(data) {
			t.Fatal("wrong reference")
		}
		m.Targets[0].OperatingSystems[0].TestedVersions = []string{"26.04"}
		widened, err := compatibility.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := VerifyDeclaration(widened, policy); err == nil {
			t.Fatal("source metadata widened independent evidence authority")
		}
	}
}

// TestPrepareCanonicalHardwareProfiles proves every authored component admits
// both canonical profiles only with the dedicated unverified override, whilst
// preserving hard target bounds and authenticated publication replay.
func TestPrepareCanonicalHardwareProfiles(t *testing.T) {
	for _, component := range []string{"audio-fullio-v19c", "iptsd-v1", "imx681-libcamera-v1"} {
		for _, device := range []string{"x1e80100-microsoft-denali-oled", "x1p64100-microsoft-denali"} {
			t.Run(component+"/"+device, func(t *testing.T) {
				data := declarationBytes(t, component)
				runner := authenticator(t, component, data)
				target := payloadTarget()
				target.DeviceProfile = device
				request := Request{Component: component, RepositoryRoot: "/oe", PayloadTarget: target}
				if _, _, err := prepareVersion(context.Background(), runner, request, "0.5.0"); err == nil {
					t.Fatal("unqualified profile accepted without dedicated override")
				}
				request.AllowUnverifiedCompatibility = true
				record, canonical, err := prepareVersion(context.Background(), runner, request, "0.5.0")
				if err != nil {
					t.Fatal(err)
				}
				if record.Decision.Status != compatibility.Unverified || record.Decision.Target.DeviceProfile != device {
					t.Fatalf("wrong profile assessment: %+v", record.Decision)
				}
				if err := ValidatePublication(canonical, record); err != nil {
					t.Fatal(err)
				}
				request.PayloadTarget.OSVersion = "24.04"
				if _, _, err := prepareVersion(context.Background(), runner, request, "0.5.0"); err == nil {
					t.Fatal("device inclusion bypassed hard OS bounds")
				}
				request.PayloadTarget = target
				request.PayloadTarget.KernelABI = "7.2.2-jg-0sp11v19-qcom-x1e"
				if _, _, err := prepareVersion(context.Background(), runner, request, "0.5.0"); err == nil {
					t.Fatal("device inclusion bypassed scoped kernel bounds")
				}
			})
		}
	}
}

// TestPrepareAndRevalidate rejects missing source authority, changed bytes,
// impossible targets and development identity without explicit acceptance.
func TestPrepareAndRevalidate(t *testing.T) {
	component := "iptsd-v1"
	data := declarationBytes(t, component)
	runner := authenticator(t, component, data)
	request := Request{Component: component, RepositoryRoot: "/oe", PayloadTarget: payloadTarget(), AllowUnverifiedCompatibility: true}
	record, canonical, err := prepareVersion(context.Background(), runner, request, "0.5.0")
	if err != nil {
		t.Fatal(err)
	}
	if record.Decision.Status != compatibility.Unverified || string(canonical) != string(data) {
		t.Fatalf("invented qualification: %+v", record)
	}
	if err := Revalidate(context.Background(), runner, "/oe", record); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePublication(canonical, record); err != nil {
		t.Fatal(err)
	}
	request.AllowUnverifiedCompatibility = false
	if _, _, err := prepareVersion(context.Background(), runner, request, "0.5.0"); err == nil {
		t.Fatal("unverified packaging accepted without override")
	}
	request.AllowUnverifiedCompatibility = true
	for _, version := range []string{"dev", "0.5.0+dirty", "0.5.0-rc.1", "0.4.0"} {
		got, _, err := prepareVersion(context.Background(), runner, request, version)
		if version == "0.4.0" || version == "0.5.0-rc.1" {
			if err == nil {
				t.Fatal("hard Lexr bound bypassed")
			}
		} else if err != nil || got.Decision.Status != compatibility.Unverified {
			t.Fatalf("dev decision: %+v %v", got, err)
		}
	}
	request.PayloadTarget.KernelABI = "7.2.2-jg-0sp11v19-qcom-x1e"
	if _, _, err := prepareVersion(context.Background(), runner, request, "0.5.0"); err == nil {
		t.Fatal("cross-patch target accepted")
	}
	for _, change := range []func(*Record){
		func(r *Record) { r.AllowUnverified = false }, func(r *Record) { r.SchemaVersion = 2 }, func(r *Record) { r.ProducerLexrVersion = "9.0.0" }, func(r *Record) { r.Decision.Reasons = []string{"invented"} }, func(r *Record) { r.Decision.Status = compatibility.Tested },
	} {
		altered := record
		change(&altered)
		if reflect.DeepEqual(altered, record) {
			t.Fatal("ineffective tampering test")
		}
		if err := ValidatePublication(canonical, altered); err == nil {
			t.Fatal("forged publication record accepted")
		}
	}
	dirty := authenticator(t, component, data)
	dirty.responses["git -C /oe status --porcelain"] = " M declaration"
	if _, _, err := Authenticate(context.Background(), dirty, "/oe", component); err == nil {
		t.Fatal("dirty HEAD accepted")
	}
	runner.responses["git -C /oe cat-file -s HEAD:"+DeclarationPath(component)] = "9999999999"
	if _, _, err := Authenticate(context.Background(), runner, "/oe", component); err == nil {
		t.Fatal("oversized HEAD blob accepted")
	}
}

// TestConfiguredSupportDeclarations checks the authored OE HEAD declarations
// against all compiled pins when the cross-repository integration job supplies
// its explicit checkout. Ordinary unit tests use the canonical fixture copies.
func TestConfiguredSupportDeclarations(t *testing.T) {
	root := strings.TrimSpace(os.Getenv("LEXR_TEST_OE_ROOT"))
	if root == "" {
		t.Skip("no explicit OE integration checkout")
	}
	for _, component := range []string{"audio-fullio-v19c", "iptsd-v1", "imx681-libcamera-v1"} {
		t.Run(component, func(t *testing.T) {
			if _, _, err := Authenticate(context.Background(), platform.ExecRunner{}, root, component); err != nil {
				t.Fatal(err)
			}
		})
	}
}
