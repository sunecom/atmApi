// Package api — 支付宝支付对接模块
//
// 从 GEO ToolKit (Python) 移植到 atmApi (Go)
// 原始 Python 版: src/api/payment.py
//
// 支付宝电脑网站支付
// 文档: https://opendocs.alipay.com/open/270/105898
package api

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

// AlipayConfig 支付宝配置
type AlipayConfig struct {
	AppID      string // APP_ID
	PrivateKey string // 应用私钥（RSA2）
	PublicKey  string // 支付宝公钥（用于验签）
	Gateway    string // 网关 https://openapi.alipay.com/gateway.do
	ReturnURL  string // 支付成功跳转地址
	NotifyURL  string // 异步通知地址
}

var alipayCfg *AlipayConfig

// InitAlipay 初始化支付宝配置（从环境变量读取）
func InitAlipay() {
	alipayCfg = &AlipayConfig{
		AppID:      os.Getenv("ALIPAY_APP_ID"),
		PrivateKey: os.Getenv("ALIPAY_APP_PRIVATE_KEY"),
		PublicKey:  os.Getenv("ALIPAY_PUBLIC_KEY"),
		Gateway:    os.Getenv("ALIPAY_GATEWAY"),
		ReturnURL:  os.Getenv("ALIPAY_RETURN_URL"),
		NotifyURL:  os.Getenv("ALIPAY_NOTIFY_URL"),
	}

	// 如果没有配置网关，使用默认值
	if alipayCfg.Gateway == "" {
		alipayCfg.Gateway = "https://openapi.alipay.com/gateway.do"
	}

	// 如果没有单独配置回调 URL，使用默认值
	if alipayCfg.ReturnURL == "" {
		alipayCfg.ReturnURL = "https://pay.aitomoney.online/account"
	}
	if alipayCfg.NotifyURL == "" {
		alipayCfg.NotifyURL = "https://pay.aitomoney.online/api/v1/payment/alipay-notify"
	}

	// 开发环境使用沙箱网关
	if os.Getenv("ALIPAY_SANDBOX") == "true" {
		alipayCfg.Gateway = "https://openapi.alipaydev.com/gateway.do"
	}
}

// AlipayReady 检查支付宝配置是否就绪
func AlipayReady() bool {
	return alipayCfg != nil && alipayCfg.AppID != "" && alipayCfg.PrivateKey != ""
}

// ==================== 密钥加载 ====================

// loadPrivateKey 加载 RSA 私钥（支持 PKCS#1 和 PKCS#8 格式）
func loadPrivateKey(pemStr string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("无法解析 PEM 私钥")
	}

	// 尝试 PKCS#8 优先
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if rsaKey, ok := key.(*rsa.PrivateKey); ok {
			return rsaKey, nil
		}
		return nil, fmt.Errorf("私钥不是 RSA 类型")
	}

	// 尝试 PKCS#1
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}

	return nil, fmt.Errorf("无法解析私钥，不支持的格式")
}

// loadPublicKey 加载支付宝公钥
func loadPublicKey(pemStr string) (*rsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("无法解析 PEM 公钥")
	}

	if key, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		if rsaKey, ok := key.(*rsa.PublicKey); ok {
			return rsaKey, nil
		}
		return nil, fmt.Errorf("公钥不是 RSA 类型")
	}

	return nil, fmt.Errorf("无法解析公钥")
}

// ==================== 签名 / 验签 ====================

// sign RSA2 签名（SHA256withRSA）
func sign(data string) (string, error) {
	privateKey, err := loadPrivateKey(alipayCfg.PrivateKey)
	if err != nil {
		return "", fmt.Errorf("加载私钥失败: %w", err)
	}

	hash := sha256.Sum256([]byte(data))
	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, hash[:])
	if err != nil {
		return "", fmt.Errorf("签名失败: %w", err)
	}

	return base64.StdEncoding.EncodeToString(signature), nil
}

