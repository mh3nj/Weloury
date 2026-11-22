package fwp

import (
	"encoding/json"
	"testing"
)

func TestEnvelopeRoundTripsThroughJSON(t *testing.T) {
	original := Envelope{
		V:      ProtocolVersion,
		Type:   TypeRequest,
		ID:     "req-1",
		Method: "system.ping",
		Params: json.RawMessage(`{"echo":"hi"}`),
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded Envelope
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.Type != TypeRequest || decoded.Method != "system.ping" || decoded.ID != "req-1" {
		t.Fatalf("decoded envelope mismatch: %+v", decoded)
	}
	if string(decoded.Params) != `{"echo":"hi"}` {
		t.Fatalf("Params = %s, want %s", decoded.Params, `{"echo":"hi"}`)
	}
}

func TestErrorEnvelopeCarriesErrorObject(t *testing.T) {
	e := Envelope{
		V:    ProtocolVersion,
		Type: TypeError,
		ID:   "req-2",
		Error: &ErrorObject{
			Code:    ErrUnsupportedMethod,
			Message: "no such method",
		},
	}

	data, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded Envelope
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.Error == nil || decoded.Error.Code != ErrUnsupportedMethod {
		t.Fatalf("decoded error = %+v, want code %s", decoded.Error, ErrUnsupportedMethod)
	}
}

func TestErrorObjectImplementsError(t *testing.T) {
	var err error = &ErrorObject{Code: ErrInternal, Message: "boom"}
	if err.Error() == "" {
		t.Fatal("expected non-empty error string")
	}
}

func TestEventEnvelopeOmitsRequestOnlyFields(t *testing.T) {
	e := Envelope{
		V:       ProtocolVersion,
		Type:    TypeEvent,
		Channel: "system.heartbeat",
		Seq:     1,
		Payload: json.RawMessage(`{"sequence":1}`),
	}

	data, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var asMap map[string]any
	if err := json.Unmarshal(data, &asMap); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	for _, field := range []string{"method", "params", "result", "error", "id"} {
		if _, present := asMap[field]; present {
			t.Fatalf("expected field %q to be omitted from an event envelope, got %v", field, asMap[field])
		}
	}
}
