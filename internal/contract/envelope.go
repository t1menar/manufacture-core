package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/text/language"
	"golang.org/x/text/message"

	"github.com/t1menar/manufacture-core/schemas"
)

// SchemaVersion is the envelope version this core produces and accepts.
const SchemaVersion = 2

// MaxMessageBytes is the largest envelope; bigger values go to object storage by reference.
const MaxMessageBytes = 256 << 10

// InlineMaxBytes is the largest JSON value an SDK keeps inline; bigger values become {"$blob": ...}.
const InlineMaxBytes = 64 << 10

// ProducerCore is the producer of messages sent by the core.
const ProducerCore = "core"

// Type is a message type.
type Type string

// Message types. Commands flow from the core to modules, events from modules to the core.
const (
	TypeStepRequested   Type = "step.requested"
	TypeStepCancel      Type = "step.cancel"
	TypeSourceTrigger   Type = "source.trigger"
	TypeStepHeartbeat   Type = "step.heartbeat"
	TypeStepSucceeded   Type = "step.succeeded"
	TypeStepFailed      Type = "step.failed"
	TypeRunRequested    Type = "run.requested"
	TypeModuleOnline    Type = "module.online"
	TypeModuleHeartbeat Type = "module.heartbeat"
	TypeModuleOffline   Type = "module.offline"
)

// AttemptRef identifies one attempt of one step.
type AttemptRef struct {
	ID     uuid.UUID `json:"id"`
	Number int       `json:"number"`
}

// Envelope is one broker message.
type Envelope struct {
	MessageID     uuid.UUID       `json:"message_id"`
	Type          Type            `json:"type"`
	SchemaVersion int             `json:"schema_version"`
	OccurredAt    time.Time       `json:"occurred_at"`
	Producer      string          `json:"producer"`
	Traceparent   string          `json:"traceparent,omitempty"`
	RunID         *uuid.UUID      `json:"run_id,omitempty"`
	Attempt       *AttemptRef     `json:"attempt,omitempty"`
	Payload       json.RawMessage `json:"payload"`
}

// ModuleProducer returns the producer value of a module.
func ModuleProducer(module string) string { return "module:" + module }

// ModuleOf returns the module name of a producer, or "" for the core.
func ModuleOf(producer string) string {
	name, _ := strings.CutPrefix(producer, "module:")
	if name == producer {
		return ""
	}
	return name
}

// New builds an envelope with a payload encoded from v.
func New(id uuid.UUID, typ Type, producer string, at time.Time, payload any) (Envelope, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return Envelope{}, fmt.Errorf("encode %s payload: %w", typ, err)
	}
	return Envelope{MessageID: id, Type: typ, SchemaVersion: SchemaVersion, OccurredAt: at.UTC(), Producer: producer, Payload: b}, nil
}

// Decode validates raw against the envelope schema and decodes it.
func Decode(raw []byte) (Envelope, error) {
	if len(raw) > MaxMessageBytes {
		return Envelope{}, fmt.Errorf("message is %d bytes, the limit is %d", len(raw), MaxMessageBytes)
	}
	if err := validate(raw); err != nil {
		return Envelope{}, err
	}
	var e Envelope
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&e); err != nil {
		return Envelope{}, fmt.Errorf("decode envelope: %w", err)
	}
	return e, nil
}

// Encode validates and serializes the envelope, so the core never sends what it would reject.
func Encode(e Envelope) ([]byte, error) {
	b, err := json.Marshal(e)
	if err != nil {
		return nil, err
	}
	if len(b) > MaxMessageBytes {
		return nil, fmt.Errorf("message is %d bytes, the limit is %d", len(b), MaxMessageBytes)
	}
	if err := validate(b); err != nil {
		return nil, err
	}
	return b, nil
}

// DecodePayload decodes the payload strictly into v.
func (e Envelope) DecodePayload(v any) error {
	dec := json.NewDecoder(bytes.NewReader(e.Payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("decode %s payload: %w", e.Type, err)
	}
	return nil
}

var envelopeSchema = sync.OnceValues(func() (*jsonschema.Schema, error) {
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.AssertFormat()
	for _, f := range []string{"envelope/v2/envelope.schema.json", "envelope/v2/blob-ref.schema.json"} {
		b, err := schemas.FS().ReadFile(f)
		if err != nil {
			return nil, err
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
		if err != nil {
			return nil, err
		}
		if err := c.AddResource(schemas.BaseURL+f, doc); err != nil {
			return nil, err
		}
	}
	return c.Compile(schemas.BaseURL + "envelope/v2/envelope.schema.json")
})

var printer = message.NewPrinter(language.English)

// ValidationError lists the schema violations of a message.
type ValidationError struct{ Problems []string }

func (e *ValidationError) Error() string { return "invalid message: " + strings.Join(e.Problems, "; ") }

func validate(raw []byte) error {
	sch, err := envelopeSchema()
	if err != nil { // embedded schema, so a build defect
		panic(err)
	}
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("invalid message: not JSON: %w", err)
	}
	err = sch.Validate(v)
	var verr *jsonschema.ValidationError
	if errors.As(err, &verr) {
		return &ValidationError{Problems: leaves(verr)}
	}
	return err
}

func leaves(e *jsonschema.ValidationError) []string {
	if len(e.Causes) == 0 {
		return []string{fmt.Sprintf("/%s: %s", strings.Join(e.InstanceLocation, "/"), e.ErrorKind.LocalizedString(printer))}
	}
	var out []string
	for _, c := range e.Causes {
		out = append(out, leaves(c)...)
	}
	return out
}

// StableID derives a deterministic message id (UUIDv5), so a retransmitted
// command or event keeps its id and is deduplicated by the consumer.
func StableID(parts ...string) uuid.UUID {
	return uuid.NewSHA1(Namespace, []byte(strings.Join(parts, "\x00")))
}

// Namespace of derived message ids: uuid5(NAMESPACE_URL, "https://manufacture.dev/messages").
var Namespace = uuid.NewSHA1(uuid.NameSpaceURL, []byte("https://manufacture.dev/messages"))

// ResultMessageID is the message id a module uses for the outcome of an attempt
// (step.succeeded, step.failed): uuid5(Namespace, "<attempt_id>:<type>").
func ResultMessageID(attemptID uuid.UUID, typ Type) uuid.UUID {
	return uuid.NewSHA1(Namespace, []byte(attemptID.String()+":"+string(typ)))
}
