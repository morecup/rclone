package db

import (
	"context"
	"fmt"
	"github.com/pkg/errors"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/config/configmap"
	"github.com/rclone/rclone/fs/config/configstruct"
	"github.com/rclone/rclone/fs/hash"
	"github.com/rclone/rclone/lib/dircache"
	"github.com/rclone/rclone/lib/encoder"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"io"
	"path"
	"regexp"
	"strings"
	"time"
)

const (
	dirRootID  = "root"
	linkSuffix = ".rclonelink"
)

var (
	rootDir = FileInfo{
		Id:       dirRootID,
		Name:     "",
		IsDir:    true,
		FileSize: -1,
	}
)

// Register with Fs
func init() {
	fs.Register(&fs.RegInfo{
		Name:        "db",
		Description: "基于数据库",
		NewFs:       NewFs,
		Config:      Config,
		Options: []fs.Option{{
			Name:      "db_type",
			Help:      "cookie BDUSS",
			Default:   "sqlite",
			Advanced:  false,
			Sensitive: true,
			Examples: []fs.OptionExample{{
				Value: "sqlite",
				Help:  `sqlite.`,
			}, {
				Value: "mysql",
				Help:  `mysql.`,
			}, {
				Value: "postgres",
				Help:  `postgres.`,
			}},
		}, {
			Name: "data_source_name",
			Help: `sqlite example: rcloneDB.db
mysql example: user:password@tcp(127.0.0.1:3306)/dbname?charset=utf8mb4&parseTime=True&loc=Local
postgres example: user=postgres password=yourpassword dbname=yourdbname sslmode=disable`,
			Default:   "rcloneDB.db",
			Advanced:  false,
			Sensitive: true,
		}, {
			Name:      "db_id",
			Help:      `move and copy need, Used to distinguish between identical databases`,
			Default:   "",
			Advanced:  true,
			Sensitive: true,
		}, {
			Name:     "isLinkFileMode",
			Help:     "Translate symlinks to/from regular files with a '" + linkSuffix + "' extension.",
			Default:  false,
			NoPrefix: true,
			Advanced: true,
		}},
	})
}
func Config(ctx context.Context, name string, m configmap.Mapper, config fs.ConfigIn) (*fs.ConfigOut, error) {
	dbType, _ := m.Get("db_type")
	//默认存空字符串，但是get的时候会把默认值返回来，比如这里能返回rcloneDB.db
	dsn, _ := m.Get("data_source_name")
	var dialector gorm.Dialector
	if dbType == "sqlite" {
		dialector = sqlite.Open(dsn)
	} else if dbType == "mysql" {
		dialector = mysql.Open(dsn)
	} else if dbType == "postgres" {
		dialector = postgres.Open(dsn)
	} else {
		return nil, errors.Errorf("db type %s not supported", dbType)
	}
	_, err := gorm.Open(dialector, &gorm.Config{})
	if err != nil {
		return nil, errors.Wrapf(err, "%s can not connect", dsn)
	}
	return nil, nil
}

// Options defines the configuration for this backend
type Options struct {
	Enc            encoder.MultiEncoder `config:"encoding"`
	IsLinkFileMode bool                 `config:"isLinkFileMode"`
}

// Fs represents a remote OneDrive
type Fs struct {
	name      string         // name of this remote
	root      string         // the path we are working on
	opt       Options        // parsed options
	ci        *fs.ConfigInfo // global config
	features  *fs.Features   // optional features
	db        *gorm.DB
	rootDirId string
	dbId      string
	dirCache  *dircache.DirCache
}

