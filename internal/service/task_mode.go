package service

import (
	"log"
	"regexp"
)

// TaskMode 任务模式
type TaskMode string

const (
	TaskModeConsult  TaskMode = "咨询"   // 解释/什么是/为什么
	TaskModeCode     TaskMode = "代码执行" // 写/实现/修复/部署
	TaskModeDebug    TaskMode = "调试"    // bug/报错/异常
	TaskModeChat     TaskMode = "闲聊"    // 你好/谢谢/哈哈
	TaskModeUnknown  TaskMode = "未知"
)

// TaskModeResult 任务模式分类结果
type TaskModeResult struct {
	Mode        TaskMode
	Model       string // 推荐模型
	Hint        string // 策略提示
}

// 关键词正则
var (
	consultPattern  = regexp.MustCompile(`(?i)(什么是|为什么|怎么理解|解释一下|什么意思|如何工作|原理|概念|区别|对比)`)
	codePattern     = regexp.MustCompile(`(?i)(写[一个段]|实现|开发|创建|部署|部署|添加|新增|重构|优化代码|写代码|编程)`)
	debugPattern    = regexp.MustCompile(`(?i)(bug|报错|异常|错误|panic|失败|不工作|无法|不能|问题出在|堆栈|traceback|error)`)
	chatPattern     = regexp.MustCompile(`(?i)^(你好|hello|hi|嗨|谢谢|感谢|哈哈|呵呵|好的|明白|了解|再见|bye)$`)
)

// ClassifyTaskMode 分类任务模式
// 分析最后一条用户消息，返回任务模式和推荐策略
func ClassifyTaskMode(messages []map[string]interface{}) TaskModeResult {
	// 提取最后一条用户消息
	lastUserMsg := ""
	for i := len(messages) - 1; i >= 0; i-- {
		if role, ok := messages[i]["role"].(string); ok && role == "user" {
			if content, ok := messages[i]["content"].(string); ok {
				lastUserMsg = content
				break
			}
		}
	}

	if lastUserMsg == "" {
		return TaskModeResult{Mode: TaskModeUnknown, Model: "", Hint: ""}
	}

	// 按优先级匹配（调试 > 代码 > 咨询 > 闲聊）
	if debugPattern.MatchString(lastUserMsg) {
		log.Printf("[模式路由] mode=调试 model=pro")
		return TaskModeResult{
			Mode:  TaskModeDebug,
			Model: "deepseek-v4-pro",
			Hint:  "用户遇到技术问题，需要深入分析和调试。请仔细分析错误原因，提供详细的排查步骤和解决方案。",
		}
	}

	if codePattern.MatchString(lastUserMsg) {
		log.Printf("[模式路由] mode=代码执行 model=flash/pro")
		return TaskModeResult{
			Mode:  TaskModeCode,
			Model: "", // 保持原有路由逻辑
			Hint:  "用户要求编写或修改代码。请直接提供代码实现，减少不必要的确认和解释。",
		}
	}

	if consultPattern.MatchString(lastUserMsg) {
		log.Printf("[模式路由] mode=咨询 model=flash")
		return TaskModeResult{
			Mode:  TaskModeConsult,
			Model: "deepseek-v4-flash",
			Hint:  "用户在询问概念或原理。请提供清晰易懂的解释，可以用类比或示例帮助理解。",
		}
	}

	if chatPattern.MatchString(lastUserMsg) {
		log.Printf("[模式路由] mode=闲聊 model=flash")
		return TaskModeResult{
			Mode:  TaskModeChat,
			Model: "deepseek-v4-flash",
			Hint:  "用户在闲聊或打招呼。请简短友好地回应。",
		}
	}

	return TaskModeResult{Mode: TaskModeUnknown, Model: "", Hint: ""}
}

// ApplyTaskModeHint 将任务模式提示注入到 messages 中
func ApplyTaskModeHint(messages []map[string]interface{}, result TaskModeResult) []map[string]interface{} {
	if result.Hint == "" {
		return messages
	}

	hintMsg := map[string]interface{}{
		"role":    "system",
		"content": "[任务模式提示] " + result.Hint,
	}

	// 使用 InsertAfterSystemBlock 保证前缀稳定
	return InsertAfterSystemBlock(messages, hintMsg)
}

// GetTaskModeModel 根据任务模式返回推荐模型
// 如果返回空字符串，表示保持原有路由逻辑
func GetTaskModeModel(result TaskModeResult) string {
	return result.Model
}

