// Package bisync implements bisync
// Copyright (c) 2017-2020 Chris Nelson
// Contributions to original python version: Hildo G. Jr., e2t, kalemas, silenceleaf
package bisync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	gosync "sync"

	"github.com/rclone/rclone/cmd/bisync/bilib"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/filter"
	"github.com/rclone/rclone/fs/operations"
	"github.com/rclone/rclone/lib/atexit"
	"github.com/rclone/rclone/lib/terminal"
)

// ErrBisyncAborted signals that bisync is aborted and forces exit code 2
var ErrBisyncAborted = errors.New("bisync aborted")

// bisyncRun keeps bisync runtime state
type bisyncRun struct {
	fs1         fs.Fs
	fs2         fs.Fs
	abort       bool
	critical    bool
	retryable   bool
	basePath    string
	workDir     string
	listing1    string
	listing2    string
	newListing1 string
	newListing2 string
	opt         *Options
}

type queues struct {
	copy1to2      bilib.Names
	copy2to1      bilib.Names
	renamed1      bilib.Names // renamed on 1 and copied to 2
	renamed2      bilib.Names // renamed on 2 and copied to 1
	renameSkipped bilib.Names // not renamed because it was equal
	deletedonboth bilib.Names
}

// Bisync handles lock file, performs bisync run and checks exit status
func Bisync(ctx context.Context, fs1, fs2 fs.Fs, optArg *Options) (err error) {
	opt := *optArg // ensure that input is never changed
	b := &bisyncRun{
		fs1: fs1,
		fs2: fs2,
		opt: &opt,
	}

	if opt.CheckFilename == "" {
		opt.CheckFilename = DefaultCheckFilename
	}
	if opt.Workdir == "" {
		opt.Workdir = DefaultWorkdir
	}

	if !opt.DryRun && !opt.Force {
		if fs1.Precision() == fs.ModTimeNotSupported {
			return errors.New("modification time support is missing on path1")
		}
		if fs2.Precision() == fs.ModTimeNotSupported {
			return errors.New("modification time support is missing on path2")
		}
	}

	if b.workDir, err = filepath.Abs(opt.Workdir); err != nil {
		return fmt.Errorf("failed to make workdir absolute: %w", err)
	}
	if err = os.MkdirAll(b.workDir, os.ModePerm); err != nil {
		return fmt.Errorf("failed to create workdir: %w", err)
	}

	// Produce a unique name for the sync operation
	b.basePath = filepath.Join(b.workDir, bilib.SessionName(b.fs1, b.fs2))
	b.listing1 = b.basePath + ".path1.lst"
	b.listing2 = b.basePath + ".path2.lst"
	b.newListing1 = b.listing1 + "-new"
	b.newListing2 = b.listing2 + "-new"

	// Handle lock file
	lockFile := ""
	if !opt.DryRun {
		lockFile = b.basePath + ".lck"
		if bilib.FileExists(lockFile) {
			return fmt.Errorf("prior lock file found: %s", lockFile)
		}

		pidStr := []byte(strconv.Itoa(os.Getpid()))
		if err = os.WriteFile(lockFile, pidStr, bilib.PermSecure); err != nil {
			return fmt.Errorf("cannot create lock file: %s: %w", lockFile, err)
		}
		fs.Debugf(nil, "Lock file created: %s", lockFile)
	}

	// Handle SIGINT
	var finaliseOnce gosync.Once
	markFailed := func(file string) {
		failFile := file + "-err"
		if bilib.FileExists(file) {
			_ = os.Remove(failFile)
			_ = os.Rename(file, failFile)
		}
	}
	finalise := func() {
		finaliseOnce.Do(func() {
			if atexit.Signalled() {
				fs.Logf(nil, "Bisync interrupted. Must run --resync to recover.")
				markFailed(b.listing1)
				markFailed(b.listing2)
				_ = os.Remove(lockFile)
			}
		})
	}
	fnHandle := atexit.Register(finalise)
	defer atexit.Unregister(fnHandle)

	// run bisync
	err = b.runLocked(ctx)

	if lockFile != "" {
		errUnlock := os.Remove(lockFile)
		if errUnlock == nil {
			fs.Debugf(nil, "Lock file removed: %s", lockFile)
		} else if err == nil {
			err = errUnlock
		} else {
			fs.Errorf(nil, "cannot remove lockfile %s: %v", lockFile, errUnlock)
		}
	}

	if b.critical {
		if b.retryable && b.opt.Resilient {
			fs.Errorf(nil, Color(terminal.RedFg, "Bisync critical error: %v"), err)
			fs.Errorf(nil, Color(terminal.YellowFg, "Bisync aborted. Error is retryable without --resync due to --resilient mode."))
		} else {
			if bilib.FileExists(b.listing1) {
				_ = os.Rename(b.listing1, b.listing1+"-err")
			}
			if bilib.FileExists(b.listing2) {
				_ = os.Rename(b.listing2, b.listing2+"-err")
			}
			fs.Errorf(nil, Color(terminal.RedFg, "Bisync critical error: %v"), err)
			fs.Errorf(nil, Color(terminal.RedFg, "Bisync aborted. Must run --resync to recover."))
		}
		return ErrBisyncAborted
	}
	if b.abort {
		fs.Logf(nil, Color(terminal.RedFg, "Bisync aborted. Please try again."))
	}
	if err == nil {
		fs.Infof(nil, Color(terminal.GreenFg, "Bisync successful"))
	}
	return err
}

