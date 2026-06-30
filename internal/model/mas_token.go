package model

import (
	"fmt"
	"math/rand"
	"strings"
	"time"

	"gorm.io/gorm"
)

type MASToken struct {
	ID               uint           `gorm:"primarykey" json:"id"`
	Token            string         `gorm:"uniqueIndex;size:64" json:"token"`
	Tier             string         `gorm:"size:20;index" json:"tier"`
	TierName         string         `gorm:"size:20" json:"tier_name"`
	StudentID        string         `gorm:"size:64;index" json:"student_id"`
	Quota            int64          `gorm:"default:0" json:"quota"`
	Used             int64          `gorm:"default:0" json:"used"`
	Remaining        int64          `gorm:"default:0" json:"remaining"`
	ExpiresAt        time.Time      `gorm:"index" json:"expires_at"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
	DeletedAt        gorm.DeletedAt `gorm:"index" json:"-"`
	Models           string         `gorm:"size:500" json:"models"`
	ConcurrencyLimit int            `gorm:"default:1" json:"concurrency_limit"`
	Status           int            `gorm:"default:1;index" json:"status"`
	DefaultModel     string         `gorm:"size:50" json:"default_model"`
	AlertThreshold   int64          `gorm:"default:0" json:"alert_threshold"`
	LastAlertAt      *time.Time     `json:"last_alert_at,omitempty"`
	NotifyURL        string         `gorm:"size:500" json:"notify_url"`
	RenewalCount     int            `gorm:"default:0" json:"renewal_count"`
}

func (m *MASToken) TableName() string { return "mas_tokens" }

type MASTierConfig struct {
	Name, Models, DefaultModel string
	Quota, ExpiresHours, AlertThreshold int64
	Price, RenewalPrice float64
	ConcurrencyLimit int
	Renewable bool
}

var MASTierConfigs = map[string]MASTierConfig{
	"trial":     {Name:"体验版", Quota:100, ExpiresHours:2, Price:0, Models:"qwen3.5-plus", DefaultModel:"qwen3.5-plus", ConcurrencyLimit:1, AlertThreshold:10, Renewable:false, RenewalPrice:0},
	"starter":   {Name:"入门版", Quota:500, ExpiresHours:168, Price:9.9, Models:"qwen3.5-plus,deepseek-chat", DefaultModel:"qwen3.5-plus", ConcurrencyLimit:2, AlertThreshold:100, Renewable:true, RenewalPrice:9.9},
	"pro":       {Name:"专业版", Quota:10000, ExpiresHours:720, Price:99, Models:"qwen3.5-plus,deepseek-chat,qwen3.6-plus", DefaultModel:"qwen3.5-plus", ConcurrencyLimit:5, AlertThreshold:1000, Renewable:true, RenewalPrice:79},
	"max":       {Name:"旗舰版", Quota:50000, ExpiresHours:2160, Price:249, Models:"qwen3.5-plus,deepseek-chat,qwen3.6-plus", DefaultModel:"qwen3.5-plus", ConcurrencyLimit:10, AlertThreshold:5000, Renewable:true, RenewalPrice:199},
	"unlimited": {Name:"无限版", Quota:-1, ExpiresHours:720, Price:499, Models:"qwen3.5-plus,deepseek-chat,qwen3.6-plus", DefaultModel:"qwen3.5-plus", ConcurrencyLimit:-1, AlertThreshold:-1, Renewable:true, RenewalPrice:399},
}

func GenerateMASToken(tier string) string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 8)
	for i := range b { b[i] = chars[rand.Intn(len(chars))] }
	return fmt.Sprintf("sk-m-%s-%s", tier, string(b))
}

func (m *MASToken) CheckAndSetStatus() {
	if m.Status != 1 { return }
	if m.Quota >= 0 && m.Used >= m.Quota { m.Status = 2; return }
	if m.ExpiresAt.Before(time.Now()) { m.Status = 3 }
}

func (m *MASToken) IsAvailable() bool { m.CheckAndSetStatus(); return m.Status == 1 }

func (m *MASToken) AllowedModels() []string {
	if m.Models == "" { return []string{"qwen3.5-plus"} }
	return strings.Split(m.Models, ",")
}

func (m *MASToken) HasAlert() bool {
	if m.AlertThreshold <= 0 { return false }
	return m.Remaining > 0 && m.Remaining <= m.AlertThreshold
}
