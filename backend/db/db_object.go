package db

import (
	"bytes"
	"context"
	"github.com/pkg/errors"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/hash"
	"github.com/rclone/rclone/lib/paths"
	"gorm.io/gorm"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// Object describes a OneDrive object
//
// Will definitely have info but maybe not meta
type Object struct {
	fs       *Fs       // what this object is part of
	remote   string    // The remote path  base root relativePath path,have file name
	size     int64     // size of the object
	modTime  time.Time // modification time of the object
	id       string    // ID of the object
	parentId string
	fileName string
}

// ------------------------------------------------------------
func NewObjectFromFileInfo(file *FileInfo, absolutePath string, f *Fs) *Object {
	if file.IsLink && f.opt.IsLinkFileMode {
		return &Object{
			id:       file.Id,
			parentId: file.ParentId,
			remote:   absolutePath + linkSuffix,
			modTime:  file.ModTime,
			size:     file.FileSize,
			fs:       f,
			fileName: file.Name + linkSuffix,
		}
	} else {
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
	//如果打开的软链接文件，需要特殊处理

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
		reader := bytes.NewReader(fileInfo.Content[rangeStart : rangeEnd+1])
		return io.NopCloser(reader), nil
	}
}

// ResolveDerivedPathFromRelative 根据aPath->bPath的相对路径推倒cPath->dPath的相对路径，返回dPath
func ResolveDerivedPathFromRelative(aPath, bPath, cPath string, isDirResolve bool) (string, error) {
	// 处理 A 路径：去除前缀并转换为linux格式
	aLinuxPath := convertWindowsPathToLinuxStyle(aPath)
	bLinuxPath := convertWindowsPathToLinuxStyle(bPath)

	if !isDirResolve {
		aLinuxPath = path.Dir(aLinuxPath)
	}

	// 计算 A 目录到 B 文件的相对路径（需统一为 Unix 分隔符以适配 C 路径）
	var relative string

	relative, err := filepath.Rel(aLinuxPath, bLinuxPath)

	if err != nil {
		return "", err
	}

	linuxRelative := filepath.ToSlash(relative)

	// 处理 C 路径：提取目录并应用相对路径
	if !isDirResolve {
		cPath = path.Dir(cPath)
	}
	dPath, err := paths.Join(cPath, linuxRelative)

	return dPath, err
}
func convertWindowsPathToLinuxStyle(windowsPath string) string {
	// 去除特殊的长路径前缀
	windowsPath = strings.TrimPrefix(windowsPath, `\\?\`)
	windowsPath = strings.TrimPrefix(windowsPath, `//?/`)
	windowsPath = strings.TrimPrefix(windowsPath, `/?/`)

	// 替换所有反斜杠为正斜杠
	linuxStylePath := strings.ReplaceAll(windowsPath, `\`, `/`)

	// 将驱动器标记D:替换为/D
	linuxStylePath = strings.Replace(linuxStylePath, `:`, ``, 1)

	// 可选：为驱动器添加前导斜杠
	if len(linuxStylePath) > 0 && linuxStylePath[1] == '/' {
		linuxStylePath = "/" + linuxStylePath
	}

	return linuxStylePath
}

// Update the object with the contents of the io.Reader, modTime and size
//
// The new object may have been created if an error is returned 可能本身这个object就不存在？？
func (o *Object) Update(ctx context.Context, in io.Reader, src fs.ObjectInfo, options ...fs.OpenOption) (err error) {
	//如果是更新软链接文件内容，则需要更新链接到的文件的内容

	allByte, err := io.ReadAll(in)
	if err != nil {
		return err
	}
	//如何文件本身就没有，会去创建，如果中间的文件夹不存在，不会自动创建

	fileInfo := FileInfo{
		Id:       o.id,
		Name:     o.fileName,
		FileSize: int64(len(allByte)),
		Content:  allByte,
		ModTime:  src.ModTime(ctx),
		IsDir:    false,
		ParentId: o.parentId,
	}
	isLinkModeFile := strings.HasSuffix(src.Remote(), linkSuffix)
	if isLinkModeFile {
		fileInfo.IsLink = true
		fileInfo.Name = strings.TrimSuffix(o.fileName, linkSuffix)
		//localObject := src.(*local.Object)
		////	判断是文件夹软链还是文件软链
		//linkdst, err := os.Readlink(localObject.path)
		//if err != nil {
		//	return nil, err
		//}

		linkToPath := string(allByte)
		linkToFileInfo, err := os.Stat(linkToPath)
		if err != nil {
			return err
		}
		fileInfo.LinkToLocalPath = linkToPath
		srcLinkPath := strings.TrimSuffix(path.Join(src.Fs().Root(), src.Remote()), linkSuffix)
		dstLinkPath := strings.TrimSuffix(path.Join(o.fs.root, o.remote), linkSuffix)

		fileInfo.IsDir = linkToFileInfo.IsDir()
		derivedPathFromRelative, err := ResolveDerivedPathFromRelative(srcLinkPath, linkToPath, dstLinkPath, fileInfo.IsDir)
		if err != nil {
			//如果转化的路径超过了绝对路径的根路径就会报错
			return err
		} else {
			fileInfo.LinkToPath = derivedPathFromRelative
		}

	}
	tx := o.fs.db.Begin()
	if o.id == "" {
		result := tx.Create(&fileInfo)
		if result.Error != nil {
			tx.Rollback()
			return errors.Wrapf(result.Error, "Error update object %s", o)
		}
	} else {
		result := tx.Model(&FileInfo{}).Where("id = ?", o.id).Updates(fileInfo)
		if result.Error != nil {
			tx.Rollback()
			return errors.Wrapf(result.Error, "Error update object %s", o)
		}
	}

	//更新o对象信息
	o.id = fileInfo.Id
	o.modTime = fileInfo.ModTime
	o.size = fileInfo.FileSize
	o.parentId = fileInfo.ParentId
	if isLinkModeFile {
		o.fileName = fileInfo.Name + linkSuffix
	} else {
		o.fileName = fileInfo.Name
	}
	o.remote = src.Remote()

	tx.Commit()
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
