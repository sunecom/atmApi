package model

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

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
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		log.Fatalf("数据库连接失败：%v", err)
	}

	// 自动迁移（容错模式：失败只警告不退出）
	err = DB.AutoMigrate(&User{}, &Token{}, &Channel{}, &RequestLog{}, &RateLimit{}, &Plan{})
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
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && (s[:len(substr)] == substr || s[len(s)-len(substr):] == substr || containsMiddle(s, substr)))
}

func containsMiddle(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
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
func initDefaultPlans() {
	defaultPlans := []Plan{
		{
			Name:        "basic",
			DisplayName: "性价比月卡",
			Hourly5Max:  500,
			WeeklyMax:   40000,
			Description: "GLM-5.2 性价比月卡：每5小时最多500次调用，适合轻量使用。",
			Price:       "9.9",
		},
		{
			Name:        "standard",
			DisplayName: "基础版月卡",
			Hourly5Max:  1000,
			WeeklyMax:   40000,
			Description: "GLM-5.2 基础版月卡：每5小时最多1000次调用，适合日常使用。",
			Price:       "19.9",
		},
		{
			Name:        "premium",
			DisplayName: "升级版月卡",
			Hourly5Max:  1500,
			WeeklyMax:   40000,
			Description: "GLM-5.2 升级版月卡：每5小时最多1500次调用，适合进阶使用。",
			Price:       "29.9",
		},
		{
			Name:        "pro",
			DisplayName: "黄金月卡",
			Hourly5Max:  2000,
			WeeklyMax:   40000,
			Description: "GLM5.2【黄金月卡】：每5小时最多2000次调用，适合高强度使用。",
			Price:       "39.9",
		},
		{
			Name:        "weekly",
			DisplayName: "大胃王月卡",
			Hourly5Max:  0,
			WeeklyMax:   40000,
			Description: "GLM5.2【大胃王月卡】：不限调用次数，仅做每周40000次总量限制。",
			Price:       "99.9",
			SkipHourly:  true,
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
