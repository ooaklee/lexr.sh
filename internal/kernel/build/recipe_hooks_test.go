package build

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests execute only the Python validator extracted from containerRecipe.
// The kernel maintainer scripts remain inert input data: no shell, package
// manager, Docker or kernel build is invoked.
const recipeKernelHookScriptBound = 131072

type recipeKernelHookInput struct {
	abi      string
	postinst string
	postrm   string
	triggers string
}

type recipeKernelHookSources struct {
	Fixtures []struct {
		Name           string `json:"name"`
		SourceRevision string `json:"source_revision"`
		ABIRelease     string `json:"abi_release"`
		ABI            string `json:"abi"`
		Templates      []struct {
			Kind           string `json:"kind"`
			FixtureFile    string `json:"fixture_file"`
			TemplateSHA256 string `json:"template_sha256"`
			RenderedSHA256 string `json:"rendered_sha256"`
		} `json:"templates"`
	} `json:"fixtures"`
}

// recipeKernelHookSourceFixture renders the independently pinned source
// templates using the kernel's exact install_control substitutions. The source
// revisions, Git blobs and transformations are in kernel_hooks/SOURCES.json.
func recipeKernelHookSourceFixture(t *testing.T, name string) recipeKernelHookInput {
	t.Helper()
	root := filepath.Join("testdata", "kernel_hooks")
	encoded, err := os.ReadFile(filepath.Join(root, "SOURCES.json"))
	if err != nil {
		t.Fatal(err)
	}
	var sources recipeKernelHookSources
	if err := json.Unmarshal(encoded, &sources); err != nil {
		t.Fatal(err)
	}
	pins := map[string]string{
		"legacy":      "63de629462c5c392402977e184c4b2248b82560c",
		"upstream729": "d84d9b29dffe2199bf295824f82099ee58997ce7",
	}
	if len(sources.Fixtures) != len(pins) {
		t.Fatalf("source fixture count = %d", len(sources.Fixtures))
	}
	for _, fixture := range sources.Fixtures {
		if fixture.Name != name {
			continue
		}
		if fixture.SourceRevision != pins[name] || len(fixture.Templates) != 2 {
			t.Fatalf("unexpected fixture provenance for %q", name)
		}
		result := recipeKernelHookInput{
			abi:      fixture.ABI,
			triggers: fmt.Sprintf("interest linux-update-%s\n", fixture.ABI),
		}
		if fixture.ABI != fixture.ABIRelease+"-qcom-x1e" {
			t.Fatalf("source ABI transformation differs for %q", name)
		}
		seen := make(map[string]bool)
		for _, template := range fixture.Templates {
			if seen[template.Kind] {
				t.Fatalf("duplicate template kind %q", template.Kind)
			}
			seen[template.Kind] = true
			raw, err := os.ReadFile(filepath.Join(root, template.FixtureFile))
			if err != nil {
				t.Fatal(err)
			}
			if digest := fmt.Sprintf("%x", sha256.Sum256(raw)); digest != template.TemplateSHA256 {
				t.Fatalf("source template digest differs for %s", template.FixtureFile)
			}
			rendered := strings.NewReplacer(
				"@abiname@", fixture.ABIRelease,
				"@localversion@", "-qcom-x1e",
				"@image-stem@", "vmlinuz",
			).Replace(string(raw))
			if digest := fmt.Sprintf("%x", sha256.Sum256([]byte(rendered))); digest != template.RenderedSHA256 {
				t.Fatalf("rendered template digest differs for %s", template.FixtureFile)
			}
			switch template.Kind {
			case "postinst":
				result.postinst = rendered
			case "postrm":
				result.postrm = rendered
			default:
				t.Fatalf("unknown source template kind %q", template.Kind)
			}
		}
		if !seen["postinst"] || !seen["postrm"] {
			t.Fatal("source fixture lacks a required template")
		}
		return result
	}
	t.Fatalf("source fixture %q not found", name)
	return recipeKernelHookInput{}
}

func recipeKernelHookReplaceOnce(t *testing.T, input, before, after string) string {
	t.Helper()
	if count := strings.Count(input, before); count != 1 {
		t.Fatalf("fixture mutation expected one occurrence of %q, got %d", before, count)
	}
	return strings.Replace(input, before, after, 1)
}

