// Package api — 支付处理路由
//
// 对接 GEO ToolKit 的支付宝支付逻辑，替换 routes.go 中的 TODO 占位
//
// 套餐定价（与 routes.go 中 planOptions 保持一致）：
//
//  | 套餐    | 价格  | 说明                        |
//  |---------|-------|-----------------------------|
//  | trial   | 0     | 体验版                       |
//  | basic   | 29.9  | 基础版 ¥29.9                |
//  | pro     | 49.9  | 专业版 ¥49.9                |
//  | premium | 89    | 旗舰版 ¥89                  |
//
package api

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"atmapi/internal/model"
	"atmapi/internal/service"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ==================== 订单处理 ====================

// createOrder 创建订单（真实支付宝支付链接）
// POST /api/v1/payment/create-order
// Body: {"plan":"basic", "user_open_id":"ou_xxx"}
// Resp: {"order_id":"xxx", "pay_url":"https://openapi.alipay.com/gateway.do?..."}
//
// 替换原 routes.go 中的 TODO 占位
func createOrder(c *gin.Context) {
	var req struct {
		PlanName   string `json:"plan" binding:"required"`
		UserOpenID string `json:"user_open_id"`
		RenewToken string `json:"renew_token"`  // 可选：续费时传旧 token
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, ErrInvalidRequest, "请提供套餐名")
		return
	}

	// 查询套餐（从数据库）
	plan, err := service.GetPlan(req.PlanName)
	if err != nil {
		respondError(c, http.StatusBadRequest, ErrInvalidRequest,
			fmt.Sprintf("套餐 %s 不存在", req.PlanName))
		return
	}

	// 解析价格（plan.Price 是字符串，如 "29.9"）
	var price float64
	if _, err := fmt.Sscanf(plan.Price, "%f", &price); err != nil {
		respondError(c, http.StatusInternalServerError, ErrInternal, "套餐价格配置异常")
		return
	}

	// 生成订单号
	orderNo := fmt.Sprintf("ATM%s%d", time.Now().Format("20060102"), time.Now().UnixNano()/1e6)

	order := &model.Order{
		OrderNo:    orderNo,
		UserOpenID: req.UserOpenID,
		PlanName:   req.PlanName,
		Price:      price,
		Status:     "pending",
	}

	// 续费场景：把旧 token key 存到订单的 token_name 字段
	// 支付成功后 activatePlanForOrder 据此延长旧 token 而不是创建新 token
	if req.RenewToken != "" {
		order.TokenName = req.RenewToken
	}

	if price > 0 {
		// 收费套餐：调用支付宝生成支付链接
		payURL, err := CreateAlipayOrder(orderNo, fmt.Sprintf("%.2f", price), plan.DisplayName)
		if err != nil {
			log.Printf("[支付] 创建支付宝订单失败: %v", err)
			// 支付宝未配置时，fallback 到模拟支付链接
			order.PayURL = fmt.Sprintf("https://pay.aitomoney.online/pay?order_id=%s&plan=%s&price=%.2f",
				orderNo, req.PlanName, price)
		} else {
			order.PayURL = payURL
		}
	} else {
		// 免费套餐直接激活
		order.Status = "paid"
		order.PaidAt = time.Now()
		activatePlanForOrder(order)
	}

	// 保存订单到数据库
	if err := model.DB.Create(order).Error; err != nil {
		log.Printf("[支付] 订单创建失败: %v", err)
		respondError(c, http.StatusInternalServerError, ErrInternal, "订单创建失败")
		return
	}

	log.Printf("[支付] 创建订单 %s: plan=%s price=%.2f open_id=%s", orderNo, req.PlanName, price, req.UserOpenID)

	c.JSON(http.StatusOK, gin.H{
		"order_id": orderNo,
		"pay_url":  order.PayURL,
		"price":    price,
		"plan":     req.PlanName,
		"status":   order.Status,
	})
}

