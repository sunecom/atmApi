package glmoptimizer

import (
	"bytes"
	"testing"
)

func TestCanonicalizeJSONSemanticEquality(t *testing.T) {
	a, err := CanonicalizeJSON([]byte(`{"b":1.0,"a":{"y":1e0,"x":2},"items":[1,2,3]}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := CanonicalizeJSON([]byte(`{"items":[1.00,2.0,3e0],"a":{"x":2.0,"y":1},"b":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatalf("equivalent objects differ:\n%s\n%s", a, b)
	}
}

func TestCanonicalizeJSONPreservesArrayOrder(t *testing.T) {
	a, _ := CanonicalizeJSON([]byte(`{"items":[1,2,3]}`))
	b, _ := CanonicalizeJSON([]byte(`{"items":[3,2,1]}`))
	if bytes.Equal(a, b) {
		t.Fatalf("array order was lost: %s", a)
	}
}

func TestCanonicalizeJSONRejectsTrailingValue(t *testing.T) {
	if _, err := CanonicalizeJSON([]byte(`{"a":1} {"b":2}`)); err == nil {
		t.Fatal("expected trailing JSON value to be rejected")
	}
}

func TestCanonicalizeJSONKeepsOrdinaryIntegersPlain(t *testing.T) {
	canonical, err := CanonicalizeJSON([]byte(`{"max_tokens":32768,"small":0.001}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(canonical) != `{"max_tokens":32768,"small":0.001}` {
		t.Fatalf("unexpected canonical numbers: %s", canonical)
	}
}
