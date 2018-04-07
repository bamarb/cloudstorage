package cloudstorage

import (
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/araddon/gou"
)

// CleanupCacheFiles cleans up old store cache files
// if your process crashes all it's old cache files, the local copies of the cloudfiles,
// will left behind.
// This function is a convenience func to help clean up those old files.
//
// I suggest you call this behind a package var sync.Once struct, so its only called at the
// startup of your application.
func CleanupCacheFiles(maxage time.Duration, TmpDir string) {
	defer func() {
		if r := recover(); r != nil {
			stackBuf := make([]byte, 4096)
			stackBufLen := runtime.Stack(stackBuf, false)
			gou.Errorf("CleanupOldStoreCacheFiles cleanup old files: panic recovery %v\n %s", r, string(stackBuf[0:stackBufLen]))
		}
	}()
	cleanoldfiles := func(path string, f os.FileInfo, err error) (e error) {
		if filepath.Ext(path) == StoreCacheFileExt {
			if f.ModTime().Before(time.Now().Add(-(maxage))) {
				// delete if the files is older than 1 day
				err := os.Remove(path)
				if err != nil {
					gou.Errorf("CleanupOldStoreCacheFiles error removing an old files: %v", err)
				}
			}
		}
		return nil
	}
	filepath.Walk(TmpDir, cleanoldfiles)
}
