package host

import sharedtypes "github.com/assurrussa/goshared/pkg/sharedtypes"

type UserID = sharedtypes.UserID

func NewUserID() UserID {
	return sharedtypes.NewUserID()
}