// runLocked performs a full bisync run
func (b *bisyncRun) runLocked(octx context.Context) (err error) {
	opt := b.opt
	path1 := bilib.FsPath(b.fs1)
	path2 := bilib.FsPath(b.fs2)

	if opt.CheckSync == CheckSyncOnly {
		fs.Infof(nil, "Validating listings for Path1 %s vs Path2 %s", quotePath(path1), quotePath(path2))
		if err = b.checkSync(b.listing1, b.listing2); err != nil {
			b.critical = true
			b.retryable = true
		}
		return err
	}

	fs.Infof(nil, "Synching Path1 %s with Path2 %s", quotePath(path1), quotePath(path2))

	if opt.DryRun {
		// In --dry-run mode, preserve original listings and save updates to the .lst-dry files
		origListing1 := b.listing1
		origListing2 := b.listing2
		b.listing1 += "-dry"
		b.listing2 += "-dry"
		b.newListing1 = b.listing1 + "-new"
		b.newListing2 = b.listing2 + "-new"
		if err := bilib.CopyFileIfExists(origListing1, b.listing1); err != nil {
			return err
		}
		if err := bilib.CopyFileIfExists(origListing2, b.listing2); err != nil {
			return err
		}
	}

	// Create second context with filters
	var fctx context.Context
	if fctx, err = b.opt.applyFilters(octx); err != nil {
		b.critical = true
		b.retryable = true
		return
	}

	// Generate Path1 and Path2 listings and copy any unique Path2 files to Path1
	if opt.Resync {
		return b.resync(octx, fctx)
	}

	// Check for existence of prior Path1 and Path2 listings
	if !bilib.FileExists(b.listing1) || !bilib.FileExists(b.listing2) {
		// On prior critical error abort, the prior listings are renamed to .lst-err to lock out further runs
		b.critical = true
		b.retryable = true
		return errors.New("cannot find prior Path1 or Path2 listings, likely due to critical error on prior run")
	}

	// Check for Path1 deltas relative to the prior sync
	fs.Infof(nil, "Path1 checking for diffs")
	newListing1 := b.listing1 + "-new"
	ds1, err := b.findDeltas(fctx, b.fs1, b.listing1, newListing1, "Path1")
	if err != nil {
		return err
	}
	ds1.printStats()

	// Check for Path2 deltas relative to the prior sync
	fs.Infof(nil, "Path2 checking for diffs")
	newListing2 := b.listing2 + "-new"
	ds2, err := b.findDeltas(fctx, b.fs2, b.listing2, newListing2, "Path2")
	if err != nil {
		return err
	}
	ds2.printStats()

	// Check access health on the Path1 and Path2 filesystems
	if opt.CheckAccess {
		fs.Infof(nil, "Checking access health")
		err = b.checkAccess(ds1.checkFiles, ds2.checkFiles)
		if err != nil {
			b.critical = true
			b.retryable = true
			return
		}
	}

	// Check for too many deleted files - possible error condition.
	// Don't want to start deleting on the other side!
	if !opt.Force {
		if ds1.excessDeletes() || ds2.excessDeletes() {
			b.abort = true
			return errors.New("too many deletes")
		}
	}

	// Check for all files changed such as all dates changed due to DST change
	// to avoid errant copy everything.
	if !opt.Force {
		msg := "Safety abort: all files were changed on %s %s. Run with --force if desired."
		if !ds1.foundSame {
			fs.Errorf(nil, msg, ds1.msg, quotePath(path1))
		}
		if !ds2.foundSame {
			fs.Errorf(nil, msg, ds2.msg, quotePath(path2))
		}
		if !ds1.foundSame || !ds2.foundSame {
			b.abort = true
			return errors.New("all files were changed")
		}
	}

	// Determine and apply changes to Path1 and Path2
	noChanges := ds1.empty() && ds2.empty()
	changes1 := false // 2to1
	changes2 := false // 1to2
	results2to1 := []Results{}
	results1to2 := []Results{}

	queues := queues{}

	if noChanges {
		fs.Infof(nil, "No changes found")
	} else {
		fs.Infof(nil, "Applying changes")
		changes1, changes2, results2to1, results1to2, queues, err = b.applyDeltas(octx, ds1, ds2)
		if err != nil {
			b.critical = true
			// b.retryable = true // not sure about this one
			return err
		}
	}

	// Clean up and check listings integrity
	fs.Infof(nil, "Updating listings")
	var err1, err2 error
	b.saveOldListings()
	// save new listings
	if noChanges {
		err1 = bilib.CopyFileIfExists(newListing1, b.listing1)
		err2 = bilib.CopyFileIfExists(newListing2, b.listing2)
	} else {
		if changes1 { // 2to1
			err1 = b.modifyListing(fctx, b.fs2, b.fs1, results2to1, queues, false)
		} else {
			err1 = bilib.CopyFileIfExists(b.newListing1, b.listing1)
		}
		if changes2 { // 1to2
			err2 = b.modifyListing(fctx, b.fs1, b.fs2, results1to2, queues, true)
		} else {
			err2 = bilib.CopyFileIfExists(b.newListing2, b.listing2)
		}
	}
	err = err1
	if err == nil {
		err = err2
	}
	if err != nil {
		b.critical = true
		b.retryable = true
		return err
	}

	if !opt.NoCleanup {
		_ = os.Remove(b.newListing1)
		_ = os.Remove(b.newListing2)
	}

	if opt.CheckSync == CheckSyncTrue && !opt.DryRun {
		fs.Infof(nil, "Validating listings for Path1 %s vs Path2 %s", quotePath(path1), quotePath(path2))
		if err := b.checkSync(b.listing1, b.listing2); err != nil {
			b.critical = true
			return err
		}
	}

	// Optional rmdirs for empty directories
	if opt.RemoveEmptyDirs {
		fs.Infof(nil, "Removing empty directories")
		err1 := operations.Rmdirs(fctx, b.fs1, "", true)
		err2 := operations.Rmdirs(fctx, b.fs2, "", true)
		err := err1
		if err == nil {
			err = err2
		}
		if err != nil {
			b.critical = true
			b.retryable = true
			return err
		}
	}

	return nil
}

