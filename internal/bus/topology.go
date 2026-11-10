// Package bus is the RabbitMQ side of the core: connection with reconnect, the
// broker topology of docs/messaging.md §2, the transactional outbox relay and the
// inbox-deduplicating consumer (carried over from v1).
package bus

import (
	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/t1menar/manufacture-core/internal/contract"
)

// Topology names exchanges and queues. Prefix isolates tests on a shared broker;
// production uses no prefix, the names of the contract.
type Topology struct {
	Prefix string
}

// Commands is the direct exchange of work and notifications.
func (t Topology) Commands() string { return t.Prefix + contract.ExchangeCommands }

// Control is the topic exchange of messages to every replica.
func (t Topology) Control() string { return t.Prefix + contract.ExchangeControl }

// Events is the topic exchange of module events.
func (t Topology) Events() string { return t.Prefix + contract.ExchangeEvents }

// DLX is the fanout dead-letter exchange.
func (t Topology) DLX() string { return t.Prefix + contract.ExchangeDeadLetter }

// CoreEvents is the queue the engine consumes.
func (t Topology) CoreEvents() string { return t.Prefix + contract.QueueCoreEvents }

// DeadLetters is the queue of dead letters.
func (t Topology) DeadLetters() string { return t.Prefix + contract.QueueDeadLetter }

// WorkQueue of a module.
func (t Topology) WorkQueue(module string) string { return t.Prefix + contract.WorkQueue(module) }

// NotifyQueue of a module.
func (t Topology) NotifyQueue(module string) string { return t.Prefix + contract.NotifyQueue(module) }

func (t Topology) queueArgs() amqp.Table {
	return amqp.Table{
		"x-queue-type":           "quorum",
		"x-delivery-limit":       int64(contract.DeliveryLimit),
		"x-dead-letter-exchange": t.DLX(),
	}
}

// DeclareCore declares the exchanges, the core events queue and the dead-letter queue.
func (t Topology) DeclareCore(ch *amqp.Channel) error {
	for _, x := range []struct{ name, kind string }{
		{t.Commands(), "direct"}, {t.Control(), "topic"}, {t.Events(), "topic"}, {t.DLX(), "fanout"},
	} {
		if err := ch.ExchangeDeclare(x.name, x.kind, true, false, false, false, nil); err != nil {
			return err
		}
	}
	if _, err := ch.QueueDeclare(t.DeadLetters(), true, false, false, false, amqp.Table{"x-queue-type": "quorum"}); err != nil {
		return err
	}
	if err := ch.QueueBind(t.DeadLetters(), "", t.DLX(), false, nil); err != nil {
		return err
	}
	if _, err := ch.QueueDeclare(t.CoreEvents(), true, false, false, false, t.queueArgs()); err != nil {
		return err
	}
	return ch.QueueBind(t.CoreEvents(), "#", t.Events(), false, nil)
}

// DeclareModule declares the work queue of a module and, when notify is set, its
// notification queue, with the arguments SDKs pin as well.
func (t Topology) DeclareModule(ch *amqp.Channel, module string, notify bool) error {
	if _, err := ch.QueueDeclare(t.WorkQueue(module), true, false, false, false, t.queueArgs()); err != nil {
		return err
	}
	if err := ch.QueueBind(t.WorkQueue(module), contract.WorkKey(module), t.Commands(), false, nil); err != nil {
		return err
	}
	if !notify {
		return nil
	}
	if _, err := ch.QueueDeclare(t.NotifyQueue(module), true, false, false, false, t.queueArgs()); err != nil {
		return err
	}
	return ch.QueueBind(t.NotifyQueue(module), contract.NotifyKey(module), t.Commands(), false, nil)
}
