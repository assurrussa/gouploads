package model

import (
	"database/sql/driver"
	"fmt"
	"strconv"
	"strings"
)

type FileType int64

const (
	FileTypeUnknown FileType = 0
	FileTypeImage   FileType = 1
	FileTypeVideo   FileType = 2
	FileTypePdf     FileType = 3
	FileTypeDocx    FileType = 4
	FileTypeLink    FileType = 5
	FileTypeText    FileType = 6
)

var fileTypes = map[FileType]string{
	FileTypeUnknown: "unknown",
	FileTypeImage:   "image",
	FileTypeVideo:   "video",
	FileTypePdf:     "pdf",
	FileTypeDocx:    "docx",
	FileTypeLink:    "link",
	FileTypeText:    "text",
}

var fileTypesReverse = map[string]FileType{
	"unknown": FileTypeUnknown,
	"image":   FileTypeImage,
	"video":   FileTypeVideo,
	"pdf":     FileTypePdf,
	"docx":    FileTypeDocx,
	"link":    FileTypeLink,
	"text":    FileTypeText,
}

func (f *FileType) Scan(value any) error {
	if value == nil {
		*f = FileTypeUnknown
		return nil
	}

	var fileTypeStr FileType
	switch v := value.(type) {
	case int64:
		fileTypeStr = FileType(v)
	case string:
		val, _ := strconv.Atoi(v)
		fileTypeStr = FileType(val)
	default:
		return fmt.Errorf("unsupported type for FileType: %T", value)
	}

	*f = fileTypeStr
	return nil
}

func (f FileType) Value() (driver.Value, error) {
	return int64(f), nil
}

func (f FileType) Validate() error {
	if f == FileTypeUnknown {
		return fmt.Errorf("invalid object type: %d, %s", f, GetFileType(f))
	}

	return nil
}

func (f FileType) String() string {
	return strconv.Itoa(int(f))
}

func (f FileType) ToID() int16 {
	return int16(f)
}

func (f FileType) ToString() string {
	return GetFileType(f)
}

// GetFileType получение значения type file.
func GetFileType(fileType FileType) string {
	val, ok := fileTypes[fileType]
	if !ok {
		return fileTypes[FileTypeUnknown]
	}

	return val
}

// GetFileTypeString получение значения type file.
func GetFileTypeString(fileType string) FileType {
	val, ok := fileTypesReverse[fileType]
	if !ok {
		return FileTypeUnknown
	}

	return val
}

func GetFileTypeFromMimeType(mime string) (FileType, error) {
	switch {
	case isImage(mime):
		return FileTypeImage, nil
	case isVideo(mime):
		return FileTypeVideo, nil
	case isText(mime):
		return FileTypeText, nil
	case isPdf(mime):
		return FileTypePdf, nil
	case isDocx(mime):
		return FileTypeDocx, nil
	case isLink(mime):
		return FileTypeLink, nil
	}

	return FileTypeUnknown, fmt.Errorf("unknown file type: %s", mime)
}

func isImage(mime string) bool {
	return len(mime) >= 6 && mime[:6] == "image/"
}

func isVideo(mime string) bool {
	return len(mime) >= 6 && mime[:6] == "video/"
}

func isText(mime string) bool {
	return len(mime) >= 5 && mime[:5] == "text/"
}

func isPdf(mime string) bool {
	return mime == "application/pdf"
}

func isLink(mime string) bool {
	return strings.HasPrefix(mime, "http://") || strings.HasPrefix(mime, "https://")
}

func isDocx(mime string) bool {
	return mime == "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
}
