package model

import "time"

// PromptSegment 请求结构分段（用于缓存贡献矢量图）
type PromptSegment struct {
	ID          uint      `gorm:"primarykey" json:"id"`
	ProfileID   uint      `gorm:"index:idx_profile" json:"profile_id"` // 关联 prompt_profiles
	TokenName   string    `gorm:"size:100;index:idx_token_time" json:"token_name"`
	SegmentType string    `gorm:"size:50" json:"segment_type"` // system_identity, tool_schema, history, current_input...
	Position    int       `gorm:"default:0" json:"position"`   // 顺序位置（从1开始）
	Tokens      int       `gorm:"default:0" json:"tokens"`     // 该分段估算 token 数
	ContentHash string    `gorm:"size:64" json:"content_hash"` // 内容哈希（前200字符）
	IsStable    bool      `gorm:"default:false" json:"is_stable"` // 是否稳定（对比历史）
	CreatedAt   time.Time `json:"created_at"`
}

func (PromptSegment) TableName() string {
	return "prompt_segments"
}

// 分段类型常量
const (
	SegmentSystemIdentity   = "system_identity"   // 身份指令
	SegmentSystemRules      = "system_rules"      // 系统规则
	SegmentToolSchema       = "tool_schema"       // 工具定义
	SegmentKnowledge        = "knowledge"         // 知识资料
	SegmentHistory          = "conversation_history" // 历史消息
	SegmentCurrentInput     = "current_input"     // 当前输入
	SegmentRuntimeData      = "runtime_data"      // 运行时数据
	SegmentOutputConstraint = "output_constraint" // 输出约束
	SegmentImageData        = "image_data"        // 图片数据
)