// NewFs root is linux path ,maybe "" "/" "/1/2" is not be "\1" "\\1" .root may be file path. need to fix
func NewFs(ctx context.Context, name, root string, m configmap.Mapper) (fs.Fs, error) {
	// Parse config into Options struct
	opt := new(Options)
	err := configstruct.Set(m, opt)
	if err != nil {
		return nil, err
	}
	root = strings.Trim(root, "/")

	dsn, _ := m.Get("data_source_name")
	dbId, ok := m.Get("db_id")
	if !ok || dbId == "" {
		dbId = dsn
	}

	//全局配置信息
	ci := fs.GetConfig(ctx)

	f := &Fs{
		name: name,
		root: root,
		opt:  *opt,
		ci:   ci,
		dbId: dbId,
	}
	f.features = (&fs.Features{
		CaseInsensitive: false,
		//这个是用作存储mime类型，用于判断文件类型的
		ReadMimeType:            false,
		CanHaveEmptyDirectories: true,
	}).Fill(ctx, f)

	dbType, _ := m.Get("db_type")
	var dialector gorm.Dialector
	if dbType == "sqlite" {
		dialector = sqlite.Open(dsn)
	} else if dbType == "mysql" {
		dialector = mysql.Open(dsn)
	} else if dbType == "postgres" {
		dialector = postgres.Open(dsn)
	} else {
		return nil, errors.Errorf("db type %s not supported", dbType)
	}
	db, err := gorm.Open(dialector, &gorm.Config{})
	if err != nil {
		return nil, errors.Wrapf(err, "%s can not connect", dsn)
	}
	db.Config.Logger = logger.Default.LogMode(logger.Error)
	f.db = db
	err = db.AutoMigrate(&FileInfo{})
	if err != nil {
		return nil, err
	}

	f.dirCache = dircache.New(root, rootDir.Id, f)

	err = f.dirCache.FindRoot(ctx, false)
	if err != nil {
		// Assume it is a file
		newRoot, _ := dircache.SplitPath(root)
		tempF := *f
		tempF.dirCache = dircache.New(newRoot, rootDir.Id, &tempF)
		tempF.root = newRoot
		// Make new Fs which is the parent
		err = tempF.dirCache.FindRoot(ctx, false)
		if err != nil {
			// No root so return old f
			return f, nil
		}
		_, err := tempF.readAbsoluteDirInfo(ctx, root)
		if err != nil {
			if errors.Is(err, fs.ErrorDirNotFound) || errors.Is(err, fs.ErrorObjectOrDirNotFound) {
				// File doesn't exist so return old f
				return f, nil
			}
			return nil, err
		}
		// XXX: update the old f here instead of returning tempF, since
		// `features` were already filled with functions having *f as a receiver.
		// See https://github.com/rclone/rclone/issues/2182
		f.dirCache = tempF.dirCache
		f.root = tempF.root
		// return an error with an fs which points to the parent
		return f, fs.ErrorIsFile
	}
	return f, nil
}

// Return an FileInfo from a path
// If it can't be found it returns the error fs.ErrorObjectNotFound.
func (f *Fs) readAbsoluteDirInfo(ctx context.Context, path string) (*FileInfo, error) {
	// 处理特殊情况，移除结果中的空字符串
	var segments = pathToSegments(path)

	var retrievedFile FileInfo = rootDir

	for i, segment := range segments {
		var foundFile []FileInfo
		var tx *gorm.DB
		if i != len(segments)-1 {
			tx = f.db.Find(&foundFile, "name = ? and parent_id = ? and is_dir = 1", segment, retrievedFile.Id)
		} else {
			tx = f.db.Find(&foundFile, "name = ? and parent_id = ?", segment, retrievedFile.Id)
		}
		if tx.Error != nil {
			return nil, errors.Wrapf(tx.Error, "db readAbsoluteDirInfo error.path (%s),segment (%s)", path, segment)
		}
		if len(foundFile) == 0 {
			if i != len(segments)-1 {
				return nil, errors.Wrapf(fs.ErrorDirNotFound, "can not found this segment in db. path (%s),segment (%s)", path, segment)
			} else {
				return nil, errors.Wrapf(fs.ErrorObjectOrDirNotFound, "can not found this segment in db. path (%s),segment (%s)", path, segment)
			}
		}
		if len(foundFile) > 1 {
			return nil, errors.Errorf("found %d same name file in db. path (%s),segment (%s)", len(foundFile), path, segment)
		}
		retrievedFile = foundFile[0]
	}
	return &retrievedFile, nil
}

// Name of the remote (as passed into NewFs)
func (f *Fs) Name() string {
	return f.name
}

// Root of the remote (as passed into NewFs)
func (f *Fs) Root() string {
	return f.root
}

// String converts this Fs to a string
func (f *Fs) String() string {
	return fmt.Sprintf("db root '%s'", f.Root())
}

// Features returns the optional features of this Fs
func (f *Fs) Features() *fs.Features {
	return f.features
}

// Hashes returns the supported hash sets.
func (f *Fs) Hashes() hash.Set {
	//return hash.Set(f.hashType)
	return hash.Set(hash.None)
}

// Precision return the precision of this Fs
func (f *Fs) Precision() time.Duration {
	return time.Second
}

