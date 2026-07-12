package api

import (
	"crypto/sha256"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"sort"
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

		switch role {
		case "system":
			// 判断 system 消息是否稳定
			// 包含动态内容（时间戳、日期、天数、到期提醒等）→ unstable
			isStable := !isDynamicSystemMessage(content)
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
					isStable:    false, // 历史对话不稳定
				})
			}
		case "assistant":
			if len(raw) > 0 && raw[len(raw)-1].segmentType == model.SegmentHistory {
				raw[len(raw)-1].content += "\n" + content
			} else {
				raw = append(raw, rawSegment{
					segmentType: model.SegmentHistory,
					content:     content,
					isStable:    false, // 历史对话不稳定
				})
			}
		case "tool":
			// tool 消息：工具定义（schema）是稳定的，工具结果不稳定
			// 简单判断：如果包含 "function" 或 "parameters" 则认为是 schema
			if strings.Contains(content, "function") || strings.Contains(content, "parameters") {
				raw = append(raw, rawSegment{
					segmentType: "tool_schema",
					content:     content,
					isStable:    true, // 工具定义稳定
				})
			} else if strings.Contains(content, "knowledge") || strings.Contains(content, "context") || strings.Contains(content, "document") {
				// 知识/RAG 内容通常是稳定的
				raw = append(raw, rawSegment{
					segmentType: "knowledge",
					content:     content,
					isStable:    true, // 知识内容稳定
				})
			} else {
				raw = append(raw, rawSegment{
					segmentType: "tool_result",
					content:     content,
					isStable:    false, // 工具结果不稳定
				})
			}
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

