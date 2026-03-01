package testsmatcher

import (
	"strings"

	"go.uber.org/mock/gomock"

	"github.com/assurrussa/gouploads/domain/files/shared"
)

var _ gomock.Matcher = EventPublishDeletedMatcher{}

// EventPublishDeletedMatcher is intended to be used only in tests.
type EventPublishDeletedMatcher struct {
	*BaseMatcher[shared.FileDeletedEvent]
}

func NewEventPublishDeletedMatcher(name string, expected shared.FileDeletedEvent) *EventPublishDeletedMatcher {
	return &EventPublishDeletedMatcher{
		NewBaseMatcher[shared.FileDeletedEvent](name, expected, checkEventPublishDeletedFields),
	}
}

func checkEventPublishDeletedFields(
	expected shared.FileDeletedEvent,
	got shared.FileDeletedEvent,
	unequalFields []string,
) ([]string, bool) {
	if got.EventType != expected.EventType {
		unequalFields = append(unequalFields, "EventType")
	}
	if got.FileID != expected.FileID {
		unequalFields = append(unequalFields, "FileID")
	}
	if got.Status != expected.Status {
		unequalFields = append(unequalFields, "Status")
	}
	if got.Error != "" && !strings.Contains(got.Error, expected.Error) {
		unequalFields = append(unequalFields, "Error")
	}
	if got.FilePath != expected.FilePath {
		unequalFields = append(unequalFields, "FilePath")
	}

	return unequalFields, len(unequalFields) == 0
}