func (f *Fs) FindLeaf(ctx context.Context, pathID, leaf string) (pathIDOut string, found bool, err error) {
	// fs.Debugf(f, "FindLeaf(%q, %q)", pathID, leaf)
	//_, ok := f.dirCache.GetInv(pathID)
	//if !ok {
	//	return "", false, errors.New("couldn't find parent ID")
	//}
	var foundFile []FileInfo
	tx := f.db.Find(&foundFile, "name = ? and parent_id = ?", leaf, pathID)
	if tx.Error != nil {
		return "", false, errors.Wrapf(tx.Error, "FindLeaf:db find error")
	}
	if len(foundFile) == 0 {
		return "", false, nil
	} else if len(foundFile) == 1 {
		if foundFile[0].IsDir {
			return foundFile[0].Id, true, nil
		} else {
			return "", false, errors.Wrapf(fs.ErrorIsDir, "expected to be a file, but is a folder. pathID (%s) leaf (%s) ", pathID, leaf)
		}
	} else {
		return "", false, errors.Errorf("found %d files in db. pathID (%s),leaf (%s)", len(foundFile), pathID, leaf)
	}
}

func (f *Fs) CreateDir(ctx context.Context, pathID, leaf string) (newID string, err error) {
	// fs.Debugf(f, "CreateDir(%q, %q)\n", dirID, leaf)
	var fileInfo FileInfo
	tx := f.db.Where(FileInfo{ParentId: pathID, Name: leaf}).FirstOrCreate(&fileInfo, FileInfo{
		IsDir:    true,
		Name:     leaf,
		ParentId: pathID,
		ModTime:  time.Now(),
		FileSize: -1,
	})
	if tx.Error != nil {
		return "", errors.Wrapf(tx.Error, "CreateDir:db find error")
	}
	if !fileInfo.IsDir {
		return "", errors.Wrapf(fs.ErrorIsDir, "CreateDir: expected to be a file, but is a folder. pathID (%s) leaf (%s) ", pathID, leaf)
	}
	return fileInfo.Id, nil
}

func pathToSegments(path string) []string {
	re := regexp.MustCompile(`\\+`)
	slashRoot := re.ReplaceAllString(path, "/")
	parts := strings.Split(slashRoot, "/")

	// 处理特殊情况，移除结果中的空字符串
	var segments []string
	for _, part := range parts {
		if part != "" {
			segments = append(segments, part)
		}
	}
	return segments
}

// 可能返回根目录
func (f *Fs) findRootRelativePathFile(path string) (*FileInfo, error) {
	// 处理特殊情况，移除结果中的空字符串
	var segments = pathToSegments(path)

	var retrievedFile FileInfo = FileInfo{
		Id:    f.rootDirId,
		IsDir: true,
	}
	if f.rootDirId == dirRootID {
		retrievedFile = rootDir
	}

	if len(segments) == 0 && f.rootDirId != dirRootID {
		var foundFile FileInfo
		tx := f.db.First(&foundFile, "id = ?", f.rootDirId)
		if errors.Is(tx.Error, gorm.ErrRecordNotFound) {
			return nil, errors.Errorf("can not found root dir in db. path (%s),rootDirId (%s)", path, f.rootDirId)
		} else if tx.Error != nil {
			return nil, errors.Wrapf(tx.Error, "db find error")
		}
		return &foundFile, nil
	}

	for _, segment := range segments {
		var foundFile []FileInfo
		tx := f.db.Find(&foundFile, "name = ? and parent_id = ?", segment, retrievedFile.Id)
		if tx.Error != nil {
			return nil, errors.Wrapf(tx.Error, "db find error")
		}
		if len(foundFile) == 0 {
			return nil, errors.Errorf("can not found this segment in db. path (%s),segment (%s)", path, segment)
		}
		if len(foundFile) > 1 {
			return nil, errors.Errorf("found %d files in db. path (%s),segment (%s)", len(foundFile), path, segment)
		}
		retrievedFile = foundFile[0]
	}
	return &retrievedFile, nil
}

