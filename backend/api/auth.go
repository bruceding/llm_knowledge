package api

import (
	"errors"
	"strings"
	"time"
	"unicode"

	"llm-knowledge/db"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/mojocn/base64Captcha"
	"golang.org/x/crypto/bcrypt"
)

// AuthHandler handles authentication operations
type AuthHandler struct{}

// GetCaptcha generates and returns a new captcha
func (h *AuthHandler) GetCaptcha(c echo.Context) error {
	// Configure captcha driver (4-character alphanumeric)
	driver := base64Captcha.NewDriverString(
		80,    // height
		240,   // width
		4,     // noise count
		0,     // show line options (0 = no lines)
		4,     // captcha length
		base64Captcha.TxtSimpleCharaters, // character set (alphanumeric)
		nil,   // fonts (use default)
		nil,   // background color (use default)
		nil,   // font color (use default)
	)
	store := base64Captcha.DefaultMemStore
	captcha := base64Captcha.NewCaptcha(driver, store)

	// Generate captcha
	idKey, b64s, answer, err := captcha.Generate()
	if err != nil {
		return c.JSON(500, echo.Map{"error": "Failed to generate captcha"})
	}

	// Store captcha in database (answer lowercase for case-insensitive verification)
	captchaRecord := db.Captcha{
		Key:       idKey,
		Answer:    strings.ToLower(answer),
		ExpiresAt: time.Now().Add(5 * time.Minute),
	}
	db.DB.Create(&captchaRecord)

	return c.JSON(200, echo.Map{
		"captchaKey":   idKey,
		"captchaImage": b64s,
	})
}

func validatePassword(password string) error {
	if len(password) < 6 || len(password) > 32 {
		return errors.New("密码长度必须在6-32字符之间")
	}

	hasLetter := false
	hasDigit := false
	for _, c := range password {
		if unicode.IsLetter(c) {
			hasLetter = true
		}
		if unicode.IsDigit(c) {
			hasDigit = true
		}
	}

	if !hasLetter {
		return errors.New("密码必须包含至少一个字母")
	}
	if !hasDigit {
		return errors.New("密码必须包含至少一个数字")
	}

	return nil
}

func validateUsername(username string) error {
	if len(username) < 3 || len(username) > 20 {
		return errors.New("用户名长度需在3-20字符之间")
	}
	return nil
}

func verifyCaptcha(key string, answer string) bool {
	var captcha db.Captcha
	result := db.DB.Where("key = ? AND expires_at > ?", key, time.Now()).First(&captcha)
	if result.Error != nil {
		return false
	}

	if captcha.Answer != strings.ToLower(answer) {
		return false
	}

	// Delete captcha after successful verification (prevent reuse)
	db.DB.Delete(&captcha)

	return true
}

type RegisterRequest struct {
	Username      string `json:"username"`
	Password      string `json:"password"`
	Email         string `json:"email"`
	CaptchaKey    string `json:"captchaKey"`
	CaptchaAnswer string `json:"captchaAnswer"`
}

func (h *AuthHandler) Register(c echo.Context) error {
	var req RegisterRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(400, echo.Map{"error": "无效的请求体"})
	}

	// Validate captcha
	if !verifyCaptcha(req.CaptchaKey, req.CaptchaAnswer) {
		return c.JSON(400, echo.Map{"error": "验证码错误或已过期"})
	}

	// Validate username
	if err := validateUsername(req.Username); err != nil {
		return c.JSON(400, echo.Map{"error": err.Error()})
	}

	// Validate password
	if err := validatePassword(req.Password); err != nil {
		return c.JSON(400, echo.Map{"error": err.Error()})
	}

	// Check if username exists
	var existingUser db.User
	if db.DB.Where("username = ?", req.Username).First(&existingUser).Error == nil {
		return c.JSON(400, echo.Map{"error": "用户名已被使用"})
	}

	// Check if email exists
	if db.DB.Where("email = ?", req.Email).First(&existingUser).Error == nil {
		return c.JSON(400, echo.Map{"error": "邮箱已被注册"})
	}

	// Hash password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return c.JSON(500, echo.Map{"error": "密码加密失败"})
	}

	// Create user
	user := db.User{
		Username:          req.Username,
		PasswordHash:      string(hashedPassword),
		Email:             req.Email,
		MustChangePassword: false,
	}
	db.DB.Create(&user)

	return c.JSON(200, echo.Map{
		"success": true,
		"userId":  user.ID,
	})
}

