package platform

import "context"

// MessageTarget describes where a provider-specific messenger should send a
// new message when there is no originating event.
type MessageTarget struct {
	Provider  string
	TeamID    string
	ChannelID string
	ThreadID  string
	UserID    string
}

// Messenger sends provider-specific chat replies and direct messages through a
// provider-neutral interface.
type Messenger interface {
	Reply(ctx context.Context, event MessageEvent, text string) error
	Send(ctx context.Context, target MessageTarget, text string) error
}