// activatePlanForOrder 支付成功后激活套餐
// 续费场景：order.TokenName 存了旧 token key → 延长 + 可能升级套餐
// 新购场景：order.TokenName 为空 → 创建新 token
func activatePlanForOrder(order *model.Order) {
	if order.Status != "paid" {
		return
	}

	// ===== 续费/升级场景 =====
	if order.TokenName != "" {
		renewTokenKey := order.TokenName
		var oldToken model.Token
		if err := model.DB.Where("key = ?", renewTokenKey).First(&oldToken).Error; err == nil {
			// 延长 30 天
			oldExpired := oldToken.ExpiredTime
			extra := time.Now().AddDate(0, 1, 0).Unix()
			if oldExpired > time.Now().Unix() {
				extra = oldExpired + 30*24*3600
			}

			// 如果新套餐和旧套餐不同，升级套餐类型
			updates := map[string]interface{}{
				"status":       1,
				"expired_time": extra,
				"activated_at": time.Now().Unix(),
			}
			if order.PlanName != oldToken.RateLimitGroup {
				// 升级套餐
				newName := fmt.Sprintf("%s-%s", order.PlanName, order.OrderNo[len(order.OrderNo)-6:])
				updates["rate_limit_group"] = order.PlanName
				updates["name"] = newName
				log.Printf("[支付] 升级套餐: token=%s old=%s new=%s", renewTokenKey[:12], oldToken.RateLimitGroup, order.PlanName)
			} else {
				log.Printf("[支付] 续费成功: token=%s plan=%s 到期=%d", renewTokenKey[:12], order.PlanName, extra)
			}

			model.DB.Model(&oldToken).Updates(updates)
			return
		}
		log.Printf("[支付] 续费失败: 旧 token %s 不存在，改创建新 token", renewTokenKey[:12])
	}

	// ===== 新购场景 =====
	// 查询套餐
	_, err := service.GetPlan(order.PlanName)
	if err != nil {
		log.Printf("[支付] 套餐 %s 不存在，无法激活", order.PlanName)
		return
	}

	// 查找或创建用户
	var user model.User
	result := model.DB.Where("username = ?", order.UserOpenID).First(&user)
	if result.Error != nil {
		user = model.User{
			Username:    order.UserOpenID,
			Password:    order.OrderNo,
			Role:        1,
			Status:      1,
			DisplayName: order.PlanName + "用户",
			Quota:       -1,
		}
		model.DB.Create(&user)
	}

	// 计算过期时间（默认1个月）
	expiredTime := time.Now().AddDate(0, 1, 0).Unix()

	// 创建 Token（使用标准 Key 生成器）
	tokenName := fmt.Sprintf("%s-%s", order.PlanName, order.OrderNo[len(order.OrderNo)-6:])

	// 根据套餐设置限流组
	rateLimitGroup := order.PlanName

	token := model.Token{
		UserID:         user.ID,
		Name:           tokenName,
		Key:            service.GenerateAPIKey(),
		Status:         1,
		RemainQuota:    -1,
		UnlimitedQuota: true,
		RateLimitGroup: rateLimitGroup,
		CreatedTime:    time.Now().Unix(),
		ActivatedAt:    time.Now().Unix(),
		ExpiredTime:    expiredTime,
	}
	if err := model.DB.Create(&token).Error; err != nil {
		log.Printf("[支付] 创建 Token 失败: %v", err)
		return
	}

	// 将 API Key 回写到订单，供用户查询
	model.DB.Model(order).Updates(map[string]interface{}{
		"token_name": tokenName,
	})

	log.Printf("[支付] 订单 %s 自动发卡成功: plan=%s key=%s... token_name=%s user=%d",
		order.OrderNo, order.PlanName, token.Key[:12], tokenName, user.ID)
}

// ==================== 支付宝异步通知 ====================

// alipayNotify 支付宝异步回调
// POST /api/v1/payment/alipay-notify
//
// 支付宝支付成功后 POST form 参数过来，需要验证签名后激活套餐
// 必须返回 "success"（全小写），否则支付宝会持续重试
func alipayNotify(c *gin.Context) {
	// 读取 form 参数
	if err := c.Request.ParseForm(); err != nil {
		log.Printf("[支付] 支付宝回调解析失败: %v", err)
		c.String(http.StatusBadRequest, "fail")
		return
	}

	// 将 form 转为 map[string]string
	params := make(map[string]string)
	for k, vs := range c.Request.Form {
		if len(vs) > 0 {
			params[k] = vs[0]
		}
	}

	log.Printf("[支付] 收到支付宝回调: out_trade_no=%s trade_status=%s",
		params["out_trade_no"], params["trade_status"])

	orderID := params["out_trade_no"]
	tradeStatus := params["trade_status"]

	if orderID == "" || tradeStatus == "" {
		log.Printf("[支付] 回调缺少必要参数")
		c.String(http.StatusBadRequest, "fail")
		return
	}

	// 验证签名
	if AlipayReady() {
		if err := VerifyAlipayNotify(params); err != nil {
			log.Printf("[支付] 验签失败: %v", err)
			c.String(http.StatusBadRequest, "fail")
			return
		}
		log.Printf("[支付] 验签成功")
	} else {
		log.Printf("[支付] 支付宝未完整配置（缺少公钥），跳过验签")
	}

	// 只处理支付成功
	if tradeStatus != "TRADE_SUCCESS" && tradeStatus != "TRADE_FINISHED" {
		c.String(http.StatusOK, "success")
		return
	}

	// 查询订单
	var order model.Order
	if err := model.DB.Where("order_no = ?", orderID).First(&order).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			log.Printf("[支付] 订单 %s 不存在", orderID)
		} else {
			log.Printf("[支付] 查询订单失败: %v", err)
		}
		c.String(http.StatusOK, "success") // 仍返回 success 避免支付宝重试
		return
	}

	if order.Status == "paid" {
		log.Printf("[支付] 订单 %s 已支付，跳过重复处理", orderID)
		c.String(http.StatusOK, "success")
		return
	}

	// 更新订单状态
	now := time.Now()
	model.DB.Model(&order).Updates(map[string]interface{}{
		"status":          "paid",
		"alipay_trade_no": params["trade_no"],
		"paid_at":         now,
	})

	order.Status = "paid"
	order.AlipayTradeNo = params["trade_no"]
	order.PaidAt = now

	// 激活套餐
	activatePlanForOrder(&order)

	log.Printf("[支付] 订单 %s 支付成功，已激活", orderID)
	c.String(http.StatusOK, "success")
}

