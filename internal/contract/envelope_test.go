package contract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func files(t *testing.T, dir string) []string {
	t.Helper()
	fs, err := filepath.Glob(filepath.Join("..", "..", "testdata", "envelope", dir, "*.json"))
	if err != nil || len(fs) == 0 {
		t.Fatalf("no testdata in %s: %v", dir, err)
	}
	return fs
}

var payloadTypes = map[Type]any{
	TypeStepRequested: &StepRequested{}, TypeStepCancel: &StepCancel{}, TypeSourceTrigger: &SourceTrigger{},
	TypeStepHeartbeat: &StepHeartbeat{}, TypeStepSucceeded: &StepSucceeded{}, TypeStepFailed: &StepFailed{},
	TypeRunRequested: &RunRequested{}, TypeModuleOnline: &ModuleOnline{}, TypeModuleHeartbeat: &ModuleHeartbeat{},
	TypeModuleOffline: &ModuleOffline{}, TypeStepWaiting: &StepWaiting{}, TypeRunRejected: &RunRejected{},
	TypeRunStarted: &RunStarted{}, TypeRunFinished: &RunFinished{},
}

func TestValidEnvelopes(t *testing.T) {
	seen := map[Type]bool{}
	for _, f := range files(t, "valid") {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		e, err := Decode(raw)
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		seen[e.Type] = true
		p := reflect.New(reflect.TypeOf(payloadTypes[e.Type]).Elem()).Interface()
		if err := e.DecodePayload(p); err != nil {
			t.Errorf("%s: payload: %v", f, err)
		}
		if _, err := Encode(e); err != nil {
			t.Errorf("%s: re-encode: %v", f, err)
		}
	}
	for typ := range payloadTypes {
		if !seen[typ] {
			t.Errorf("no valid testdata for %s", typ)
		}
	}
}

func TestInvalidEnvelopes(t *testing.T) {
	for _, f := range files(t, "invalid") {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Decode(raw); err == nil {
			t.Errorf("%s: accepted", f)
		}
	}
}

func TestEncodeRejectsInvalid(t *testing.T) {
	e, err := New(uuid.New(), TypeStepFailed, ProducerCore, time.Now(), StepFailed{Class: "nope", Message: "x"})
	if err != nil {
		t.Fatal(err)
	}
	run := uuid.New()
	e.RunID, e.Attempt = &run, &AttemptRef{ID: uuid.New(), Number: 1}
	if _, err := Encode(e); err == nil || !strings.Contains(err.Error(), "/payload/class") {
		t.Errorf("err = %v", err)
	}
	big := strings.Repeat("x", MaxMessageBytes+1)
	if _, err := Decode([]byte(big)); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Errorf("size: %v", err)
	}
}

func TestStableIDAndProducer(t *testing.T) {
	first, again := StableID("a", "b"), StableID("a", "b")
	if first != again || first == StableID("ab") {
		t.Error("StableID must be deterministic and separate parts")
	}
	if Namespace.String() != "32e7ee34-6c60-51c1-a912-93b117ba36cd" {
		t.Errorf("Namespace changed to %s: SDKs hard-code it", Namespace)
	}
	attempt := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	if ResultMessageID(attempt, TypeStepSucceeded) == ResultMessageID(attempt, TypeStepFailed) {
		t.Error("result ids must differ by type")
	}
	if got := ResultMessageID(attempt, TypeStepSucceeded).String(); got != "22c4fe18-bb14-53d2-a566-964fc52467ad" {
		t.Errorf("ResultMessageID = %s; the derivation is part of the contract", got)
	}
	if ModuleOf(ModuleProducer("hello")) != "hello" || ModuleOf(ProducerCore) != "" {
		t.Error("producer helpers")
	}
	if WorkQueue("m") != "mf.module.m" || ControlQueue("m", "r1") != "mf.control.m.r1" {
		t.Error("topology names changed: this breaks deployed modules")
	}
}

func readVectors(t *testing.T, name string, v any) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "envelope", name))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatal(err)
	}
}

// TestStepKeyVectors checks testdata/envelope/step_key.json, which SDKs test against too.
func TestStepKeyVectors(t *testing.T) {
	var v struct {
		StepNamespace uuid.UUID `json:"step_namespace"`
		Cases         []struct {
			RunID     uuid.UUID `json:"run_id"`
			Stage     string    `json:"stage"`
			Step      string    `json:"step"`
			ItemIndex *int      `json:"item_index"`
			Visit     int       `json:"visit"`
			StepKey   uuid.UUID `json:"step_key"`
		} `json:"cases"`
	}
	readVectors(t, "step_key.json", &v)
	if v.StepNamespace != StepNamespace || StepNamespace.String() != "fa1e7bfd-2c37-5637-bb2e-15cdf57e7e2d" {
		t.Fatalf("step namespace %s", StepNamespace)
	}
	for _, c := range v.Cases {
		if got := StepKey(c.RunID, c.Stage, c.Step, c.ItemIndex, c.Visit); got != c.StepKey {
			t.Errorf("StepKey(%s, %s, %s, %v, %d) = %s, want %s", c.RunID, c.Stage, c.Step, c.ItemIndex, c.Visit, got, c.StepKey)
		}
	}
}

// TestMessageIDVectors checks testdata/envelope/message_ids.json.
func TestMessageIDVectors(t *testing.T) {
	var v struct {
		MessageNamespace uuid.UUID `json:"message_namespace"`
		Cases            []struct {
			Kind      string    `json:"kind"`
			AttemptID uuid.UUID `json:"attempt_id"`
			RunID     uuid.UUID `json:"run_id"`
			Type      Type      `json:"type"`
			Seq       int       `json:"seq"`
			Pipeline  string    `json:"pipeline"`
			DedupKey  string    `json:"dedup_key"`
			MessageID uuid.UUID `json:"message_id"`
		} `json:"cases"`
	}
	readVectors(t, "message_ids.json", &v)
	if v.MessageNamespace != Namespace {
		t.Fatal("message namespace differs from the vectors")
	}
	for _, c := range v.Cases {
		var got uuid.UUID
		switch c.Kind {
		case "result":
			got = ResultMessageID(c.AttemptID, c.Type)
		case "waiting":
			got = WaitingMessageID(c.AttemptID, c.Seq)
		case "run_started":
			got = RunStartedID(c.RunID)
		case "run_finished":
			got = RunFinishedID(c.RunID)
		case "run_requested":
			got = RunRequestID(c.Pipeline, c.DedupKey)
		default:
			t.Fatalf("unknown kind %q", c.Kind)
		}
		if got != c.MessageID {
			t.Errorf("%s: got %s, want %s", c.Kind, got, c.MessageID)
		}
	}
}
