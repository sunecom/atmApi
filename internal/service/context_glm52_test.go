package service

import (
	"bytes"
	"encoding/json"
	"log"
	"strings"
	"testing"

	"atmapi/internal/glmoptimizer"
)

func TestPrepareGLM52ContextUsesSafePipelineWithoutSemanticReplacement(t *testing.T) {
	request, _ := json.Marshal(map[string]interface{}{
		"model": "glm-5.2",
		"messages": []map[string]interface{}{
			{"role": "system", "content": "policy"},
			{"role": "user", "content": strings.Repeat("历史事实", 220)},
			{"role": "assistant", "content": strings.Repeat("历史结论", 220)},
			{"role": "user", "content": "current"},
		},
	})
	updated, decision, err := PrepareGLM52Context(request, "glm-pro", 2000)
	if err != nil {
		t.Fatal(err)
	}
	if !decision.ShadowGenerated {
		t.Fatalf("shadow was not observed: %+v", decision)
	}
	originalCanonical, _ := glmoptimizer.CanonicalizeJSON(request)
	updatedCanonical, _ := glmoptimizer.CanonicalizeJSON(updated)
	if !bytes.Equal(originalCanonical, updatedCanonical) {
		t.Fatalf("GLM shadow mode changed upstream history:\n%s\n%s", originalCanonical, updatedCanonical)
	}
}

func TestObserveGLM52SummaryShadowDoesNotLogCandidateText(t *testing.T) {
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previous) })
	shadow := &glmoptimizer.SummaryShadow{
		Candidate: "TOP-SECRET-PROMPT-CONTENT", CandidateHash: strings.Repeat("a", 64),
		CandidateRunes: 25, SourceGroups: 2,
	}
	decision := glmoptimizer.ContextDecision{
		PlanName: "glm-pro", OriginalEstimatedTokens: 1000, FinalEstimatedTokens: 900,
		GroupCount: 3, ShadowHashPrefix: "aaaaaaaaaaaa",
	}
	ObserveGLM52SummaryShadow(decision, shadow)
	if strings.Contains(output.String(), shadow.Candidate) {
		t.Fatalf("shadow candidate leaked to logs: %s", output.String())
	}
	if !strings.Contains(output.String(), "candidate_hash=aaaaaaaaaaaa") {
		t.Fatalf("safe shadow metadata missing: %s", output.String())
	}
}
