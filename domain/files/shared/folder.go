package shared

import filestorage "github.com/assurrussa/gouploads/infrastructure/storage/files"

type FolderPrefixPath = filestorage.FolderPrefixPath

const (
	FolderPrefixPathPersist = filestorage.FolderPrefixPathPersist
	FolderPrefixPathTemp    = filestorage.FolderPrefixPathTemp
)
