package testsmatcher

import (
	"github.com/assurrussa/goshared/pkg/pointer"
	"go.uber.org/mock/gomock"

	"github.com/assurrussa/gouploads/domain/files/model"
)

var _ gomock.Matcher = FileMatcher{}

// FileMatcher is intended to be used only in tests.
type FileMatcher struct {
	*BaseMatcher[model.File]
}

func NewFileMatcher(name string, expected model.File) *FileMatcher {
	return &FileMatcher{NewBaseMatcher[model.File](name, expected, checkFileFields)}
}

var _ gomock.Matcher = FileShortMatcher{}

// FileMatcher is intended to be used only in tests.
type FileShortMatcher struct {
	*BaseMatcher[model.File]
}

func NewFileShortMatcher(name string, expected model.File) *FileShortMatcher {
	return &FileShortMatcher{NewBaseMatcher[model.File](name, expected, checkFileShortFields)}
}

func FileMatcherHelper(name string, expected model.File) gomock.Matcher {
	var unequalFields []string
	return gomock.WantFormatter(
		gomock.StringerFunc(func() string {
			return messageResultInError(unequalFields, expected, name)
		}),
		gomock.Cond(func(got model.File) bool {
			var resultCheck bool
			unequalFields, resultCheck = checkFileFields(expected, got, unequalFields)
			return resultCheck
		}),
	)
}

func checkFileFields(expected model.File, got model.File, unequalFields []string) ([]string, bool) {
	if got.ID != expected.ID {
		unequalFields = append(unequalFields, "FileID")
	}
	if pointer.Indirect(got.UserID) != pointer.Indirect(expected.UserID) {
		unequalFields = append(unequalFields, "UserID")
	}
	if pointer.Indirect(got.ManagerID) != pointer.Indirect(expected.ManagerID) {
		unequalFields = append(unequalFields, "ManagerID")
	}
	if got.ObjectType != expected.ObjectType {
		unequalFields = append(unequalFields, "FileObjectType")
	}
	if pointer.Indirect(got.ObjectID) != pointer.Indirect(expected.ObjectID) {
		unequalFields = append(unequalFields, "FileObjectID")
	}
	if got.OriginalFileName != expected.OriginalFileName {
		unequalFields = append(unequalFields, "OriginalFileName")
	}
	if got.FileName != expected.FileName {
		unequalFields = append(unequalFields, "PresetName")
	}
	if got.FolderPath != expected.FolderPath {
		unequalFields = append(unequalFields, "FolderPath")
	}
	if got.Provider != expected.Provider {
		unequalFields = append(unequalFields, "Provider")
	}
	if got.Size != expected.Size {
		unequalFields = append(unequalFields, "Size")
	}
	if got.MimeType != expected.MimeType {
		unequalFields = append(unequalFields, "MimeType")
	}
	if got.Moderate != expected.Moderate {
		unequalFields = append(unequalFields, "Moderate")
	}
	if got.BlockCause != expected.BlockCause {
		unequalFields = append(unequalFields, "BlockCause")
	}
	if got.Name != expected.Name {
		unequalFields = append(unequalFields, "Name")
	}
	if got.Description != expected.Description {
		unequalFields = append(unequalFields, "Description")
	}
	if got.FileType != expected.FileType {
		unequalFields = append(unequalFields, "FileType")
	}
	if got.Position != expected.Position {
		unequalFields = append(unequalFields, "Position")
	}
	if got.URL != expected.URL {
		unequalFields = append(unequalFields, "URL")
	}
	if got.Slug == "" { // обратное условие для got.Slug != ""
		unequalFields = append(unequalFields, "Slug")
	}
	if got.Locale != expected.Locale {
		unequalFields = append(unequalFields, "Locale")
	}
	if got.Data.Provider != expected.Data.Provider {
		unequalFields = append(unequalFields, "Data.Provider")
	}
	if got.Data.Width != expected.Data.Width {
		unequalFields = append(unequalFields, "Data.Width")
	}
	if got.Data.Height != expected.Data.Height {
		unequalFields = append(unequalFields, "Data.Height")
	}
	if got.Data.Alt != expected.Data.Alt {
		unequalFields = append(unequalFields, "Data.Alt")
	}
	for key, exp := range expected.Data.Presets {
		if got.Data.Presets[key] != exp {
			unequalFields = append(unequalFields, "Data.Presets[key]")
		}
	}
	if got.CreatedAt.IsZero() { // обратное условие для !got.CreatedAt.IsZero()
		unequalFields = append(unequalFields, "CreatedAt")
	}
	if got.UpdatedAt.IsZero() { // обратное условие для !got.UpdatedAt.IsZero()
		unequalFields = append(unequalFields, "UpdatedAt")
	}
	if got.IsPrimary != expected.IsPrimary {
		unequalFields = append(unequalFields, "IsPrimary")
	}

	return unequalFields, len(unequalFields) == 0
}

