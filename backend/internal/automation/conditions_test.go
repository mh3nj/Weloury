package automation

import "testing"

func TestEvaluateConditionsEmptyAlwaysPasses(t *testing.T) {
	ok, reason := evaluateConditions(nil, map[string]any{"foo": "bar"})
	if !ok {
		t.Errorf("ok = false, want true (no conditions); reason = %q", reason)
	}
}

func TestEvaluateConditionsMatch(t *testing.T) {
	conditions := []Condition{{Field: "check_id", Equals: "chk_123"}}
	ok, _ := evaluateConditions(conditions, map[string]any{"check_id": "chk_123", "state": "UNHEALTHY"})
	if !ok {
		t.Error("expected match")
	}
}

func TestEvaluateConditionsMismatch(t *testing.T) {
	conditions := []Condition{{Field: "check_id", Equals: "chk_123"}}
	ok, reason := evaluateConditions(conditions, map[string]any{"check_id": "chk_other"})
	if ok {
		t.Error("expected mismatch")
	}
	if reason == "" {
		t.Error("expected a non-empty reason for a mismatch")
	}
}

func TestEvaluateConditionsMissingFieldFailsClosed(t *testing.T) {
	conditions := []Condition{{Field: "does_not_exist", Equals: "x"}}
	ok, reason := evaluateConditions(conditions, map[string]any{"check_id": "chk_123"})
	if ok {
		t.Error("expected a missing field to fail closed, not match")
	}
	if reason == "" {
		t.Error("expected a non-empty reason")
	}
}

func TestEvaluateConditionsAllMustMatch(t *testing.T) {
	conditions := []Condition{
		{Field: "check_id", Equals: "chk_123"},
		{Field: "state", Equals: "UNHEALTHY"},
	}
	payload := map[string]any{"check_id": "chk_123", "state": "DEGRADED"}
	ok, _ := evaluateConditions(conditions, payload)
	if ok {
		t.Error("expected AND semantics: one mismatching condition should fail the whole set")
	}
}

func TestEvaluateConditionsNilPayload(t *testing.T) {
	conditions := []Condition{{Field: "x", Equals: "y"}}
	ok, _ := evaluateConditions(conditions, nil)
	if ok {
		t.Error("expected a condition against a nil payload to fail closed")
	}
}
