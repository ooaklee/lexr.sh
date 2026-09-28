//go:build linux

package bootidentity

import "golang.org/x/sys/unix"

// statDeviceFn returns the device major/minor of a block-device path. It is
// a variable so portable tests can inject fixture implementations.
var statDeviceFn = func(path string) (int, int, error) {
	var st unix.Stat_t
	if err := unix.Stat(path, &st); err != nil {
		return 0, 0, err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFBLK {
		return 0, 0, errNoDeviceIdentity
	}
	return int(unix.Major(st.Rdev)), int(unix.Minor(st.Rdev)), nil
}
