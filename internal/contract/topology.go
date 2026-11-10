package contract

// Broker topology (RabbitMQ). Exchanges and core queues are declared by the core;
// a module work queue is declared by the core when the module is applied and,
// idempotently with the same arguments, by the SDK on start.
const (
	// ExchangeCommands (direct) routes work to module queues by routing key WorkKey(module).
	ExchangeCommands = "mf.commands"
	// ExchangeControl (topic) fans control messages (step.cancel) out to every replica: ControlKey(module).
	ExchangeControl = "mf.control"
	// ExchangeEvents (topic) carries module events; the routing key is the message type.
	ExchangeEvents = "mf.events"
	// ExchangeDeadLetter (fanout) receives messages that exceeded the delivery limit.
	ExchangeDeadLetter = "mf.dlx"

	// QueueCoreEvents receives every module event (binding "#").
	QueueCoreEvents = "mf.core.events"
	// QueueDeadLetter keeps dead letters for inspection.
	QueueDeadLetter = "mf.dead-letters"

	// DeliveryLimit is x-delivery-limit of quorum queues.
	DeliveryLimit = 5
)

// WorkQueue is the quorum queue a module consumes step.requested and source.trigger from.
func WorkQueue(module string) string { return "mf.module." + module }

// WorkKey is the routing key of work for a module on ExchangeCommands.
func WorkKey(module string) string { return "module." + module }

// ControlKey is the routing key of control messages for a module on ExchangeControl.
func ControlKey(module string) string { return "control." + module }

// ControlQueue is the exclusive, auto-delete queue of one replica bound to ControlKey(module).
func ControlQueue(module, replica string) string { return "mf.control." + module + "." + replica }

// QueueArguments are the arguments of every quorum queue; declarations must match exactly.
func QueueArguments() map[string]any {
	return map[string]any{
		"x-queue-type":           "quorum",
		"x-delivery-limit":       DeliveryLimit,
		"x-dead-letter-exchange": ExchangeDeadLetter,
	}
}

// Environment variables the runtime sets for every module container.
const (
	EnvModuleName   = "MF_MODULE_NAME"
	EnvReplicaID    = "MF_REPLICA_ID"
	EnvAMQPURL      = "MF_AMQP_URL"
	EnvConcurrency  = "MF_CONCURRENCY"
	EnvHealthAddr   = "MF_HEALTH_ADDR"
	EnvS3Endpoint   = "MF_S3_ENDPOINT"
	EnvS3Bucket     = "MF_S3_BUCKET"
	EnvS3AccessKey  = "MF_S3_ACCESS_KEY"
	EnvS3SecretKey  = "MF_S3_SECRET_KEY" //nolint:gosec // variable name, not a credential.
	EnvS3TLS        = "MF_S3_TLS"
	EnvLogLevel     = "MF_LOG_LEVEL"
	EnvOTLPEndpoint = "OTEL_EXPORTER_OTLP_ENDPOINT"
)

// DefaultHealthAddr is where the SDK serves /healthz, /readyz and /metrics.
const DefaultHealthAddr = ":8081"
