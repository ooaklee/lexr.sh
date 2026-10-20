package builderimage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// This fixture executes the actual bounded recipe segment, not a rewritten
// account implementation. Every account command and both Python NSS modules
// are replaced before that segment runs. No host account tool is forwarded.
func identityRecipeSegment(t *testing.T) string {
	t.Helper()
	const begin = "# LEXR_SENSOR_ACCOUNT_BEGIN\n"
	const end = "# LEXR_SENSOR_ACCOUNT_END\n"
	if strings.Count(DependencyScript, begin) != 1 || strings.Count(DependencyScript, end) != 1 {
		t.Fatal("account segment markers must each occur exactly once")
	}
	a := strings.Index(DependencyScript, begin) + len(begin)
	b := strings.Index(DependencyScript, end)
	if b <= a || b-a > 8192 {
		t.Fatal("account segment is absent, inverted, or exceeds its fixture bound")
	}
	segment := DependencyScript[a:b]
	// This is a guard for the trusted compiled segment, not a hostile-code
	// sandbox. Keep known absolute tool/interpreter bypasses out of the fixture.
	for _, forbidden := range []string{
		"/etc/", "/usr/sbin/useradd", "/usr/sbin/groupadd", "/usr/bin/getent", "/usr/bin/passwd",
		"/usr/sbin/usermod", "/usr/sbin/groupmod", "/usr/bin/gpasswd", "/usr/sbin/chpasswd",
		"/usr/sbin/adduser", "/usr/sbin/addgroup", "/usr/bin/python", "/usr/local/bin/python",
		"/opt/homebrew/bin/python", "/bin/sh", "/bin/bash", "/usr/bin/env",
		"sudo ", "rm ", "chmod ", "mkdir ",
	} {
		if strings.Contains(segment, forbidden) {
			t.Fatalf("account fixture refuses an unmocked side effect %q", forbidden)
		}
	}
	return segment
}

func TestIdentityRecipeBoundaryAndRuntimeHomePolicy(t *testing.T) {
	// Home/nologin/package assertions below are static recipe checks. They do
	// not inspect a real Linux image or execute its filesystem policy.
	identityRecipeSegment(t)
	a := strings.Index(DependencyScript, "# LEXR_SENSOR_ACCOUNT_BEGIN\n")
	b := strings.Index(DependencyScript, "# LEXR_SENSOR_ACCOUNT_END\n")
	before, after := DependencyScript[:a], DependencyScript[b:]
	for _, check := range []string{"test -f /usr/sbin/nologin\n", "test -x /usr/sbin/nologin\n", "test ! -e /work/job/home\n", "test ! -L /work/job/home\n"} {
		if !strings.Contains(before, check) {
			t.Fatalf("missing pre-account filesystem assertion %q", check)
		}
	}
	for _, check := range []string{"test ! -e /work/job/home\n", "test ! -L /work/job/home\n"} {
		if strings.Index(after, check) < 0 || strings.Index(after, check) > strings.Index(after, "install -d") {
			t.Fatalf("missing post-account, pre-inventory home assertion %q", check)
		}
	}
	if !strings.Contains(before, "meson ninja-build passwd pkgconf") || !strings.Contains(before, "apt-get clean\n") {
		t.Fatal("account tools must be installed before the account segment")
	}
}

type identityEvent struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

type identityState struct {
	Config map[string]any  `json:"config"`
	Users  [][]any         `json:"users"`
	Groups [][]any         `json:"groups"`
	Events []identityEvent `json:"events"`
	Phase  string          `json:"phase"`
}

type identityOutput struct {
	mu       sync.Mutex
	data     bytes.Buffer
	overflow bool
}

func (o *identityOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	n := len(p)
	remaining := 64*1024 - o.data.Len()
	if n > remaining {
		o.overflow = true
		p = p[:remaining]
	}
	_, _ = o.data.Write(p)
	return n, nil
}

