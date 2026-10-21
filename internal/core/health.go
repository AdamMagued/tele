package core

import (
	"sync"
	"time"
)

// Link is whether the owner's connection to Telegram is up.
type Link int

const (
	// LinkUnknown: the connection has not come up yet.
	LinkUnknown Link = iota
	LinkUp
	LinkDown
)

// Health is how the account's connection to Telegram is doing, for whoever
// asks the host rather than draws the account: whether the link is up and
// since when, and when anything last arrived (#208).
type Health struct {
	Link      Link
	LinkSince time.Time
	// LastUpdate is when the last update from Telegram arrived, zero when
	// none has.
	LastUpdate time.Time
}

// health is the owner's record of its Health.
type health struct {
	mu sync.Mutex
	h  Health
}

// Health is how the account's connection to Telegram is doing now.
func (o *Owner) Health() Health {
	o.health.mu.Lock()
	defer o.health.mu.Unlock()
	return o.health.h
}

// SetLink records whether the connection to Telegram is up. Saying again what
// is already so changes nothing, so the time it became so stands.
func (o *Owner) SetLink(up bool) {
	link := LinkDown
	if up {
		link = LinkUp
	}
	o.health.mu.Lock()
	defer o.health.mu.Unlock()
	if o.health.h.Link == link {
		return
	}
	o.health.h.Link = link
	o.health.h.LinkSince = o.now()
}

// heard records that an update arrived from Telegram.
func (o *Owner) heard() {
	o.health.mu.Lock()
	o.health.h.LastUpdate = o.now()
	o.health.mu.Unlock()
}
