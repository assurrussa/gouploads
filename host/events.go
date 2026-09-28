package host

import "github.com/assurrussa/gouploads/internal/events"

// Event is a best-effort upload notification.
type Event = events.Event

// EventPublisher is supplied by the host; gouploads never closes its transport.
type EventPublisher = events.Publisher
