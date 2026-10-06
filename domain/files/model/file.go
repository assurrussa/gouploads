package model

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/goccy/go-json"

	filesshared "github.com/assurrussa/gouploads/domain/files/shared"
)

// File представляет файл в системе.
type File struct {
	ID               int64                          `json:"id" db:"id"`
	UserID           *int64                         `json:"userId" db:"user_id"`
	ManagerID        *int64                         `json:"managerId" db:"manager_id"`
	ObjectType       filesshared.FileObjectType     `json:"objectType" db:"object_type"`
	ObjectID         *filesshared.FileObjectID      `json:"objectId" db:"object_id"`
	OriginalFileName string                         `json:"originalFilename" db:"original_filename"`
	FileName         string                         `json:"filename" db:"filename"`
	FolderPath       string                         `json:"folderPath" db:"folder_path"`
	Provider         *string                        `json:"provider" db:"provider"`
	Size             int64                          `json:"size" db:"size"`
	MimeType         string                         `json:"mimeType" db:"mime_type"`
	Moderate         filesshared.FileModerateStatus `json:"moderate" db:"moderate"`
	BlockCause       *string                        `json:"blockCause" db:"block_cause"`
	Name             string                         `json:"name" db:"name"`
	Description      *string                        `json:"description" db:"description"`
	FileType         FileType                       `json:"fileType" db:"file_type"`
	Position         int                            `json:"position" db:"position"`
	IsPrimary        bool                           `json:"isPrimary" db:"is_primary"`
	URL              string                         `json:"url" db:"url"`
	Slug             string                         `json:"slug" db:"slug"`
	Locale           *string                        `json:"locale" db:"locale"`
	Data             *FileData                      `json:"data" db:"data"`
	CreatedAt        time.Time                      `json:"createdAt" db:"created_at"`
	UpdatedAt        time.Time                      `json:"updatedAt" db:"updated_at"`
	DeletedAt        sql.NullTime                   `json:"deletedAt" db:"deleted_at"`
	PublishedAt      sql.NullTime                   `json:"publishedAt" db:"published_at"`
}

// ProviderMetadata представляет дополнительные данные файла в JSON формате.
type ProviderMetadata struct {
	Driver string `json:"driver,omitempty"`
}

// FileData представляет дополнительные данные файла в JSON формате.
type FileData struct {
	Provider ProviderMetadata                                  `json:"provider,omitempty"`
	Width    int                                               `json:"width,omitempty"`
	Height   int                                               `json:"height,omitempty"`
	Alt      string                                            `json:"alt,omitempty"`
	Uploader filesshared.FileUploader                          `json:"fileUploader,omitempty"`
	Presets  map[filesshared.PresetName]filesshared.FilePreset `json:"presets,omitempty"`
}

func (f *File) GetWidth() int {
	if f.Data == nil {
		return 0
	}

	return f.Data.Width
}

func (f *File) GetHeight() int {
	if f.Data == nil {
		return 0
	}

	return f.Data.Height
}

func (f *File) GetData() *FileData {
	if f.Data == nil {
		return &FileData{}
	}

	return f.Data
}

func (f *File) SetData(data *FileData) {
	f.Data = data
}

func (f *File) GetPresetURL(predicate func(filesshared.FilePreset) bool) string {
	data := f.GetData()
	if data == nil || len(data.Presets) == 0 {
		return ""
	}

	for _, preset := range data.Presets {
		if !predicate(preset) {
			continue
		}

		if preset.URL != "" {
			return preset.URL
		}

		if preset.RelativePath != "" {
			return preset.RelativePath
		}
	}

	return ""
}

func (f *File) GetPreviewURL() string {
	return f.GetPresetURL(func(p filesshared.FilePreset) bool {
		return p.IsPreview
	})
}

func (f *File) GetThumbnailPresetURL() string {
	return f.GetPresetURL(func(p filesshared.FilePreset) bool {
		return p.IsThumbnail
	})
}

func (f *File) PreferredPreviewPath() string {
	if f == nil {
		return ""
	}

	if url := f.GetPreviewURL(); url != "" {
		return url
	}

	if url := f.GetThumbnailPresetURL(); url != "" {
		return url
	}

	if thumb := f.GetThumbnailURL(); thumb != "" {
		return thumb
	}

	if !f.IsVideo() {
		return f.GetPublicURL()
	}

	return ""
}

// Value Make the Attrs struct implement the driver.Valuer interface. This method
// simply returns the JSON-encoded representation of the struct.
func (a *FileData) Value() (driver.Value, error) {
	return json.Marshal(a)
}

// Scan Make the Attrs struct implement the sql.Scanner interface. This method
// simply decodes a JSON-encoded value into the struct fields.
func (a *FileData) Scan(value any) error {
	var b []byte
	switch v := value.(type) {
	case string:
		b = []byte(v)
	case []byte:
		b = v
	default:
		return errors.New("type assertion to []byte or string failed")
	}

	return json.Unmarshal(b, &a)
}

// IsImage проверяет, является ли файл изображением.
func (f *File) IsImage() bool {
	return strings.HasPrefix(f.MimeType, "image/")
}

// IsVideo проверяет, является ли файл видео.
func (f *File) IsVideo() bool {
	return strings.HasPrefix(f.MimeType, "video/")
}

// IsDocument проверяет, является ли файл документом.
func (f *File) IsDocument() bool {
	return f.MimeType != "" && (f.MimeType == "application/pdf" ||
		f.MimeType == "application/msword" ||
		f.MimeType == "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
}

func (f *File) GetFullPath() string {
	return f.FolderPath + "/" + f.FileName
}

// GetPublicURL возвращает публичный URL файла.
func (f *File) GetPublicURL() string {
	if f.URL != "" {
		return f.URL
	}

	res, _ := url.JoinPath(f.FolderPath, f.FileName)
	return res
}

// GetThumbnailURL возвращает URL thumbnail'а для изображения.
func (f *File) GetThumbnailURL() string {
	if !f.IsImage() {
		return ""
	}

	// Формируем путь к thumbnail'у
	if f.FolderPath != "" && f.FileName != "" {
		return "/" + f.FolderPath + "/" + f.getThumbnailName()
	}
	return ""
}

// getThumbnailName генерирует имя thumbnail файла.
func (f *File) getThumbnailName() string {
	if f.FileName == "" {
		return ""
	}

	// Добавляем суффикс _thumb перед расширением
	// example.jpg -> example_thumb.jpg
	ext := ""
	name := f.FileName

	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '.' {
			ext = name[i:]
			name = name[:i]
			break
		}
	}

	// return name + "_thumb" + ext
	return name + ext
}

func (f *File) GetFolderPath() string {
	switch f.FileType {
	case FileTypeVideo:
		return "videos"
	case FileTypeAudio:
		return FileTypeAudio.ToString()
	case FileTypePdf:
		return "documents/pdf"
	case FileTypeDocx:
		return "documents/docx"
	case FileTypeImage:
		return "images"
	case FileTypeLink:
		return "links"
	default:
		return "misc"
	}
}
