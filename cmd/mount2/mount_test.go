//go:build linux

package mount2

import (
	"testing"

	"github.com/morecup/rclone/vfs/vfscommon"
	"github.com/morecup/rclone/vfs/vfstest"
)

func TestMount(t *testing.T) {
	vfstest.RunTests(t, false, vfscommon.CacheModeOff, true, mount)
}
