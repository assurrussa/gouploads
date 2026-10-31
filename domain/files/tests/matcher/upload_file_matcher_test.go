package testsmatcher_test

import (
	"fmt"
	"mime/multipart"
	"net/textproto"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/domain/files/service/uploadservice"
	testsmatcher "github.com/assurrussa/gouploads/domain/files/tests/matcher"
)

func TestUploadFileHeaderMismatchPreservesExistingSemantics(t *testing.T) {
	for _, test := range []struct {
		name     string
		expected *multipart.FileHeader
		got      *multipart.FileHeader
		fields   []string
	}{
		{name: "both nil", fields: []string{"FileHeader invalid"}},
		{name: "expected nil", got: &multipart.FileHeader{}, fields: []string{"FileHeader invalid"}},
		{name: "got nil", expected: &multipart.FileHeader{}, fields: []string{"FileHeader invalid"}},
		{
			name:     "equal headers retain legacy mismatch",
			expected: &multipart.FileHeader{Filename: "a", Size: 1, Header: textproto.MIMEHeader{"X-Test": {"a"}}},
			got:      &multipart.FileHeader{Filename: "a", Size: 1, Header: textproto.MIMEHeader{"X-Test": {"a"}}},
			fields:   []string{"Config headers"},
		},
		{
			name:     "different fields retain order and legacy header comparison",
			expected: &multipart.FileHeader{Filename: "a", Size: 1, Header: textproto.MIMEHeader{"X-Test": {"a"}}},
			got:      &multipart.FileHeader{Filename: "b", Size: 2, Header: textproto.MIMEHeader{"X-Test": {"b"}}},
			fields:   []string{"Config size", "Config Filename"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			matcher := testsmatcher.NewUploadFileMatcher("header", uploadservice.SingleRequest{FileHeader: test.expected})
			require.False(t, matcher.Matches(uploadservice.SingleRequest{FileHeader: test.got}))
			require.Contains(t, matcher.String(), fmt.Sprintf("diff[%v]", test.fields))
		})
	}
}
