package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"
)

// CacheAnalytics 缓存分析器 - 企业级缓存优化
type CacheAnalytics struct {
	mu sync.RWMutex
	
	// 全局统计
	totalRequests     int64
	cacheHits         int64
	cacheMisses       int64
	totalSavedTokens  int64  // 缓存命中节省的 token
	totalSavedCost    float64 // 缓存命中节省的成本（USD）
	
	// 按 Token 统计
	tokenStats map[uint]*TokenCacheStats
	
	// Prompt 结构分析
	promptPatterns map[string]*PromptPattern
}

// TokenCacheStats 单个 Token 的缓存统计
type TokenCacheStats struct {
	TokenName     string    `json:"token_name"`
	TotalRequests int64     `json:"total_requests"`
	CacheHits     int64     `json:"cache_hits"`
	CacheMisses   int64     `json:"cache_misses"`
	HitRate       float64   `json:"hit_rate"`       // 0-1
	SavedTokens   int64     `json:"saved_tokens"`
	SavedCost     float64   `json:"saved_cost"`     // USD
	LastAccess    time.Time `json:"last_access"`
}

// PromptPattern Prompt 模式分析
type PromptPattern struct {
	PatternHash string  `json:"pattern_hash"`
	SystemLen   int     `json:"system_len"`   // 系统指令长度
	HistoryLen  int     `json:"history_len"`  // 历史对话长度
	NewMsgLen   int     `json:"new_msg_len"`  // 新消息长度
	HitCount    int64   `json:"hit_count"`
	AvgTokens   int64   `json:"avg_tokens"`
}

var GlobalAnalytics *CacheAnalytics

func InitCacheAnalytics() {
	GlobalAnalytics = &CacheAnalytics{
		tokenStats:     make(map[uint]*TokenCacheStats),
		promptPatterns: make(map[string]*PromptPattern),
	}
}

// RecordRequest 记录一次请求的缓存情况
func (ca *CacheAnalytics) RecordRequest(tokenID uint, tokenName string, 
	promptTokens, cachedTokens int64, isCacheHit bool, savedCost float64) {
	
	ca.mu.Lock()
	defer ca.mu.Unlock()
	
	ca.totalRequests++
	
	// 获取或创建 Token 统计
	stats, exists := ca.tokenStats[tokenID]
	if !exists {
		stats = &TokenCacheStats{
			TokenName: tokenName,
		}
		ca.tokenStats[tokenID] = stats
	}
	
	stats.TotalRequests++
	stats.LastAccess = time.Now()
	
	if isCacheHit || cachedTokens > 0 {
		ca.cacheHits++
		stats.CacheHits++
		
		// 计算节省的 token（缓存部分）
		savedTokens := cachedTokens
		if savedTokens == 0 && isCacheHit {
			// 完全命中，节省全部 prompt tokens
			savedTokens = promptTokens
		}
		
		ca.totalSavedTokens += savedTokens
		stats.SavedTokens += savedTokens
		
		// 计算节省的成本
		if savedCost > 0 {
			ca.totalSavedCost += savedCost
			stats.SavedCost += savedCost
		}
	} else {
		ca.cacheMisses++
		stats.CacheMisses++
	}
	
	// 更新命中率
	if stats.TotalRequests > 0 {
		stats.HitRate = float64(stats.CacheHits) / float64(stats.TotalRequests)
	}
}

// AnalyzePromptStructure 分析 Prompt 结构，给出优化建议
func (ca *CacheAnalytics) AnalyzePromptStructure(messages []map[string]interface{}) *PromptAnalysis {
	analysis := &PromptAnalysis{
		Structure: make([]MessageBlock, 0),
	}
	
	totalLen := 0
	for i, msg := range messages {
		role, _ := msg["role"].(string)
		content, _ := msg["content"].(string)
		contentLen := len(content)
		
		block := MessageBlock{
			Index:  i,
			Role:   role,
			Length: contentLen,
		}
		analysis.Structure = append(analysis.Structure, block)
		totalLen += contentLen
		
		// 记录模式
		if role == "system" {
			analysis.SystemLen += contentLen
		} else if role == "user" || role == "assistant" {
			if i == len(messages)-1 {
				analysis.NewMsgLen = contentLen
			} else {
				analysis.HistoryLen += contentLen
			}
		}
	}
	
	analysis.TotalLen = totalLen
	
	// 计算结构评分
	analysis.Score = ca.calculateStructureScore(analysis)
	
	// 生成优化建议
	analysis.Suggestions = ca.generateSuggestions(analysis)
	
	return analysis
}

// PromptAnalysis Prompt 结构分析结果
type PromptAnalysis struct {
	Structure   []MessageBlock `json:"structure"`
	SystemLen   int            `json:"system_len"`
	HistoryLen  int            `json:"history_len"`
	NewMsgLen   int            `json:"new_msg_len"`
	TotalLen    int            `json:"total_len"`
	Score       int            `json:"score"`        // 0-100，缓存友好度评分
	Suggestions []string       `json:"suggestions"`  // 优化建议
}

type MessageBlock struct {
	Index  int    `json:"index"`
	Role   string `json:"role"`
	Length int    `json:"length"`
}

