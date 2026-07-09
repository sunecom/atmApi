package model

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"atmapi/internal/config"

	"gorm.io/driver/mysql"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var DB *gorm.DB

func InitDB(cfg *config.Config) {
	var err error
	var dialector gorm.Dialector

	switch cfg.DBType {
	case "mysql":
		dialector = mysql.Open(cfg.DBPath)
	case "sqlite":
		// 确保数据目录存在
		dir := filepath.Dir(cfg.DBPath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			log.Fatalf("创建数据目录失败：%v", err)
		}
		dialector = sqlite.Open(cfg.DBPath)
	default:
		log.Fatalf("不支持的数据库类型：%s", cfg.DBType)
	}

	DB, err = gorm.Open(dialector, &gorm.Config{
		Logger: logger.Default.LogMode(logger.Info),
	})
	if err != nil {
		log.Fatalf("数据库连接失败：%v", err)
	}

	// 自动迁移（容错模式：失败只警告不退出）
	err = DB.AutoMigrate(&User{}, &Token{}, &Channel{}, &RequestLog{}, &RateLimit{}, &Plan{}, &UsageLog{}, &Order{}, &ImageUsage{}, &ModelPricing{})
	if err != nil {
		log.Printf("[警告] 数据库迁移部分失败：%v（继续启动）", err)
		// 尝试手动添加缺失字段
		migrateMissingFields()
	}

	// 创建默认管理员
	createDefaultUser()

	// 初始化默认套餐
	initDefaultPlans()

	fmt.Println("数据库初始化成功")
}

// migrateMissingFields 手动添加缺失字段
func migrateMissingFields() {
	// 检查并添加 channels 表缺失字段
	missingFields := []struct {
		table string
		col   string
		typ   string
	}{
		{"channels", "model_group", "VARCHAR(100)"},
		{"channels", "max_concurrent", "INTEGER DEFAULT 0"},
		{"tokens", "rate_limit_group", "VARCHAR(50) DEFAULT ''"},
		{"plans", "image_enabled", "BOOLEAN DEFAULT 0"},
		{"plans", "allowed_models", "TEXT DEFAULT ''"},
		{"plans", "max_input_tokens", "INTEGER DEFAULT 0"},
		{"plans", "max_output_tokens", "INTEGER DEFAULT 0"},
		{"plans", "daily_image_max", "INTEGER DEFAULT 0"},
	}

	for _, f := range missingFields {
		sql := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", f.table, f.col, f.typ)
		if err := DB.Exec(sql).Error; err != nil {
			// 忽略 "duplicate column" 错误
			if !containsString(err.Error(), "duplicate column") {
				log.Printf("[迁移] 添加字段 %s.%s 失败: %v", f.table, f.col, err)
			}
		} else {
			log.Printf("[迁移] 添加字段 %s.%s 成功", f.table, f.col)
		}
	}

	// 创建 rate_limits 表
	if !DB.Migrator().HasTable(&RateLimit{}) {
		if err := DB.AutoMigrate(&RateLimit{}).Error; err != nil {
			log.Printf("[迁移] 创建 rate_limits 表失败: %v", err)
		} else {
			log.Printf("[迁移] 创建 rate_limits 表成功")
		}
	}
}

// containsString 检查字符串是否包含子串
func containsString(s, substr string) bool {
	return strings.Contains(s, substr)
}

func createDefaultUser() {
	var count int64
	DB.Model(&User{}).Count(&count)
	if count == 0 {
		user := User{
			Username:    "admin",
			Password:    "admin123", // 实际应该用 bcrypt 加密
			DisplayName: "系统管理员",
			Role:        100,
			Status:      1,
			Quota:       -1, // 无限配额
		}
		DB.Create(&user)
		fmt.Println("默认管理员已创建：admin / admin123")
	}
}

