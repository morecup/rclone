package db

import (
	"bytes"
	"context"
	"github.com/pkg/errors"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/hash"
	"gorm.io/gorm"
	"io"
	"time"
)

// Object describes a OneDrive object
//
// Will definitely have info but maybe not meta
type Object struct {
	fs       *Fs       // what this object is part of
	remote   string    // The remote path  absolute path,no object name
	size     int64     // size of the object
	modTime  time.Time // modification time of the object
	id       string    // ID of the object
	parentId string
	fileName string
}

// ------------------------------------------------------------
func NewObjectFromFileInfo(file *FileInfo, absolutePath string, f *Fs) *Object {
	return &Object{
		id:       file.Id,
		parentId: file.ParentId,
		remote:   absolutePath,
		modTime:  file.ModTime,
		size:     file.FileSize,
		fs:       f,
		fileName: file.Name,
	}
}

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

func (o *Object) Open(ctx context.Context, options ...fs.OpenOption) (in io.ReadCloser, err error) {
	fs.FixRangeOption(options, o.size)
	var fileInfo FileInfo
	result := o.fs.db.Select("content", "is_dir").First(&fileInfo, "id = ?", o.id)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, fs.ErrorObjectNotFound
		} else {
			return nil, errors.Wrapf(result.Error, "Error open object %s", o)
		}
	} else {
		if fileInfo.IsDir {
			return nil, fs.ErrorIsDir
		}
		var rangeStart int64 = 0
		var rangeEnd = o.Size() - 1
		for _, option := range options {
			if rangeOption, ok := option.(*fs.RangeOption); ok {
				rangeStart = rangeOption.Start
				if rangeOption.End != -1 {
					rangeEnd = min(rangeOption.End, rangeEnd)
				}
			}
		}
		reader := bytes.NewReader(fileInfo.Content[rangeStart:rangeEnd])
		return io.NopCloser(reader), nil
	}
}

// Update the object with the contents of the io.Reader, modTime and size
//
// The new object may have been created if an error is returned 可能本身这个object就不存在？？
func (o *Object) Update(ctx context.Context, in io.Reader, src fs.ObjectInfo, options ...fs.OpenOption) (err error) {
	allByte, err := io.ReadAll(in)
	if err != nil {
		return err
	}
	//如何文件本身就没有，会去创建，如果中间的文件夹不存在，也会自动创建
	fileInfo := FileInfo{
		Id:       o.id,
		Name:     o.fileName,
		FileSize: int64(len(allByte)),
		Content:  allByte,
		ModTime:  src.ModTime(ctx),
		IsDir:    false,
		ParentId: o.parentId,
	}
	result := o.fs.db.Model(&FileInfo{}).Where("id = ?", o.id).Updates(fileInfo)
	if result.Error != nil {
		return errors.Wrapf(result.Error, "Error update object %s", o)
	}
	return err
}

// Remove an object
func (o *Object) Remove(ctx context.Context) error {
	result := o.fs.db.Delete(&FileInfo{Id: o.id})
	if result.Error != nil {
		return errors.Wrapf(result.Error, "Error remove object %s", o)
	}
	return nil
}

// MimeType of an Object if known, "" otherwise 这个是用作存储mime类型，用于判断文件类型的
//func (o *Object) MimeType(ctx context.Context) string {
//	return ""
//}

// ID returns the ID of the Object if known, or "" if not
func (o *Object) ID() string {
	return o.id
}
