package fedora

import (
	"context"
	"fmt"
	"strings"

	"github.com/ooaklee/lexr.sh/internal/platform"
)

// anacondaBootPolicyScript extends only the source's preserved argument list.
// ConfigParser independently checks the resulting semantics while insertion
// retains comments and every unrelated byte. Later profile/drop-in overrides
// are rejected because their effective policy would need separate inspection.
const anacondaBootPolicyScript = `import configparser, pathlib, re, stat, sys
root, operation = pathlib.Path(sys.argv[1]), sys.argv[2]
required = sys.argv[3:]
directory = root / 'etc/anaconda'
for parent in (root/'etc', directory):
    if parent.is_symlink() or not parent.is_dir():
        raise SystemExit('unsafe Anaconda configuration directory')

def read_config(path):
    info = path.lstat()
    if not stat.S_ISREG(info.st_mode) or not 0 < info.st_size <= 1048576:
        raise SystemExit('Anaconda configuration is not a bounded regular file')
    data = path.read_bytes()
    parser = configparser.ConfigParser()
    parser.read_string(data.decode('utf-8'))
    return data, parser

for name in ('profile.d', 'conf.d'):
    extra = directory / name
    if extra.is_symlink() or (extra.exists() and not extra.is_dir()):
        raise SystemExit('unsafe Anaconda configuration override directory')
    for path in sorted(extra.glob('*.conf')):
        _, parser = read_config(path)
        if parser.has_option('Bootloader', 'preserved_arguments'):
            raise SystemExit('uninspected Anaconda preserved_arguments override: ' + path.name)

path = directory / 'anaconda.conf'
data, parser = read_config(path)
existing = parser.get('Bootloader', 'preserved_arguments').split()
if not existing or any(not re.fullmatch(r'[A-Za-z0-9_.-]+', key) for key in existing):
    raise SystemExit('unsupported Anaconda preserved argument syntax')
missing = [key for key in required if key not in existing]
if operation == 'check':
    if missing:
        raise SystemExit('Anaconda omits installed boot argument keys: ' + ' '.join(missing))
    print('Anaconda preserves every installed boot argument key')
elif operation == 'prepare':
    if not missing:
        sys.exit(0)
    lines = data.decode('utf-8').splitlines(keepends=True)
    section, starts = '', []
    for index, line in enumerate(lines):
        stripped = line.strip()
        if stripped.startswith('['):
            section = stripped
        if section == '[Bootloader]' and re.match(r'^preserved_arguments[ \t]*=', line):
            starts.append(index)
    if len(starts) != 1:
        raise SystemExit('ambiguous Anaconda preserved argument declaration')
    end = starts[0] + 1
    for index in range(end, len(lines)):
        stripped = lines[index].strip()
        if not stripped or stripped.startswith(('#', ';')):
            continue
        if not lines[index][0].isspace():
            break
        end = index + 1
    newline = '\r\n' if lines[starts[0]].endswith('\r\n') else '\n'
    if not lines[end-1].endswith('\n'):
        lines[end-1] += newline
    lines.insert(end, '    ' + ' '.join(missing) + newline)
    updated = ''.join(lines).encode('utf-8')
    checked = configparser.ConfigParser()
    checked.read_string(updated.decode('utf-8'))
    if checked.get('Bootloader', 'preserved_arguments').split() != existing + missing:
        raise SystemExit('Anaconda boot argument insertion changed source semantics')
    for section in parser.sections():
        for key, value in parser.items(section):
            if (section, key) != ('Bootloader', 'preserved_arguments') and checked.get(section, key) != value:
                raise SystemExit('Anaconda boot argument insertion changed unrelated policy')
    path.write_bytes(updated)
else:
    raise SystemExit('unsupported Anaconda boot policy operation')
`

// anacondaBootArgumentKeys preserves the values supplied by the selected live
// entry while allowing Anaconda to generate its normal installed root options.
func anacondaBootArgumentKeys() []string {
	keys := make([]string, 0, len(installedBootArguments))
	for _, argument := range installedBootArguments {
		key, _, _ := strings.Cut(argument, "=")
		keys = append(keys, key)
	}
	return keys
}

// runAnacondaBootPolicy prepares or independently checks the extracted root's
// installer policy without touching its bootloader implementation.
func runAnacondaBootPolicy(ctx context.Context, docker *platform.Docker, image, workspace, volume, operation string) error {
	arguments := append([]string{"python3", "-c", anacondaBootPolicyScript, "/linux-work/rootfs", operation}, anacondaBootArgumentKeys()...)
	if err := docker.RunInWorkspaceVolume(ctx, image, workspace, volume, arguments...); err != nil {
		return fmt.Errorf("%s Fedora Anaconda boot argument preservation: %w", operation, err)
	}
	return nil
}
