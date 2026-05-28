package hosttest

import (
	"fmt"
	"reflect"

	"go.uber.org/mock/gomock"

	"github.com/assurrussa/gouploads/host"
)

var _ gomock.Matcher = &ListenResizeRequestMatcher{}

type ListenResizeRequestMatcher struct {
	name          string
	expected      host.ListenResizeRequest
	unequalFields []string
}

func NewListenResizeRequestMatcher(name string, expected host.ListenResizeRequest) *ListenResizeRequestMatcher {
	return &ListenResizeRequestMatcher{
		name:          name,
		expected:      expected,
		unequalFields: make([]string, 0, 8),
	}
}

func (m *ListenResizeRequestMatcher) Matches(x any) bool {
	got, ok := x.(host.ListenResizeRequest)
	if !ok {
		return false
	}

	m.unequalFields = m.unequalFields[:0]
	if got.ExternalID != m.expected.ExternalID {
		m.unequalFields = append(m.unequalFields, "ExternalID")
	}
	if got.Status != m.expected.Status {
		m.unequalFields = append(m.unequalFields, "Status")
	}
	if got.Error != m.expected.Error {
		m.unequalFields = append(m.unequalFields, "Error")
	}
	if got.Attempt != m.expected.Attempt {
		m.unequalFields = append(m.unequalFields, "Attempt")
	}
	if !got.Timestamp.Equal(m.expected.Timestamp) {
		m.unequalFields = append(m.unequalFields, "Timestamp")
	}
	if !metadataEqual(got.Metadata, m.expected.Metadata) {
		m.unequalFields = append(m.unequalFields, "Metadata")
	}
	if !artifactsEqual(got.Artifacts, m.expected.Artifacts) {
		m.unequalFields = append(m.unequalFields, "Artifacts")
	}

	return len(m.unequalFields) == 0
}

func (m *ListenResizeRequestMatcher) String() string {
	return fmt.Sprintf("listen resize request %s diff=%v expected=%v", m.name, m.unequalFields, m.expected)
}

func artifactsEqual(got []host.ResizeArtifact, expected []host.ResizeArtifact) bool {
	if len(got) != len(expected) {
		return false
	}

	for i := range expected {
		if got[i].Preset != expected[i].Preset ||
			got[i].URL != expected[i].URL ||
			got[i].MediaType != expected[i].MediaType ||
			got[i].ContentType != expected[i].ContentType ||
			got[i].Size != expected[i].Size ||
			!got[i].ExpireAt.Equal(expected[i].ExpireAt) ||
			!metadataEqual(got[i].Metadata, expected[i].Metadata) {
			return false
		}
	}

	return true
}

func metadataEqual(got map[string]any, expected map[string]any) bool {
	if len(got) != len(expected) {
		return false
	}

	for key, exp := range expected {
		value, ok := got[key]
		if !ok {
			return false
		}
		if reflect.DeepEqual(value, exp) {
			continue
		}
		if fmt.Sprint(value) != fmt.Sprint(exp) {
			return false
		}
	}

	return true
}
