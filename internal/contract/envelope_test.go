package contract

import (
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
	TypeModuleOffline: &ModuleOffline{},
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
