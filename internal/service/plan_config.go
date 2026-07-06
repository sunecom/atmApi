package service

import (
	"encoding/json"
	"fmt"
	"log"
	"os"

	"atmapi/internal/model"
)

// PlanConfig JSON 配置文件结构
type PlanConfig struct {
	Plans []PlanDef `json:"plans"`
}

// PlanDef 单个套餐定义
type PlanDef struct {
	Name              string   `json:"name"`
	DisplayName       string   `json:"display_name"`
	Price             string   `json:"price"`
	MonthlyTokenLimit int64    `json:"monthly_token_limit"`
	FiveHourLimit     int64    `json:"five_hour_limit"`
	MaxInputTokens    int      `json:"max_input_tokens"`
	MaxOutputTokens   int      `json:"max_output_tokens"`
	ImageEnabled      bool     `json:"image_enabled"`
	ImageDailyLimit   int64    `json:"image_daily_limit"`
	ConcurrencyLimit  int64    `json:"concurrency_limit"`
	AllowedModels     []string `json:"allowed_models"`
	Description       string   `json:"description"`
}

// LoadPlanConfig 从 JSON 文件加载套餐配置
func LoadPlanConfig(path string) (*PlanConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取套餐配置文件失败: %w", err)
	}

	var config PlanConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("解析套餐配置 JSON 失败: %w", err)
	}

	return &config, nil
}

// SyncPlansToDB 将配置文件同步到数据库（upsert 逻辑）
func SyncPlansToDB(config *PlanConfig) error {
	for _, def := range config.Plans {
		allowedModelsJSON, _ := json.Marshal(def.AllowedModels)

		plan := model.Plan{
			Name:            def.Name,
			DisplayName:     def.DisplayName,
			Price:           def.Price,
			MonthlyMax:      def.MonthlyTokenLimit,
			Hourly5Max:      def.FiveHourLimit,
			MaxInputTokens:  def.MaxInputTokens,
			MaxOutputTokens: def.MaxOutputTokens,
			ImageEnabled:    def.ImageEnabled,
			DailyImageMax:   def.ImageDailyLimit,
			MaxQPS:          def.ConcurrencyLimit,
			AllowedModels:   string(allowedModelsJSON),
			Description:     def.Description,
			DailyMax:        def.FiveHourLimit * 4, // 每日 = 5小时 * 4（约）
			WeeklyMax:       0,
			SkipHourly:      false,
		}

		// upsert: 按 name 查找，存在则更新，不存在则创建
		var existing model.Plan
		err := model.DB.Where("name = ?", def.Name).First(&existing).Error
		if err != nil {
			// 不存在，创建
			if err := model.DB.Create(&plan).Error; err != nil {
				log.Printf("[套餐同步] 创建套餐 %s 失败: %v", def.Name, err)
			} else {
				log.Printf("[套餐同步] 创建套餐 %s 成功", def.Name)
			}
		} else {
			// 存在，更新
			plan.ID = existing.ID
			if err := model.DB.Save(&plan).Error; err != nil {
				log.Printf("[套餐同步] 更新套餐 %s 失败: %v", def.Name, err)
			} else {
				log.Printf("[套餐同步] 更新套餐 %s 成功", def.Name)
			}
		}
	}

	return nil
}

// GetAllowedModels 获取套餐允许的模型列表
func GetAllowedModels(planName string) []string {
	var plan model.Plan
	if err := model.DB.Where("name = ?", planName).First(&plan).Error; err != nil {
		return nil
	}

	if plan.AllowedModels == "" {
		return nil
	}

	var models []string
	if err := json.Unmarshal([]byte(plan.AllowedModels), &models); err != nil {
		return nil
	}

	return models
}

// IsModelAllowed 检查模型是否在套餐允许列表中
func IsModelAllowed(planName, modelName string) bool {
	models := GetAllowedModels(planName)
	if len(models) == 0 {
		return true // 空列表 = 不限制
	}

	for _, m := range models {
		if m == modelName {
			return true
		}
	}

	return false
}