func runIdentityRecipe(t *testing.T, config map[string]any, users, groups [][]any) (identityState, string, error) {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal("controlled recipe fixture requires Python 3:", err)
	}
	python, err = filepath.EvalSymlinks(python)
	if err != nil {
		t.Fatal(err)
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(dir, "state.json")
	state := identityState{Config: config, Users: users, Groups: groups, Events: []identityEvent{}, Phase: "pre"}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, data, 0600); err != nil {
		t.Fatal(err)
	}
	driver := filepath.Join(dir, "fixture.py")
	if err := os.WriteFile(driver, []byte(identityFixturePython), 0600); err != nil {
		t.Fatal(err)
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	for _, name := range []string{"python3", "getent", "groupadd", "useradd", "passwd"} {
		wrapper := "#!/bin/sh\nexec " + quote(python) + " -I " + quote(driver) + " " + quote(name) + " \"$@\"\n"
		if err := os.WriteFile(filepath.Join(bin, name), []byte(wrapper), 0700); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bash, "--noprofile", "--norc", "-c", "set -euo pipefail\n"+identityRecipeSegment(t)+"\nprintf '%s\\n' LEXR_IDENTITY_FIXTURE_COMPLETE\n")
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + bin, "HOME=" + dir, "LANG=C", "LC_ALL=C", "FIXTURE_STATE=" + statePath, "PYTHONDONTWRITEBYTECODE=1"}
	cmd.WaitDelay = time.Second
	var output identityOutput
	cmd.Stdout, cmd.Stderr = &output, &output
	runErr := cmd.Run()
	if ctx.Err() != nil {
		t.Fatal("controlled account fixture exceeded its deadline:", ctx.Err())
	}
	if output.overflow {
		t.Fatal("controlled account fixture exceeded its 64 KiB output limit")
	}
	data, err = os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	return state, output.data.String(), runErr
}

func TestIdentityRecipeCreatesExactLockedAccountInOrder(t *testing.T) {
	state, output, err := runIdentityRecipe(t, nil, nil, nil)
	if err != nil || output != "LEXR_IDENTITY_FIXTURE_COMPLETE\n" {
		t.Fatalf("fresh fixture failed: %v\n%s", err, output)
	}
	wantCommands := []string{"python3", "pwd.getpwnam", "pwd.getpwuid", "grp.getgrnam", "grp.getgrgid", "groupadd", "useradd", "python3", "pwd.getpwnam", "grp.getgrnam", "pwd.getpwuid", "grp.getgrgid", "getent", "getent", "getent", "getent", "passwd"}
	var got []string
	for _, event := range state.Events {
		got = append(got, event.Command)
	}
	if !reflect.DeepEqual(got, wantCommands) {
		t.Fatalf("preflight/create/verify order:\n got %q\nwant %q", got, wantCommands)
	}
	if !reflect.DeepEqual(state.Events[5].Args, []string{"--system", "--gid", "65532", "--key", "SYS_GID_MAX=65532", "lexr-sensor-build"}) {
		t.Fatalf("unexpected group creation policy: %q", state.Events[5].Args)
	}
	wantUser := []string{"--system", "--uid", "65532", "--gid", "65532", "--no-user-group", "--no-create-home", "--no-log-init", "--home-dir", "/work/job/home", "--shell", "/usr/sbin/nologin", "--comment", "Lexr sensor source builder", "--password", "!", "--key", "SYS_UID_MAX=65532", "lexr-sensor-build"}
	if !reflect.DeepEqual(state.Events[6].Args, wantUser) {
		t.Fatalf("unexpected user creation policy: %q", state.Events[6].Args)
	}
	if !reflect.DeepEqual(state.Events[16].Args, []string{"--status", "lexr-sensor-build"}) {
		t.Fatalf("password lock was not independently queried: %q", state.Events[16].Args)
	}
	for i, want := range [][]string{{"passwd", "lexr-sensor-build"}, {"passwd", "65532"}, {"group", "lexr-sensor-build"}, {"group", "65532"}} {
		if !reflect.DeepEqual(state.Events[12+i].Args, want) {
			t.Fatalf("unexpected name/numeric getent query: got %q, want %q", state.Events[12+i].Args, want)
		}
	}
	if len(state.Users) != 1 || len(state.Groups) != 1 || state.Phase != "post" {
		t.Fatalf("unexpected fixture database: %+v", state)
	}
}

