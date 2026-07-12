package api

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"atmapi/internal/model"

	"github.com/gin-gonic/gin"
)

// analyzePromptProfile 分析 Prompt 结构，生成量化 profile
func analyzePromptProfile(messages []map[string]interface{}, tokenID uint, tokenName, modelName string) *model.PromptProfile {
	if len(messages) == 0 {
		return nil
	}

	p := &model.PromptProfile{
		TokenID:   tokenID,
		TokenName: tokenName,
		Model:     modelName,
		MsgCount:  len(messages),
	}

	var totalLen, systemLen int
	prevIsUser := false
	firstMsgContent := ""

	for i, msg := range messages {
		role, _ := msg["role"].(string)
		content, _ := msg["content"].(string)
		contentLen := len(content)
		totalLen += contentLen

		if i == 0 {
			p.FirstRole = role
			firstMsgContent = content
			if role == "system" {
				systemLen = contentLen
			}
		} else if role == "system" {
			systemLen += contentLen
		}

		switch role {
		case "user":
			if !prevIsUser {
				p.HistoryRounds++
			}
			prevIsUser = true
			if i == len(messages)-1 && contentLen > 0 {
				p.NewMsgLen = contentLen
			}
			if strings.Contains(content, "data:image") || strings.Contains(content, "image_url") {
				p.HasImage = true
			}
		case "assistant":
			prevIsUser = false
			if _, hasTC := msg["tool_calls"]; hasTC {
				p.HasToolCalls = true
			}
		case "tool":
			p.HasToolCalls = true
		}
	}

	p.SystemLen = systemLen
	p.HistoryLen = totalLen - systemLen - p.NewMsgLen
	if totalLen > 0 {
		p.SystemRatio = float64(systemLen) / float64(totalLen)
	}

	// 前缀指纹
	preview := firstMsgContent
	if len(preview) > 200 {
		preview = preview[:200]
	}
	hash := sha256.Sum256([]byte(preview))
	p.PrefixHash = fmt.Sprintf("%x", hash)

	// 缓存友好度评分
	score := 100
	if p.FirstRole != "system" {
		score -= 30
	}
	if systemLen == 0 {
		score -= 20
	}
	if totalLen > 50000 {
		score -= 10
	}
	if p.HasToolCalls {
		score -= 15
	}
	if score < 0 {
		score = 0
	}
	p.CacheScore = score

	return p
}

// parsePromptSegments 将 messages 解析为分段列表（合并连续同类型）
func parsePromptSegments(messages []map[string]interface{}) []model.PromptSegment {
	type rawSegment struct {
		segmentType string
		content     string
		isStable    bool
	}

	var raw []rawSegment
	for _, msg := range messages {
		role, _ := msg["role"].(string)
		content, _ := msg["content"].(string)
		isStable := false

		switch role {
		case "system":
			isStable = true
			raw = append(raw, rawSegment{
				segmentType: "system_identity",
				content:     content,
				isStable:    isStable,
			})
		case "user":
			if len(raw) > 0 && raw[len(raw)-1].segmentType == model.SegmentHistory {
				raw[len(raw)-1].content += "\n" + content
			} else {
				raw = append(raw, rawSegment{
					segmentType: model.SegmentHistory,
					content:     content,
					isStable:    false,
				})
			}
		case "assistant":
			if len(raw) > 0 && raw[len(raw)-1].segmentType == model.SegmentHistory {
				raw[len(raw)-1].content += "\n" + content
			} else {
				raw = append(raw, rawSegment{
					segmentType: model.SegmentHistory,
					content:     content,
					isStable:    false,
				})
			}
		case "tool":
			raw = append(raw, rawSegment{
				segmentType: "tool_result",
				content:     content,
				isStable:    false,
			})
		}
	}

	var segments []model.PromptSegment
	for _, r := range raw {
		segments = append(segments, model.PromptSegment{
			SegmentType: r.segmentType,
			Tokens:      estimateTokens(r.content),
			ContentHash: hashContent(r.content),
			IsStable:    r.isStable,
		})
	}
	return segments
}

// estimateTokens 估算内容 token 数（4字符 ≈ 1 token）
func estimateTokens(content string) int {
	return len([]rune(content))/4 + 1
}

// hashContent 生成内容哈希
func hashContent(content string) string {
	if content == "" {
		return ""
	}
	h := sha256.Sum256([]byte(content))
	return fmt.Sprintf("%x", h[:8])
}

// ==================== Prompt API 路由 ====================

// getPromptProfiles 获取 Prompt profile 列表
func getPromptProfiles(c *gin.Context) {
	tokenName := c.Query("token")
	limitStr := c.DefaultQuery("limit", "50")
	limit, _ := strconv.Atoi(limitStr)
	if limit > 200 {
		limit = 200
	}

	var profiles []model.PromptProfile
	q := model.DB.Order("id DESC").Limit(limit)
	if tokenName != "" {
		q = q.Where("token_name = ?", tokenName)
	}
	q.Find(&profiles)
	c.JSON(http.StatusOK, gin.H{"data": profiles})
}

