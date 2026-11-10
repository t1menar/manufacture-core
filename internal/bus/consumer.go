package bus

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/t1menar/manufacture-core/internal/contract"
	"github.com/t1menar/manufacture-core/internal/db"
)

// Inbox outcomes.
const (
	Applied = "applied"
	Stale   = "stale"
	Ignored = "ignored"
	Invalid = "invalid"
)

// Message is a decoded delivery.
type Message struct {
	contract.Envelope
	// UserID is the AMQP user_id property, validated by the broker when set.
	UserID string
}

// Handler applies one message inside the consumer transaction and returns the
// inbox outcome. An error rolls back; the message is retried with backoff and
// dead-lettered after MaxAttempts.
type Handler func(ctx context.Context, tx pgx.Tx, m Message) (string, error)

// Consumer processes a queue with manual acks: inbox insert and effect commit
// first, ack after, so a crash in between redelivers a message the inbox dedups.
type Consumer struct {
	once        sync.Once
	Pool        *pgxpool.Pool
	Name        string // inbox consumer name
	Queue       string
	Handler     Handler
	Prefetch    int
	MaxAttempts int
	Log         *slog.Logger
	// OnInvalid is called for a message that can never be applied (it is dead-lettered).
	OnInvalid func(d amqp.Delivery, err error)

	mu       sync.Mutex
	failures map[string]int
}

func (c *Consumer) defaults() {
	c.once.Do(func() {
		if c.Prefetch == 0 {
			c.Prefetch = 32
		}
		if c.Log == nil {
			c.Log = slog.Default()
		}
		if c.MaxAttempts == 0 {
			c.MaxAttempts = 5
		}
		c.failures = map[string]int{}
	})
}

// Run consumes until ctx is done, re-subscribing after connection loss.
func (c *Consumer) Run(ctx context.Context, conn func() *amqp.Connection) {
	c.defaults()
	for ctx.Err() == nil {
		if cn := conn(); cn != nil {
			if err := c.Consume(ctx, cn); err != nil && ctx.Err() == nil {
				c.Log.Warn("consumer stopped; restarting", "queue", c.Queue, "err", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

// Consume runs until ctx is done or the channel closes.
func (c *Consumer) Consume(ctx context.Context, conn *amqp.Connection) error {
	c.defaults()
	ch, err := conn.Channel()
	if err != nil {
		return err
	}
	shutdown := func(deliveries <-chan amqp.Delivery) {
		go func() { _ = ch.Close() }()
		for range deliveries { // drain so the library does not block
		}
	}
	if err := ch.Qos(c.Prefetch, 0, false); err != nil {
		_ = ch.Close()
		return err
	}
	deliveries, err := ch.Consume(c.Queue, "", false, false, false, false, nil)
	if err != nil {
		_ = ch.Close()
		return err
	}
	for {
		select {
		case <-ctx.Done():
			shutdown(deliveries)
			return nil
		case d, ok := <-deliveries:
			if !ok {
				return errors.New("delivery channel closed")
			}
			if err := c.handle(ctx, &d); err != nil {
				c.Log.Warn("consume", "queue", c.Queue, "err", err)
				if ch.IsClosed() {
					shutdown(deliveries)
					return err
				}
			}
		}
	}
}

func (c *Consumer) handle(ctx context.Context, d *amqp.Delivery) error {
	env, err := contract.Decode(d.Body)
	if err == nil && d.MessageId != "" && d.MessageId != env.MessageID.String() {
		err = fmt.Errorf("AMQP message_id %q differs from the envelope", d.MessageId)
	}
	if err != nil {
		c.Log.Error("invalid message dead-lettered", "queue", c.Queue, "type", d.Type, "err", err)
		if c.OnInvalid != nil {
			c.OnInvalid(*d, err)
		}
		return d.Nack(false, false)
	}
	m := Message{Envelope: env, UserID: d.UserId}
	duplicate := false
	err = db.WithTx(ctx, c.Pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO inbox (consumer, message_id, type, outcome) VALUES ($1, $2, $3, 'applied')
			ON CONFLICT DO NOTHING`, c.Name, env.MessageID, string(env.Type))
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			duplicate = true
			return nil
		}
		outcome, err := c.Handler(ctx, tx, m)
		if err != nil {
			return err
		}
		if outcome != Applied {
			_, err = tx.Exec(ctx, `UPDATE inbox SET outcome = $3 WHERE consumer = $1 AND message_id = $2`, c.Name, env.MessageID, outcome)
		}
		return err
	})
	key := env.MessageID.String()
	if err != nil {
		c.mu.Lock()
		c.failures[key]++
		n := c.failures[key]
		if n >= c.MaxAttempts {
			delete(c.failures, key)
		}
		c.mu.Unlock()
		if n >= c.MaxAttempts {
			c.Log.Error("message failed too often; dead-lettered", "message_id", key, "type", env.Type, "attempts", n, "err", err)
			return d.Nack(false, false)
		}
		delay := min(time.Duration(n)*200*time.Millisecond, 5*time.Second)
		c.Log.Warn("message failed; requeue", "message_id", key, "type", env.Type, "attempt", n, "err", err)
		time.AfterFunc(delay, func() { _ = d.Nack(false, true) })
		return nil
	}
	c.mu.Lock()
	delete(c.failures, key)
	c.mu.Unlock()
	if duplicate {
		c.Log.Debug("duplicate message acked without effect", "message_id", key)
	}
	return d.Ack(false)
}