func TestIdentityRecipeRejectsOccupiedOrUnavailableNSSBeforeMutation(t *testing.T) {
	user := []any{"lexr-sensor-build", "x", 1234, 1234, "occupied", "/unused", "/unused"}
	uid := []any{"another-user", "x", 65532, 1234, "occupied", "/unused", "/unused"}
	group := []any{"lexr-sensor-build", "x", 1234, []any{}}
	gid := []any{"another-group", "x", 65532, []any{}}
	cases := []struct {
		name          string
		config        map[string]any
		users, groups [][]any
	}{
		{name: "user-name", users: [][]any{user}}, {name: "uid", users: [][]any{uid}},
		{name: "group-name", groups: [][]any{group}}, {name: "gid", groups: [][]any{gid}},
	}
	for _, lookup := range []string{"pwd.getpwnam", "pwd.getpwuid", "grp.getgrnam", "grp.getgrgid"} {
		cases = append(cases, struct {
			name          string
			config        map[string]any
			users, groups [][]any
		}{name: lookup + "-nss-error", config: map[string]any{"lookup_error": lookup, "lookup_error_phase": "pre"}})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state, output, err := runIdentityRecipe(t, tc.config, tc.users, tc.groups)
			if err == nil || strings.Contains(output, "LEXR_IDENTITY_FIXTURE_COMPLETE") {
				t.Fatalf("occupied/error fixture was accepted: %v\n%s", err, output)
			}
			for _, event := range state.Events {
				if event.Command == "groupadd" || event.Command == "useradd" || event.Command == "getent" || event.Command == "passwd" {
					t.Fatalf("preflight failure reached account mutation or postcheck: %+v", event)
				}
			}
		})
	}
}

func TestIdentityRecipeRejectsToolFailuresAndWrongPostIdentity(t *testing.T) {
	cases := []struct {
		name   string
		config map[string]any
	}{}
	for _, tool := range []string{"groupadd", "useradd", "passwd"} {
		cases = append(cases, struct {
			name   string
			config map[string]any
		}{tool + "-failure", map[string]any{"fail_command": tool}})
	}
	for _, query := range []string{"passwd:lexr-sensor-build", "passwd:65532", "group:lexr-sensor-build", "group:65532"} {
		for _, exit := range []int{2, 3} {
			cases = append(cases, struct {
				name   string
				config map[string]any
			}{fmt.Sprintf("getent-%s-exit%d", query, exit), map[string]any{"getent_fail": query, "getent_exit": exit}})
		}
	}
	for _, lookup := range []string{"pwd.getpwnam", "pwd.getpwuid", "grp.getgrnam", "grp.getgrgid"} {
		cases = append(cases, struct {
			name   string
			config map[string]any
		}{lookup + "-post-nss-error", map[string]any{"lookup_error": lookup, "lookup_error_phase": "post"}})
	}
	for _, field := range []string{"name", "passwd", "uid", "gid", "gecos", "home", "shell"} {
		cases = append(cases, struct {
			name   string
			config map[string]any
		}{"wrong-user-" + field, map[string]any{"corrupt_user": field}})
	}
	for _, field := range []string{"name", "passwd", "gid", "members"} {
		cases = append(cases, struct {
			name   string
			config map[string]any
		}{"wrong-group-" + field, map[string]any{"corrupt_group": field}})
	}
	for _, status := range []string{
		"lexr-sensor-build P 01/01/1970 0 99999 7 -1",
		"lexr-sensor-build NP 01/01/1970 0 99999 7 -1",
		"lexr-sensor-build X 01/01/1970 0 99999 7 -1",
		"wrong-user L 01/01/1970 0 99999 7 -1", "",
	} {
		cases = append(cases, struct {
			name   string
			config map[string]any
		}{"wrong-lock-" + status, map[string]any{"passwd_status": status}})
	}
	for _, query := range []string{"passwd:65532", "group:65532"} {
		cases = append(cases, struct {
			name   string
			config map[string]any
		}{"getent-inconsistent-" + query, map[string]any{"getent_corrupt": query}})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, output, err := runIdentityRecipe(t, tc.config, nil, nil)
			if err == nil || strings.Contains(output, "LEXR_IDENTITY_FIXTURE_COMPLETE") {
				t.Fatalf("invalid account fixture was accepted: %v\n%s", err, output)
			}
		})
	}
}