// getPromptSummary 获取 Prompt 分析汇总
func getPromptSummary(c *gin.Context) {
	type Summary struct {
		TokenName      string  `json:"token_name"`
		TotalProfiles  int64   `json:"total_profiles"`
		SampleCount    int64   `json:"sample_count"`
		AvgCacheScore  float64 `json:"avg_cache_score"`
		AvgMsgCount    float64 `json:"avg_msg_count"`
		AvgSysRatio    float64 `json:"avg_system_ratio"`
		AvgHistoryRds  float64 `json:"avg_history_rounds"`
		ImageRatio     float64 `json:"image_ratio"`
		ToolRatio      float64 `json:"tool_call_ratio"`
		UpdatedAt      string  `json:"updated_at"`
	}
	var summaries []Summary
	model.DB.Raw(`
		SELECT 
			token_name,
			COUNT(*) as total_profiles,
			COUNT(*) as sample_count,
			AVG(cache_score) as avg_cache_score,
			AVG(msg_count) as avg_msg_count,
			AVG(system_ratio) * 100 as avg_system_ratio,
			AVG(history_rounds) as avg_history_rounds,
			AVG(CASE WHEN has_image = 1 THEN 1.0 ELSE 0.0 END) as image_ratio,
			AVG(CASE WHEN has_tool_calls = 1 THEN 1.0 ELSE 0.0 END) as tool_call_ratio,
			MAX(created_at) as updated_at
		FROM prompt_profiles
		GROUP BY token_name
		ORDER BY total_profiles DESC
	`).Scan(&summaries)

	c.JSON(http.StatusOK, gin.H{"data": summaries})
}

// getPromptSegments 获取分段数据
func getPromptSegments(c *gin.Context) {
	profileIDStr := c.Query("profile_id")
	if profileIDStr == "" {
		respondError(c, http.StatusBadRequest, ErrInvalidRequest, "profile_id is required")
		return
	}
	profileID, err := strconv.Atoi(profileIDStr)
	if err != nil {
		respondError(c, http.StatusBadRequest, ErrInvalidRequest, "invalid profile_id")
		return
	}

	var segments []model.PromptSegment
	model.DB.Where("profile_id = ?", profileID).Order("position ASC").Find(&segments)
	c.JSON(http.StatusOK, gin.H{"data": segments})
}

// getPromptVectorData 获取矢量图数据
func getPromptVectorData(c *gin.Context) {
	tokenName := c.Query("token")
	if tokenName == "" {
		respondError(c, http.StatusBadRequest, ErrInvalidRequest, "token parameter is required")
		return
	}

	type VectorPoint struct {
		ProfileID   uint    `json:"profile_id"`
		MsgCount    int     `json:"msg_count"`
		SystemRatio float64 `json:"system_ratio"`
		Stability   float64 `json:"stability"`
		CacheScore  int     `json:"cache_score"`
		CreatedAt   string  `json:"created_at"`
	}
	var points []VectorPoint
	model.DB.Raw(`
		SELECT 
			p.id as profile_id,
			p.msg_count,
			p.system_ratio * 100 as system_ratio,
			COALESCE(AVG(CASE WHEN s.is_stable = 1 THEN s.tokens ELSE 0 END) * 100.0 / NULLIF(SUM(s.tokens), 0), 0) as stability,
			p.cache_score,
			p.created_at
		FROM prompt_profiles p
		LEFT JOIN prompt_segments s ON s.profile_id = p.id
		WHERE p.token_name = ?
		GROUP BY p.id
		ORDER BY p.id ASC
	`, tokenName).Scan(&points)

	c.JSON(http.StatusOK, gin.H{
		"token_name": tokenName,
		"points":     points,
	})
}

// getPromptBreakpoints 获取断点分析数据
func getPromptBreakpoints(c *gin.Context) {
	tokenName := c.Query("token")
	if tokenName == "" {
		respondError(c, http.StatusBadRequest, ErrInvalidRequest, "token parameter is required")
		return
	}

	type Breakpoint struct {
		ProfileID         uint    `json:"profile_id"`
		SystemTokens      int     `json:"system_tokens"`
		HistoryTokens     int     `json:"history_tokens"`
		NewMsgTokens      int     `json:"new_msg_tokens"`
		UnstableTokens    int     `json:"unstable_tokens"`
		UnstableRatio     float64 `json:"unstable_ratio"`
		HistoryRounds     int     `json:"history_rounds"`
		SystemRatio       float64 `json:"system_ratio"`
		PredictedCacheHit bool    `json:"predicted_cache_hit"`
		CreatedAt         string  `json:"created_at"`
	}
	var data []Breakpoint
	model.DB.Raw(`
		SELECT 
			p.id as profile_id,
			p.system_len / 4 as system_tokens,
			p.history_len / 4 as history_tokens,
			p.new_msg_len / 4 as new_msg_tokens,
			COALESCE(SUM(CASE WHEN s.is_stable = 0 THEN s.tokens ELSE 0 END), 0) as unstable_tokens,
			COALESCE(SUM(CASE WHEN s.is_stable = 0 THEN s.tokens ELSE 0 END) * 100.0 / NULLIF(SUM(s.tokens), 0), 0) as unstable_ratio,
			p.history_rounds,
			p.system_ratio * 100 as system_ratio,
			CASE WHEN p.cache_score >= 70 THEN TRUE ELSE FALSE END as predicted_cache_hit,
			p.created_at
		FROM prompt_profiles p
		LEFT JOIN prompt_segments s ON s.profile_id = p.id
		WHERE p.token_name = ?
		GROUP BY p.id
		ORDER BY p.id ASC
	`, tokenName).Scan(&data)

	c.JSON(http.StatusOK, gin.H{
		"token_name": tokenName,
		"data":       data,
	})
}