// resync implements the --resync mode.
// It will generate path1 and path2 listings
// and copy any unique path2 files to path1.
func (b *bisyncRun) resync(octx, fctx context.Context) error {
	fs.Infof(nil, "Copying unique Path2 files to Path1")

	filesNow1, err := b.makeListing(fctx, b.fs1, b.newListing1)
	if err == nil {
		err = b.checkListing(filesNow1, b.newListing1, "current Path1")
	}
	if err != nil {
		return err
	}

	filesNow2, err := b.makeListing(fctx, b.fs2, b.newListing2)
	if err == nil {
		err = b.checkListing(filesNow2, b.newListing2, "current Path2")
	}
	if err != nil {
		return err
	}

	// Check access health on the Path1 and Path2 filesystems
	// enforce even though this is --resync
	if b.opt.CheckAccess {
		fs.Infof(nil, "Checking access health")

		ds1 := &deltaSet{
			checkFiles: bilib.Names{},
		}

		ds2 := &deltaSet{
			checkFiles: bilib.Names{},
		}

		for _, file := range filesNow1.list {
			if filepath.Base(file) == b.opt.CheckFilename {
				ds1.checkFiles.Add(file)
			}
		}

		for _, file := range filesNow2.list {
			if filepath.Base(file) == b.opt.CheckFilename {
				ds2.checkFiles.Add(file)
			}
		}

		err = b.checkAccess(ds1.checkFiles, ds2.checkFiles)
		if err != nil {
			b.critical = true
			b.retryable = true
			return err
		}
	}

	copy2to1 := []string{}
	for _, file := range filesNow2.list {
		if !filesNow1.has(file) {
			b.indent("Path2", file, "Resync will copy to Path1")
			copy2to1 = append(copy2to1, file)
		}
	}
	var results2to1 []Results
	var results1to2 []Results
	var results2to1Dirs []Results
	queues := queues{}

	if len(copy2to1) > 0 {
		b.indent("Path2", "Path1", "Resync is doing queued copies to")
		// octx does not have extra filters!
		results2to1, err = b.fastCopy(octx, b.fs2, b.fs1, bilib.ToNames(copy2to1), "resync-copy2to1")
		if err != nil {
			b.critical = true
			return err
		}
	}

	fs.Infof(nil, "Resynching Path1 to Path2")
	ctxRun := b.opt.setDryRun(fctx)
	// fctx has our extra filters added!
	ctxSync, filterSync := filter.AddConfig(ctxRun)
	if filterSync.Opt.MinSize == -1 {
		// prevent overwriting Google Doc files (their size is -1)
		filterSync.Opt.MinSize = 0
	}
	if results1to2, err = b.resyncDir(ctxSync, b.fs1, b.fs2); err != nil {
		b.critical = true
		return err
	}

	if b.opt.CreateEmptySrcDirs {
		// copy Path2 back to Path1, for empty dirs
		// the fastCopy above cannot include directories, because it relies on --files-from for filtering,
		// so instead we'll copy them here, relying on fctx for our filtering.

		// This preserves the original resync order for backward compatibility. It is essentially:
		// rclone copy Path2 Path1 --ignore-existing
		// rclone copy Path1 Path2 --create-empty-src-dirs
		// rclone copy Path2 Path1 --create-empty-src-dirs

		// although if we were starting from scratch, it might be cleaner and faster to just do:
		// rclone copy Path2 Path1 --create-empty-src-dirs
		// rclone copy Path1 Path2 --create-empty-src-dirs

		fs.Infof(nil, "Resynching Path2 to Path1 (for empty dirs)")

		// note copy (not sync) and dst comes before src
		if results2to1Dirs, err = b.resyncDir(ctxSync, b.fs2, b.fs1); err != nil {
			b.critical = true
			return err
		}
	}

	fs.Infof(nil, "Resync updating listings")
	b.saveOldListings() // TODO: also make replaceCurrentListings?
	if err := bilib.CopyFileIfExists(b.newListing1, b.listing1); err != nil {
		return err
	}
	if err := bilib.CopyFileIfExists(b.newListing2, b.listing2); err != nil {
		return err
	}

	// resync 2to1
	queues.copy2to1 = bilib.ToNames(copy2to1)
	if err = b.modifyListing(fctx, b.fs2, b.fs1, results2to1, queues, false); err != nil {
		b.critical = true
		return err
	}

	// resync 1to2
	queues.copy1to2 = bilib.ToNames(filesNow1.list)
	if err = b.modifyListing(fctx, b.fs1, b.fs2, results1to2, queues, true); err != nil {
		b.critical = true
		return err
	}

	// resync 2to1 (dirs)
	dirs2, _ := b.listDirsOnly(2)
	queues.copy2to1 = bilib.ToNames(dirs2.list)
	if err = b.modifyListing(fctx, b.fs2, b.fs1, results2to1Dirs, queues, false); err != nil {
		b.critical = true
		return err
	}

	if !b.opt.NoCleanup {
		_ = os.Remove(b.newListing1)
		_ = os.Remove(b.newListing2)
	}
	return nil
}

