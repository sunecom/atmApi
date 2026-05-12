package api

import (
	"net/http"
	"strconv"

	"atmapi/internal/model"

	"github.com/gin-gonic/gin"
)

// ===== 用户管理 =====

func getUsers(c *gin.Context) {
	var users []model.User
	model.DB.Select("id, username, display_name, email, role, status, quota, used_quota, request_count, created_at").Find(&users)
	c.JSON(http.StatusOK, gin.H{"data": users})
}

func createUser(c *gin.Context) {
	var req struct {
		Username    string `json:"username" binding:"required"`
		Password    string `json:"password" binding:"required,min=6"`
		DisplayName string `json:"display_name"`
		Email       string `json:"email"`
		Role        int    `json:"role"`
		Quota       int64  `json:"quota"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var count int64
	model.DB.Model(&model.User{}).Where("username = ?", req.Username).Count(&count)
	if count > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "用户名已存在"})
		return
	}
	user := model.User{
		Username:    req.Username,
		Password:    req.Password,
		DisplayName: req.DisplayName,
		Email:       req.Email,
		Role:        req.Role,
		Status:      1,
		Quota:       req.Quota,
	}
	if err := model.DB.Create(&user).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建失败"})
		return
	}
	user.Password = ""
	c.JSON(http.StatusCreated, gin.H{"message": "用户创建成功", "user": user})
}

func updateUser(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效ID"})
		return
	}
	var user model.User
	if err := model.DB.First(&user, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "用户不存在"})
		return
	}
	var req map[string]interface{}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	delete(req, "id")
	if err := model.DB.Model(&user).Updates(req).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "更新失败"})
		return
	}
	user.Password = ""
	c.JSON(http.StatusOK, gin.H{"message": "更新成功", "user": user})
}

func deleteUser(c *gin.Context) {
	id := c.Param("id")
	if err := model.DB.Delete(&model.User{}, id).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "删除成功"})
}
