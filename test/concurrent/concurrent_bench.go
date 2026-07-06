package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"time"
)

const (
	apiURL   = "http://localhost:3002/v1/chat/completions"
	apiToken = "atm-1778491177466775297"
	model    = "glm-5.2" // 走聚合组路由
)

type Result struct {
	Success    bool
	StatusCode int
	Latency    time.Duration
	Error      string
}

func main() {
	concurrencyLevels := []int{1, 5, 10, 20, 30, 50}

	fmt.Println("╔════════════════════════════════════════════════════════════════╗")
	fmt.Println("║           atmApi 并发压力测试 (模型: glm-5.2)                  ║")
	fmt.Println("╚════════════════════════════════════════════════════════════════╝")
	fmt.Println()

	for _, concurrency := range concurrencyLevels {
		requestCount := concurrency * 2
		fmt.Printf("▶ 测试并发=%d, 请求数=%d\n", concurrency, requestCount)
		runTest(concurrency, requestCount)
		fmt.Println()
	}
}

func runTest(concurrency, totalRequests int) {
	var (
		wg           sync.WaitGroup
		mu           sync.Mutex
		successCount int
		failedCount  int
		totalLatency time.Duration
		results      []Result
	)

	sem := make(chan struct{}, concurrency)
	startTime := time.Now()

	for i := 0; i < totalRequests; i++ {
		wg.Add(1)
		sem <- struct{}{}

		go func(reqID int) {
			defer wg.Done()
			defer func() { <-sem }()

			result := sendRequest(reqID)

			mu.Lock()
			results = append(results, result)
			if result.Success {
				successCount++
				totalLatency += result.Latency
			} else {
				failedCount++
			}
			mu.Unlock()
		}(i)
	}

	wg.Wait()
	elapsed := time.Since(startTime)

	var avgLatency time.Duration
	if successCount > 0 {
		avgLatency = totalLatency / time.Duration(successCount)
	}

	var latencies []time.Duration
	for _, r := range results {
		if r.Success {
			latencies = append(latencies, r.Latency)
		}
	}
	p95 := calculateP95(latencies)

	successRate := float64(successCount) / float64(totalRequests) * 100
	throughput := float64(successCount) / elapsed.Seconds()

	fmt.Printf("  ├─ 成功: %d/%d (%.1f%%)\n", successCount, totalRequests, successRate)
	fmt.Printf("  ├─ 失败: %d\n", failedCount)
	fmt.Printf("  ├─ 吞吐: %.2f req/s\n", throughput)
	fmt.Printf("  ├─ 平均延迟: %v\n", avgLatency.Round(time.Millisecond))
	fmt.Printf("  └─ P95延迟: %v\n", p95)

	if failedCount > 0 {
		fmt.Println("  ── 失败原因（前3个）──")
		count := 0
		for _, r := range results {
			if !r.Success && count < 3 {
				fmt.Printf("    [%d] HTTP %d: %s\n", count+1, r.StatusCode, r.Error)
				count++
			}
		}
	}
}

func sendRequest(reqID int) Result {
	start := time.Now()

	reqBody := map[string]interface{}{
		"model":      model,
		"max_tokens": 50,
		"messages": []map[string]string{
			{"role": "user", "content": "用一句话回答：1+1等于几？"},
		},
	}
	jsonData, _ := json.Marshal(reqBody)

	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return Result{Success: false, Latency: time.Since(start), Error: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiToken)

	resp, err := client.Do(req)
	latency := time.Since(start)

	if err != nil {
		return Result{Success: false, Latency: latency, Error: err.Error()}
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == 200 {
		var result map[string]interface{}
		if err := json.Unmarshal(body, &result); err != nil {
			return Result{Success: false, StatusCode: resp.StatusCode, Latency: latency, Error: "JSON parse error"}
		}
		return Result{Success: true, StatusCode: resp.StatusCode, Latency: latency}
	}

	return Result{
		Success:    false,
		StatusCode: resp.StatusCode,
		Latency:    latency,
		Error:      string(body),
	}
}

func calculateP95(latencies []time.Duration) time.Duration {
	if len(latencies) == 0 {
		return 0
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	idx := int(float64(len(latencies)) * 0.95)
	if idx >= len(latencies) {
		idx = len(latencies) - 1
	}
	return latencies[idx]
}