// List entries normal need to implement fs.Directory or fs.Object ,dir is relative path,f.root is base path
func (f *Fs) List(ctx context.Context, dir string) (entries fs.DirEntries, err error) {
	directoryID, err := f.dirCache.FindDir(ctx, dir, false)
	if err != nil {
		return nil, err
	}
	if !f.opt.IsLinkFileMode {
		var dirFileInfo FileInfo
		db := f.db.Find(&dirFileInfo, &FileInfo{Id: directoryID})
		if db.Error != nil && errors.Is(db.Error, gorm.ErrRecordNotFound) {
			return nil, fs.ErrorDirNotFound
		}
		if db.Error != nil {
			return nil, errors.Wrapf(db.Error, "List: dir find error")
		}
		for dirFileInfo.IsLink {
			linkToDirInfo, err := f.readAbsoluteDirInfo(ctx, dirFileInfo.LinkToPath)
			if err != nil {
				return nil, err
			}
			dirFileInfo = *linkToDirInfo
			if !linkToDirInfo.IsLink {
				directoryID = linkToDirInfo.Id
			}
		}
	}

	var foundFile []FileInfo
	tx := f.db.Find(&foundFile, "parent_id = ?", directoryID)
	if errors.Is(tx.Error, gorm.ErrRecordNotFound) {
		return entries, nil
	} else if tx.Error != nil {
		return nil, errors.Wrapf(tx.Error, "db find error")
	}
	if len(foundFile) == 0 {
		return entries, nil
	}
	for _, file := range foundFile {
		var remote string
		if dir == "" {
			remote = file.Name
		} else {
			remote = dir + "/" + file.Name
		}
		if file.IsLink && f.opt.IsLinkFileMode {
			entries = append(entries, NewObjectFromFileInfo(&file, remote, f))
		} else {
			if file.IsDir {
				entries = append(entries, &Dir{
					id:       file.Id,
					parentId: file.ParentId,
					remote:   remote,
					modTime:  file.ModTime,
					size:     -1,
					items:    -1,
					fs:       f,
				})
			} else {
				entries = append(entries, NewObjectFromFileInfo(&file, remote, f))
			}
		}
	}
	return entries, nil
}

// NewObject finds the Object at remote.  If it can't be found
// it returns the error fs.ErrorObjectNotFound.
func (f *Fs) NewObject(ctx context.Context, remote string) (fs.Object, error) {
	fileName, parentDirId, err := f.dirCache.FindPath(ctx, remote, false)
	if err != nil {
		return nil, fs.ErrorObjectNotFound
	}

	var remoteFile FileInfo
	tx := f.db.Where(FileInfo{ParentId: parentDirId, Name: fileName}).First(&remoteFile)
	if tx.Error != nil {
		if !errors.Is(tx.Error, gorm.ErrRecordNotFound) {
			return nil, errors.Wrapf(tx.Error, "db move error")
		} else {
			return nil, fs.ErrorObjectNotFound
		}
	} else {
		if remoteFile.IsDir {
			return nil, errors.Wrapf(fs.ErrorIsDir, "expected to be a file, but is a folder. remote (%s)", remote)
		} else {
			return NewObjectFromFileInfo(&remoteFile, remote, f), nil
		}
	}
}

// Put in to the remote path with the modTime given of the given size
//
// When called from outside an Fs by rclone, src.Size() will always be >= 0.
// But for unknown-sized objects (indicated by src.Size() == -1), Put should either
// return an error or upload it properly (rather than e.g. calling panic).
//
// May create the object even if it returns an error - if so
// will return the object and the error, otherwise will return
// nil and the error
func (f *Fs) Put(ctx context.Context, in io.Reader, src fs.ObjectInfo, options ...fs.OpenOption) (fs.Object, error) {
	remote := src.Remote()
	// Create the directory for the object if it doesn't exist
	leaf, directoryID, err := f.dirCache.FindPath(ctx, remote, true)
	if err != nil {
		return nil, err
	}
	o := &Object{
		fs:       f,
		remote:   src.Remote(),
		size:     src.Size(),
		modTime:  src.ModTime(ctx),
		parentId: directoryID,
		fileName: leaf,
	}

	return o, o.Update(ctx, in, src, options...)
}

// PutStream uploads to the remote path with the modTime given of indeterminate size
func (f *Fs) PutStream(ctx context.Context, in io.Reader, src fs.ObjectInfo, options ...fs.OpenOption) (fs.Object, error) {
	return f.Put(ctx, in, src, options...)
}

// Mkdir makes the directory (container, bucket)
//
// Shouldn't return an error if it already exists
func (f *Fs) Mkdir(ctx context.Context, dir string) error {
	_, err := f.dirCache.FindDir(ctx, dir, true)
	return err
}

