package ipc

import (
	"context"
	"encoding/json"
	"testing"

	"weloury/pkg/fwp"
)

func TestDispatchCallsRegisteredHandler(t *testing.T) {
	r := NewRouter()
	r.Handle("echo", func(ctx context.Context, params json.RawMessage) (any, error) {
		var in struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(params, &in); err != nil {
			return nil, fwp.NewError(fwp.ErrInvalidParams, err.Error())
		}
		return map[string]string{"echoed": in.Message}, nil
	})

	req := fwp.Envelope{
		V: fwp.ProtocolVersion, Type: fwp.TypeRequest, ID: "1",
		Method: "echo", Params: json.RawMessage(`{"message":"hi"}`),
	}
	resp := r.Dispatch(context.Background(), req)

	if resp.Type != fwp.TypeResponse {
		t.Fatalf("Type = %s, want response", resp.Type)
	}
	if resp.ID != "1" {
		t.Fatalf("ID = %s, want request ID to be preserved", resp.ID)
	}
	var out struct {
		Echoed string `json:"echoed"`
	}
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if out.Echoed != "hi" {
		t.Fatalf("Echoed = %q, want %q", out.Echoed, "hi")
	}
}

func TestDispatchUnknownMethodReturnsStructuredError(t *testing.T) {
	r := NewRouter()
	resp := r.Dispatch(context.Background(), fwp.Envelope{ID: "2", Method: "does.not.exist"})
	if resp.Type != fwp.TypeError || resp.Error == nil || resp.Error.Code != fwp.ErrUnsupportedMethod {
		t.Fatalf("resp = %+v, want UNSUPPORTED_METHOD error", resp)
	}
}

func TestDispatchWrapsPlainErrorAsInternal(t *testing.T) {
	r := NewRouter()
	r.Handle("boom", func(ctx context.Context, params json.RawMessage) (any, error) {
		return nil, errPlain{"boom"}
	})
	resp := r.Dispatch(context.Background(), fwp.Envelope{ID: "3", Method: "boom"})
	if resp.Error == nil || resp.Error.Code != fwp.ErrInternal {
		t.Fatalf("resp.Error = %+v, want INTERNAL", resp.Error)
	}
}

type errPlain struct{ msg string }

func (e errPlain) Error() string { return e.msg }

func TestHandleTwicePanics(t *testing.T) {
	r := NewRouter()
	noop := func(ctx context.Context, params json.RawMessage) (any, error) { return nil, nil }
	r.Handle("dup", noop)

	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on duplicate Handle registration")
		}
	}()
	r.Handle("dup", noop)
}
