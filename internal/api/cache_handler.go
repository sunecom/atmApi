package api

import (
	"fmt"
	"net/http"
	"sort"

	"atmapi/internal/model"
	"atmapi/internal/service"

	"github.com/gin-gonic/gin"
)

// getCacheAnalytics 获取全局缓存报告（从数据库聚合，不依赖内存）
func getCacheAnalytics(c *gin.Context) {
	type globalStats struct {
		TotalRequests int64   `json:"total_requests"`
		CacheHits     int64   `json:"cache_hits"`
		CacheMisses   int64   `json:"cache_misses"`
		TotalInput    int64   `json:"total_input"`
		TotalCached   int64   `json:"total_cached"`
		TotalCost     float64 `json:"total_cost"`
	}

	var gs globalStats
	model.DB.Raw(`
		SELECT
			COUNT(*) as total_requests,
			SUM(CASE WHEN cached_tokens > 0 THEN 1 ELSE 0 END) as cache_hits,
			SUM(CASE WHEN cached_tokens = 0 OR cached_tokens IS NULL THEN 1 ELSE 0 END) as cache_misses,
			COALESCE(SUM(input_tokens), 0) as total_input,
			COALESCE(SUM(cached_tokens), 0) as total_cached,
			COALESCE(SUM(estimated_cost), 0) as total_cost
		FROM usage_logs
		WHERE created_at >= DATE_SUB(NOW(), INTERVAL 7 DAY)
	`).Scan(&gs)

	hitRate := 0.0
	if gs.TotalRequests > 0 {
		hitRate = float64(gs.CacheHits) / float64(gs.TotalRequests)
	}

	// 按 Token 聚合
	type tokenStats struct {
		TokenName    string  `json:"token_name"`
		TotalRequests int64  `json:"total_requests"`
		CacheHits    int64   `json:"cache_hits"`
		CacheMisses  int64   `json:"cache_misses"`
		HitRate      float64 `json:"hit_rate"`
		SavedTokens  int64   `json:"saved_tokens"`
		SavedCost    float64 `json:"saved_cost"`
	}

	var tokens []tokenStats
	model.DB.Raw(`
		SELECT
			token_name,
			COUNT(*) as total_requests,
			SUM(CASE WHEN cached_tokens > 0 THEN 1 ELSE 0 END) as cache_hits,
			SUM(CASE WHEN cached_tokens = 0 OR cached_tokens IS NULL THEN 1 ELSE 0 END) as cache_misses,
			COALESCE(SUM(cached_tokens), 0) as saved_tokens,
			0 as saved_cost
		FROM usage_logs
		WHERE created_at >= DATE_SUB(NOW(), INTERVAL 7 DAY)
		GROUP BY token_name
		ORDER BY total_requests DESC
	`).Scan(&tokens)

	for i := range tokens {
		if tokens[i].TotalRequests > 0 {
			tokens[i].HitRate = float64(tokens[i].CacheHits) / float64(tokens[i].TotalRequests)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"global": gin.H{
			"total_requests":     gs.TotalRequests,
			"cache_hits":         gs.CacheHits,
			"cache_misses":       gs.CacheMisses,
			"hit_rate":           hitRate,
			"total_saved_tokens": gs.TotalCached,
			"total_saved_cost":   gs.TotalCost,
		},
		"tokens": tokens,
	})
}

// getCacheAutoAnalysis 低命中 Token 自动分析
func getCacheAutoAnalysis(c *gin.Context) {
	type tokenAnalysis struct {
		TokenName   string  `json:"token_name"`
		TotalReqs   int64   `json:"total_requests"`
		CacheMisses int64   `json:"cache_misses"`
		HitRate     float64 `json:"hit_rate"`
		MissTokens  int64   `json:"miss_tokens"`
		WastedCost  float64 `json:"wasted_cost"`
		Issue       string  `json:"issue"`
		Suggestion  string  `json:"suggestion"`
	}

	var results []struct {
		TokenName   string  `json:"token_name"`
		TotalReqs   int64   `json:"total_requests"`
		CacheMisses int64   `json:"cache_misses"`
		TotalInput  int64   `json:"total_input"`
		TotalCached int64   `json:"total_cached"`
		TotalCost   float64 `json:"total_cost"`
	}

	timeFilter := "created_at >= DATE_SUB(NOW(), INTERVAL 7 DAY)"

	model.DB.Raw(fmt.Sprintf(`
		SELECT 
			token_name,
			COUNT(*) as total_reqs,
			SUM(CASE WHEN cached_tokens = 0 OR cached_tokens IS NULL THEN 1 ELSE 0 END) as cache_misses,
			COALESCE(SUM(input_tokens), 0) as total_input,
			COALESCE(SUM(cached_tokens), 0) as total_cached,
			COALESCE(SUM(estimated_cost), 0) as total_cost
		FROM usage_logs
		WHERE %s
		GROUP BY token_name
		HAVING total_reqs >= 5
		ORDER BY cache_misses DESC
		LIMIT 50
	`, timeFilter)).Scan(&results)

	analysis := make([]tokenAnalysis, 0, len(results))
	for _, r := range results {
		hitRate := 0.0
		if r.TotalReqs > 0 {
			hitRate = float64(r.TotalReqs-r.CacheMisses) / float64(r.TotalReqs) * 100
		}
		missTokens := r.TotalInput - r.TotalCached
		if missTokens < 0 {
			missTokens = 0
		}

		var issue, suggestion string
		switch {
		case hitRate < 10:
			issue = "几乎无缓存命中"
			suggestion = "检查 system 指令是否固定、是否存在动态时间戳或随机 ID"
		case hitRate < 50:
			issue = "缓存命中率较低"
			suggestion = "建议将不变内容（system/tools/knowledge）集中在前部，动态内容放尾部"
		case hitRate < 80:
			issue = "缓存命中率一般"
			suggestion = "可优化历史对话长度，减少中间插入的动态内容"
		default:
			continue
		}

		analysis = append(analysis, tokenAnalysis{
			TokenName:   r.TokenName,
			TotalReqs:   r.TotalReqs,
			CacheMisses: r.CacheMisses,
			HitRate:     hitRate,
			MissTokens:  missTokens,
			WastedCost:  r.TotalCost * (100 - hitRate) / 100,
			Issue:       issue,
			Suggestion:  suggestion,
		})
	}

	sort.Slice(analysis, func(i, j int) bool {
		return analysis[i].WastedCost > analysis[j].WastedCost
	})

	c.JSON(http.StatusOK, gin.H{"data": analysis})
}