func recipeKernelHookReparameterize(t *testing.T, input recipeKernelHookInput, abi string) recipeKernelHookInput {
	t.Helper()
	original := input.abi
	input.postinst = recipeKernelHookReplaceOnce(t, input.postinst, original, abi)
	input.postrm = recipeKernelHookReplaceOnce(t, input.postrm, original, abi)
	input.triggers = recipeKernelHookReplaceOnce(t, input.triggers, original, abi)
	input.abi = abi
	return input
}

// recipeKernelHookReindent changes outer leading indentation only. In
// particular, the generated trigger payload and its raw EOF stay byte-exact.
func recipeKernelHookReindent(input string) string {
	lines := strings.SplitAfter(input, "\n")
	inPayload := false
	for index, line := range lines {
		if inPayload {
			if line == "EOF\n" {
				inPayload = false
			}
			continue
		}
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			lines[index] = "\t  " + strings.TrimLeft(line, " \t")
		}
		if strings.Contains(line, "cat - >/usr/lib/linux/triggers/$version <<EOF") {
			inPayload = true
		}
	}
	return strings.Join(lines, "")
}

func recipeKernelHookPad(t *testing.T, script string, size int) string {
	t.Helper()
	remaining := size - len(script)
	if remaining < 2 || !strings.HasSuffix(script, "\n") {
		t.Fatal("invalid fixture padding request")
	}
	return script + "#" + strings.Repeat("p", remaining-2) + "\n"
}

func TestRecipeKernelHookContractAcceptsPinnedSourceTemplates(t *testing.T) {
	for _, name := range []string{"legacy", "upstream729"} {
		t.Run(name, func(t *testing.T) {
			original := recipeKernelHookSourceFixture(t, name)
			variants := []struct {
				name  string
				input recipeKernelHookInput
			}{
				{name: "exact", input: original},
				{name: "different safe ABI", input: recipeKernelHookReparameterize(t, original, "7.2.9-jg-0sp11v1000-qcom-x1e")},
			}
			commented := original
			commented.postinst = recipeKernelHookReplaceOnce(t, commented.postinst, "set -e\n", "set -e\n\n# Source-test outer comment.\n")
			commented.postrm = recipeKernelHookReplaceOnce(t, commented.postrm, "set -e\n", "set -e\n\n# Source-test outer comment.\n")
			commented.postinst += "\n# Trailing outer comment.\n"
			commented.postrm += "\n# Trailing outer comment.\n"
			variants = append(variants, struct {
				name  string
				input recipeKernelHookInput
			}{name: "outer comments and blanks", input: commented})
			indented := original
			indented.postinst = recipeKernelHookReindent(indented.postinst)
			indented.postrm = recipeKernelHookReindent(indented.postrm)
			variants = append(variants, struct {
				name  string
				input recipeKernelHookInput
			}{name: "outer indentation", input: indented})
			for _, variant := range variants {
				t.Run(variant.name, func(t *testing.T) {
					if output, err := executeRecipeKernelHookContract(t, variant.input); err != nil {
						t.Fatalf("source contract rejected: %v\n%s", err, output)
					}
				})
			}
		})
	}
}

func TestRecipeKernelHookContractAcceptsExactScriptBound(t *testing.T) {
	for _, name := range []string{"legacy", "upstream729"} {
		t.Run(name, func(t *testing.T) {
			input := recipeKernelHookSourceFixture(t, name)
			input.postinst = recipeKernelHookPad(t, input.postinst, recipeKernelHookScriptBound)
			input.postrm = recipeKernelHookPad(t, input.postrm, recipeKernelHookScriptBound)
			if output, err := executeRecipeKernelHookContract(t, input); err != nil {
				t.Fatalf("exact-bound scripts rejected: %v\n%s", err, output)
			}
		})
	}
}

