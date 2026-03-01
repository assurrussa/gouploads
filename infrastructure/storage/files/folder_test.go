package filestorage_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	filestorage "github.com/assurrussa/gouploads/infrastructure/storage/files"
)

func TestDirPersistPath(t *testing.T) {
	persistPrefix := filestorage.FolderPrefixPathPersist.String()
	tempPrefix := filestorage.FolderPrefixPathTemp.String()
	assert.Equal(t, persistPrefix+"/entity/folder/custom/1", filestorage.DirPersistPath(persistPrefix+"/entity/folder/custom/1"))
	assert.Equal(t, persistPrefix+"/entity/folder/custom/1", filestorage.DirPersistPath(tempPrefix+"/entity/folder/custom/1"))
	assert.Equal(t, persistPrefix+"/entity/folder/custom/1", filestorage.DirPersistPath("/entity/folder/custom/1"))
	assert.Equal(t, tempPrefix+"/entity/folder/custom/1", filestorage.DirTemptPath(persistPrefix+"/entity/folder/custom/1"))
	assert.Equal(t, tempPrefix+"/entity/folder/custom/1", filestorage.DirTemptPath(tempPrefix+"/entity/folder/custom/1"))
	assert.Equal(t, tempPrefix+"/entity/folder/custom/1", filestorage.DirTemptPath("/entity/folder/custom/1"))
}

func BenchmarkDirPersistPath(b *testing.B) {
	dirPath := filestorage.FolderPrefixPathPersist.String() + "/entity/folder/custom/1"
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = filestorage.DirPersistPath(dirPath)
	}
}

func BenchmarkDirPersistPathWithTemp(b *testing.B) {
	dirPath := filestorage.FolderPrefixPathTemp.String() + "/entity/folder/custom/1"
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = filestorage.DirPersistPath(dirPath)
	}
}

func BenchmarkDirPersistPathWithoutPersist(b *testing.B) {
	dirPath := "/entity/folder/custom/1"
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = filestorage.DirPersistPath(dirPath)
	}
}
