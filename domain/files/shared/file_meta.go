package shared

type PresetName string

func (p PresetName) String() string {
	return string(p)
}

const (
	FilePresetMainName PresetName = "main"
)

type PresetURL string

func (p PresetURL) String() string {
	return string(p)
}

type PresetMeta struct {
	PresetOriginalName string        `json:"fileName"`
	Presets            []PresetShort `json:"presets"`
}

type PresetShort struct {
	PresetName string `json:"fileName"`
	URL        string `json:"url,omitempty"`
}

type FileMetaPreset struct {
	PresetName string                    `validate:"preset"`
	FileName   string                    `json:"fileName"`
	FolderPath string                    `json:"folderPath"`
	Size       int64                     `json:"size"`
	MimeType   string                    `json:"mimeType"`
	URL        string                    `json:"url"`
	Width      int                       `json:"width"`
	Height     int                       `json:"height"`
	Presets    map[PresetName]FilePreset `json:"presets"`
}
type FilePreset struct {
	PresetName     string `json:"presetName"`
	Size           int64  `json:"size"`
	MimeType       string `json:"mimeType,omitempty"`
	URL            string `json:"url,omitempty"`
	Width          int    `json:"width"`
	Height         int    `json:"height"`
	RelativePath   string `json:"relativePath,omitempty"`
	ChecksumSHA256 string `json:"checksumSha256,omitempty"`
	MediaType      string `json:"mediaType,omitempty"`
	IsPreview      bool   `json:"isPreview,omitempty"`
	IsThumbnail    bool   `json:"isThumbnail,omitempty"`
}
