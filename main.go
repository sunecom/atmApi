package main

import (
	"fmt"
	"log"
	"time"

	"atmapi/internal/api"
	"atmapi/internal/config"
	"atmapi/internal/model"
	"atmapi/internal/service"

	"github.com/gin-gonic/gin"
)

func main() {
	// 加载配置
	cfg := config.Load()

	// 初始化数据库
	model.InitDB(cfg)

	// 初始化响应缓存（TTL 10分钟，最大 1000 条）
	service.InitCache(10*time.Minute, 1000)

	// 初始化图片缓存（TTL 5分钟）
	service.InitImageCache(5)

	// 启动定时清理 rate_limits 过期记录（每天凌晨 3 点清理 7 天前的数据）
	go func() {
		for {
			now := time.Now()
			next := time.Date(now.Year(), now.Month(), now.Day(), 3, 0, 0, 0, now.Location())
			if next.Before(now) {
				next = next.Add(24 * time.Hour)
			}
			time.Sleep(next.Sub(now))
			service.CleanOldRecords()
		}
	}()

	// 创建 Gin 引擎
	r := gin.Default()

	// 注册路由
	api.RegisterRoutes(r)

	// 启动服务
	addr := fmt.Sprintf(":%s", cfg.Port)
	fmt.Printf("🚀 atmApi v2.0.1 启动成功！\n")
	fmt.Printf("📍 访问地址：http://localhost:%s\n", cfg.Port)
	fmt.Printf("🔑 默认账号：admin / admin123\n")
	fmt.Printf("📊 数据库：%s (%s)\n", cfg.DBType, cfg.DBPath)

	log.Fatal(r.Run(addr))
}
