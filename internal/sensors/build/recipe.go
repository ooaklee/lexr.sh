package build

import (
	"crypto/sha256"
	"encoding/hex"
)

// InitScript initializes only an exclusively new named-volume job. The
// controller must run it as root with no capabilities except CHOWN. Case
// probes remain available for diagnosis; neither script deletes old work.
// Transfer the private parent last: CHOWN does not bypass directory search.
const InitScript = `#!/bin/bash
set -euo pipefail
export PATH=/usr/sbin:/usr/bin:/sbin:/bin
export LANG=C.UTF-8 LC_ALL=C.UTF-8 TZ=UTC
umask 077
test "$(id -u)" = 0
test -d /work
first_work_entry="$(find /work -mindepth 1 -maxdepth 1 -print -quit)"
test -z "$first_work_entry"
test ! -e /work/job
test ! -L /work/job
mkdir -m 0700 /work/job
printf 'upper\n' >/work/job/CaseProbe
printf 'lower\n' >/work/job/caseprobe
test "$(cat /work/job/CaseProbe)" = upper
test "$(cat /work/job/caseprobe)" = lower
test ! /work/job/CaseProbe -ef /work/job/caseprobe
mkdir -m 0700 /work/job/home /work/job/cache /work/job/tmp
chmod 0700 /work/job /work/job/home /work/job/cache /work/job/tmp
chmod 0444 /work/job/CaseProbe /work/job/caseprobe
chown 65532:65532 /work/job/home /work/job/cache /work/job/tmp \
  /work/job/CaseProbe /work/job/caseprobe
chown 65532:65532 /work/job
`

