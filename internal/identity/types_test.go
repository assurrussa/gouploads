package identity_test

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/internal/identity"
)

func TestUserIDWireAndSQLCompatibility(t *testing.T) {
	const value = "368c1d49-713e-4b7f-9e8c-bd2b73b1d274"
	id := identity.MustParse[identity.UserID](value)
	encoded, err := json.Marshal(id)
	require.NoError(t, err)
	require.Equal(t, `"`+value+`"`, string(encoded))
	var decoded identity.UserID
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	require.Equal(t, id, decoded)
	sqlValue, err := id.Value()
	require.NoError(t, err)
	require.Equal(t, value, sqlValue)
	uuidValue := uuid.MustParse(value)
	for _, input := range []any{value, []byte(value), uuidValue[:]} {
		var scanned identity.UserID
		require.NoError(t, scanned.Scan(input))
		require.Equal(t, id, scanned)
	}
	require.True(t, id.Matches(decoded))
	require.False(t, id.Matches(identity.EventID(id)))
	require.Equal(t, id, *id.AsPointer())
	require.NoError(t, id.Validate())
	require.Error(t, decoded.UnmarshalText([]byte("invalid")))
	var zero identity.UserID
	require.True(t, zero.IsZero())
	require.Nil(t, zero.AsPointer())
	require.ErrorIs(t, zero.Validate(), identity.ErrUserIDUuidZero)
	require.NoError(t, zero.Scan(nil))
	require.True(t, zero.IsZero())
}

func TestGeneratedIDsKeepUUIDv4Contract(t *testing.T) {
	ids := []uuid.UUID{uuid.UUID(identity.NewUserID()), uuid.UUID(identity.NewEventID()), uuid.UUID(identity.NewRequestID())}
	for _, id := range ids {
		require.Equal(t, uuid.Version(4), id.Version())
		require.NotEqual(t, uuid.Nil, id)
	}
}
