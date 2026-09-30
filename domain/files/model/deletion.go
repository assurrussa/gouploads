package model

import (
	"github.com/assurrussa/gouploads/domain/files/shared"
	"github.com/assurrussa/gouploads/internal/identity"
)

// DeletionPlan freezes the exact approved artifacts and follow-up events before
// a file is hidden. Storage deletion happens only after this plan is committed.
type DeletionPlan struct {
	File        File                       `json:"file"`
	Paths       []string                   `json:"paths"`
	AfterEvents []shared.FileEventAfterJob `json:"afterEvents,omitempty"`
	UserID      identity.UserID            `json:"userId"`
	Completed   bool                       `json:"-"`
}
