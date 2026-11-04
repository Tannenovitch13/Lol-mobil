package lcu

import (
	"context"
	"log"
	"net/http"
	"sync"
	"time"
)

// Connector keeps a connection to the League client alive: it finds the
// client, connects to its event socket and reconnects when it restarts.
type Connector struct {
	// LeaguePath is an optional install folder to look in first.
	LeaguePath string
	// Override forces credentials (tests, mock client).
	Override *Credentials

	// OnEvent receives every LCU event.
	OnEvent func(Event)
	// OnStatus is called when the client connects (client != nil) or
	// disconnects (client == nil).
	OnStatus func(client *Client)

	mu     sync.RWMutex
	client *Client
}

// Client returns the connected client, or nil.
func (c *Connector) Client() *Client {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.client
}

func (c *Connector) setClient(cl *Client) {
	c.mu.Lock()
	c.client = cl
	c.mu.Unlock()
	if c.OnStatus != nil {
		c.OnStatus(cl)
	}
}

// Run loops until ctx is cancelled.
func (c *Connector) Run(ctx context.Context) {
	lastScan := time.Time{}
	notFoundLogged := false
	for ctx.Err() == nil {
		var creds Credentials
		var err error
		if c.Override != nil {
			creds = *c.Override
		} else {
			// The process scan spawns PowerShell: do it at most every 10s.
			allowScan := time.Since(lastScan) > 10*time.Second
			if allowScan {
				lastScan = time.Now()
			}
			creds, _, err = Discover(c.LeaguePath, allowScan)
		}
		if err != nil {
			if !notFoundLogged {
				log.Printf("[LCU] Waiting for the League client...")
				notFoundLogged = true
			}
			sleep(ctx, 2*time.Second)
			continue
		}

		client := NewClient(creds)
		if !c.ready(ctx, client) {
			sleep(ctx, 2*time.Second)
			continue
		}
		notFoundLogged = false
		log.Printf("[LCU] Connected to the League client (%s)", creds.Source)

		// Connect the event socket before announcing the client, so no event
		// is missed between the first snapshot and the stream.
		listenCtx, cancel := context.WithCancel(ctx)
		var listenErr error
		go func() {
			defer cancel()
			listenErr = client.Listen(listenCtx, func(ev Event) {
				if c.OnEvent != nil {
					c.OnEvent(ev)
				}
			})
		}()
		// Give the socket a moment to subscribe.
		select {
		case <-listenCtx.Done():
		case <-time.After(400 * time.Millisecond):
		}
		if listenCtx.Err() != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("[LCU] Event socket failed: %v", listenErr)
			sleep(ctx, 2*time.Second)
			continue
		}
		c.setClient(client)
		<-listenCtx.Done()
		c.setClient(nil)
		if ctx.Err() != nil {
			return
		}
		log.Printf("[LCU] Disconnected from the League client (%v)", listenErr)
		sleep(ctx, 2*time.Second)
	}
}

// ready waits until the client API answers (it starts before the API is
// usable).
func (c *Connector) ready(ctx context.Context, client *Client) bool {
	reqCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	resp, err := client.Do(reqCtx, http.MethodGet, "/lol-gameflow/v1/gameflow-phase", nil)
	if err != nil {
		return false
	}
	return resp.Status == http.StatusOK
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
