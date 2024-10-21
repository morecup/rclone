// Test GoogleCloudStorage filesystem interface

package googlecloudstorage_test

import (
	"testing"

	"github.com/morecup/rclone/backend/googlecloudstorage"
	"github.com/morecup/rclone/fstest"
	"github.com/morecup/rclone/fstest/fstests"
)

// TestIntegration runs integration tests against the remote
func TestIntegration(t *testing.T) {
	fstests.Run(t, &fstests.Opt{
		RemoteName: "TestGoogleCloudStorage:",
		NilObject:  (*googlecloudstorage.Object)(nil),
	})
}

func TestIntegration2(t *testing.T) {
	if *fstest.RemoteName != "" {
		t.Skip("Skipping as -remote set")
	}
	name := "TestGoogleCloudStorage"
	fstests.Run(t, &fstests.Opt{
		RemoteName: name + ":",
		NilObject:  (*googlecloudstorage.Object)(nil),
		ExtraConfig: []fstests.ExtraConfigItem{
			{Name: name, Key: "directory_markers", Value: "true"},
		},
	})
}