func TestRecipeKernelHookContractRejectsBrokenLifecycle(t *testing.T) {
	original := recipeKernelHookSourceFixture(t, "upstream729")
	wrongABI := "7.2.9-jg-0sp11v998-qcom-x1e"
	mutations := []struct {
		name   string
		target string
		before string
		after  string
	}{
		{"postinst ABI", "postinst", "version=" + original.abi, "version=" + wrongABI},
		{"postrm ABI", "postrm", "version=" + original.abi, "version=" + wrongABI},
		{"unrendered ABI", "postinst", "version=" + original.abi, "version=@abiname@@localversion@"},
		{"unrendered image", "postrm", "image_path=/boot/vmlinuz-$version", "image_path=/boot/@image-stem@-$version"},
		{"duplicate version", "postinst", "version=" + original.abi, "version=" + original.abi + "\nversion=" + wrongABI},
		{"wrong image directory", "postinst", "image_path=/boot/vmlinuz-$version", "image_path=/tmp/vmlinuz-$version"},
		{"wrong image stem", "postrm", "image_path=/boot/vmlinuz-$version", "image_path=/boot/Image-$version"},
		{"wrong trigger owner", "triggers", original.abi, wrongABI},
		{"duplicate trigger owner", "triggers", original.triggers, original.triggers + original.triggers},
		{"different trigger declaration", "triggers", "interest ", "interest-noawait "},
		{"trigger missing final newline", "triggers", original.triggers, strings.TrimSuffix(original.triggers, "\n")},
		{"wrong triggered action", "postinst", "if [ \"$1\" = triggered ]; then", "if [ \"$1\" = configure ]; then"},
		{"wrong trigger path", "postinst", "trigger=/usr/lib/linux/triggers/$version", "trigger=/tmp/triggers/$version"},
		{"missing trigger execution", "postinst", "        sh \"$trigger\"\n", ""},
		{"comment-only trigger execution", "postinst", "        sh \"$trigger\"", "        # sh \"$trigger\""},
		{"missing trigger removal", "postinst", "        rm -f \"$trigger\"\n", ""},
		{"early triggered exit", "postinst", "        sh \"$trigger\"", "        exit 0\n        sh \"$trigger\""},
		{"unconditional early exit", "postinst", "set -e\n", "set -e\nexit 0\n"},
		{"missing configure gate", "postinst", "if [ \"$1\" != configure ]; then\n    exit 0\nfi\n", ""},
		{"wrong configure gate", "postinst", "if [ \"$1\" != configure ]; then", "if [ \"$1\" = configure ]; then"},
		{"missing trigger mkdir", "postinst", "    mkdir -p /usr/lib/linux/triggers\n", ""},
		{"wrong trigger write", "postinst", "cat - >/usr/lib/linux/triggers/$version <<EOF", "cat - >/tmp/triggers/$version <<EOF"},
		{"missing EOF", "postinst", "\nEOF\n", "\n"},
		{"indented EOF", "postinst", "\nEOF\n", "\n    EOF\n"},
		{"space-suffixed EOF", "postinst", "\nEOF\n", "\nEOF \n"},
		{"quoted heredoc", "postinst", "<<EOF\n", "<<'EOF'\n"},
		{"blank inside payload", "postinst", "\nDEB_MAINT_PARAMS=", "\n\nDEB_MAINT_PARAMS="},
		{"comment inside payload", "postinst", "\nDEB_MAINT_PARAMS=", "\n# Not an outer comment.\nDEB_MAINT_PARAMS="},
		{"commented payload invocation", "postinst", "\nDEB_MAINT_PARAMS=", "\n# DEB_MAINT_PARAMS="},
		{"payload continuation trailing space", "postinst", "--arg=$version \\\n", "--arg=$version \\ \n"},
		{"missing trigger activation", "postinst", "    dpkg-trigger --no-await linux-update-$version\n", ""},
		{"wrong activated trigger ABI", "postinst", "linux-update-$version", "linux-update-wrong"},
		{"postinst fixed action", "postinst", "DEB_MAINT_PARAMS=\"$*\"", "DEB_MAINT_PARAMS=\"configure\""},
		{"postrm fixed action", "postrm", "DEB_MAINT_PARAMS=\"$*\"", "DEB_MAINT_PARAMS=\"configure\""},
		{"postrm wrong argument forwarding", "postrm", "DEB_MAINT_PARAMS=\"$*\"", "DEB_MAINT_PARAMS=\"$@\""},
		{"postinst missing version argument", "postinst", " --arg=$version", ""},
		{"postrm missing image argument", "postrm", " --arg=$image_path", ""},
		{"postinst suppress hook errors", "postinst", " --exit-on-error", ""},
		{"postrm suppress hook errors", "postrm", " --exit-on-error", ""},
		{"postrm ignore hook failure", "postrm", "--arg=$image_path /etc/kernel/postrm.d /usr/share/kernel/postrm.d", "--arg=$image_path /etc/kernel/postrm.d /usr/share/kernel/postrm.d || true"},
		{"postinst reversed directory priority", "postinst", "--arg=$image_path /etc/kernel/postinst.d /usr/share/kernel/postinst.d", "--arg=$image_path /usr/share/kernel/postinst.d /etc/kernel/postinst.d"},
		{"postrm reversed directory priority", "postrm", "--arg=$image_path /etc/kernel/postrm.d /usr/share/kernel/postrm.d", "--arg=$image_path /usr/share/kernel/postrm.d /etc/kernel/postrm.d"},
		{"postinst extra hook directory", "postinst", "--arg=$image_path /etc/kernel/postinst.d /usr/share/kernel/postinst.d", "--arg=$image_path /etc/kernel/postinst.d /usr/share/kernel/postinst.d /tmp/hooks"},
		{"postrm extra hook directory", "postrm", "--arg=$image_path /etc/kernel/postrm.d /usr/share/kernel/postrm.d", "--arg=$image_path /etc/kernel/postrm.d /usr/share/kernel/postrm.d /tmp/hooks"},
		{"postinst missing etc invocation", "postinst", "--arg=$image_path /etc/kernel/postinst.d /usr/share/kernel/postinst.d", "--arg=$image_path /usr/share/kernel/postinst.d"},
		{"postrm missing etc invocation", "postrm", "--arg=$image_path /etc/kernel/postrm.d /usr/share/kernel/postrm.d", "--arg=$image_path /usr/share/kernel/postrm.d"},
		{"postinst missing etc guard", "postinst", "if [ -d /etc/kernel/postinst.d ] || [ -d /usr/share/kernel/postinst.d ]; then", "if [ -d /usr/share/kernel/postinst.d ]; then"},
		{"postrm wrong guard operator", "postrm", "if [ -d /etc/kernel/postrm.d ] || [ -d /usr/share/kernel/postrm.d ]; then", "if [ -d /etc/kernel/postrm.d ] && [ -d /usr/share/kernel/postrm.d ]; then"},
		{"postrm missing pending-trigger cleanup", "postrm", "        rm -f /usr/lib/linux/triggers/$version\n", ""},
		{"postrm pending-trigger wrong ABI", "postrm", "if [ -f /usr/lib/linux/triggers/$version ]; then", "if [ -f /usr/lib/linux/triggers/wrong ]; then"},
		{"postrm purge blank continuation", "postrm", "modules.pcimap \\\n", "modules.pcimap \\\n\n"},
		{"postrm purge comment continuation", "postrm", "modules.pcimap \\\n", "modules.pcimap \\\n# Not an outer comment in a continuation.\n"},
		{"postrm purge backslash trailing space", "postrm", "modules.pcimap \\\n", "modules.pcimap \\ \n"},
		{"missing shell error mode", "postinst", "set -e\n", ""},
		{"postrm missing shell error mode", "postrm", "set -e\n", ""},
		{"unsupported interpreter", "postrm", "#!/bin/sh\n", "#!/bin/false\n"},
		{"blank before interpreter", "postinst", "#!/bin/sh\n", "\n#!/bin/sh\n"},
		{"missing final script newline", "postinst", "\nexit 0\n", "\nexit 0"},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			input := original
			var target *string
			switch mutation.target {
			case "postinst":
				target = &input.postinst
			case "postrm":
				target = &input.postrm
			case "triggers":
				target = &input.triggers
			default:
				t.Fatalf("unknown mutation target %q", mutation.target)
			}
			*target = recipeKernelHookReplaceOnce(t, *target, mutation.before, mutation.after)
			assertRecipeKernelHookRejected(t, input)
		})
	}
}

