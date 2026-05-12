package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"time"

	"atmapi/internal/model"
)

// RouteRequestResult 路由请求结果
type RouteRequestResult struct {
	Response    *http.Response
	ChannelName string
}

// RouteRequest 路由请求到合适的渠道
func RouteRequest(targetModel string, requestBody []byte, tokenKey string) (*RouteRequestResult, error) {
	// 1. 验证 token
	token, err := validateToken(tokenKey)
	if err != nil {
		return nil, fmt.Errorf("token 验证失败：%w", err)
	}

	// 2. 检查配额
	if token.RemainQuota == 0 && !token.UnlimitedQuota {
		return nil, fmt.Errorf("token 配额已用完")
	}

	// 3. 获取支持该模型的渠道列表（按优先级排序）
	log.Printf("[路由] 查找模型: %s", targetModel)
	channels, err := getChannelsForModel(targetModel)
	if err != nil {
		return nil, fmt.Errorf("获取渠道失败：%w", err)
	}
	log.Printf("[路由] 找到 %d 个渠道", len(channels))

	if len(channels) == 0 {
		return nil, fmt.Errorf("没有可用渠道")
	}

	// 4. 尝试每个渠道（自动 fallback）
	var lastErr error
	for _, channel := range channels {
		log.Printf("[路由] 尝试渠道: %s (status=%d)", channel.Name, channel.Status)
		if channel.Status != 1 {
			log.Printf("[路由] 渠道 %s 未启用，跳过", channel.Name)
			continue
		}

		mappedModel := applyModelMapping(channel.ModelMapping, targetModel)
		modifiedBody := replaceModelInRequest(requestBody, mappedModel)

		resp, err := sendToChannel(channel, modifiedBody)
		if err != nil {
			lastErr = err
			log.Printf("渠道 %s 失败：%v", channel.Name, err)
			continue
		}

		if resp.StatusCode >= 400 {
			lastErr = fmt.Errorf("渠道 %s 返回 HTTP %d", channel.Name, resp.StatusCode)
			log.Printf("渠道 %s 返回 HTTP %d，触发 fallback", channel.Name, resp.StatusCode)
			resp.Body.Close()
			continue
		}

		// 更新配额
		updateQuota(token, 1)

		return &RouteRequestResult{Response: resp, ChannelName: channel.Name}, nil
	}

	return nil, fmt.Errorf("所有渠道均失败：%w", lastErr)
}

// TestChannel 测试单个渠道连通性
func TestChannel(channel model.Channel) (int, error) {
	testBody := []byte(`{"model":"test","messages":[{"role":"user","content":"hi"}],"max_tokens":1}`)
	url := channel.BaseURL + "/v1/chat/completions"

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(testBody))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+channel.Key)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

// validateToken 验证 token
func validateToken(key string) (*model.Token, error) {
	var token model.Token
	result := model.DB.Where("key = ? AND status = ?", key, 1).First(&token)
	if result.Error != nil {
		return nil, fmt.Errorf("token 不存在或已禁用")
	}

	if token.ExpiredTime > 0 && time.Now().Unix() > token.ExpiredTime {
		token.Status = 3
		model.DB.Save(&token)
		return nil, fmt.Errorf("token 已过期")
	}

	return &token, nil
}

// getChannelsForModel 获取支持指定模型的渠道列表
func getChannelsForModel(modelName string) ([]model.Channel, error) {
	var channels []model.Channel
	result := model.DB.Where(
		"models LIKE ? AND status = ?",
		"%"+modelName+"%",
		1,
	).Order("priority DESC, weight DESC").Find(&channels)

	if result.Error != nil {
		return nil, result.Error
	}
	return channels, nil
}

// applyModelMapping 应用模型映射
func applyModelMapping(mappingJSON string, originalModel string) string {
	if mappingJSON == "" {
		return originalModel
	}
	var mapping map[string]string
	if err := json.Unmarshal([]byte(mappingJSON), &mapping); err != nil {
		return originalModel
	}
	if mapped, ok := mapping[originalModel]; ok {
		return mapped
	}
	return originalModel
}

// replaceModelInRequest 替换请求体中的模型名
func replaceModelInRequest(body []byte, newModel string) []byte {
	var req map[string]interface{}
	if err := json.Unmarshal(body, &req); err != nil {
		return body
	}
	req["model"] = newModel
	newBody, _ := json.Marshal(req)
	return newBody
}

// sendToChannel 发送请求到指定渠道
func sendToChannel(channel model.Channel, body []byte) (*http.Response, error) {
	url := channel.BaseURL + "/v1/chat/completions"
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+channel.Key)
	client := &http.Client{Timeout: 30 * time.Second}
	return client.Do(req)
}

// updateQuota 更新配额
func updateQuota(token *model.Token, count int64) {
	if !token.UnlimitedQuota && token.RemainQuota > 0 {
		token.RemainQuota -= count
	}
	token.AccessedTime = time.Now().Unix()
	model.DB.Save(token)
}

// SortChannels 排序渠道
func SortChannels(channels []model.Channel) {
	sort.Slice(channels, func(i, j int) bool {
		if channels[i].Priority != channels[j].Priority {
			return channels[i].Priority > channels[j].Priority
		}
		return channels[i].Weight > channels[j].Weight
	})
}

// ReadBody 读取请求体
func ReadBody(r *http.Request) ([]byte, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	r.Body = io.NopCloser(bytes.NewBuffer(body))
	return body, nil
}
