package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
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
	TypeStepWaiting     Type = "step.waiting"
	TypeStepSucceeded   Type = "step.succeeded"
	TypeStepFailed      Type = "step.failed"
	TypeRunRequested    Type = "run.requested"
	TypeRunRejected     Type = "run.rejected"
	TypeRunStarted      Type = "run.started"
	TypeRunFinished     Type = "run.finished"
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

var blobRefSchema = sync.OnceValues(func() (*jsonschema.Schema, error) {
	if _, err := envelopeSchema(); err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.AssertFormat()
	b, err := schemas.FS().ReadFile("envelope/v2/blob-ref.schema.json")
	if err != nil {
		return nil, err
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	if err := c.AddResource(schemas.BaseURL+"envelope/v2/blob-ref.schema.json", doc); err != nil {
		return nil, err
	}
	return c.Compile(schemas.BaseURL + "envelope/v2/blob-ref.schema.json")
})

// checkBlobRefs validates every object with a $blob key inside the payload
// (input, output, data) against blob-ref.schema.json.
func checkBlobRefs(sch *jsonschema.Schema, v any, path []string, problems *[]string) {
	switch x := v.(type) {
	case map[string]any:
		if _, ok := x["$blob"]; ok {
			if err := sch.Validate(x); err != nil {
				var verr *jsonschema.ValidationError
				if errors.As(err, &verr) {
					for _, l := range leaves(verr) {
						l = strings.TrimPrefix(l, "/")
						if !strings.HasPrefix(l, ":") {
							l = "/" + l
						}
						*problems = append(*problems, "/"+strings.Join(path, "/")+l)
					}
				}
			}
			return
		}
		for k, e := range x {
			checkBlobRefs(sch, e, append(path, k), problems)
		}
	case []any:
		for i, e := range x {
			checkBlobRefs(sch, e, append(path, strconv.Itoa(i)), problems)
		}
	}
}

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
	if err != nil {
		return err
	}
	if m, ok := v.(map[string]any); ok {
		bs, err := blobRefSchema()
		if err != nil {
			panic(err)
		}
		var problems []string
		checkBlobRefs(bs, m["payload"], []string{"payload"}, &problems)
		if len(problems) > 0 {
			slices.Sort(problems)
			return &ValidationError{Problems: problems}
		}
	}
	return nil
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

// StepNamespace is the namespace of step keys: uuid5(NAMESPACE_URL, "https://manufacture.dev/steps").
var StepNamespace = uuid.NewSHA1(uuid.NameSpaceURL, []byte("https://manufacture.dev/steps"))

// StepKey is the stable id of one visit of a step, shared by all its attempts:
// uuid5(StepNamespace, "<run_id>:<stage>:<step_id>:<item_index or ->:<visit>").
func StepKey(runID uuid.UUID, stage, step string, itemIndex *int, visit int) uuid.UUID {
	item := "-"
	if itemIndex != nil {
		item = strconv.Itoa(*itemIndex)
	}
	return uuid.NewSHA1(StepNamespace, []byte(runID.String()+":"+stage+":"+step+":"+item+":"+strconv.Itoa(visit)))
}

// WaitingMessageID is the message id of the seq-th step.waiting of an attempt.
func WaitingMessageID(attemptID uuid.UUID, seq int) uuid.UUID {
	return uuid.NewSHA1(Namespace, []byte(attemptID.String()+":step.waiting:"+strconv.Itoa(seq)))
}

// RunStartedID is the message id of the run.started notification of a run.
func RunStartedID(runID uuid.UUID) uuid.UUID {
	return uuid.NewSHA1(Namespace, []byte(runID.String()+":run.started"))
}

// RunFinishedID is the message id of the run.finished notification of a run.
func RunFinishedID(runID uuid.UUID) uuid.UUID {
	return uuid.NewSHA1(Namespace, []byte(runID.String()+":run.finished"))
}

// RunRequestID is the message id of a run.requested with a dedup key:
// uuid5(Namespace, "run.requested:<pipeline>:<dedup_key>").
func RunRequestID(pipeline, dedupKey string) uuid.UUID {
	return uuid.NewSHA1(Namespace, []byte("run.requested:"+pipeline+":"+dedupKey))
}

// ResultMessageID is the message id a module uses for the outcome of an attempt
// (step.succeeded, step.failed): uuid5(Namespace, "<attempt_id>:<type>").
func ResultMessageID(attemptID uuid.UUID, typ Type) uuid.UUID {
	return uuid.NewSHA1(Namespace, []byte(attemptID.String()+":"+string(typ)))
}
