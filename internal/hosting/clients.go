package hosting

import (
	"sync"

	"github.com/sorokin-vladimir/tele/internal/core"
)

// Clients is every attachment the host has, each seated at the owner of the
// moment. The host seats them all at each owner in turn, tells them all what
// it has to say, and hears any of them ask for the account to end (#208).
type Clients struct {
	mu    sync.Mutex
	all   map[*core.Attachment]chan struct{}
	owner *core.Owner
	// ends carries the requests that the account end, from whichever client
	// made one. One waiting is enough, as it is for a single attachment.
	ends chan core.EndReason
}

// NewClients is a host with no clients and no owner yet.
func NewClients() *Clients {
	return &Clients{
		all:  make(map[*core.Attachment]chan struct{}),
		ends: make(chan core.EndReason, 1),
	}
}

// Add takes att in, seating it at the owner of the moment if there is one,
// and returns what takes it out again.
func (c *Clients) Add(att *core.Attachment) (remove func()) {
	gone := make(chan struct{})
	c.mu.Lock()
	c.all[att] = gone
	if c.owner != nil {
		att.Join(c.owner)
	}
	c.mu.Unlock()
	go c.hearEnds(att, gone)

	var once sync.Once
	return func() {
		once.Do(func() {
			c.mu.Lock()
			delete(c.all, att)
			c.mu.Unlock()
			close(gone)
		})
	}
}

// hearEnds passes att's requests that the account end on, until att is taken
// out.
func (c *Clients) hearEnds(att *core.Attachment, gone <-chan struct{}) {
	for {
		select {
		case <-gone:
			return
		case reason := <-att.EndRequests():
			select {
			case c.ends <- reason:
			default:
			}
		}
	}
}

// Join seats every client at o, the owner now current, and every client added
// from now on.
func (c *Clients) Join(o *core.Owner) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.owner = o
	for att := range c.all {
		att.Join(o)
	}
}

// Leave unseats every client from the owner that ended, telling each why.
// Telling waits on each client to take it, so it is done outside the lock.
func (c *Clients) Leave(ended core.AccountEnded) {
	for _, att := range c.unseat() {
		att.Leave(ended)
	}
}

func (c *Clients) unseat() []*core.Attachment {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.owner = nil
	return c.list()
}

// Tell tells every client something from the host.
func (c *Clients) Tell(v any) {
	c.mu.Lock()
	all := c.list()
	c.mu.Unlock()
	for _, att := range all {
		att.Tell(v)
	}
}

// Count is how many clients there are.
func (c *Clients) Count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.all)
}

// EndRequests is where the host hears that a client asked for the account to
// end.
func (c *Clients) EndRequests() <-chan core.EndReason { return c.ends }

func (c *Clients) list() []*core.Attachment {
	all := make([]*core.Attachment, 0, len(c.all))
	for att := range c.all {
		all = append(all, att)
	}
	return all
}
