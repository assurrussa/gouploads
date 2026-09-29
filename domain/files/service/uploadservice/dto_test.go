package uploadservice_test

import (
	"mime/multipart"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/domain/files/service/uploadservice"
	"github.com/assurrussa/gouploads/domain/files/shared"
	sharedtypes "github.com/assurrussa/gouploads/internal/identity"
)

func TestSingleRequestValidateAllowsObjectIDEqualToDeletedFileID(t *testing.T) {
	t.Parallel()

	const coincidentID = shared.FileObjectID(3)

	err := (uploadservice.SingleRequest{
		UploaderUUID: sharedtypes.NewUserID(),
		ManagerID:    3,
		FileHeader:   &multipart.FileHeader{},
		ObjectType:   shared.ObjectTypeAdmin,
		ObjectID:     coincidentID,
		DeletedID:    coincidentID,
	}).Validate()

	require.NoError(t, err)
}

func TestReaderRequestValidateAllowsObjectIDEqualToDeletedFileID(t *testing.T) {
	t.Parallel()

	const coincidentID = shared.FileObjectID(3)

	err := (uploadservice.ReaderRequest{
		UploaderUUID: sharedtypes.NewUserID(),
		ManagerID:    3,
		ObjectType:   shared.ObjectTypeAdmin,
		ObjectID:     coincidentID,
		DeletedID:    coincidentID,
	}).Validate()

	require.NoError(t, err)
}