func checkFileShortFields(expected model.File, got model.File, unequalFields []string) ([]string, bool) {
	if got.FileName == "" {
		unequalFields = append(unequalFields, "PresetName")
	}
	if got.FolderPath == "" {
		unequalFields = append(unequalFields, "FolderPath")
	}

	if got.ID != expected.ID {
		unequalFields = append(unequalFields, "FileID")
	}
	if pointer.Indirect(got.UserID) != pointer.Indirect(expected.UserID) {
		unequalFields = append(unequalFields, "UserID")
	}
	if pointer.Indirect(got.ManagerID) != pointer.Indirect(expected.ManagerID) {
		unequalFields = append(unequalFields, "ManagerID")
	}
	if got.ObjectType != expected.ObjectType {
		unequalFields = append(unequalFields, "FileObjectType")
	}
	if pointer.Indirect(got.ObjectID) != pointer.Indirect(expected.ObjectID) {
		unequalFields = append(unequalFields, "FileObjectID")
	}
	if got.OriginalFileName != expected.OriginalFileName {
		unequalFields = append(unequalFields, "OriginalFileName")
	}
	if got.Provider != expected.Provider {
		unequalFields = append(unequalFields, "Provider")
	}
	if got.Size != expected.Size {
		unequalFields = append(unequalFields, "Size")
	}
	if got.MimeType != expected.MimeType {
		unequalFields = append(unequalFields, "MimeType")
	}
	if got.Moderate != expected.Moderate {
		unequalFields = append(unequalFields, "Moderate")
	}
	if got.BlockCause != expected.BlockCause {
		unequalFields = append(unequalFields, "BlockCause")
	}
	if got.Name != expected.Name {
		unequalFields = append(unequalFields, "Name")
	}
	if got.Description != expected.Description {
		unequalFields = append(unequalFields, "Description")
	}
	if got.FileType != expected.FileType {
		unequalFields = append(unequalFields, "FileType")
	}
	if got.Position != expected.Position {
		unequalFields = append(unequalFields, "Position")
	}
	if got.URL != expected.URL {
		unequalFields = append(unequalFields, "URL")
	}
	if got.Slug == "" { // обратное условие для got.Slug != ""
		unequalFields = append(unequalFields, "Slug")
	}
	if got.Locale != expected.Locale {
		unequalFields = append(unequalFields, "Locale")
	}
	if got.Data.Provider != expected.Data.Provider {
		unequalFields = append(unequalFields, "Data.Provider")
	}
	if got.Data.Width != expected.Data.Width {
		unequalFields = append(unequalFields, "Data.Width")
	}
	if got.Data.Height != expected.Data.Height {
		unequalFields = append(unequalFields, "Data.Height")
	}
	if got.Data.Alt != expected.Data.Alt {
		unequalFields = append(unequalFields, "Data.Alt")
	}
	for key, exp := range expected.Data.Presets {
		if got.Data.Presets[key] != exp {
			unequalFields = append(unequalFields, "Data.Presets[key]")
		}
	}
	if got.CreatedAt.IsZero() { // обратное условие для !got.CreatedAt.IsZero()
		unequalFields = append(unequalFields, "CreatedAt")
	}
	if got.UpdatedAt.IsZero() { // обратное условие для !got.UpdatedAt.IsZero()
		unequalFields = append(unequalFields, "UpdatedAt")
	}
	if got.IsPrimary != expected.IsPrimary {
		unequalFields = append(unequalFields, "IsPrimary")
	}

	return unequalFields, len(unequalFields) == 0
}
