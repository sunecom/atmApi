package main

import (
	"fmt"
	"log"
	"os"
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

	// 加载支付宝配置（从 .env.alipay 文件）
	log.Printf("[支付] 开始加载 .env.alipay...")
	if err := api.LoadEnvFile(".env.alipay"); err != nil {
		log.Printf("[警告] 加载 .env.alipay 失败: %v（支付宝支付不可用）", err)
	} else {
		log.Printf("[支付] .env.alipay 加载成功")
		// 初始化支付宝支付模块（仅在配置加载成功时）
		api.InitAlipay()
		if api.AlipayReady() {
			log.Printf("[支付] 支付宝初始化成功: APP_ID=%s", os.Getenv("ALIPAY_APP_ID"))
		} else {
			log.Printf("[警告] 支付宝就绪检查失败: app_id=%q, private_key_len=%d",
				os.Getenv("ALIPAY_APP_ID"), len(os.Getenv("ALIPAY_APP_PRIVATE_KEY")))
		}
	}

	// 初始化数据库
	model.InitDB(cfg)

	// 初始化响应缓存（TTL 10分钟，最大 1000 条）
	service.InitCache(10*time.Minute, 1000)

	// 初始化缓存分析器（企业级缓存优化）
	service.InitCacheAnalytics()

	// 初始化图片缓存（TTL 5分钟）
	service.InitImageCache(5)

	// 初始化模型偏好缓存（TTL 5分钟）
	service.InitModelPreferenceCache(5)

	// 初始化图片分析缓存
	service.InitImageAnalysisCache()

	// 初始化飞书通知器
	feishuAppID := os.Getenv("FEISHU_APP_ID")
	feishuAppSecret := os.Getenv("FEISHU_APP_SECRET")
	if feishuAppID != "" && feishuAppSecret != "" {
		service.InitFeishuNotifier(feishuAppID, feishuAppSecret)
		log.Printf("[飞书通知] 飞书通知器已初始化")
	} else {
		log.Printf("[警告] 飞书通知器未初始化：缺少 FEISHU_APP_ID 或 FEISHU_APP_SECRET")
	}

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

	// 启动定时任务
	api.StartExpiryChecker() // 过期 token 自动禁用
	api.StartUsageAlerter()  // 用量告警
	api.StartCostAlerter()   // 成本告警（亏损检测）

	// 启动服务
	addr := fmt.Sprintf(":%s", cfg.Port)
	fmt.Printf("🚀 atmApi v2.0.1 启动成功！\n")
	fmt.Printf("📍 访问地址：http://localhost:%s\n", cfg.Port)
	fmt.Printf("🔑 默认账号：admin / admin123\n")
	fmt.Printf("📊 数据库：%s (%s)\n", cfg.DBType, cfg.DBPath)

	log.Fatal(r.Run(addr))
}
