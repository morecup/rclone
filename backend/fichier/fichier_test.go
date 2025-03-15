// Test 1Fichier filesystem interface
package fichier

import (
	"testing"

	"github.com/morecup/rclone/fstest/fstests"
)

// TestIntegration runs integration tests against the remote
func TestIntegration(t *testing.T) {
	fstests.Run(t, &fstests.Opt{
		RemoteName: "TestFichier:",
	})
}