const identityFixturePython = `import collections
import json
import os
import sys
import types

path = os.environ['FIXTURE_STATE']
with open(path, encoding='utf-8') as stream:
    state = json.load(stream)
config = state['config'] or {}
state['users'] = state['users'] or []
state['groups'] = state['groups'] or []
command, args = sys.argv[1], sys.argv[2:]

def save():
    with open(path, 'w', encoding='utf-8') as stream:
        json.dump(state, stream)

def event(name, values):
    state['events'].append({'command': name, 'args': [str(v) for v in values]})
    save()

event(command, args)
if config.get('fail_command') == command:
    raise SystemExit(19)

User = collections.namedtuple('User', 'pw_name pw_passwd pw_uid pw_gid pw_gecos pw_dir pw_shell')
Group = collections.namedtuple('Group', 'gr_name gr_passwd gr_gid gr_mem')

def lookup(kind, index, key, name):
    event(name, [key])
    if config.get('lookup_error') == name and config.get('lookup_error_phase') == state['phase']:
        raise OSError('controlled NSS provider failure')
    rows = state[kind]
    matches = [row for row in rows if row[index] == key]
    if len(matches) != 1:
        raise KeyError(key)
    return (User if kind == 'users' else Group)(*matches[0])

if command == 'python3':
    if args != ['-']:
        raise SystemExit('unexpected Python invocation')
    # pwd is a builtin module: PYTHONPATH cannot shadow it. Inject both NSS
    # modules explicitly before compiling the exact stdin recipe program.
    pwd = types.ModuleType('pwd')
    pwd.getpwnam = lambda key: lookup('users', 0, key, 'pwd.getpwnam')
    pwd.getpwuid = lambda key: lookup('users', 2, key, 'pwd.getpwuid')
    grp = types.ModuleType('grp')
    grp.getgrnam = lambda key: lookup('groups', 0, key, 'grp.getgrnam')
    grp.getgrgid = lambda key: lookup('groups', 2, key, 'grp.getgrgid')
    sys.modules['pwd'], sys.modules['grp'] = pwd, grp
    source = sys.stdin.read(8193)
    if len(source) > 8192:
        raise SystemExit('fixture Python body exceeds bound')
    exec(compile(source, '<actual-dependency-account-python>', 'exec'), {'__name__': '__main__'})
elif command in ('groupadd', 'useradd'):
    values, switches = {}, set()
    value_flags = {'--gid', '--uid', '--key', '--home-dir', '--shell', '--comment', '--password'}
    switch_flags = {'--system', '--no-user-group', '--no-create-home', '--no-log-init'}
    i = 0
    while i < len(args) - 1:
        flag = args[i]
        if flag in value_flags and i + 1 < len(args) - 1:
            values[flag] = args[i + 1]
            i += 2
        elif flag in switch_flags:
            switches.add(flag)
            i += 1
        else:
            raise SystemExit('unknown account-tool option')
    if i != len(args) - 1 or args[-1].startswith('-'):
        raise SystemExit('missing account name')
    if command == 'groupadd':
        row = [args[-1], 'x', int(values['--gid']), []]
        field = config.get('corrupt_group')
        if field:
            index = {'name': 0, 'passwd': 1, 'gid': 2, 'members': 3}[field]
            row[index] = 1234 if field == 'gid' else (['unexpected'] if field == 'members' else 'wrong')
        state['groups'].append(row)
    else:
        row = [args[-1], 'x', int(values['--uid']), int(values['--gid']), values['--comment'], values['--home-dir'], values['--shell']]
        field = config.get('corrupt_user')
        if field:
            index = {'name': 0, 'passwd': 1, 'uid': 2, 'gid': 3, 'gecos': 4, 'home': 5, 'shell': 6}[field]
            row[index] = 1234 if field in ('uid', 'gid') else 'wrong'
        state['users'].append(row)
        state['phase'] = 'post'
    save()
elif command == 'getent':
    if len(args) != 2 or args[0] not in ('passwd', 'group'):
        raise SystemExit('unexpected getent invocation')
    query = ':'.join(args)
    if config.get('getent_fail') == query:
        raise SystemExit(config['getent_exit'])
    kind = 'users' if args[0] == 'passwd' else 'groups'
    numeric = args[1].isdigit()
    matches = [row for row in state[kind] if row[2 if numeric else 0] == (int(args[1]) if numeric else args[1])]
    if len(matches) != 1:
        raise SystemExit(2)
    row = list(matches[0])
    if args[0] == 'group':
        row[3] = ','.join(row[3])
    if config.get('getent_corrupt') == query:
        row[0] = 'inconsistent'
    print(':'.join(str(value) for value in row))
elif command == 'passwd':
    if args != ['--status', 'lexr-sensor-build']:
        raise SystemExit('unexpected passwd invocation')
    print(config.get('passwd_status', 'lexr-sensor-build L 01/01/1970 0 99999 7 -1'))
else:
    raise SystemExit('unmocked account command')
`
