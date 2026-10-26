package shared_test

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/domain/files/shared"
	"github.com/assurrussa/gouploads/internal/identity"
)

func TestServiceUploaderCannotMixHumanIdentity(t *testing.T) {
	principal := &shared.ServicePrincipal{SiteID: uuid.New(), ClientID: "cms"}
	uploader := shared.FileUploader{Service: principal, Type: shared.UserTypeService}
	require.NoError(t, uploader.Validate())
	uploader.UserID = 1
	require.Error(t, uploader.Validate())
	uploader.UserID = 0
	uploader.UserUUID = identity.NewUserID()
	require.Error(t, uploader.Validate())
	uploader.UserID = 1
	uploader.Type = shared.UserTypeAdmin
	require.Error(t, uploader.Validate())
	uploader.Service = nil
	require.NoError(t, uploader.Validate())
	data, err := json.Marshal(uploader)
	require.NoError(t, err)
	require.NotContains(t, string(data), `"service"`)
	for _, client := range []string{"", " cms", "cms\n", "cms\x00client", string([]byte{0xff}), string([]byte{0xfe})} {
		require.Error(t, (shared.ServicePrincipal{SiteID: uuid.New(), ClientID: client}).Validate())
	}
}