// verify 验证支付宝回调签名
func verify(data, signStr string) error {
	publicKey, err := loadPublicKey(alipayCfg.PublicKey)
	if err != nil {
		return fmt.Errorf("加载支付宝公钥失败: %w", err)
	}

	sigBytes, err := base64.StdEncoding.DecodeString(signStr)
	if err != nil {
		return fmt.Errorf("base64 解码失败: %w", err)
	}

	hash := sha256.Sum256([]byte(data))
	return rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, hash[:], sigBytes)
}

// ==================== 支付 URL 生成 ====================

// BuildOrderSignStr 构建待签名字符串（按 key 升序排列，跳过空值和 sign）
func BuildOrderSignStr(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		v := params[k]
		if v != "" && k != "sign" {
			pairs = append(pairs, fmt.Sprintf("%s=%s", k, v))
		}
	}
	return strings.Join(pairs, "&")
}

// CreateAlipayOrder 创建支付宝支付订单，返回支付 URL
//
// 参数:
//   - outTradeNo: 商户订单号（唯一）
//   - totalAmount: 订单金额（元，保留两位小数，如 "29.90"）
//   - subject: 订单标题
//
// 返回:
//   - payURL: 支付宝电脑网站支付链接（302 跳转到此即可）
//   - err: 错误信息
func CreateAlipayOrder(outTradeNo, totalAmount, subject string) (string, error) {
	if !AlipayReady() {
		return "", fmt.Errorf("支付宝未配置，请设置 ALIPAY_APP_ID 和 ALIPAY_APP_PRIVATE_KEY")
	}

	bizContent := fmt.Sprintf(`{"out_trade_no":"%s","total_amount":"%s","subject":"%s","product_code":"FAST_INSTANT_TRADE_PAY"}`,
		outTradeNo, totalAmount, subject)

	params := map[string]string{
		"app_id":      alipayCfg.AppID,
		"method":      "alipay.trade.page.pay", // 电脑网站支付
		"charset":     "utf-8",
		"sign_type":   "RSA2",
		"timestamp":   time.Now().Format("2006-01-02 15:04:05"),
		"version":     "1.0",
		"return_url":  alipayCfg.ReturnURL,
		"notify_url":  alipayCfg.NotifyURL,
		"biz_content": bizContent,
	}

	// 构建签名原文
	signStr := BuildOrderSignStr(params)

	// 计算签名
	signature, err := sign(signStr)
	if err != nil {
		return "", fmt.Errorf("签名失败: %w", err)
	}
	params["sign"] = signature

	// 构造最终 URL
	query := url.Values{}
	for k, v := range params {
		query.Set(k, v)
	}

	return fmt.Sprintf("%s?%s", alipayCfg.Gateway, query.Encode()), nil
}

// ==================== 回调验签 ====================

// VerifyAlipayNotify 验证支付宝异步通知签名
//
// 支付宝 POST 过来的 form 参数已解析为 map，需要取 "sign" 字段验证
func VerifyAlipayNotify(params map[string]string) error {
	signStr := params["sign"]
	if signStr == "" {
		return fmt.Errorf("回调缺少 sign 参数")
	}
	signType := params["sign_type"]

	// 重建待签名字符串（注意：sign 和 sign_type 都不参与签名）
	keys := make([]string, 0, len(params))
	for k := range params {
		if k != "sign" && k != "sign_type" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		v := params[k]
		if v != "" {
			pairs = append(pairs, fmt.Sprintf("%s=%s", k, v))
		}
	}
	rawData := strings.Join(pairs, "&")

	_ = signType // RSA2 固定，暂不校验

	return verify(rawData, signStr)
}

// ParseAlipayNotify 解析异步通知参数，提取关键信息
func ParseAlipayNotify(params map[string]string) map[string]string {
	return map[string]string{
		"out_trade_no": params["out_trade_no"],
		"trade_no":     params["trade_no"],
		"trade_status": params["trade_status"],
		"total_amount": params["total_amount"],
		"buyer_id":     params["buyer_id"],
		"gmt_payment":  params["gmt_payment"],
	}
}
