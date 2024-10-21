// Test PikPak filesystem interface
package pikpak_test

import (
	"testing"

	"github.com/morecup/rclone/backend/pikpak"
	"github.com/morecup/rclone/fstest/fstests"
)

// TestIntegration runs integration tests against the remote
func TestIntegration(t *testing.T) {
	fstests.Run(t, &fstests.Opt{
		RemoteName: "TestPikPak:",
		NilObject:  (*pikpak.Object)(nil),
	})
}
