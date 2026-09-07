package weixin

import (
	"context"
	"log"
	"math/rand"
	"sync"
	"time"
)

const (
	configCacheTTL          = 24 * time.Hour
	configCacheInitialRetry = 2 * time.Second
	configCacheMaxRetry     = time.Hour
)

type configCacheEntry struct {
	typingTicket  string
	everSucceeded bool
	nextFetchAt   time.Time
	retryDelay    time.Duration
}

// ConfigCache caches per-user getconfig results, primarily the typing ticket.
//
// Refresh is spread randomly across a 24h window so a busy gateway does not
// re-fetch every user at once, and failures back off exponentially to an hour
// rather than retrying on every message.
type ConfigCache struct {
	client *Client
	logger *log.Logger

	mu      sync.Mutex
	entries map[string]*configCacheEntry

	now    func() time.Time
	jitter func() float64
}

// NewConfigCache returns a per-user config cache.
func NewConfigCache(client *Client, logger *log.Logger) *ConfigCache {
	return &ConfigCache{
		client:  client,
		logger:  logger,
		entries: make(map[string]*configCacheEntry),
		now:     time.Now,
		jitter:  rand.Float64,
	}
}

// TypingTicket returns the cached typing ticket for a user, refreshing it when
// due. A failure yields an empty ticket rather than an error: the typing
// indicator is cosmetic and must never block a reply.
func (c *ConfigCache) TypingTicket(ctx context.Context, userID, contextToken string) string {
	if c == nil || c.client == nil {
		return ""
	}

	c.mu.Lock()
	entry, exists := c.entries[userID]
	now := c.now()
	shouldFetch := !exists || !now.Before(entry.nextFetchAt)
	c.mu.Unlock()

	if !shouldFetch {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.entries[userID].typingTicket
	}

	resp, err := c.client.GetConfig(ctx, userID, contextToken)
	c.mu.Lock()
	defer c.mu.Unlock()
	entry = c.entries[userID]

	if err == nil && resp.Ret == 0 {
		c.entries[userID] = &configCacheEntry{
			typingTicket:  resp.TypingTicket,
			everSucceeded: true,
			nextFetchAt:   now.Add(time.Duration(c.jitter() * float64(configCacheTTL))),
			retryDelay:    configCacheInitialRetry,
		}
		return resp.TypingTicket
	}

	if err != nil && c.logger != nil {
		c.logger.Printf("getconfig failed for user_id=%s (ignored): %v", userID, err)
	}
	if entry == nil {
		c.entries[userID] = &configCacheEntry{
			nextFetchAt: now.Add(configCacheInitialRetry),
			retryDelay:  configCacheInitialRetry,
		}
		return ""
	}
	delay := entry.retryDelay * 2
	if delay <= 0 {
		delay = configCacheInitialRetry
	}
	if delay > configCacheMaxRetry {
		delay = configCacheMaxRetry
	}
	entry.retryDelay = delay
	entry.nextFetchAt = now.Add(delay)
	return entry.typingTicket
}
