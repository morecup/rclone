// Run the more functional vfstest package on the vfs

package vfs_test

import (
	"testing"

	_ "github.com/morecup/rclone/backend/all" // import all the backends
	"github.com/morecup/rclone/cmd/mountlib"
	"github.com/morecup/rclone/fstest"
	"github.com/morecup/rclone/vfs"
	"github.com/morecup/rclone/vfs/vfscommon"
	"github.com/morecup/rclone/vfs/vfstest"
)

// TestFunctional runs more functional tests all the tests against all the
// VFS cache modes
func TestFunctional(t *testing.T) {
	if *fstest.RemoteName != "" {
		t.Skip("Skip on non local")
	}
	vfstest.RunTests(t, true, vfscommon.CacheModeOff, true, func(VFS *vfs.VFS, mountpoint string, opt *mountlib.Options) (unmountResult <-chan error, unmount func() error, err error) {
		unmountResultChan := make(chan (error), 1)
		unmount = func() error {
			unmountResultChan <- nil
			return nil
		}
		return unmountResultChan, unmount, nil
	})
}