// SourceScript is fixed native, offline build policy. The controller supplies
// only a decimal job count, a read-only authenticated source snapshot and one
// fresh owned volume. It must independently enforce UID/GID 65532, a read-only
// root, no network/devices/capabilities and bounded execution/resources.
// Development outputs and their in-band report are not installation authority.
const SourceScript = `#!/bin/bash
set -euo pipefail
export PATH=/usr/sbin:/usr/bin:/sbin:/bin
export LANG=C.UTF-8 LC_ALL=C.UTF-8 TZ=UTC
umask 077
test "$#" -eq 1
case "$1" in [1-8]) ;; *) echo 'jobs must be a decimal integer from 1 through 8' >&2; exit 1 ;; esac
JOBS="$1"
test "$(id -u)" = 65532
test "$(id -g)" = 65532
# Numeric --user alone does not make the identity usable by private D-Bus.
# Reject a stale or incorrectly provisioned image before touching the work job.
python3 - <<'LEXR_SENSOR_SOURCE_IDENTITY'
import grp
import pwd

user = pwd.getpwnam('lexr-sensor-build')
group = grp.getgrnam('lexr-sensor-build')
assert user == pwd.getpwuid(65532)
assert (user.pw_name, user.pw_passwd, user.pw_uid, user.pw_gid,
        user.pw_gecos, user.pw_dir, user.pw_shell) == (
    'lexr-sensor-build', 'x', 65532, 65532, 'Lexr sensor source builder',
    '/work/job/home', '/usr/sbin/nologin')
assert group == grp.getgrgid(65532)
assert (group.gr_name, group.gr_passwd, group.gr_gid, group.gr_mem) == (
    'lexr-sensor-build', 'x', 65532, [])
LEXR_SENSOR_SOURCE_IDENTITY
. /etc/os-release
test "$ID" = ubuntu
test "$VERSION_ID" = 26.04
test "$(dpkg --print-architecture)" = arm64
case "$(uname -m)" in aarch64|arm64) ;; *) exit 1 ;; esac
test "$(gcc-14 -dumpversion)" = 14
test "$(gcc-14 -dumpmachine)" = aarch64-linux-gnu
test "$(cat /work/job/CaseProbe)" = upper
test "$(cat /work/job/caseprobe)" = lower
test ! /work/job/CaseProbe -ef /work/job/caseprobe
for leaf in qmi-build ssc-build proxy-build providers proxy-root metadata export sensor-source-artifacts.tar; do
  test ! -e "/work/job/$leaf"
  test ! -L "/work/job/$leaf"
done
available_bytes="$(df --output=avail -B1 /work/job | tail -n1)"
available_bytes="${available_bytes#"${available_bytes%%[![:space:]]*}"}"
available_bytes="${available_bytes%"${available_bytes##*[![:space:]]}"}"
case "$available_bytes" in ''|*[!0-9]*) echo 'invalid source-volume free-space result' >&2; exit 1 ;; esac
if ! test "$available_bytes" -ge 6442450944; then
  printf 'source volume requires at least 6442450944 available bytes (6 GiB); observed %s\n' "$available_bytes" >&2
  exit 1
fi
# This is a conservative entry gate, not a reservation or completion guarantee.
mkdir -m 0700 /work/job/providers /work/job/metadata
printf '%s\n' "$available_bytes" >/work/job/metadata/free-before.txt

SENSOR_SOURCES=/source/userspace/sensors
SENSOR_STAGE=/work/job/providers
export HOME=/work/job/home
export XDG_CACHE_HOME=/work/job/cache
export TMPDIR=/work/job/tmp
export CC=gcc-14 CFLAGS= CPPFLAGS= CXXFLAGS= LDFLAGS=
export PKG_CONFIG_PATH= PKG_CONFIG_SYSROOT_DIR=
export PKG_CONFIG_LIBDIR="$SENSOR_STAGE/lib/pkgconfig:/usr/lib/aarch64-linux-gnu/pkgconfig:/usr/lib/pkgconfig:/usr/share/pkgconfig"
export LD_LIBRARY_PATH="$SENSOR_STAGE/lib"
export LD_BIND_NOW=1 PYTHONDONTWRITEBYTECODE=1
cd /work/job

meson setup /work/job/qmi-build "$SENSOR_SOURCES/libqmi/source" \
  --prefix="$SENSOR_STAGE" --libdir=lib --libexecdir=libexec \
  --wrap-mode=nodownload -Dpkgconfig.relocatable=true -Ddefault_library=both \
  -Dcollection=full -Dqrtr=true -Dmbim_qmux=false -Dfirmware_update=false \
  -Dudev=false -Dintrospection=false -Dman=false -Dgtk_doc=false \
  -Dbash_completion=false -Dmm_runtime_check=false -Drmnet=false
meson introspect --dependencies /work/job/qmi-build >/work/job/metadata/qmi-dependencies.json
meson compile -C /work/job/qmi-build -j "$JOBS" -v
meson install -C /work/job/qmi-build --no-rebuild
test "$(pkg-config --variable=pcfiledir qmi-glib)" = "$SENSOR_STAGE/lib/pkgconfig"
test "$(pkg-config --modversion qmi-glib)" = 1.39.1

meson setup /work/job/ssc-build "$SENSOR_SOURCES/libssc/source" \
  --prefix="$SENSOR_STAGE" --libdir=lib --libexecdir=libexec \
  --wrap-mode=nodownload -Dpkgconfig.relocatable=true \
  -Dintrospection=disabled -Dvapi=disabled -Dbuild-tests=false \
  -Dc_std=gnu11 -Dwarning_level=2 -Dwerror=true -Dbuildtype=custom -Ddebug=true \
  -Db_lundef=true -Doptimization=2 -Db_sanitize=none \
  '-Dc_args=["-DQMI_DISABLE_DEPRECATED","-fPIC"]' -Dstrip=false
meson introspect --dependencies /work/job/ssc-build >/work/job/metadata/ssc-dependencies.json
meson compile -C /work/job/ssc-build -j "$JOBS" -v
meson install -C /work/job/ssc-build --no-rebuild
test "$(pkg-config --variable=pcfiledir libssc)" = "$SENSOR_STAGE/lib/pkgconfig"
test "$(pkg-config --modversion libssc)" = 0.4.7
policy_dir="$(pkg-config --variable=policydir polkit-gobject-1)"
printf 'polkit policy directory: %s\n' "$policy_dir"
test "$policy_dir" = /usr/share/polkit-1/actions

meson setup /work/job/proxy-build "$SENSOR_SOURCES/iio-sensor-proxy/source" \
  --prefix=/usr --libexecdir=libexec --wrap-mode=nodownload \
  -Dgtk-tests=false -Dgtk_doc=false -Dssc-support=enabled -Dtests=true \
  -Dudevrulesdir=/usr/lib/udev/rules.d \
  -Dsystemdsystemunitdir=/usr/lib/systemd/system
meson introspect --dependencies /work/job/proxy-build >/work/job/metadata/proxy-dependencies.json
meson introspect --installed /work/job/proxy-build >/work/job/metadata/proxy-installed.json
# Even a private prefix would not contain the system polkit policy path.
# Validate all logical destinations before compiling or staging the proxy.
python3 - <<'LEXR_PROXY_PLAN'
import json
from pathlib import Path
plan = json.loads(Path('/work/job/metadata/proxy-installed.json').read_text())
expected = {
    '/usr/bin/monitor-sensor',
    '/usr/lib/systemd/system/iio-sensor-proxy.service',
    '/usr/lib/udev/rules.d/80-iio-sensor-proxy.rules',
    '/usr/libexec/iio-sensor-proxy',
    '/usr/share/dbus-1/system.d/net.hadess.SensorProxy.conf',
    '/usr/share/polkit-1/actions/net.hadess.SensorProxy.policy',
}
assert isinstance(plan, dict) and len(plan) == 6
assert all(isinstance(value, str) for value in plan.values())
assert set(plan.values()) == expected
config = Path('/work/job/proxy-build/src/config.h').read_text()
assert '#define HAS_LIBSSC "1"' in config
print('Verified six private-DESTDIR proxy destinations before compilation.', flush=True)
LEXR_PROXY_PLAN
meson compile -C /work/job/proxy-build -j "$JOBS" -v
meson test -C /work/job/proxy-build --no-rebuild --num-processes=1 \
  --print-errorlogs --logbase testlog \
  test-mount-matrix test-orientation test-ssc-drivers test-iio-buffer \
  test-iio-acquisition test-iio-sysfs-write test-sensor-selection
DESTDIR=/work/job/proxy-root meson install -C /work/job/proxy-build --no-rebuild
timeout --signal=TERM --kill-after=5s 10s "$SENSOR_STAGE/bin/ssccli" --help
cli_version="$(timeout --signal=TERM --kill-after=5s 10s "$SENSOR_STAGE/bin/ssccli" --version)"
printf '%s\n' "$cli_version"
test "$cli_version" = 'libssc version 0.4.7'

python3 - "$JOBS" <<'LEXR_SENSOR_EXPORT'
import hashlib
import io
import json
import os
from pathlib import Path
import posixpath
import re
import stat
import subprocess
import sys
import tarfile

JOB = Path('/work/job')
PREFIX = JOB / 'providers'
SOURCE = Path('/source/userspace/sensors')
EXPORT = JOB / 'export'
REPORT = 'build-report.json'
ARCHIVE = JOB / 'sensor-source-artifacts.tar'
MAX_ARCHIVE = 134217728
# This closed roster is compiled recipe policy, never loaded from a manifest.
RULES = json.loads(r'''
{
  "libqmi-glib.so.5.12.0": {"maximum":67108864,"mode":493,"license":""},
  "libssc.so.2": {"maximum":16777216,"mode":493,"license":""},
  "ssccli": {"maximum":8388608,"mode":493,"license":""},
  "iio-sensor-proxy": {"maximum":16777216,"mode":493,"license":""},
  "monitor-sensor": {"maximum":8388608,"mode":493,"license":""},
  "COPYING.libqmi": {"maximum":18092,"mode":420,"license":"8177f97513213526df2cf6184d8ff986c675afb514d4e68a404010521b880643"},
  "COPYING.LIB.libqmi": {"maximum":26530,"mode":420,"license":"dc626520dcd53a22f727af3ee42c770e56c97a64fe3adb063799d8ab032fe551"},
  "LICENSE.libssc": {"maximum":35149,"mode":420,"license":"3972dc9744f6499f0f9b2dbf76696f2ae7ad8af9b23dde66d6af86c9dfb36986"},
  "COPYING.iio-sensor-proxy": {"maximum":35147,"mode":420,"license":"8ceb4b9ee5adedde47b31e975c1d90c73ad27b6b165a1dcd80c7c545eb65b903"},
  "testlog.json": {"maximum":8388608,"mode":420,"license":""},
  "testlog.txt": {"maximum":8388608,"mode":420,"license":""},
  "qmi-dependencies.json": {"maximum":2097152,"mode":420,"license":""},
  "ssc-dependencies.json": {"maximum":2097152,"mode":420,"license":""},
  "proxy-dependencies.json": {"maximum":2097152,"mode":420,"license":""},
  "loader-evidence.txt": {"maximum":2097152,"mode":420,"license":""},
  "build-report.json": {"maximum":1048576,"mode":420,"license":""}
}
''')
LINKS = json.loads(r'''[{"name":"libqmi-glib.so","target":"libqmi-glib.so.5"},{"name":"libqmi-glib.so.5","target":"libqmi-glib.so.5.12.0"},{"name":"libssc.so","target":"libssc.so.2"}]''')
FIXTURES = json.loads(r'''["test-mount-matrix","test-orientation","test-ssc-drivers","test-iio-buffer","test-iio-acquisition","test-iio-sysfs-write","test-sensor-selection"]''')
PROXY_TARGETS = json.loads(r'''["/usr/bin/monitor-sensor","/usr/lib/systemd/system/iio-sensor-proxy.service","/usr/lib/udev/rules.d/80-iio-sensor-proxy.rules","/usr/libexec/iio-sensor-proxy","/usr/share/dbus-1/system.d/net.hadess.SensorProxy.conf","/usr/share/polkit-1/actions/net.hadess.SensorProxy.policy"]''')
ELFS = {
    'libqmi-glib.so.5.12.0': PREFIX / 'lib/libqmi-glib.so.5.12.0',
    'libssc.so.2': PREFIX / 'lib/libssc.so.2',
    'ssccli': PREFIX / 'bin/ssccli',
    'iio-sensor-proxy': JOB / 'proxy-root/usr/libexec/iio-sensor-proxy',
    'monitor-sensor': JOB / 'proxy-root/usr/bin/monitor-sensor',
}
# LEXR_SENSOR_LOADER_POLICY_BEGIN
QMI_MAP = {'libqmi-glib.so.5': str(PREFIX / 'lib/libqmi-glib.so.5')}
BOTH_MAP = {**QMI_MAP, 'libssc.so.2': str(PREFIX / 'lib/libssc.so.2')}
EXPECTED_LOADERS = {
    'libqmi-glib.so.5.12.0': {}, 'libssc.so.2': QMI_MAP,
    'ssccli': BOTH_MAP, 'iio-sensor-proxy': BOTH_MAP, 'monitor-sensor': {},
}
# LEXR_SENSOR_LOADER_POLICY_END

def regular_bytes(path, maximum):
    assert path.is_absolute() and path.resolve(strict=True) == path
    fd = os.open(path, os.O_RDONLY | os.O_CLOEXEC | os.O_NOFOLLOW | os.O_NONBLOCK)
    with os.fdopen(fd, 'rb') as stream:
        before = os.fstat(stream.fileno())
        assert stat.S_ISREG(before.st_mode) and not before.st_mode & 0o7000
        assert 0 < before.st_size <= maximum
        data = stream.read(maximum + 1)
        after = os.fstat(stream.fileno())
        current = path.lstat()
        assert len(data) == before.st_size == after.st_size <= maximum
        assert (before.st_dev, before.st_ino) == (after.st_dev, after.st_ino) == (current.st_dev, current.st_ino)
        assert stat.S_ISREG(current.st_mode)
    return data

# LEXR_SENSOR_LOADER_OBSERVER_BEGIN
def observe(argv, *, env=None):
    result = subprocess.run(argv, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                            text=True, timeout=30, check=False, env=env)
    sys.stdout.write(result.stdout)
    sys.stderr.write(result.stderr)
    assert result.returncode == 0, argv
    return result.stdout

def observe_loader(path):
    # Explicit non-secure loader invocation: value 1 initialises ordinary trace
    # mode before --list. Copy, never mutate, the environment (including the
    # private LD_LIBRARY_PATH); never skip or rewrite an unnamed output row.
    environment = os.environ.copy()
    environment['LD_TRACE_LOADED_OBJECTS'] = '1'
    return observe(['/lib/ld-linux-aarch64.so.1', '--list', str(path)], env=environment)
# LEXR_SENSOR_LOADER_OBSERVER_END

# LEXR_SENSOR_LOADER_PARSER_BEGIN
def parse_loader_listing(raw, expected):
    assert raw.strip() and not any(char in raw for char in '\x00\r')
    providers, seen = {}, set()
    for row in raw.strip().split('\n'):
        row = row.strip()
        assert row
        selected = re.fullmatch(r'([^\s/]+) => (/[^\s]+) \(0x[0-9a-fA-F]+\)', row)
        if selected:
            soname, origin = selected.groups()
            assert re.fullmatch(r'[A-Za-z0-9._+-]{1,128}', soname) and soname not in ('.', '..')
            assert soname != 'ld-linux-aarch64.so.1'
            assert soname not in seen
            assert not origin.startswith('//') and posixpath.normpath(origin) == origin
            seen.add(soname)
            if soname.startswith(('libqmi-glib.so', 'libssc.so')):
                assert soname in BOTH_MAP
                assert origin == BOTH_MAP[soname]
                providers[soname] = origin
            else:
                assert origin.startswith(('/lib/', '/usr/lib/'))
        elif re.fullmatch(r'linux-vdso.so.1 \(0x[0-9a-fA-F]+\)', row):
            assert 'linux-vdso.so.1' not in seen
            seen.add('linux-vdso.so.1')
        else:
            assert re.fullmatch(r'/(?:lib|usr/lib)/[^\s]+ \(0x[0-9a-fA-F]+\)', row), row
            assert row == '/lib/ld-linux-aarch64.so.1 ' + row[row.index('('):]
            assert '/lib/ld-linux-aarch64.so.1' not in seen
            seen.add('/lib/ld-linux-aarch64.so.1')
    assert '/lib/ld-linux-aarch64.so.1' in seen
    assert providers == expected
    return providers
# LEXR_SENSOR_LOADER_PARSER_END

def write_product(name, data):
    rule = RULES[name]
    assert 0 < len(data) <= rule['maximum']
    if rule['license']:
        assert len(data) == rule['maximum']
        assert hashlib.sha256(data).hexdigest() == rule['license']
    with (EXPORT / name).open('xb') as stream:
        stream.write(data)
    os.chmod(EXPORT / name, rule['mode'])
    assert stat.S_IMODE((EXPORT / name).stat().st_mode) == rule['mode']

EXPORT.mkdir(mode=0o700)
for link in LINKS:
    installed = PREFIX / 'lib' / link['name']
    assert installed.parent.resolve(strict=True) == installed.parent
    assert stat.S_ISLNK(installed.lstat().st_mode)
    assert os.readlink(installed) == link['target']
    expected = PREFIX / 'lib' / ('libssc.so.2' if link['name'] == 'libssc.so' else 'libqmi-glib.so.5.12.0')
    assert installed.resolve(strict=True) == expected

# Confirm the staged installation is exactly six files, without activation.
destdir = JOB / 'proxy-root'
actual_targets = []
for parent, dirs, files in os.walk(destdir, followlinks=False):
    for name in dirs:
        assert not (Path(parent) / name).is_symlink()
    for name in files:
        member = Path(parent) / name
        assert stat.S_ISREG(member.lstat().st_mode)
        actual_targets.append('/' + member.relative_to(destdir).as_posix())
assert sorted(actual_targets) == PROXY_TARGETS

raw_loaders, loaders = {}, []
for name, path in ELFS.items():
    data = regular_bytes(path, RULES[name]['maximum'])
    header = observe(['/usr/bin/readelf', '-hW', str(path)])
    assert re.search(r'^\s*Class:\s+ELF64\s*$', header, re.M)
    assert re.search(r"^\s*Data:\s+2's complement, little endian\s*$", header, re.M)
    assert re.search(r'^\s*Machine:\s+AArch64\s*$', header, re.M)
    dynamic = observe(['/usr/bin/readelf', '-dW', str(path)])
    assert not re.search(r'\((?:RPATH|RUNPATH)\)', dynamic)
    raw = observe_loader(path)
    raw_loaders[name] = raw
    providers = parse_loader_listing(raw, EXPECTED_LOADERS[name])
    loaders.append({'artifact': name, 'providers': providers})
    write_product(name, data)

provider_records = []
for name, version, library, artifact in [
    ('qmi-glib', '1.39.1', PREFIX / 'lib/libqmi-glib.so.5.12.0', 'libqmi-glib.so.5.12.0'),
    ('libssc', '0.4.7', PREFIX / 'lib/libssc.so.2', 'libssc.so.2'),
]:
    pcdir = observe(['/usr/bin/pkg-config', '--variable=pcfiledir', name]).strip()
    actual_version = observe(['/usr/bin/pkg-config', '--modversion', name]).strip()
    assert pcdir == str(PREFIX / 'lib/pkgconfig') and actual_version == version
    provider_records.append({'name': name, 'version': version, 'pcfile_directory': pcdir,
                             'library': str(library), 'sha256': hashlib.sha256((EXPORT / artifact).read_bytes()).hexdigest()})

def validate_private_dependency(entry, include_ssc):
    includes, libraries = set(), set()
    private_libdir = False
    for field in ('compile_args', 'link_args'):
        assert isinstance(entry[field], list)
        assert all(isinstance(value, str) and not any(char in value for char in '\x00\r\n')
                   for value in entry[field])
    for argument in entry['compile_args']:
        if not argument.startswith('-I'):
            continue
        directory = posixpath.normpath(argument[2:])
        if 'libqmi-glib' in directory or directory.endswith('/libssc'):
            assert directory in (str(PREFIX / 'include/libqmi-glib'), str(PREFIX / 'include/libssc'))
            includes.add(directory)
    for argument in entry['link_args']:
        if argument.startswith('-L') and posixpath.normpath(argument[2:]) == str(PREFIX / 'lib'):
            private_libdir = True
        for library in ('qmi-glib', 'ssc'):
            if argument == '-l' + library:
                libraries.add(library)
            elif 'lib' + library + '.so' in argument:
                assert argument.startswith(str(PREFIX / 'lib') + '/')
                assert posixpath.dirname(posixpath.normpath(argument)) == str(PREFIX / 'lib')
                libraries.add(library + '-absolute')
    for library, header in [('qmi-glib', 'libqmi-glib')] + ([('ssc', 'libssc')] if include_ssc else []):
        assert str(PREFIX / 'include' / header) in includes
        assert library + '-absolute' in libraries or (private_libdir and library in libraries)

for label in ('qmi', 'ssc', 'proxy'):
    data = regular_bytes(JOB / 'metadata' / (label + '-dependencies.json'), RULES[label + '-dependencies.json']['maximum'])
    assert b'\x00' not in data and data.endswith(b'\n')
    dependencies = json.loads(data)
    assert isinstance(dependencies, list) and 0 < len(dependencies) <= 512
    assert all(isinstance(item, dict) for item in dependencies)
    names = [item['name'] for item in dependencies]
    assert all(isinstance(name, str) and name for name in names) and len(names) == len(set(names))
    selected = set()
    for dependency in dependencies:
        assert isinstance(dependency['version'], str) and dependency['version']
        assert not any(char in dependency['version'] for char in '\x00\r\n\t ')
        if dependency['name'] not in ('qmi-glib', 'libssc'):
            continue
        assert label != 'qmi'
        selected.add(dependency['name'])
        assert dependency['version'] == {'qmi-glib': '1.39.1', 'libssc': '0.4.7'}[dependency['name']]
        validate_private_dependency(dependency, dependency['name'] == 'libssc')
    assert label != 'ssc' or 'qmi-glib' in selected
    assert label != 'proxy' or 'libssc' in selected
    write_product(label + '-dependencies.json', data)
for name in ('testlog.json', 'testlog.txt'):
    data = regular_bytes(JOB / 'proxy-build/meson-logs' / name, RULES[name]['maximum'])
    assert b'\x00' not in data and data.endswith(b'\n')
    write_product(name, data)
records = [json.loads(row) for row in (EXPORT / 'testlog.json').read_bytes().splitlines() if row]
assert len(records) == 7
tested = []
for record in records:
    name = record['name']
    if name.startswith('iio-sensor-proxy:'):
        name = name[len('iio-sensor-proxy:'):]
    assert name in FIXTURES and name not in tested
    assert record['result'] == 'OK' and type(record['returncode']) is int and record['returncode'] == 0
    assert isinstance(record['stdout'], str) and record['stdout'].strip()
    tested.append(name)
assert set(tested) == set(FIXTURES)
write_product('loader-evidence.txt', (json.dumps(raw_loaders, sort_keys=True, indent=2) + '\n').encode())
for output, source in [
    ('COPYING.libqmi', SOURCE / 'libqmi/source/COPYING'),
    ('COPYING.LIB.libqmi', SOURCE / 'libqmi/source/COPYING.LIB'),
    ('LICENSE.libssc', SOURCE / 'libssc/source/LICENSE'),
    ('COPYING.iio-sensor-proxy', SOURCE / 'iio-sensor-proxy/source/COPYING'),
]:
    write_product(output, regular_bytes(source, RULES[output]['maximum']))
for link in LINKS:
    os.symlink(link['target'], EXPORT / link['name'])

products, payloads = [], {}
for name in sorted(RULES):
    if name == REPORT:
        continue
    data = regular_bytes(EXPORT / name, RULES[name]['maximum'])
    mode = stat.S_IMODE((EXPORT / name).stat().st_mode)
    assert mode == RULES[name]['mode']
    payloads[name] = data
    products.append({'name': name, 'bytes': len(data), 'sha256': hashlib.sha256(data).hexdigest(), 'mode': mode})
free_before = regular_bytes(JOB / 'metadata/free-before.txt', 32).decode().strip()
assert re.fullmatch(r'[0-9]+', free_before) and int(free_before) >= 6442450944
memory_peak = None
try:
    memory_text = Path('/sys/fs/cgroup/memory.peak').read_text().strip()
except (FileNotFoundError, PermissionError):
    pass
else:
    assert re.fullmatch(r'[0-9]+', memory_text)
    memory_peak = int(memory_text)
    assert 0 < memory_peak < 2**63
# Apparent job-tree bytes at this point, not allocated blocks or a build peak.
# Measure after payload copying and before creating either report or archive.
du = observe(['/usr/bin/du', '--bytes', '--summarize', '/work/job']).rstrip('\n')
du_match = re.fullmatch(r'([0-9]+)\t/work/job', du)
assert du_match and int(du_match[1]) < 2**63
print('job_apparent_before_archive_bytes is measured before report/archive creation; not allocated bytes or peak usage.', flush=True)
report = {
    'schema': 'lexr-sensors-source-build-v1',
    'source_commit': '649cc875d08c04ce33bc8afc2a0ff7a22ce362e0',
    'jobs': int(sys.argv[1]), 'products': products, 'links': LINKS,
    'providers': provider_records, 'loaders': loaders, 'fixtures': FIXTURES,
    'proxy_install_targets': PROXY_TARGETS, 'volume_free_before_bytes': int(free_before),
    'job_apparent_before_archive_bytes': int(du_match[1]), 'memory_peak_bytes': memory_peak,
}
report_bytes = (json.dumps(report, sort_keys=True, indent=2) + '\n').encode()
write_product(REPORT, report_bytes)
payloads[REPORT] = report_bytes
assert set(entry.name for entry in EXPORT.iterdir()) == set(RULES) | {link['name'] for link in LINKS}
# USTAR records are flat, fixed and independently checked by the host. This
# report does not supply its own member authority or hash itself.
expected_size = sum(512 + ((len(data) + 511) // 512) * 512 for data in payloads.values()) + 512 * len(LINKS) + 1024
expected_size = ((expected_size + 10239) // 10240) * 10240
assert expected_size <= MAX_ARCHIVE
with tarfile.open(ARCHIVE, mode='x', format=tarfile.USTAR_FORMAT) as archive:
    for name in sorted(payloads):
        info = tarfile.TarInfo(name)
        info.type = tarfile.REGTYPE
        info.size = len(payloads[name])
        info.mode = RULES[name]['mode']
        info.uid = info.gid = info.mtime = 0
        info.uname = info.gname = ''
        archive.addfile(info, io.BytesIO(payloads[name]))
    for link in LINKS:
        info = tarfile.TarInfo(link['name'])
        info.type = tarfile.SYMTYPE
        info.linkname = link['target']
        info.mode = 0o777
        info.uid = info.gid = info.mtime = 0
        info.uname = info.gname = ''
        archive.addfile(info)
assert ARCHIVE.stat().st_size == expected_size <= MAX_ARCHIVE
os.chmod(ARCHIVE, 0o444)
# The stopped-container copy must not need precision-timestamp PAX records.
# This narrows file metadata, not the host's closed outer-tar acceptance policy.
os.utime(ARCHIVE, ns=(0, 0))
print('Created bounded development-artifact USTAR; not an installable or hardware-qualified package.', flush=True)
LEXR_SENSOR_EXPORT
`

// InitRecipeSHA256 identifies the exact volume-initialization command bytes.
func InitRecipeSHA256() string {
	digest := sha256.Sum256([]byte(InitScript))
	return hex.EncodeToString(digest[:])
}

// SourceRecipeSHA256 identifies the exact offline build command bytes.
func SourceRecipeSHA256() string {
	digest := sha256.Sum256([]byte(SourceScript))
	return hex.EncodeToString(digest[:])
}
