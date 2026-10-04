package weixin

import (
	"fmt"
	"math"
	"sync"
	"time"
)

// StaleTokenPause is how long every API call for an account is held off after
// the server reports an expired bot token.
const StaleTokenPause = time.Hour

// SessionGuard suppresses API traffic for one account after a stale-token
// error. Hammering the server with a dead token is what earns a longer block,
// so the gateway backs off for a full hour instead.
type SessionGuard struct {
	mu         sync.Mutex
	pauseUntil time.Time
	now        func() time.Time
}

// NewSessionGuard returns an unpaused guard.
func NewSessionGuard() *SessionGuard {
	return &SessionGuard{now: time.Now}
}

func (g *SessionGuard) clock() time.Time {
	if g.now == nil {
		return time.Now()
	}
	return g.now()
}

// Pause starts (or restarts) the cooldown window and returns its length.
func (g *SessionGuard) Pause() time.Duration {
	if g == nil {
		return 0
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.pauseUntil = g.clock().Add(StaleTokenPause)
	return StaleTokenPause
}

// Remaining returns the time left in the cooldown window, or zero when active.
func (g *SessionGuard) Remaining() time.Duration {
	if g == nil {
		return 0
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.pauseUntil.IsZero() {
		return 0
	}
	remaining := g.pauseUntil.Sub(g.clock())
	if remaining <= 0 {
		g.pauseUntil = time.Time{}
		return 0
	}
	return remaining
}

// Paused reports whether the account is inside its cooldown window.
func (g *SessionGuard) Paused() bool {
	return g.Remaining() > 0
}

// AssertActive returns an error while the account is paused. Every outbound
// call consults it first.
func (g *SessionGuard) AssertActive() error {
	remaining := g.Remaining()
	if remaining <= 0 {
		return nil
	}
	minutes := int(math.Ceil(remaining.Minutes()))
	return fmt.Errorf("weixin session paused, %d min remaining (errcode %d)", minutes, StaleTokenErrCode)
}