// initDefaultPlans 初始化默认套餐配置
// 优先从 config/plans.json 加载，失败则用内置默认值
func initDefaultPlans() {
	// 尝试从 JSON 配置文件加载
	configPaths := []string{
		"config/plans.json",
		"./config/plans.json",
		"../config/plans.json",
	}

	for _, path := range configPaths {
		if data, err := os.ReadFile(path); err == nil {
			var config struct {
				Plans []struct {
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
				} `json:"plans"`
			}
			if jsonErr := json.Unmarshal(data, &config); jsonErr == nil && len(config.Plans) > 0 {
				for _, def := range config.Plans {
					allowedModelsJSON, _ := json.Marshal(def.AllowedModels)
					plan := Plan{
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
						DailyMax:        def.FiveHourLimit * 4,
						WeeklyMax:       0,
						SkipHourly:      false,
					}
					var count int64
					DB.Model(&Plan{}).Where("name = ?", plan.Name).Count(&count)
					if count == 0 {
						if err := DB.Create(&plan).Error; err != nil {
							log.Printf("[初始化] 创建套餐 %s 失败: %v", plan.Name, err)
						} else {
							log.Printf("[初始化] 从配置创建套餐 %s 成功", plan.Name)
						}
					} else {
						// 已存在 → 更新配置字段
						plan.ID = 0
						DB.Model(&Plan{}).Where("name = ?", plan.Name).Updates(map[string]interface{}{
							"display_name":      plan.DisplayName,
							"price":             plan.Price,
							"monthly_max":       plan.MonthlyMax,
							"hourly_5_max":      plan.Hourly5Max,
							"max_input_tokens":  plan.MaxInputTokens,
							"max_output_tokens": plan.MaxOutputTokens,
							"image_enabled":     plan.ImageEnabled,
							"daily_image_max":   plan.DailyImageMax,
							"max_qps":           plan.MaxQPS,
							"allowed_models":    plan.AllowedModels,
							"description":       plan.Description,
							"daily_max":         plan.DailyMax,
						})
						log.Printf("[初始化] 更新套餐 %s 配置", plan.Name)
					}
				}
				log.Printf("[初始化] 从 %s 加载 %d 个套餐完成", path, len(config.Plans))
				return
			}
		}
	}

	// 配置文件不存在或解析失败 → 使用内置默认值（6档套餐）
	log.Printf("[初始化] 未找到配置文件，使用内置默认套餐")
	defaultPlans := []Plan{
		{
			Name: "basic", DisplayName: "基础版 ¥29.9/月", Price: "29.9",
			Hourly5Max: 100, DailyMax: 400, MonthlyMax: 8000, MaxQPS: 3,
			MaxInputTokens: 32000, MaxOutputTokens: 4096,
			ImageEnabled: false, DailyImageMax: 0,
			Description: "入门级套餐，适合个人开发者",
		},
		{
			Name: "pro", DisplayName: "专业版 ¥69/月", Price: "69",
			Hourly5Max: 300, DailyMax: 1200, MonthlyMax: 24000, MaxQPS: 5,
			MaxInputTokens: 64000, MaxOutputTokens: 8192,
			ImageEnabled: true, DailyImageMax: 50,
			Description: "专业开发者套餐，支持图片理解",
		},
		{
			Name: "flagship", DisplayName: "旗舰版 ¥129/月", Price: "129",
			Hourly5Max: 600, DailyMax: 3000, MonthlyMax: 50000, MaxQPS: 10,
			MaxInputTokens: 128000, MaxOutputTokens: 16384,
			ImageEnabled: true, DailyImageMax: 200,
			Description: "旗舰套餐，全模型访问权限",
		},
		{
			Name: "starter", DisplayName: "创业版 ¥99/月", Price: "99",
			Hourly5Max: 400, DailyMax: 2000, MonthlyMax: 30000, MaxQPS: 8,
			MaxInputTokens: 64000, MaxOutputTokens: 8192,
			ImageEnabled: true, DailyImageMax: 100,
			Description: "创业团队套餐",
		},
		{
			Name: "advanced", DisplayName: "高级版 ¥299/月", Price: "299",
			Hourly5Max: 1200, DailyMax: 6000, MonthlyMax: 100000, MaxQPS: 20,
			MaxInputTokens: 256000, MaxOutputTokens: 32768,
			ImageEnabled: true, DailyImageMax: 500,
			Description: "高级企业套餐，大并发支持",
		},
		{
			Name: "enterprise", DisplayName: "企业版 ¥599/月", Price: "599",
			Hourly5Max: 3000, DailyMax: 15000, MonthlyMax: 300000, MaxQPS: 50,
			MaxInputTokens: 512000, MaxOutputTokens: 65536,
			ImageEnabled: true, DailyImageMax: 2000,
			Description: "企业旗舰套餐，全功能无限制",
		},
	}

	for _, plan := range defaultPlans {
		var count int64
		DB.Model(&Plan{}).Where("name = ?", plan.Name).Count(&count)
		if count == 0 {
			if err := DB.Create(&plan).Error; err != nil {
				log.Printf("[初始化] 创建套餐 %s 失败: %v", plan.Name, err)
			} else {
				log.Printf("[初始化] 创建套餐 %s 成功", plan.Name)
			}
		}
	}
}
