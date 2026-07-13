package glmoptimizer

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestIsGLM52Request(t *testing.T) {
	tests := []struct {
		name     string
		model    string
		labels   []string
		expected bool
	}{
		{name: "public model", model: "glm-5.2", expected: true},
		{name: "case and space", model: " GLM-5.2 ", expected: true},
		{name: "plan group", model: "deepseek-a4", labels: []string{"glm-5.2"}, expected: true},
		{name: "rate limit plan", model: "deepseek-a4", labels: []string{"", "glm52-pro"}, expected: true},
		{name: "deepseek package", model: "deepseek-a4", labels: []string{"dp-a4"}, expected: false},
		{name: "similar model is not accepted", model: "glm-5.2-preview", expected: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := IsGLM52Request(test.model, test.labels...); got != test.expected {
				t.Fatalf("IsGLM52Request(%q, %q) = %v, want %v", test.model, test.labels, got, test.expected)
			}
		})
	}
}

func TestPrepareRequestLocksGLMPlanBeforeLegacyRouting(t *testing.T) {
	body := []byte(`{"model":"deepseek-a4","messages":[{"role":"user","content":"debug this"}],"temperature":0,"custom_extension":{"keep":true}}`)
	prepared, err := PrepareRequest(body, "glm-5.2", "glm52-pro")
	if err != nil {
		t.Fatalf("PrepareRequest: %v", err)
	}
	if !prepared.IsGLM52 {
		t.Fatal("GLM plan must enter the isolated pipeline")
	}
	if prepared.RequestedModel != "deepseek-a4" {
		t.Fatalf("requested model = %q, want deepseek-a4", prepared.RequestedModel)
	}
	if prepared.LockedModel != ModelGLM52 {
		t.Fatalf("locked model = %q, want %q", prepared.LockedModel, ModelGLM52)
	}

	var locked map[string]json.RawMessage
	if err := json.Unmarshal(prepared.Body, &locked); err != nil {
		t.Fatalf("decode locked body: %v", err)
	}
	var model string
	if err := json.Unmarshal(locked["model"], &model); err != nil {
		t.Fatalf("decode locked model: %v", err)
	}
	if model != ModelGLM52 {
		t.Fatalf("body model = %q, want %q", model, ModelGLM52)
	}
	if _, ok := locked["custom_extension"]; !ok {
		t.Fatal("model lock dropped an unknown provider extension")
	}

	for _, candidate := range []string{"deepseek-v4-flash", "deepseek-v4-pro", "qwen3.7-plus", "glm-4.7"} {
		if got := prepared.SelectModel(candidate); got != ModelGLM52 {
			t.Fatalf("legacy candidate %q escaped model lock as %q", candidate, got)
		}
		if prepared.AllowsModel(candidate) {
			t.Fatalf("fallback candidate %q must be rejected", candidate)
		}
	}
	if !prepared.AllowsModel(ModelGLM52) {
		t.Fatal("same-model fallback must remain allowed")
	}
}

func TestPrepareRequestPreservesDeepseekA4Path(t *testing.T) {
	body := []byte(`{"model":"deepseek-a4","messages":[{"role":"user","content":"hello"}]}`)
	prepared, err := PrepareRequest(body, "dp-a4")
	if err != nil {
		t.Fatalf("PrepareRequest: %v", err)
	}
	if prepared.IsGLM52 {
		t.Fatal("deepseek-a4 package must not enter the GLM pipeline")
	}
	if got := prepared.SelectModel("deepseek-v4-flash"); got != "deepseek-v4-flash" {
		t.Fatalf("legacy A4 route changed to %q", got)
	}
	if !prepared.AllowsModel("qwen3.7-plus") {
		t.Fatal("GLM entry gate must not change legacy A4 fallback policy")
	}
	if string(prepared.Body) != string(body) {
		t.Fatal("non-GLM request body must remain byte-for-byte unchanged")
	}
}

func TestParseRequestValidation(t *testing.T) {
	tests := []struct {
		name string
		body string
		want error
	}{
		{name: "malformed", body: `{`, want: ErrInvalidRequest},
		{name: "missing model", body: `{"messages":[]}`, want: ErrMissingModel},
		{name: "missing messages", body: `{"model":"glm-5.2"}`, want: ErrMissingMessage},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseRequest([]byte(test.body))
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want errors.Is(_, %v)", err, test.want)
			}
		})
	}
}
