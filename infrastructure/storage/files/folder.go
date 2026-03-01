package filestorage

import (
	"path"
	"strings"
)

type FolderPrefixPath string

const (
	FolderPrefixPathPersist FolderPrefixPath = "uploads"
	FolderPrefixPathTemp    FolderPrefixPath = "tmp/uploads"
)

func (f FolderPrefixPath) String() string {
	return string(f)
}

func DirPersistPath(dir string) string {
	switch {
	case strings.HasPrefix(dir, FolderPrefixPathTemp.String()):
		dir = path.Join(FolderPrefixPathPersist.String(), dir[len(FolderPrefixPathTemp):])
	case !strings.HasPrefix(dir, FolderPrefixPathPersist.String()):
		dir = path.Join(FolderPrefixPathPersist.String(), dir)
	}

	return dir
}

func DirTemptPath(dir string) string {
	switch {
	case strings.HasPrefix(dir, FolderPrefixPathPersist.String()):
		dir = path.Join(FolderPrefixPathTemp.String(), dir[len(FolderPrefixPathPersist):])
	case !strings.HasPrefix(dir, FolderPrefixPathTemp.String()):
		dir = path.Join(FolderPrefixPathTemp.String(), dir)
	}

	return dir
}
