package archlinux

import _ "embed"

// prepareRootScript installs the locked package set into a pristine rootfs.
//
//go:embed scripts/prepare-root.sh
var prepareRootScript string

// liveSessionScript configures only the disposable live account and services.
//
//go:embed scripts/live-session.sh
var liveSessionScript string

// buildBootScript creates native initramfs and GRUB payloads from the chosen ABI.
//
//go:embed scripts/build-boot.sh
var buildBootScript string

// gettingStarted is retained with the live companion and available from the terminal setup menu.
//
//go:embed LEXR_GETTING_STARTED.txt
var gettingStarted string

// setupScript presents networking, customisation and the explicitly confirmed installer.
//
//go:embed scripts/setup.sh
var setupScript string

// installerGuided retains Archinstall menus with Surface-specific boundaries.
//
//go:embed installer/guided.py
var installerGuided string

// installerTarget installs verified kernel and boot assets into the selected root.
//
//go:embed installer/target.py
var installerTarget string

// installerLauncher invokes the bundled, version-bound Archinstall integration.
const installerLauncher = "#!/bin/sh\nexec /usr/bin/python /usr/share/lexr/archinstall/guided.py \"$@\"\n"
