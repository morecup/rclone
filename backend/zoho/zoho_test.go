// Test Zoho filesystem interface
package zoho_test

import (
	"testing"

	"github.com/morecup/rclone/backend/zoho"
	"github.com/morecup/rclone/fstest/fstests"
)

// TestIntegration runs integration tests against the remote
func TestIntegration(t *testing.T) {
	fstests.Run(t, &fstests.Opt{
		RemoteName:      "TestZoho:",
		SkipInvalidUTF8: true,
		NilObject:       (*zoho.Object)(nil),
	})
}
