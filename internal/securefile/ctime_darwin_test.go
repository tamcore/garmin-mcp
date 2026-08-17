//go:build darwin

package securefile

import "syscall"

// statCtime returns the inode change time, the only timestamp a chmod(2) call
// updates even when the requested mode already matches. It is the seam a test
// uses to observe whether a chmod happened at all, not merely whether the mode
// ended up correct.
func statCtime(path string) (sec, nsec int64, err error) {
	var st syscall.Stat_t
	if statErr := syscall.Stat(path, &st); statErr != nil {
		return 0, 0, statErr
	}
	return st.Ctimespec.Sec, st.Ctimespec.Nsec, nil
}
