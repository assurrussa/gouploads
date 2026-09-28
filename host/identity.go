package host

import sharedtypes "github.com/assurrussa/gouploads/internal/identity"

type UserID = sharedtypes.UserID

func NewUserID() UserID {
	return sharedtypes.NewUserID()
}

// EventID identifies a live upload notification.
type EventID = sharedtypes.EventID

func NewEventID() EventID { return sharedtypes.NewEventID() }