// checkSync validates listings
func (b *bisyncRun) checkSync(listing1, listing2 string) error {
	files1, err := b.loadListing(listing1)
	if err != nil {
		return fmt.Errorf("cannot read prior listing of Path1: %w", err)
	}
	files2, err := b.loadListing(listing2)
	if err != nil {
		return fmt.Errorf("cannot read prior listing of Path2: %w", err)
	}

	ok := true
	for _, file := range files1.list {
		if !files2.has(file) {
			b.indent("ERROR", file, "Path1 file not found in Path2")
			ok = false
		}
	}
	for _, file := range files2.list {
		if !files1.has(file) {
			b.indent("ERROR", file, "Path2 file not found in Path1")
			ok = false
		}
	}
	if !ok {
		return errors.New("path1 and path2 are out of sync, run --resync to recover")
	}
	return nil
}

// checkAccess validates access health
func (b *bisyncRun) checkAccess(checkFiles1, checkFiles2 bilib.Names) error {
	ok := true
	opt := b.opt
	prefix := "Access test failed:"

	numChecks1 := len(checkFiles1)
	numChecks2 := len(checkFiles2)
	if numChecks1 == 0 || numChecks1 != numChecks2 {
		fs.Errorf(nil, "%s Path1 count %d, Path2 count %d - %s", prefix, numChecks1, numChecks2, opt.CheckFilename)
		ok = false
	}

	for file := range checkFiles1 {
		if !checkFiles2.Has(file) {
			b.indentf("ERROR", file, "%s Path1 file not found in Path2", prefix)
			ok = false
		}
	}

	for file := range checkFiles2 {
		if !checkFiles1.Has(file) {
			b.indentf("ERROR", file, "%s Path2 file not found in Path1", prefix)
			ok = false
		}
	}

	if !ok {
		return errors.New("check file check failed")
	}
	fs.Infof(nil, "Found %d matching %q files on both paths", numChecks1, opt.CheckFilename)
	return nil
}

func (b *bisyncRun) testFn() {
	if b.opt.TestFn != nil {
		b.opt.TestFn()
	}
}
