package bus

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Conn keeps one AMQP connection open, declares the core topology on every
// (re)connect and reconnects with backoff.
type Conn struct {
	URL  string
	Topo Topology
	Log  *slog.Logger

	mu   sync.RWMutex
	conn *amqp.Connection
	up   chan struct{} // closed on the first successful connect
	once sync.Once
}

func (c *Conn) init() { c.once.Do(func() { c.up = make(chan struct{}) }) }

// Current returns the open connection or nil.
func (c *Conn) Current() *amqp.Connection {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.conn == nil || c.conn.IsClosed() {
		return nil
	}
	return c.conn
}

// Check reports whether the broker connection is open (readiness).
func (c *Conn) Check(context.Context) error {
	if c.Current() == nil {
		return errors.New("broker connection closed")
	}
	return nil
}

// WaitUp blocks until the first connection is established or ctx is done.
func (c *Conn) WaitUp(ctx context.Context) error {
	c.init()
	select {
	case <-c.up:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Channel opens a channel on the current connection.
func (c *Conn) Channel() (*amqp.Channel, error) {
	conn := c.Current()
	if conn == nil {
		return nil, errors.New("broker connection closed")
	}
	return conn.Channel()
}

func (c *Conn) dial() error {
	conn, err := amqp.DialConfig(c.URL, amqp.Config{Heartbeat: 10 * time.Second, Dial: amqp.DefaultDial(5 * time.Second),
		Properties: amqp.Table{"connection_name": "manufacture-core"}})
	if err != nil {
		return err
	}
	ch, err := conn.Channel()
	if err == nil {
		err = c.Topo.DeclareCore(ch)
		_ = ch.Close()
	}
	if err != nil {
		_ = conn.Close()
		return err
	}
	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()
	return nil
}

// Run maintains the connection until ctx is done.
func (c *Conn) Run(ctx context.Context) {
	c.init()
	attempt := 0
	for ctx.Err() == nil {
		if err := c.dial(); err != nil {
			attempt++
			c.Log.Warn("broker connect failed", "attempt", attempt, "err", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(min(time.Duration(attempt)*time.Second, 15*time.Second)):
			}
			continue
		}
		attempt = 0
		select {
		case <-c.up:
		default:
			close(c.up)
		}
		c.Log.Info("broker connected")
		closed := c.Current().NotifyClose(make(chan *amqp.Error, 1))
		select {
		case <-ctx.Done():
			c.mu.Lock()
			_ = c.conn.Close()
			c.mu.Unlock()
			return
		case err := <-closed:
			c.Log.Warn("broker connection closed; reconnecting", "err", err)
		}
	}
}
