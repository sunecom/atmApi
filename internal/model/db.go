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

	// 自动迁移
	err = DB.AutoMigrate(&User{}, &Token{}, &Channel{}, &RequestLog{})
	if err != nil {
		log.Fatalf("数据库迁移失败：%v", err)
	}

	// 创建默认管理员
	createDefaultUser()

	fmt.Println("数据库初始化成功")
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