func TestRecipeKernelHookContractRejectsMixedTemplateFamilies(t *testing.T) {
	for _, name := range []string{"legacy", "upstream729"} {
		t.Run(name, func(t *testing.T) {
			input := recipeKernelHookSourceFixture(t, name)
			otherName := "legacy"
			if name == otherName {
				otherName = "upstream729"
			}
			other := recipeKernelHookReparameterize(t, recipeKernelHookSourceFixture(t, otherName), input.abi)
			input.postrm = other.postrm
			assertRecipeKernelHookRejected(t, input)
		})
	}
}

func TestRecipeKernelHookContractRejectsUnsafeABI(t *testing.T) {
	for _, abi := range []string{"", ".hidden", "-leading", "../escape", "bad/abi", "bad\nabi", "bad\rabi", "bad abi"} {
		t.Run(fmt.Sprintf("%q", abi), func(t *testing.T) {
			input := recipeKernelHookReparameterize(t, recipeKernelHookSourceFixture(t, "upstream729"), abi)
			assertRecipeKernelHookRejected(t, input)
		})
	}
}

func TestRecipeKernelHookContractRejectsEmptyOversizedOrNontextScripts(t *testing.T) {
	for _, target := range []string{"postinst", "postrm"} {
		for _, name := range []string{"empty", "oversized", "NUL", "CRLF", "invalid UTF8"} {
			t.Run(target+"/"+name, func(t *testing.T) {
				input := recipeKernelHookSourceFixture(t, "upstream729")
				script := &input.postinst
				if target == "postrm" {
					script = &input.postrm
				}
				switch name {
				case "empty":
					*script = ""
				case "oversized":
					*script = recipeKernelHookPad(t, *script, recipeKernelHookScriptBound+1)
				case "NUL":
					*script += "\x00"
				case "CRLF":
					*script = strings.ReplaceAll(*script, "\n", "\r\n")
				case "invalid UTF8":
					*script += string([]byte{0xff})
				}
				assertRecipeKernelHookRejected(t, input)
			})
		}
	}
}