// getCacheTokenDetail 单个 Token 的缓存深度分析
func getCacheTokenDetail(c *gin.Context) {
	tokenName := c.Query("token")
	if tokenName == "" {
		respondError(c, http.StatusBadRequest, ErrInvalidRequest, "token parameter required")
		return
	}

	var stats []struct {
		InputTokens   int64   `json:"input_tokens"`
		CachedTokens  int64   `json:"cached_tokens"`
		TotalTokens   int64   `json:"total_tokens"`
		EstimatedCost float64 `json:"estimated_cost"`
		Model         string  `json:"model"`
	}

	timeFilter := "created_at >= DATE_SUB(NOW(), INTERVAL 7 DAY)"

	model.DB.Raw(fmt.Sprintf(`
		SELECT input_tokens, cached_tokens, total_tokens, estimated_cost, model
		FROM usage_logs
		WHERE token_name = ? AND %s
		ORDER BY id DESC
		LIMIT 200
	`, timeFilter), tokenName).Scan(&stats)

	if len(stats) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"token_name": tokenName,
			"total":      0,
			"miss":       0,
			"avg_input":  0,
			"avg_cached": 0,
			"diagnosis":  []string{"最近 7 天无请求记录"},
			"size_dist":  []interface{}{},
		})
		return
	}

	var totalMiss, totalInput, totalCached int64
	for _, s := range stats {
		if s.CachedTokens == 0 {
			totalMiss++
		}
		totalInput += s.InputTokens
		totalCached += s.CachedTokens
	}

	avgInput := totalInput / int64(len(stats))
	avgCached := totalCached / int64(len(stats))

	diagnosis := make([]string, 0)
	hitRate := float64(len(stats)-int(totalMiss)) / float64(len(stats)) * 100
	if hitRate < 10 {
		diagnosis = append(diagnosis, "🔴 缓存命中率极低（<10%），几乎每次请求都全量计算")
		diagnosis = append(diagnosis, "可能原因：system 指令动态变化、对话历史插入位置不稳定")
	} else if hitRate < 50 {
		diagnosis = append(diagnosis, "🟡 缓存命中率偏低，部分请求享用了缓存")
	} else if hitRate < 80 {
		diagnosis = append(diagnosis, "🟢 缓存命中率良好，大部分请求享用了缓存")
	} else {
		diagnosis = append(diagnosis, "✅ 缓存命中率优秀")
	}

	if avgInput > 10000 && avgCached < avgInput/5 {
		diagnosis = append(diagnosis, fmt.Sprintf("⚠️ 平均输入 %d tokens 但缓存仅 %d tokens，大量上下文未被缓存", avgInput, avgCached))
	}

	type sizeBucket struct {
		Label string `json:"label"`
		Count int64  `json:"count"`
	}
	buckets := []sizeBucket{
		{"<1K", 0},
		{"1-5K", 0},
		{"5-10K", 0},
		{"10-30K", 0},
		{"30K+", 0},
	}
	for _, s := range stats {
		if s.CachedTokens > 0 {
			continue
		}
		t := s.InputTokens
		switch {
		case t < 1000:
			buckets[0].Count++
		case t < 5000:
			buckets[1].Count++
		case t < 10000:
			buckets[2].Count++
		case t < 30000:
			buckets[3].Count++
		default:
			buckets[4].Count++
		}
	}

	sizeDist := make([]map[string]interface{}, 0, len(buckets))
	for _, b := range buckets {
		if b.Count > 0 {
			sizeDist = append(sizeDist, map[string]interface{}{
				"label": b.Label,
				"count": b.Count,
			})
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"token_name": tokenName,
		"total":      len(stats),
		"miss":       totalMiss,
		"avg_input":  avgInput,
		"avg_cached": avgCached,
		"diagnosis":  diagnosis,
		"size_dist":  sizeDist,
	})
}

// analyzePrompt Prompt 结构分析
func analyzePrompt(c *gin.Context) {
	var req struct {
		Messages []map[string]interface{} `json:"messages"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.Messages) == 0 {
		respondError(c, http.StatusBadRequest, ErrInvalidRequest, "messages required")
		return
	}
	if service.GlobalAnalytics == nil {
		respondError(c, http.StatusServiceUnavailable, ErrChannelUnavail, "Analytics not initialized")
		return
	}
	analysis := service.GlobalAnalytics.AnalyzePromptStructure(req.Messages)
	c.JSON(http.StatusOK, gin.H{"data": analysis})
}

