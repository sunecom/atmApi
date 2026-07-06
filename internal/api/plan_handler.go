package api

import (
	"fmt"
	"net/http"
	"strconv"

	"atmapi/internal/model"
	"atmapi/internal/service"

	"github.com/gin-gonic/gin"
)

// ===== 套餐管理 =====

// getPlans 获取所有套餐列表
func getPlans(c *gin.Context) {
	var plans []model.Plan
	model.DB.Order("id ASC").Find(&plans)
	c.JSON(http.StatusOK, gin.H{"data": plans})
}

// syncPlans 从配置文件重新同步套餐到数据库
func syncPlans(c *gin.Context) {
	config, err := service.LoadPlanConfig("config/plans.json")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("加载配置失败: %v", err)})
		return
	}

	if err := service.SyncPlansToDB(config); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("同步失败: %v", err)})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": fmt.Sprintf("同步完成，共 %d 个套餐", len(config.Plans)),
	})
}

// ===== API Key 生成与套餐绑定 =====

// generateKey 生成新 API Key 并绑定套餐
func generateKey(c *gin.Context) {
	var req struct {
		Name     string `json:"name" binding:"required"`
		PlanName string `json:"plan_name" binding:"required"`
		UserID   uint   `json:"user_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 如果没指定 user_id，从 JWT context 取
	userID := req.UserID
	if userID == 0 {
		if uid, exists := c.Get("user_id"); exists {
			userID = uid.(uint)
		} else {
			c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 user_id"})
			return
		}
	}

	token, err := service.CreateKeyWithPlan(userID, req.Name, req.PlanName)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"message": "API Key 生成成功",
		"token":   token,
	})
}

// bindPlanToKey 将已有 Key 绑定/切换套餐
func bindPlanToKey(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效 ID"})
		return
	}

	var req struct {
		PlanName string `json:"plan_name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := service.BindKeyToPlan(uint(id), req.PlanName); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": fmt.Sprintf("已绑定套餐 %s", req.PlanName)})
}

// getKeyPlanInfo 查询 Key 的套餐详情
func getKeyPlanInfo(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效 ID"})
		return
	}

	var token model.Token
	if err := model.DB.First(&token, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Token 不存在"})
		return
	}

	_, plan, err := service.GetKeyPlan(token.Key)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"token": token,
			"plan":  nil,
			"error": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"token": token,
		"plan":  plan,
	})
}
