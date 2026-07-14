package api

import (
	"testing"

	"atmapi/internal/glmoptimizer"
	"atmapi/internal/model"
	"atmapi/internal/service"
)

func TestBuildGLM52UsageLogPrefersReportedCost(t *testing.T) {
	reported := 0.024
	entry := buildGLM52UsageLog(&model.Token{ID: 7, Name: "safe-name", RateLimitGroup: "pro"},
		&service.RouteRequestResult{ChannelID: 9, ChannelName: "OpenRouter", ActualModel: "z-ai/glm-5.2", RetryCount: 1, BreakerState: glmoptimizer.BreakerClosed},
		"glm-5.2", "glm-5.2", model.ProviderUsage{PromptTokens: 1_000, CachedTokens: 600,
			CompletionTokens: 200, ReasoningTokens: 50, UpstreamReportedCost: &reported,
			UpstreamCurrency: "OPENROUTER_CREDITS"}, "Together", "z-ai/glm-5.2", "abcdef123456",
		false, 200, 1234, "success_content", "stop", 250, false, false)

	if entry.CostSource != model.CostSourceUpstreamReported || entry.CostAmount != reported || entry.EstimatedCost != 0 {
		t.Fatalf("reported cost fields = %+v", entry)
	}
	if entry.VisibleOutputTokens != 150 || entry.UpstreamProvider != "Together" || entry.RetryCount != 1 {
		t.Fatalf("audit fields = %+v", entry)
	}
}

func TestBuildGLM52UsageLogDoesNotDuplicateSingleflightCost(t *testing.T) {
	reported := 0.024
	entry := buildGLM52UsageLog(&model.Token{}, &service.RouteRequestResult{ChannelName: "OpenRouter"},
		"glm-5.2", "glm-5.2", model.ProviderUsage{PromptTokens: 1_000, CompletionTokens: 200,
			UpstreamReportedCost: &reported, UpstreamCurrency: "OPENROUTER_CREDITS"}, "", "", "", true,
		200, 100, "success_content", "stop", 0, false, false)
	if entry.CostAmount != 0 || entry.UpstreamReportedCost != 0 || entry.CostSource != model.CostSourceSingleflightShared {
		t.Fatalf("shared follower duplicated upstream cost: %+v", entry)
	}
}

func TestSessionHashPrefixIsStableAndDoesNotExposeSession(t *testing.T) {
	body := []byte(`{"session_id":"session-secret","messages":[]}`)
	a, b := sessionHashPrefix(body), sessionHashPrefix(body)
	if a == "" || a != b || a == "session-secret" || len(a) != 12 {
		t.Fatalf("session hash prefix = %q / %q", a, b)
	}
}