// wechatNotify 微信异步回调
// POST /api/v1/payment/wechat-notify
// TODO: 微信支付对接（接口与支付宝类似）
func wechatNotify(c *gin.Context) {
	body, _ := io.ReadAll(c.Request.Body)
	log.Printf("[支付] 收到微信回调: %s", string(body))
	// TODO: 解析微信支付结果通知 XML
	c.String(http.StatusOK, `<xml><return_code><![CDATA[SUCCESS]]></return_code></xml>`)
}

// getOrderStatus 查询订单状态
// GET /api/v1/payment/order-status?order_id=xxx
func getOrderStatus(c *gin.Context) {
	orderID := c.Query("order_id")
	if orderID == "" {
		respondError(c, http.StatusBadRequest, ErrInvalidRequest, "请提供 order_id")
		return
	}

	var order model.Order
	if err := model.DB.Where("order_no = ?", orderID).First(&order).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			respondError(c, http.StatusNotFound, ErrOrderNotFound, "订单不存在")
		} else {
			respondError(c, http.StatusInternalServerError, ErrInternal, "查询失败")
		}
		return
	}

	// 支付成功后返回 API Key（自动发卡）
	var apiKey string
	if order.Status == "paid" && order.TokenName != "" {
		var token model.Token
		if err := model.DB.Where("name = ?", order.TokenName).First(&token).Error; err == nil {
			apiKey = token.Key
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"order_id":        order.OrderNo,
		"plan":            order.PlanName,
		"price":           order.Price,
		"status":          order.Status,
		"api_key":         apiKey,
		"pay_url":         order.PayURL,
		"created_at":      order.CreatedAt,
		"paid_at":         order.PaidAt,
		"alipay_trade_no": order.AlipayTradeNo,
	})
}

// getPayments 管理后台：查看所有支付记录
func getPayments(c *gin.Context) {
	var orders []model.Order
	result := model.DB.Order("created_at DESC").Find(&orders)
	if result.Error != nil {
		respondError(c, http.StatusInternalServerError, ErrInternal, "查询失败")
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": orders})
}

// refundPayment 管理后台：退款
func refundPayment(c *gin.Context) {
	var req struct {
		OrderID string `json:"order_id" binding:"required"`
		Reason  string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, ErrInvalidRequest, "请提供订单ID")
		return
	}

	var order model.Order
	if err := model.DB.Where("order_no = ?", req.OrderID).First(&order).Error; err != nil {
		respondError(c, http.StatusNotFound, ErrOrderNotFound, "订单不存在")
		return
	}

	if order.Status != "paid" {
		respondError(c, http.StatusBadRequest, ErrInvalidRequest, "只有已支付的订单才能退款")
		return
	}

	// TODO: 调用支付宝 refund API
	// alipay.trade.refund(out_trade_no, refund_amount)

	model.DB.Model(&order).Update("status", "refunded")
	log.Printf("[支付] 订单 %s 已退款: %s", req.OrderID, req.Reason)
	c.JSON(http.StatusOK, gin.H{"message": "退款成功", "order_id": req.OrderID})
}

// ==================== 开发测试端点（上线前删除）====================

// testActivateOrder 模拟支付成功，用于测试自动发卡流程
// POST /api/v1/payment/test-activate?order_id=xxx
func testActivateOrder(c *gin.Context) {
	orderID := c.Query("order_id")
	if orderID == "" {
		respondError(c, http.StatusBadRequest, ErrInvalidRequest, "请提供 order_id")
		return
	}

	var order model.Order
	if err := model.DB.Where("order_no = ?", orderID).First(&order).Error; err != nil {
		respondError(c, http.StatusNotFound, ErrOrderNotFound, "订单不存在")
		return
	}

	if order.Status == "paid" {
		respondError(c, http.StatusBadRequest, ErrInvalidRequest, "订单已支付")
		return
	}

	// 模拟支付成功
	now := time.Now()
	model.DB.Model(&order).Updates(map[string]interface{}{
		"status":          "paid",
		"alipay_trade_no": "TEST_" + fmt.Sprintf("%d", now.Unix()),
		"paid_at":         now,
	})
	order.Status = "paid"
	order.AlipayTradeNo = "TEST_" + fmt.Sprintf("%d", now.Unix())

	// 触发激活
	activatePlanForOrder(&order)

	c.JSON(http.StatusOK, gin.H{
		"message":  "激活成功（测试模式）",
		"order_id": orderID,
	})
}