type LoginRequest struct {
	Username      string `json:"username"`
	Password      string `json:"password"`
	CaptchaKey    string `json:"captchaKey"`
	CaptchaAnswer string `json:"captchaAnswer"`
}

func (h *AuthHandler) Login(c echo.Context) error {
	var req LoginRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(400, echo.Map{"error": "无效的请求体"})
	}

	// Validate captcha
	if !verifyCaptcha(req.CaptchaKey, req.CaptchaAnswer) {
		return c.JSON(400, echo.Map{"error": "验证码错误或已过期"})
	}

	// Find user
	var user db.User
	result := db.DB.Where("username = ?", req.Username).First(&user)
	if result.Error != nil {
		return c.JSON(401, echo.Map{"error": "用户名或密码错误"})
	}

	// Verify password
	err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password))
	if err != nil {
		return c.JSON(401, echo.Map{"error": "用户名或密码错误"})
	}

	// Create session
	token := uuid.New().String()
	session := db.Session{
		UserID:     user.ID,
		Token:      token,
		ExpiresAt:  time.Now().Add(7 * 24 * time.Hour),
		LastAccess: time.Now(),
	}
	db.DB.Create(&session)

	// Build response
	response := echo.Map{
		"success":            true,
		"token":             token,
		"userId":            user.ID,
		"username":          user.Username,
		"mustChangePassword": user.MustChangePassword,
	}

	if user.MustChangePassword {
		response["message"] = "请先修改默认密码"
	}

	return c.JSON(200, response)
}

func (h *AuthHandler) Logout(c echo.Context) error {
	authHeader := c.Request().Header.Get("Authorization")
	if authHeader == "" {
		return c.JSON(401, echo.Map{"error": "未登录"})
	}

	token := strings.TrimPrefix(authHeader, "Bearer ")
	if token == authHeader {
		return c.JSON(401, echo.Map{"error": "无效的认证格式"})
	}

	// Delete session
	db.DB.Where("token = ?", token).Delete(&db.Session{})

	return c.JSON(200, echo.Map{"success": true})
}

func (h *AuthHandler) Status(c echo.Context) error {
	authHeader := c.Request().Header.Get("Authorization")
	if authHeader == "" {
		return c.JSON(200, echo.Map{"loggedIn": false})
	}

	token := strings.TrimPrefix(authHeader, "Bearer ")
	if token == authHeader {
		return c.JSON(200, echo.Map{"loggedIn": false})
	}

	// Find session
	var session db.Session
	result := db.DB.Where("token = ? AND expires_at > ?", token, time.Now()).First(&session)
	if result.Error != nil {
		return c.JSON(200, echo.Map{"loggedIn": false})
	}

	// Find user
	var user db.User
	db.DB.First(&user, session.UserID)

	return c.JSON(200, echo.Map{
		"loggedIn": true,
		"userId":   user.ID,
		"username": user.Username,
	})
}

type ChangePasswordRequest struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

func (h *AuthHandler) ChangePassword(c echo.Context) error {
	// Get user ID from context (set by auth middleware)
	userId := GetCurrentUserId(c)
	if userId == 0 {
		return c.JSON(401, echo.Map{"error": "未登录"})
	}

	var req ChangePasswordRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(400, echo.Map{"error": "无效的请求体"})
	}

	// Find user
	var user db.User
	db.DB.First(&user, userId)

	// Verify current password
	err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.CurrentPassword))
	if err != nil {
		return c.JSON(401, echo.Map{"error": "当前密码错误"})
	}

	// Validate new password
	if err := validatePassword(req.NewPassword); err != nil {
		return c.JSON(400, echo.Map{"error": err.Error()})
	}

	// Hash new password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		return c.JSON(500, echo.Map{"error": "密码加密失败"})
	}

	// Update password
	user.PasswordHash = string(hashedPassword)
	user.MustChangePassword = false
	db.DB.Save(&user)

	return c.JSON(200, echo.Map{"success": true})
}