// isDynamicSystemMessage 判断 system 消息是否包含动态内容
// 到期预警、时间戳、日期等 → 不稳定
func isDynamicSystemMessage(content string) bool {
	// 到期预警特征
	if strings.Contains(content, "到期提醒") || strings.Contains(content, "套餐到期") {
		return true
	}
	// 包含具体天数/日期（如 "7 天后"、"2026-07-20"）
	if strings.Contains(content, "天后到期") || strings.Contains(content, "天后") {
		return true
	}
	// 包含具体日期格式
	if matched, _ := regexp.MatchString(`\d{4}-\d{2}-\d{2}`, content); matched {
		return true
	}
	return false
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

// SavePromptAnalysis 异步保存 Prompt 分析结果（在 chatCompletions 中调用）
func SavePromptAnalysis(messages []map[string]interface{}, tokenID uint, tokenName, modelName string, inputTokens, cachedTokens int64) {
	log.Printf("[PromptAnalysis] START: token=%s msgs=%d", tokenName, len(messages))
	if len(messages) == 0 {
		log.Printf("[PromptAnalysis] SKIP: messages empty")
		return
	}
	// 写入 profile
	profile := analyzePromptProfile(messages, tokenID, tokenName, modelName)
	if profile == nil {
		log.Printf("[PromptAnalysis] SKIP: profile is nil")
		return
	}
	profile.InputTokens = inputTokens
	profile.CachedTokens = cachedTokens
	if err := model.DB.Create(profile).Error; err != nil {
		log.Printf("[PromptAnalysis] ERROR create profile: %v", err)
		return
	}
	log.Printf("[PromptAnalysis] profile created: id=%d token=%s", profile.ID, tokenName)
	// 写入 segments
	segments := parsePromptSegments(messages)
	for i := range segments {
		segments[i].ProfileID = profile.ID
		segments[i].TokenName = tokenName
		segments[i].Position = i + 1
	}
	if len(segments) > 0 {
		if err := model.DB.Create(&segments).Error; err != nil {
			log.Printf("[PromptAnalysis] ERROR create segments: %v", err)
		} else {
			log.Printf("[PromptAnalysis] segments created: %d", len(segments))
		}
	}
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

// getPromptVectorData 获取矢量图数据（从 prompt_segments 按 position 聚合）
func getPromptVectorData(c *gin.Context) {
	tokenName := c.Query("token")
	if tokenName == "" {
		respondError(c, http.StatusBadRequest, ErrInvalidRequest, "token parameter is required")
		return
	}

	type SegmentAgg struct {
		Position    int     `json:"position"`
		SegmentType string  `json:"segment_type"`
		AvgTokens   float64 `json:"avg_tokens"`
		StableRate  float64 `json:"stable_rate"`
		Breakpoint  bool    `json:"breakpoint"`
	}

	var segments []SegmentAgg
	model.DB.Raw(`
		SELECT 
			position,
			segment_type,
			AVG(tokens) as avg_tokens,
			COALESCE(SUM(CASE WHEN is_stable = 1 THEN 1 ELSE 0 END) * 1.0 / NULLIF(COUNT(*), 0), 0) as stable_rate,
			COALESCE(SUM(CASE WHEN is_stable = 0 THEN 1 ELSE 0 END) * 1.0 / NULLIF(COUNT(*), 0), 0) > 0.5 as breakpoint
		FROM prompt_segments
		WHERE token_name = ?
		GROUP BY position, segment_type
		ORDER BY position ASC, avg_tokens DESC
	`, tokenName).Scan(&segments)

	// 同一 position 去重：只保留 tokens 最大的那个 segment_type
	positionSeen := make(map[int]bool)
	cleaned := make([]SegmentAgg, 0, len(segments))
	for _, s := range segments {
		if !positionSeen[s.Position] {
			positionSeen[s.Position] = true
			cleaned = append(cleaned, s)
		}
	}

	totalCount := len(cleaned)

	// 超过 10 个 position 时，保留 avg_tokens 最大的 10 个
	if len(cleaned) > 10 {
		sort.Slice(cleaned, func(i, j int) bool {
			return cleaned[i].AvgTokens > cleaned[j].AvgTokens
		})
		cleaned = cleaned[:10]
		// 重新按 position 排序
		sort.Slice(cleaned, func(i, j int) bool {
			return cleaned[i].Position < cleaned[j].Position
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"data":        cleaned,
		"token_name":  tokenName,
		"total_count": totalCount,
		"shown_count": len(cleaned),
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
		Position    int     `json:"position"`
		SegmentType string  `json:"segment_type"`
		RiskLevel   string  `json:"risk_level"`
		StableRate  float64 `json:"stable_rate"`
		Impact      string  `json:"impact"`
		Suggestion  string  `json:"suggestion"`
	}

	var segments []struct {
		Position    int     `json:"position"`
		SegmentType string  `json:"segment_type"`
		AvgTokens   float64 `json:"avg_tokens"`
		StableRate  float64 `json:"stable_rate"`
		Breakpoint  bool    `json:"breakpoint"`
	}
	model.DB.Raw(`
		SELECT 
			position,
			segment_type,
			AVG(tokens) as avg_tokens,
			COALESCE(SUM(CASE WHEN is_stable = 1 THEN 1 ELSE 0 END) * 1.0 / NULLIF(COUNT(*), 0), 0) as stable_rate,
			COALESCE(SUM(CASE WHEN is_stable = 0 THEN 1 ELSE 0 END) * 1.0 / NULLIF(COUNT(*), 0), 0) > 0.5 as breakpoint
		FROM prompt_segments
		WHERE token_name = ? AND is_stable = 0
		GROUP BY position, segment_type
		ORDER BY position ASC
	`, tokenName).Scan(&segments)

	// 转换为前端期望的格式，只显示中高风险且去重 position
	data := make([]Breakpoint, 0)
	positionSeen := make(map[int]bool)
	for _, s := range segments {
		if positionSeen[s.Position] {
			continue
		}
		risk := "低"
		if s.AvgTokens > 5000 {
			risk = "高"
		} else if s.AvgTokens > 1000 {
			risk = "中"
		} else {
			continue
		}
		positionSeen[s.Position] = true
		data = append(data, Breakpoint{
			Position:    s.Position,
			SegmentType: s.SegmentType,
			RiskLevel:   risk,
			StableRate:  s.StableRate,
			Impact:      fmt.Sprintf("平均 %d tokens，稳定率 %.1f%%", int(s.AvgTokens), s.StableRate*100),
			Suggestion:  getSegmentSuggestion(s.SegmentType, s.StableRate),
		})
	}

	// 硬限制：最多 10 个，优先高风险 + 大 tokens
	sort.Slice(data, func(i, j int) bool {
		ri, rj := 0, 0
		if data[i].RiskLevel == "高" { ri = 2 } else if data[i].RiskLevel == "中" { ri = 1 }
		if data[j].RiskLevel == "高" { rj = 2 } else if data[j].RiskLevel == "中" { rj = 1 }
		if ri != rj {
			return ri > rj
		}
		return data[i].Position < data[j].Position
	})
	if len(data) > 10 {
		data = data[:10]
	}

	c.JSON(http.StatusOK, gin.H{
		"token_name": tokenName,
		"data":       data,
	})
}

// getSegmentSuggestion 根据分段类型给出优化建议
func getSegmentSuggestion(segmentType string, stableRate float64) string {
	suggestions := map[string]string{
		"system_identity":      "身份指令应保持不变，检查是否包含动态内容",
		"system_rules":         "系统规则应固定，避免每次请求变化",
		"tool_schema":          "工具定义应稳定，检查是否有动态工具列表",
		"knowledge":            "知识资料应固定，避免插入时间戳或随机ID",
		"conversation_history": "历史对话天然不稳定，考虑用滑动窗口压缩",
		"current_input":        "当前输入天然变化，这是正常的缓存断点",
		"runtime_data":         "运行时数据（时间/日期）导致缓存失效",
		"image_data":           "图片数据应缓存，避免重复传输",
		"output_constraint":    "输出约束应固定",
	}
	if s, ok := suggestions[segmentType]; ok {
		return s
	}
	return "检查此分段是否包含动态内容"
}