func (f *Fs) Remove(fileId string, isDir bool) error {
	if isDir {
		var foundFiles []FileInfo
		result := f.db.Find(&foundFiles, FileInfo{ParentId: fileId})
		if result.Error != nil {
			return errors.Wrapf(result.Error, "db Remove find error")
		}
		for _, foundFile := range foundFiles {
			err := f.Remove(foundFile.Id, foundFile.IsDir)
			if err != nil {
				return err
			}
		}
		result = f.db.Delete(FileInfo{Id: fileId})
		if result.Error != nil {
			return errors.Wrap(result.Error, "db Remove dir error")
		}
	} else {
		result := f.db.Delete(FileInfo{Id: fileId})
		if result.Error != nil {
			return errors.Wrap(result.Error, "db Remove file error")
		}
	}
	return nil
}

// Purge all files in the directory specified
//
// Implement this if you have a way of deleting all the files
// quicker than just running Remove() on the result of List()
//
// Return an error if it doesn't exist
func (f *Fs) Purge(ctx context.Context, dir string) error {
	rootID, err := f.dirCache.FindDir(ctx, dir, false)
	if err != nil {
		return err
	}
	err = f.Remove(rootID, true)
	if err != nil {
		return err
	}
	f.dirCache.FlushDir(dir)
	return nil
}

// Rmdir removes the directory (container, bucket) if empty
//
// Return an error if it doesn't exist or isn't empty
func (f *Fs) Rmdir(ctx context.Context, dir string) error {
	root := path.Join(f.root, dir)
	if root == "" {
		return errors.New("can't purge root directory")
	}

	entries, err := f.List(ctx, dir)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return f.Purge(ctx, dir)
	} else {
		return fs.ErrorDirectoryNotEmpty
	}
}

// Move src to this remote using server-side move operations.
//
// This is stored with the remote path given.
//
// It returns the destination Object and a possible error.
//
// Will only be called if src.Fs().Name() == f.Name()
//
// If it isn't possible then return fs.ErrorCantMove remote:have file name and is relativePath
func (f *Fs) Move(ctx context.Context, src fs.Object, remote string) (fs.Object, error) {
	srcObj, ok := src.(*Object)
	if ok {
		//need to sure same db
		if srcObj.fs.dbId != f.dbId {
			fs.Debugf(f, "Can't move files between drives (%q != %q)", srcObj.fs.dbId, srcObj.fs.dbId)
			return nil, fs.ErrorCantMove
		}
		//now is same db
		dstFileName, dstParentDirID, err := f.dirCache.FindPath(ctx, remote, true)
		if err != nil {
			return nil, err
		}

		var remoteFile FileInfo
		tx := f.db.Where(FileInfo{ParentId: dstParentDirID, Name: dstFileName}).First(&remoteFile)
		if tx.Error != nil {
			if !errors.Is(tx.Error, gorm.ErrRecordNotFound) {
				return nil, errors.Wrapf(tx.Error, "db move error")
			}
		} else {
			if remoteFile.IsDir {
				return nil, errors.Wrapf(fs.ErrorIsDir, "expected to be a file, but is a folder. remote (%s)", remote)
			} else {
				//排除重命名操作时，两个路径相同的情况
				if dstFileName != srcObj.fileName {
					tx = f.db.Delete(&remoteFile)
					if tx.Error != nil {
						return nil, errors.Wrapf(tx.Error, "db move remoteFile delete error")
					}
				}
			}
		}
		if srcObj.parentId == dstParentDirID {
			// need to rename
			result := f.db.Where("id = ?", srcObj.id).Updates(FileInfo{ModTime: time.Now(), Name: dstFileName})
			if result.Error != nil {
				return nil, errors.Wrap(result.Error, "db rename error")
			}
		} else {
			result := f.db.Where("id = ?", srcObj.id).Updates(FileInfo{ModTime: time.Now(), Name: dstFileName, ParentId: dstParentDirID})
			if result.Error != nil {
				return nil, errors.Wrap(result.Error, "db real move error")
			}
		}
		var finalFile FileInfo
		result := f.db.Where("id = ?", srcObj.id).First(&finalFile)
		if result.Error != nil {
			return nil, errors.Wrap(result.Error, "db find final file error")
		}
		return NewObjectFromFileInfo(&finalFile, remote, f), nil
	} else {
		fs.Debugf(src, "Can't move - not same remote type")
		return nil, fs.ErrorCantMove
	}
}

