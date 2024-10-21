// Test internetarchive filesystem interface
package internetarchive_test

import (
	"testing"

	"github.com/morecup/rclone/backend/internetarchive"
	"github.com/morecup/rclone/fstest/fstests"
)

// TestIntegration runs integration tests against the remote
func TestIntegration(t *testing.T) {
	fstests.Run(t, &fstests.Opt{
		RemoteName: "TestIA:lesmi-rclone-test/",
		NilObject:  (*internetarchive.Object)(nil),
	})
}
