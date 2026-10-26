package externalconsumer_test

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/host"
)

func TestServiceUploadPublicSurface(t *testing.T) {
	principal := host.ServicePrincipal{SiteID: uuid.New(), ClientID: "cms"}
	req := host.ReaderRequest{
		Service:    &principal,
		ObjectType: "cms_asset",
		ObjectID:   42,
		AfterJobs:  []host.FileEventAfterJob{{JobName: "cms_asset_ready", Payload: `{"generation":"fixture"}`}},
	}
	require.NoError(t, req.Validate())
	require.Equal(t, host.UserTypeService, host.UserType("service"))
	file := host.File{Data: &host.FileData{Service: &principal}}
	data, err := json.Marshal(file)
	require.NoError(t, err)
	var decoded host.File
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.Equal(t, &principal, decoded.GetData().Service)
	require.Nil(t, decoded.UserID)
	require.Nil(t, decoded.ManagerID)
}
