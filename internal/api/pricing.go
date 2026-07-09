package api

import (
	"atmapi/internal/model"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// getPricings 获取所有定价
func getPricings(c *gin.Context) {
	var pricings []model.ModelPricing
	model.DB.Order("provider, model_name").Find(&pricings)
	c.JSON(http.StatusOK, gin.H{"data": pricings})
}

// getPricing 获取单个定价
func getPricing(c *gin.Context) {
	id := c.Param("id")
	var pricing model.ModelPricing
	if err := model.DB.First(&pricing, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "定价不存在"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": pricing})
}

// createPricing 创建定价
func createPricing(c *gin.Context) {
	var req struct {
		ModelName       string  `json:"model_name"`
		PricingType     string  `json:"pricing_type"`
		MonthlyFee      float64 `json:"monthly_fee"`
		IncludedQuota   int64   `json:"included_quota"`
		InputPrice      float64 `json:"input_price"`
		InputCachePrice float64 `json:"input_cache_price"`
		OutputPrice     float64 `json:"output_price"`
		OveragePrice    float64 `json:"overage_price"`
		Provider        string  `json:"provider"`
		EffectiveDate   string  `json:"effective_date"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.ModelName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "模型名不能为空"})
		return
	}

	// 检查是否已存在
	var count int64
	model.DB.Model(&model.ModelPricing{}).Where("model_name = ?", req.ModelName).Count(&count)
	if count > 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "该模型定价已存在"})
		return
	}

	// 解析生效日期
	effectiveDate := time.Now()
	if req.EffectiveDate != "" {
		if t, err := time.Parse("2006-01-02", req.EffectiveDate); err == nil {
			effectiveDate = t
		}
	}

	pricing := model.ModelPricing{
		ModelName:       req.ModelName,
		PricingType:     req.PricingType,
		MonthlyFee:      req.MonthlyFee,
		IncludedQuota:   req.IncludedQuota,
		InputPrice:      req.InputPrice,
		InputCachePrice: req.InputCachePrice,
		OutputPrice:     req.OutputPrice,
		OveragePrice:    req.OveragePrice,
		Provider:        req.Provider,
		EffectiveDate:   effectiveDate,
		Status:          1,
	}

	if err := model.DB.Create(&pricing).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "创建成功", "data": pricing})
}

// updatePricing 更新定价
func updatePricing(c *gin.Context) {
	id := c.Param("id")
	var pricing model.ModelPricing
	if err := model.DB.First(&pricing, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "定价不存在"})
		return
	}

	var req struct {
		ModelName       string  `json:"model_name"`
		PricingType     string  `json:"pricing_type"`
		MonthlyFee      float64 `json:"monthly_fee"`
		IncludedQuota   int64   `json:"included_quota"`
		InputPrice      float64 `json:"input_price"`
		InputCachePrice float64 `json:"input_cache_price"`
		OutputPrice     float64 `json:"output_price"`
		OveragePrice    float64 `json:"overage_price"`
		Provider        string  `json:"provider"`
		EffectiveDate   string  `json:"effective_date"`
		Status          int     `json:"status"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 解析生效日期
	if req.EffectiveDate != "" {
		if t, err := time.Parse("2006-01-02", req.EffectiveDate); err == nil {
			pricing.EffectiveDate = t
		}
	}

	pricing.ModelName = req.ModelName
	pricing.PricingType = req.PricingType
	pricing.MonthlyFee = req.MonthlyFee
	pricing.IncludedQuota = req.IncludedQuota
	pricing.InputPrice = req.InputPrice
	pricing.InputCachePrice = req.InputCachePrice
	pricing.OutputPrice = req.OutputPrice
	pricing.OveragePrice = req.OveragePrice
	pricing.Provider = req.Provider
	pricing.Status = req.Status

	if err := model.DB.Save(&pricing).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "更新成功", "data": pricing})
}

// deletePricing 删除定价
func deletePricing(c *gin.Context) {
	id := c.Param("id")
	var pricing model.ModelPricing
	if err := model.DB.First(&pricing, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "定价不存在"})
		return
	}

	if err := model.DB.Delete(&pricing).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "删除成功"})
}

// getPricingByID 根据 ID 获取定价（用于测试）
func getPricingByID(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 ID"})
		return
	}

	var pricing model.ModelPricing
	if err := model.DB.First(&pricing, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "定价不存在"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": pricing})
}
