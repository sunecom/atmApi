package main

import (
	"fmt"
	"log"

	"atmapi/internal/api"
	"atmapi/internal/config"
	"atmapi/internal/model"

	"github.com/gin-gonic/gin"
)

func main() {
	// 加载配置
	cfg := config.Load()

	// 初始化数据库
	model.InitDB(cfg)

	// 创建 Gin 引擎
	r := gin.Default()

	// 注册路由
	api.RegisterRoutes(r)

	// 启动服务
	addr := fmt.Sprintf(":%s", cfg.Port)
	fmt.Printf("🚀 atmApi 启动成功！\n")
	fmt.Printf("📍 访问地址：http://localhost:%s\n", cfg.Port)
	fmt.Printf("🔑 默认账号：admin / admin123\n")
	fmt.Printf("📊 数据库：%s (%s)\n", cfg.DBType, cfg.DBPath)

	log.Fatal(r.Run(addr))
}
