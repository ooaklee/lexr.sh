#!/usr/bin/python3
"""Add exact-kernel device trees to Debian's retained native GRUB generator.

Debian's original file remains owned by grub-common at its diverted path.
The generic Lexr kernel package owns device selection and DTB staging.
"""
import os
import re
import stat
import subprocess
import sys

NATIVE_GENERATOR = "/usr/share/lexr/debian-grub/10_linux"
MAXIMUM_BYTES = 256 * 1024


def transform_generator(source):
    """Retain native normal/recovery and root-device handling at the emission site."""
    if not source or len(source.encode("utf-8")) > MAXIMUM_BYTES:
        raise ValueError("Debian GRUB generator is empty or oversized")
    code = "\n".join(line for line in source.splitlines() if not line.lstrip().startswith("#"))
    if not source.startswith("#! /bin/sh\n") or code.count("linux_entry ()\n") != 1:
        raise ValueError("Debian GRUB generator structure has changed")
    if re.search(r"\bdevicetree\b", code):
        raise ValueError("Debian GRUB now handles device trees; review Lexr integration")
    anchor = '  sed "s/^/$submenu_indentation/" << EOF\n\tlinux\t${rel_dirname}/${basename} root=${linux_root_device_thisversion} ro ${args}\nEOF\n'
    if source.count(anchor) != 1:
        raise ValueError("Debian GRUB kernel emission has changed")
    function_start = source.index("linux_entry ()\n")
    emission = source.index(anchor)
    function_end = source.index('\nmachine=`uname -m`', function_start)
    if not function_start < emission < function_end:
        raise ValueError("Debian GRUB kernel emission escaped linux_entry")
    if source[function_start:emission].count('  version="$2"\n') != 1:
        raise ValueError("Debian GRUB kernel version binding has changed")
    addition = '''  # Lexr: only an exact-ABI regular DTB belongs to this kernel entry.
  case "${version}" in
    ''|*[!a-zA-Z0-9.+_-]*) echo "Unsafe kernel version for Lexr device tree" >&2; exit 1 ;;
  esac
  if test -f "${dirname}/dtb-${version}" && ! test -L "${dirname}/dtb-${version}"; then
    sed "s/^/$submenu_indentation/" << EOF
\tdevicetree ${rel_dirname}/dtb-${version}
EOF
  elif test -d "/var/lib/lexr/kernel-boot/${version}"; then
    echo "Missing exact-ABI Lexr device tree for ${version}; refusing an incomplete boot entry" >&2
    exit 1
  fi
'''
    return source.replace(anchor, anchor + addition, 1)


def main():
    """Read a protected native script before adapting it in memory."""
    try:
        before = os.lstat(NATIVE_GENERATOR)
        if not stat.S_ISREG(before.st_mode) or before.st_uid != 0 or before.st_mode & 0o022:
            raise ValueError("native GRUB generator must be a protected root-owned regular file")
        with open(NATIVE_GENERATOR, "rb") as stream:
            opened = os.fstat(stream.fileno())
            if (opened.st_dev, opened.st_ino) != (before.st_dev, before.st_ino):
                raise ValueError("native GRUB generator changed while opening it")
            contents = stream.read(MAXIMUM_BYTES + 1)
        patched = transform_generator(contents.decode("utf-8"))
        return subprocess.run(["/bin/sh"], input=patched.encode("utf-8")).returncode
    except (OSError, UnicodeError, ValueError) as error:
        print("Lexr Debian GRUB support: " + str(error), file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
