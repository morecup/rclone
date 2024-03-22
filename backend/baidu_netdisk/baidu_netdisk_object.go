package baidu_netdisk

import (
	"context"
	"errors"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/hash"
	"io"
	"net/http"
	"time"
)

// Object describes a OneDrive object
//
// Will definitely have info but maybe not meta
type Object struct {
	fs            *Fs       // what this object is part of
	remote        string    // The remote path  absolute path
	hasMetaData   bool      // whether info below has been set
	isOneNoteFile bool      // Whether the object is a OneNote file
	size          int64     // size of the object
	modTime       time.Time // modification time of the object
	id            string    // ID of the object
	hash          string    // Hash of the content, usually QuickXorHash but set as hash_type
	mimeType      string    // Content-Type of object from server (may not be as uploaded)
}

// ------------------------------------------------------------

// Fs returns the parent Fs
func (o *Object) Fs() fs.Info {
	return o.fs
}

// Return a string version
func (o *Object) String() string {
	if o == nil {
		return "<nil>"
	}
	return o.remote
}

// Remote returns the remote path
func (o *Object) Remote() string {
	return o.remote
}

// Hash returns the SHA-1 of an object returning a lowercase hex string
func (o *Object) Hash(ctx context.Context, t hash.Type) (string, error) {
	if t == o.fs.hashType {
		return o.hash, nil
	}
	return "", hash.ErrUnsupported
}

// Size returns the size of an object in bytes
func (o *Object) Size() int64 {
	return o.size
}

// ModTime returns the modification time of the object
//
// It attempts to read the objects mtime and if that isn't present the
// LastModified returned in the http headers
func (o *Object) ModTime(ctx context.Context) time.Time {
	return o.modTime
}

// SetModTime sets the modification time of the local fs object
func (o *Object) SetModTime(ctx context.Context, modTime time.Time) error {
	o.modTime = modTime
	return nil
}

// Storable returns a boolean showing whether this object storable
func (o *Object) Storable() bool {
	return true
}

// Open an object for read
func (o *Object) Open(ctx context.Context, options ...fs.OpenOption) (in io.ReadCloser, err error) {
	if o.remote == "" {
		return nil, errors.New("can't download - no id")
	}
	if o.isOneNoteFile {
		return nil, errors.New("can't open a OneNote file")
	}
	fs.FixRangeOption(options, o.size)
	resp, err := o.fs.DownFileDisguiseBaiduClient(ctx, o.fs.ToAbsolutePath(o.remote), options)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusOK && resp.ContentLength > 0 && resp.Header.Get("Content-Range") == "" {
		//Overwrite size with actual size since size readings from Onedrive is unreliable.
		o.size = resp.ContentLength
	}
	return resp.Body, err
}

// Update the object with the contents of the io.Reader, modTime and size
//
// The new object may have been created if an error is returned
func (o *Object) Update(ctx context.Context, in io.Reader, src fs.ObjectInfo, options ...fs.OpenOption) (err error) {

	return nil
}

// Remove an object
func (o *Object) Remove(ctx context.Context) error {
	err := o.fs.DeleteDirOrFile(ctx, o.fs.ToAbsolutePath(o.remote))
	return err
}

// MimeType of an Object if known, "" otherwise
func (o *Object) MimeType(ctx context.Context) string {
	return o.mimeType
}

// ID returns the ID of the Object if known, or "" if not
func (o *Object) ID() string {
	return o.id
}