func TestRecipeKernelHookContractRejectsEmptyOversizedOrNontextTriggers(t *testing.T) {
	for _, name := range []string{"empty", "oversized", "NUL", "CRLF", "invalid UTF8", "extra trigger"} {
		t.Run(name, func(t *testing.T) {
			input := recipeKernelHookSourceFixture(t, "upstream729")
			switch name {
			case "empty":
				input.triggers = ""
			case "oversized":
				input.triggers = strings.Repeat("x", recipeKernelHookScriptBound+1)
			case "NUL":
				input.triggers += "\x00"
			case "CRLF":
				input.triggers = strings.ReplaceAll(input.triggers, "\n", "\r\n")
			case "invalid UTF8":
				input.triggers += string([]byte{0xff})
			case "extra trigger":
				input.triggers += "interest linux-update-another-abi\n"
			}
			assertRecipeKernelHookRejected(t, input)
		})
	}
}

func TestRecipeKernelHookContractRejectsCommentDecoys(t *testing.T) {
	input := recipeKernelHookSourceFixture(t, "upstream729")
	input.postinst = "#!/bin/sh\nset -e\nexit 0\n# " + strings.ReplaceAll(input.postinst, "\n", "\n# ")
	assertRecipeKernelHookRejected(t, input)
}

// assertRecipeKernelHookRejected distinguishes a deliberate contract rejection
// from a missing interpreter, timeout, signal, syntax error or unrelated crash.
func assertRecipeKernelHookRejected(t *testing.T, input recipeKernelHookInput) {
	t.Helper()
	output, err := executeRecipeKernelHookContract(t, input)
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 ||
		(!strings.HasPrefix(string(output), "Generated linux-image maintainer contract rejected: ") &&
			!strings.HasPrefix(string(output), "Generated kernel ABI is unsafe.")) {
		t.Fatalf("expected deliberate contract rejection, got %v:\n%s", err, output)
	}
}

// executeRecipeKernelHookContract runs the exact Python shipped in the recipe,
// never the source templates or their rendered shell commands.
func executeRecipeKernelHookContract(t *testing.T, input recipeKernelHookInput) ([]byte, error) {
	t.Helper()
	const startMarker = "<<'PY_KERNEL_HOOK_CONTRACT'\n"
	const endMarker = "\nPY_KERNEL_HOOK_CONTRACT\n"
	if strings.Count(containerRecipe, startMarker) != 1 || strings.Count(containerRecipe, endMarker) != 1 {
		t.Fatal("recipe must contain exactly one kernel-hook validator heredoc")
	}
	start := strings.Index(containerRecipe, startMarker) + len(startMarker)
	end := strings.Index(containerRecipe[start:], endMarker)
	if end < 0 {
		t.Fatal("kernel-hook validator heredoc is incomplete")
	}
	root := t.TempDir()
	paths := make([]string, 0, 3)
	for _, fixture := range []struct {
		name string
		body string
	}{
		{name: "postinst", body: input.postinst},
		{name: "postrm", body: input.postrm},
		{name: "triggers", body: input.triggers},
	} {
		path := filepath.Join(root, fixture.name)
		if err := os.WriteFile(path, []byte(fixture.body), 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "python3", append([]string{"-", input.abi}, paths...)...)
	command.Stdin = strings.NewReader(containerRecipe[start : start+end])
	return command.CombinedOutput()
}