// calculateStructureScore 计算缓存友好度评分
func (ca *CacheAnalytics) calculateStructureScore(a *PromptAnalysis) int {
	score := 100
	
	// 检查是否有 system 指令
	if a.SystemLen == 0 {
		score -= 20 // 没有 system 指令，扣分
	}
	
	// 检查 system 指令是否在最前面
	if len(a.Structure) > 0 && a.Structure[0].Role != "system" {
		score -= 30 // system 不在最前面，严重影响缓存
	}
	
	// 检查历史对话是否稳定
	if a.HistoryLen > 0 {
		historyRatio := float64(a.HistoryLen) / float64(a.TotalLen)
		if historyRatio > 0.8 {
			score -= 10 // 历史太长，缓存效率低
		}
	}
	
	// 检查新消息占比
	if a.TotalLen > 0 {
		newMsgRatio := float64(a.NewMsgLen) / float64(a.TotalLen)
		if newMsgRatio > 0.5 {
			score -= 15 // 新消息占比太高，缓存效果差
		}
	}
	
	if score < 0 {
		score = 0
	}
	return score
}

// generateSuggestions 生成优化建议
func (ca *CacheAnalytics) generateSuggestions(a *PromptAnalysis) []string {
	suggestions := make([]string, 0)
	
	if a.SystemLen == 0 {
		suggestions = append(suggestions, "建议添加 system 指令，将不变的上下文放在最前面")
	}
	
	if len(a.Structure) > 0 && a.Structure[0].Role != "system" {
		suggestions = append(suggestions, "⚠️ system 指令不在最前面，会严重影响缓存命中率。建议将 system 放在 messages 数组的第一个位置")
	}
	
	if a.HistoryLen > 10000 {
		suggestions = append(suggestions, "历史对话较长（>10K 字符），建议定期压缩或使用滑动窗口，只保留最近 N 轮对话")
	}
	
	if a.TotalLen > 0 {
		cacheableRatio := float64(a.SystemLen+a.HistoryLen) / float64(a.TotalLen)
		if cacheableRatio < 0.5 {
			suggestions = append(suggestions, "可缓存部分占比低于 50%，建议增加 few-shot 示例或固定指令，提高缓存命中率")
		}
	}
	
	if len(suggestions) == 0 {
		suggestions = append(suggestions, "✅ Prompt 结构良好，缓存友好度高")
	}
	
	return suggestions
}

// GetGlobalReport 获取全局缓存报告
func (ca *CacheAnalytics) GetGlobalReport() *CacheReport {
	ca.mu.RLock()
	defer ca.mu.RUnlock()
	
	hitRate := 0.0
	if ca.totalRequests > 0 {
		hitRate = float64(ca.cacheHits) / float64(ca.totalRequests)
	}
	
	return &CacheReport{
		TotalRequests:    ca.totalRequests,
		CacheHits:        ca.cacheHits,
		CacheMisses:      ca.cacheMisses,
		HitRate:          hitRate,
		TotalSavedTokens: ca.totalSavedTokens,
		TotalSavedCost:   ca.totalSavedCost,
		GeneratedAt:      time.Now(),
	}
}

// GetTokenReport 获取单个 Token 的缓存报告
func (ca *CacheAnalytics) GetTokenReport(tokenID uint) *TokenCacheStats {
	ca.mu.RLock()
	defer ca.mu.RUnlock()
	
	stats, exists := ca.tokenStats[tokenID]
	if !exists {
		return &TokenCacheStats{
			TokenName: "unknown",
		}
	}
	return stats
}

// GetAllTokenReports 获取所有 Token 的缓存报告
func (ca *CacheAnalytics) GetAllTokenReports() []*TokenCacheStats {
	ca.mu.RLock()
	defer ca.mu.RUnlock()
	
	reports := make([]*TokenCacheStats, 0, len(ca.tokenStats))
	for _, stats := range ca.tokenStats {
		reports = append(reports, stats)
	}
	return reports
}

// CacheReport 全局缓存报告
type CacheReport struct {
	TotalRequests    int64     `json:"total_requests"`
	CacheHits        int64     `json:"cache_hits"`
	CacheMisses      int64     `json:"cache_misses"`
	HitRate          float64   `json:"hit_rate"`
	TotalSavedTokens int64     `json:"total_saved_tokens"`
	TotalSavedCost   float64   `json:"total_saved_cost"`
	GeneratedAt      time.Time `json:"generated_at"`
}

// EstimateSavings 估算缓存节省的成本
// 假设：缓存读取价格是正常价格的 10%
func EstimateSavings(promptTokens, cachedTokens int64, inputPricePer1K float64) float64 {
	if cachedTokens == 0 {
		return 0
	}
	
	// 正常价格
	normalCost := float64(promptTokens) / 1000 * inputPricePer1K
	
	// 缓存价格（10%）
	cacheCost := float64(cachedTokens) / 1000 * inputPricePer1K * 0.1
	uncachedCost := float64(promptTokens-cachedTokens) / 1000 * inputPricePer1K
	
	// 节省 = 正常价格 - (缓存价格 + 未缓存价格)
	saved := normalCost - (cacheCost + uncachedCost)
	
	return saved
}

// GeneratePromptHash 生成 Prompt 结构哈希（用于模式识别）
func GeneratePromptHash(messages []map[string]interface{}) string {
	// 只提取结构，不提取内容
	structure := make([]map[string]interface{}, len(messages))
	for i, msg := range messages {
		role, _ := msg["role"].(string)
		content, _ := msg["content"].(string)
		structure[i] = map[string]interface{}{
			"role":       role,
			"contentLen": len(content),
		}
	}
	
	data, _ := json.Marshal(structure)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}