// DirMove moves src, srcRemote to this remote at dstRemote
// using server-side move operations.
//
// Will only be called if src.Fs().Name() == f.Name()
//
// If it isn't possible then return fs.ErrorCantDirMove
//
// # If destination exists then return fs.ErrorDirExists
//
// srcRemote is absolute path,dstRemote is absolute path,can not end with "/"
func (f *Fs) DirMove(ctx context.Context, src fs.Fs, srcRemote, dstRemote string) error {
	srcFs, ok := src.(*Fs)
	if ok {
		//need to sure same db
		if srcFs.dbId != f.dbId {
			fs.Debugf(f, "Can't move dir between drives (%q != %q)", srcFs.dbId, srcFs.dbId)
			return fs.ErrorCantMove
		}
		_, srcParentDirId, err := srcFs.dirCache.FindPath(ctx, srcRemote, false)
		if err != nil {
			return err
		}
		dstDirName, dstParentDirId, err := f.dirCache.FindPath(ctx, dstRemote, true)
		if err != nil {
			return err
		}
		srcDirId, err := srcFs.dirCache.FindDir(ctx, srcRemote, false)
		if err != nil {
			return fs.ErrorDirNotFound
		}
		_, err = f.dirCache.FindDir(ctx, dstRemote, false)
		if err == nil {
			return fs.ErrorDirExists
		}

		if srcParentDirId == dstParentDirId {
			// need to rename
			result := f.db.Where("id = ?", srcDirId).Updates(FileInfo{ModTime: time.Now(), Name: dstDirName})
			if result.Error != nil {
				return errors.Wrap(result.Error, "db rename error")
			}
			srcFs.dirCache.FlushDir(srcRemote)
		} else {
			result := f.db.Where("id = ?", srcDirId).Updates(FileInfo{ModTime: time.Now(), Name: dstDirName, ParentId: dstParentDirId})
			if result.Error != nil {
				return errors.Wrap(result.Error, "db real move error")
			}
			srcFs.dirCache.FlushDir(srcRemote)
		}
		return nil
	} else {
		fs.Debugf(srcFs, "Can't move directory - not same remote type")
		return fs.ErrorCantDirMove
	}
}

// Copy src to this remote using server-side copy operations.
//
// This is stored with the remote path given.
//
// It returns the destination Object and a possible error.
//
// Will only be called if src.Fs().Name() == f.Name()
//
// If it isn't possible then return fs.ErrorCantCopy
// remote have file name,if dir copy dir,first list then copy file to remote(file)
func (f *Fs) Copy(ctx context.Context, src fs.Object, remote string) (fs.Object, error) {
	srcObj, ok := src.(*Object)
	if !ok {
		fs.Debugf(src, "Can't copy - not same remote type")
		return nil, fs.ErrorCantCopy
	}
	//need to sure same account
	if srcObj.fs.dbId != f.dbId {
		fs.Debugf(f, "Can't move files between dbId (%q != %q)", srcObj.fs.dbId, f.dbId)
		return nil, fs.ErrorCantMove
	}
	//now is same db
	dstFileName, dstParentDirID, err := f.dirCache.FindPath(ctx, remote, true)
	if err != nil {
		return nil, err
	}

	var remoteFile FileInfo
	tx := f.db.Where(FileInfo{ParentId: dstParentDirID, Name: dstFileName}).First(&remoteFile)
	if tx.Error != nil {
		if !errors.Is(tx.Error, gorm.ErrRecordNotFound) {
			return nil, errors.Wrapf(tx.Error, "db move error")
		}
	} else {
		if remoteFile.IsDir {
			return nil, errors.Wrapf(fs.ErrorIsDir, "expected to be a file, but is a folder. remote (%s)", remote)
		} else {
			//本来就存在那个文件，但是我又要拷贝一个同名文件过去
			//暂定走覆盖,暂时写成先删除再新增
			tx = f.db.Delete(&remoteFile)
			if tx.Error != nil {
				return nil, errors.Wrapf(tx.Error, "db move remoteFile delete error")
			}
		}
	}

	in, err := srcObj.Open(ctx)
	if err != nil {
		return nil, err
	}
	content, err := io.ReadAll(in)
	if err != nil {
		return nil, err
	}
	fileInfo := FileInfo{
		Name:     dstFileName,
		ParentId: dstParentDirID,
		FileSize: srcObj.size,
		IsDir:    false,
		ModTime:  time.Now(),
		Content:  content,
	}
	result := f.db.Create(&fileInfo)
	if result.Error != nil {
		return nil, errors.Wrap(result.Error, "db copy error")
	}

	return NewObjectFromFileInfo(&fileInfo, remote, f), nil
}

// DirCacheFlush resets the directory cache - used in testing as an
// optional interface
func (f *Fs) DirCacheFlush() {
	f.dirCache.ResetRoot()
}
