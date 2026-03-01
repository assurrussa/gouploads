package testsmatcher

import (
	"strings"

	"go.uber.org/mock/gomock"

	"github.com/assurrussa/gouploads/domain/files/shared"
)

var _ gomock.Matcher = EventPublishMatcher{}

// EventPublishMatcher is intended to be used only in tests.
type EventPublishMatcher struct {
	*BaseMatcher[shared.FileUploadStatusEvent]
}

func NewEventPublishMatcher(name string, expected shared.FileUploadStatusEvent) *EventPublishMatcher {
	return &EventPublishMatcher{
		NewBaseMatcher[shared.FileUploadStatusEvent](name, expected, checkEventPublishFields),
	}
}

//nolint:gocognit // tests
func checkEventPublishFields(
	expected shared.FileUploadStatusEvent,
	got shared.FileUploadStatusEvent,
	unequalFields []string,
) ([]string, bool) {
	if got.EventType != expected.EventType {
		unequalFields = append(unequalFields, "EventType")
	}
	if got.TaskID != expected.TaskID {
		unequalFields = append(unequalFields, "FileID")
	}
	if got.Status != expected.Status {
		unequalFields = append(unequalFields, "Status")
	}
	//nolint:nestif // compare optional file envelope
	if (got.File != nil && expected.File == nil) || (got.File == nil && expected.File != nil) {
		unequalFields = append(unequalFields, "File")
	} else if got.File != nil {
		if got.File.ID != expected.File.ID {
			unequalFields = append(unequalFields, "file.UserID")
		}
		if got.File.FileName != expected.File.FileName {
			unequalFields = append(unequalFields, "file.PresetName")
		}
		if got.File.OriginalName != expected.File.OriginalName {
			unequalFields = append(unequalFields, "file.OriginalName")
		}
		if got.File.URL != expected.File.URL {
			unequalFields = append(unequalFields, "file.URL")
		}
		if got.File.Size != expected.File.Size {
			unequalFields = append(unequalFields, "file.Size")
		}
		if got.File.MimeType != expected.File.MimeType {
			unequalFields = append(unequalFields, "file.MimeType")
		}
		if got.File.Width != expected.File.Width {
			unequalFields = append(unequalFields, "file.Width")
		}
		if got.File.Height != expected.File.Height {
			unequalFields = append(unequalFields, "file.Height")
		}
		if got.File.IsPrimary != expected.File.IsPrimary {
			unequalFields = append(unequalFields, "file.IsPrimary")
		}
	}
	if got.Error != "" && !strings.Contains(got.Error, expected.Error) {
		unequalFields = append(unequalFields, "Error")
	}
	if got.TempFileURL != expected.TempFileURL {
		unequalFields = append(unequalFields, "TempFileURL")
	}
	if got.Metadata["uploadUuid"] != expected.Metadata["uploadUuid"] {
		unequalFields = append(unequalFields, "Metadata.uploadUuid")
	}
	if got.Metadata["objectType"] != expected.Metadata["objectType"] {
		unequalFields = append(unequalFields, "Metadata.objectType")
	}
	if got.Metadata["objectId"] != expected.Metadata["objectId"] {
		unequalFields = append(unequalFields, "Metadata.objectId")
	}
	if got.Metadata["uploaderUuid"] != expected.Metadata["uploaderUuid"] {
		unequalFields = append(unequalFields, "Metadata.uploaderUuid")
	}
	if got.Metadata["jobId"] != expected.Metadata["jobId"] {
		unequalFields = append(unequalFields, "Metadata.jobId")
	}

	return unequalFields, len(unequalFields) == 0
}
