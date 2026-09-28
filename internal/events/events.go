package events

import (
	"context"

	"github.com/assurrussa/gouploads/internal/identity"
)

// Event is the upload notification contract. Delivery is best effort.
type Event interface {
	EventID() identity.EventID
	EventName() string
	Validate() error
}

//go:generate mockgen -source=events.go -destination=mocks/events_mock.gen.go -package=mocks

// Publisher publishes live notifications without owning the transport lifecycle.
type Publisher interface {
	Publish(ctx context.Context, userID identity.UserID, event Event) error
}

// Discard disables optional live notifications. Durable after-jobs are unaffected.
type Discard struct{}

func (Discard) Publish(ctx context.Context, _ identity.UserID, _ Event) error { return ctx.Err() }
