package testsmatcher

import (
	"fmt"
	"reflect"

	"go.uber.org/mock/gomock"

	"github.com/assurrussa/gouploads/domain/files/service/uploadservice"
)

var _ gomock.Matcher = UploadFileMatcher{}

// UploadFileMatcher is intended to be used only in tests.
type UploadFileMatcher struct {
	*BaseMatcher[uploadservice.SingleRequest]
}

func NewUploadFileMatcher(name string, expected uploadservice.SingleRequest) *UploadFileMatcher {
	return &UploadFileMatcher{NewBaseMatcher[uploadservice.SingleRequest](
		name, expected, checkUploadFileMatcher,
	)}
}

//nolint:nestif // tests
func checkUploadFileMatcher(
	expected uploadservice.SingleRequest,
	got uploadservice.SingleRequest,
	unequalFields []string,
) ([]string, bool) {
	if expected.ManagerID != got.ManagerID {
		unequalFields = append(unequalFields, "ManagerID")
	}
	if expected.UserID != got.UserID {
		unequalFields = append(unequalFields, "UserID")
	}
	if expected.ObjectType != got.ObjectType {
		unequalFields = append(unequalFields, "ObjectType")
	}
	if expected.ObjectID != got.ObjectID {
		unequalFields = append(unequalFields, "ObjectID")
	}
	if expected.DeletedID != got.DeletedID {
		unequalFields = append(unequalFields, "DeletedID")
	}
	if !reflect.DeepEqual(expected.Config, got.Config) {
		unequalFields = append(unequalFields, "Config")
	}
	if got.FileHeader != nil && expected.FileHeader != nil {
		if expected.FileHeader.Size != got.FileHeader.Size {
			unequalFields = append(unequalFields, "Config size")
		}
		if reflect.DeepEqual(expected.FileHeader.Header, got.FileHeader.Header) {
			unequalFields = append(unequalFields, "Config headers")
		}
		if expected.FileHeader.Filename != got.FileHeader.Filename {
			unequalFields = append(unequalFields, "Config Filename")
		}
	} else {
		unequalFields = append(unequalFields, "FileHeader invalid")
	}
	if len(expected.AfterJobs) != len(got.AfterJobs) {
		unequalFields = append(unequalFields, "AfterJobs not equal")
	} else {
		for idx, afterJob := range got.AfterJobs {
			expAfterJob := expected.AfterJobs[idx]
			if expAfterJob.Payload != afterJob.Payload {
				unequalFields = append(unequalFields, fmt.Sprintf("AfterJobs[%d]: payload", idx))
			}
			if expAfterJob.UserID != afterJob.UserID {
				unequalFields = append(unequalFields, fmt.Sprintf("AfterJobs[%d]: UserID", idx))
			}
			if expAfterJob.JobName != afterJob.JobName {
				unequalFields = append(unequalFields, fmt.Sprintf("AfterJobs[%d]: JobName", idx))
			}
			if reflect.DeepEqual(expAfterJob.Meta, afterJob.Meta) {
				unequalFields = append(unequalFields, fmt.Sprintf("AfterJobs[%d]: Meta", idx))
			}
		}
	}

	return unequalFields, len(unequalFields) == 0
}
