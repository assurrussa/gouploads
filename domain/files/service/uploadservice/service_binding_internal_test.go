package uploadservice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/domain/files/model"
	"github.com/assurrussa/gouploads/domain/files/shared"
	"github.com/assurrussa/gouploads/internal/identity"
)

type bindingCaptureRepo struct {
	fileRepository
	binding        string
	requireSession bool
}

func (r *bindingCaptureRepo) FinalizeUpload(
	_ context.Context, _, binding string, requireSession bool,
	_ func(context.Context) (model.File, error),
) (model.File, error) {
	r.binding = binding
	r.requireSession = requireSession
	return model.File{ID: 21}, nil
}

func TestHumanFinalizationHashRemainsCompatible(t *testing.T) {
	repo := &bindingCaptureRepo{}
	service := &Service{Options: Options{fileRepo: repo}}
	user := identity.NewUserID()
	request := ReaderRequest{UploaderUUID: user, ManagerID: 12, ObjectType: "admin", ObjectID: 42, FinalizationKey: "server-key"}
	_, err := service.withFinalization(t.Context(), request, "original.png", 10, false, nil)
	require.NoError(t, err)
	legacy := `{"UserUUID":"` + user.String() + `","ManagerID":12,"UserID":0` +
		`,"ObjectType":"admin","ObjectID":42,"ReplacementID":0,"Name":"original.png","Size":10}`
	sum := sha256.Sum256([]byte(legacy))
	require.Equal(t, hex.EncodeToString(sum[:]), repo.binding)
}

func TestStoredFinalizationRequiresTUSSessionOnlyForHumans(t *testing.T) {
	for _, serviceActor := range []bool{false, true} {
		repo := &bindingCaptureRepo{}
		service := &Service{Options: Options{fileRepo: repo}}
		req := ReaderRequest{
			UploaderUUID: identity.NewUserID(), ManagerID: 1,
			ObjectType:      "asset",
			ObjectID:        42,
			FinalizationKey: uuid.NewString(),
		}
		if serviceActor {
			req.UploaderUUID = identity.UserIDNil
			req.ManagerID = 0
			req.Service = &shared.ServicePrincipal{SiteID: uuid.New(), ClientID: "cms"}
		}
		_, err := service.UploadStored(t.Context(), req, UploadedFile{OriginalName: "original.png", Size: 10})
		require.NoError(t, err)
		require.Equal(t, !serviceActor, repo.requireSession)
	}
}
