# User Authentication Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add user registration/login with session management, captcha protection, and data isolation.

**Architecture:** Extend existing Go/Echo backend with auth middleware and user models. Frontend uses Zustand for auth state with route guards.

**Tech Stack:** Go (bcrypt, base64Captcha), Echo middleware, React (Zustand), TypeScript

---

## Task 1: Add User and Session Data Models

**Files:**
- Modify: `backend/db/models.go`

**Step 1: Add User model**

Add after the existing models:

```go
type User struct {
	ID                uint      `gorm:"primaryKey" json:"id"`
	Username          string    `gorm:"unique;not null" json:"username"`
	PasswordHash      string    `gorm:"not null" json:"-"`
	Email             string    `gorm:"unique;not null" json:"email"`
	MustChangePassword bool     `gorm:"default:false" json:"mustChangePassword"`
	CreatedAt         time.Time `json:"createdAt"`
	UpdatedAt         time.Time `json:"updatedAt"`
}
```

**Step 2: Add Session model**

```go
type Session struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	UserID     uint      `gorm:"index;not null" json:"userId"`
	Token      string    `gorm:"unique;not null" json:"token"`
	ExpiresAt  time.Time `gorm:"not null" json:"expiresAt"`
	LastAccess time.Time `json:"lastAccess"`
	CreatedAt  time.Time `json:"createdAt"`
}
```

**Step 3: Add Captcha model**

```go
type Captcha struct {
	ID        uint      `gorm:"primaryKey"`
	Key       string    `gorm:"unique;not null"`
	Answer    string    `gorm:"not null"`
	ExpiresAt time.Time `gorm:"not null"`
	CreatedAt time.Time
}
```

**Step 4: Add UserID field to existing models**

Add `UserID` field to Document, Conversation, UserSettings, RSSFeed, DocNote:

```go
// In Document struct (after SourceGUID field)
UserID uint `gorm:"index;not null" json:"userId"`

// In Conversation struct (after SessionID field)
UserID uint `gorm:"index;not null" json:"userId"`

// In UserSettings struct (after ID field)
UserID uint `gorm:"index;not null" json:"userId"`

// In RSSFeed struct (after ID field)
UserID uint `gorm:"index;not null" json:"userId"`

// In DocNote struct (after ID field)
UserID uint `gorm:"index;not null" json:"userId"`
```

**Step 5: Commit**

```bash
cd /Users/bruceding\ 1/Projects/llm_knowledge
git add backend/db/models.go
git commit -m "feat(db): add User, Session, Captcha models and UserID to existing tables"
```

---

## Task 2: Update Database Init with Migration Logic

**Files:**
- Modify: `backend/db/db.go`

**Step 1: Update AutoMigrate to include new models**

Update the `Init` function:

```go
func Init(path string) error {
	var err error
	DB, err = gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		return err
	}

	// AutoMigrate all tables including new auth models
	err = DB.AutoMigrate(
		&User{}, &Session{}, &Captcha{},
		&Document{}, &Tag{}, &DocumentTag{},
		&Conversation{}, &ConversationMessage{},
		&UserSettings{}, &RSSFeed{}, &DocNote{},
	)
	if err != nil {
		return err
	}

	// Check if default user needed (migration for existing data)
	var userCount int64
	DB.Model(&User{}).Count(&userCount)
	if userCount == 0 {
		// Create default user for existing data
		defaultPassword := generateRandomPassword(12)
		hashedPassword, _ := bcrypt.GenerateFromPassword([]byte(defaultPassword), bcrypt.DefaultCost)
		
		defaultUser := User{
			Username:          "admin",
			PasswordHash:      string(hashedPassword),
			Email:             "admin@localhost",
			MustChangePassword: true,
		}
		DB.Create(&defaultUser)

		// Update all existing data to UserID=1
		DB.Model(&Document{}).Where("user_id IS NULL OR user_id = 0").Update("user_id", 1)
		DB.Model(&Conversation{}).Where("user_id IS NULL OR user_id = 0").Update("user_id", 1)
		DB.Model(&UserSettings{}).Where("user_id IS NULL OR user_id = 0").Update("user_id", 1)
		DB.Model(&RSSFeed{}).Where("user_id IS NULL OR user_id = 0").Update("user_id", 1)
		DB.Model(&DocNote{}).Where("user_id IS NULL OR user_id = 0").Update("user_id", 1)

		log.Printf("Created default user 'admin', password: %s", defaultPassword)
	}

	// Start session cleanup scheduler
	go startSessionCleanup()

	return nil
}
```

**Step 2: Add helper functions**

Add at the end of the file:

```go
func generateRandomPassword(length int) string {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, length)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}

func startSessionCleanup() {
	ticker := time.NewTicker(1 * time.Hour)
	for range ticker.C {
		DB.Where("expires_at < ?", time.Now()).Delete(&Session{})
		DB.Where("expires_at < ?", time.Now()).Delete(&Captcha{})
	}
}
```

**Step 3: Add imports**

Update imports:

```go
package db

import (
	"log"
	"math/rand"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var DB *gorm.DB
```

**Step 4: Add bcrypt dependency**

```bash
cd /Users/bruceding\ 1/Projects/llm_knowledge/backend
go get golang.org/x/crypto/bcrypt
```

**Step 5: Commit**

```bash
cd /Users/bruceding\ 1/Projects/llm_knowledge
git add backend/db/db.go backend/go.mod backend/go.sum
git commit -m "feat(db): add migration logic for default user and session cleanup"
```

---

## Task 3: Add base64Captcha Dependency

**Files:**
- Modify: `backend/go.mod`, `backend/go.sum`

**Step 1: Install base64Captcha**

```bash
cd /Users/bruceding\ 1/Projects/llm_knowledge/backend
go get github.com/mojocn/base64Captcha
```

**Step 2: Verify installation**

```bash
cd /Users/bruceding\ 1/Projects/llm_knowledge/backend
go mod tidy
```

**Step 3: Commit**

```bash
cd /Users/bruceding\ 1/Projects/llm_knowledge
git add backend/go.mod backend/go.sum
git commit -m "chore: add base64Captcha dependency for captcha generation"
```

---

## Task 4: Create Auth Handler (Captcha APIs)

**Files:**
- Create: `backend/api/auth.go`

**Step 1: Create auth.go with captcha handler**

```go
package api

import (
	"llm-knowledge/db"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/mojocn/base64Captcha"
)

// AuthHandler handles authentication operations
type AuthHandler struct{}

// GetCaptcha generates and returns a new captcha
func (h *AuthHandler) GetCaptcha(c echo.Context) error {
	// Generate captcha key
	key := uuid.New().String()

	// Configure captcha (4-character alphanumeric)
	config := base64Captcha.ConfigCharacter{
		Height:          80,
		Width:           240,
		Mode:            base64Captcha.ModeNumberAlphabet,
		ComplexOfNoise:  base64Captcha.ComplexOfNoiseSimple,
		IsShowHollowLine: false,
		IsShowNoiseDot:   true,
		IsShowNoiseText:  false,
		IsShowSlimeLine:  false,
		IsShowSineLine:   false,
		CaptchaLen:       4,
	}

	// Generate captcha image
	idKey, cap := base64Captcha.GenerateCaptcha("", config)
	answer := cap.Text

	// Store captcha in database (answer lowercase for case-insensitive verification)
	captcha := db.Captcha{
		Key:       idKey,
		Answer:    strings.ToLower(answer),
		ExpiresAt: time.Now().Add(5 * time.Minute),
	}
	db.DB.Create(&captcha)

	return c.JSON(200, echo.Map{
		"captchaKey":   idKey,
		"captchaImage": cap.ToBase64(),
	})
}
```

**Step 2: Add uuid dependency**

```bash
cd /Users/bruceding\ 1/Projects/llm_knowledge/backend
go get github.com/google/uuid
```

**Step 3: Commit**

```bash
cd /Users/bruceding\ 1/Projects/llm_knowledge
git add backend/api/auth.go backend/go.mod backend/go.sum
git commit -m "feat(api): add captcha generation handler"
```

---

## Task 5: Add Password Validation and Captcha Verification Helpers

**Files:**
- Modify: `backend/api/auth.go`

**Step 1: Add password validation function**

Add at the end of auth.go:

```go
import (
	"errors"
	"unicode"
)

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
```

**Step 2: Add captcha verification function**

```go
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
```

**Step 3: Commit**

```bash
cd /Users/bruceding\ 1/Projects/llm_knowledge
git add backend/api/auth.go
git commit -m "feat(api): add password validation and captcha verification helpers"
```

---

## Task 6: Add Register Handler

**Files:**
- Modify: `backend/api/auth.go`

**Step 1: Add Register handler**

```go
import (
	"golang.org/x/crypto/bcrypt"
)

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
```

**Step 2: Commit**

```bash
cd /Users/bruceding\ 1/Projects/llm_knowledge
git add backend/api/auth.go
git commit -m "feat(api): add user registration handler"
```

---

## Task 7: Add Login Handler

**Files:**
- Modify: `backend/api/auth.go`

**Step 1: Add Login handler**

```go
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
		"success":           true,
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
```

**Step 2: Commit**

```bash
cd /Users/bruceding\ 1/Projects/llm_knowledge
git add backend/api/auth.go
git commit -m "feat(api): add login handler with session creation"
```

---

## Task 8: Add Logout and Status Handlers

**Files:**
- Modify: `backend/api/auth.go`

**Step 1: Add Logout handler**

```go
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
```

**Step 2: Add Status handler**

```go
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
```

**Step 3: Commit**

```bash
cd /Users/bruceding\ 1/Projects/llm_knowledge
git add backend/api/auth.go
git commit -m "feat(api): add logout and status check handlers"
```

---

## Task 9: Add Change Password Handler

**Files:**
- Modify: `backend/api/auth.go`

**Step 1: Add ChangePassword handler**

```go
type ChangePasswordRequest struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

func (h *AuthHandler) ChangePassword(c echo.Context) error {
	// Get user ID from context (set by auth middleware)
	userId := c.Get("userId").(uint)

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
```

**Step 2: Commit**

```bash
cd /Users/bruceding\ 1/Projects/llm_knowledge
git add backend/api/auth.go
git commit -m "feat(api): add change password handler"
```

---

## Task 10: Create Auth Middleware

**Files:**
- Create: `backend/api/middleware.go`

**Step 1: Create middleware.go**

```go
package api

import (
	"llm-knowledge/db"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
)

// AuthMiddleware validates session tokens and auto-renews sessions
func AuthMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		authHeader := c.Request().Header.Get("Authorization")
		if authHeader == "" {
			return c.JSON(401, echo.Map{"error": "未登录"})
		}

		token := strings.TrimPrefix(authHeader, "Bearer ")
		if token == authHeader {
			return c.JSON(401, echo.Map{"error": "无效的认证格式"})
		}

		// Find session
		var session db.Session
		result := db.DB.Where("token = ? AND expires_at > ?", token, time.Now()).First(&session)
		if result.Error != nil {
			return c.JSON(401, echo.Map{"error": "Session无效或已过期"})
		}

		// Sliding expiration: renew if > 30 minutes since last access
		now := time.Now()
		if now.Sub(session.LastAccess) > 30*time.Minute {
			session.ExpiresAt = now.Add(7 * 24 * time.Hour)
			session.LastAccess = now
			db.DB.Save(&session)
		} else {
			session.LastAccess = now
			db.DB.Save(&session)
		}

		// Inject userId into context
		c.Set("userId", session.UserID)

		return next(c)
	}
}

// GetCurrentUserId extracts userId from context
func GetCurrentUserId(c echo.Context) uint {
	userId, ok := c.Get("userId").(uint)
	if !ok {
		return 0
	}
	return userId
}
```

**Step 2: Commit**

```bash
cd /Users/bruceding\ 1/Projects/llm_knowledge
git add backend/api/middleware.go
git commit -m "feat(api): add auth middleware with sliding expiration"
```

---

## Task 11: Modify Document Handler with UserID Filtering

**Files:**
- Modify: `backend/api/documents.go`

**Step 1: Update ListInbox with UserID filter**

```go
func (h *DocHandler) ListInbox(c echo.Context) error {
	userId := GetCurrentUserId(c)
	var docs []db.Document
	result := db.DB.Where("status = ? AND user_id = ?", "inbox", userId).Order("created_at desc").Find(&docs)
	if result.Error != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": result.Error.Error()})
	}
	return c.JSON(http.StatusOK, docs)
}
```

**Step 2: Update GetDoc with UserID filter**

```go
func (h *DocHandler) GetDoc(c echo.Context) error {
	userId := GetCurrentUserId(c)
	id := c.Param("id")
	var doc db.Document
	result := db.DB.Preload("Tags").Where("id = ? AND user_id = ?", id, userId).First(&doc)
	if result.Error != nil {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "文档不存在或无权访问"})
	}
	return c.JSON(http.StatusOK, doc)
}
```

**Step 3: Update UpdateDoc with UserID check**

In the UpdateDoc function, change the document lookup:

```go
func (h *DocHandler) UpdateDoc(c echo.Context) error {
	userId := GetCurrentUserId(c)
	id := c.Param("id")

	// Check if document exists and belongs to user
	var doc db.Document
	result := db.DB.Where("id = ? AND user_id = ?", id, userId).First(&doc)
	if result.Error != nil {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "文档不存在或无权访问"})
	}
	// ... rest of the function unchanged
}
```

**Step 4: Update DeleteDoc with UserID check**

Find the DeleteDoc function and update:

```go
func (h *DocHandler) DeleteDoc(c echo.Context) error {
	userId := GetCurrentUserId(c)
	id := c.Param("id")

	// Check ownership before delete
	result := db.DB.Where("id = ? AND user_id = ?", id, userId).Delete(&db.Document{})
	if result.RowsAffected == 0 {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "文档不存在或无权删除"})
	}
	// ... rest of the function
}
```

**Step 5: Commit**

```bash
cd /Users/bruceding\ 1/Projects/llm_knowledge
git add backend/api/documents.go
git commit -m "feat(api): add UserID filtering to document handlers"
```

---

## Task 12: Modify Query Handler with UserID Filtering

**Files:**
- Modify: `backend/api/query.go`

**Step 1: Read the file to understand structure**

```bash
cat /Users/bruceding\ 1/Projects/llm_knowledge/backend/api/query.go
```

**Step 2: Update CreateConversation**

Find CreateConversation and add UserID:

```go
func (h *QueryHandler) CreateConversation(c echo.Context) error {
	userId := GetCurrentUserId(c)
	// ... in the Conversation creation:
	conv := db.Conversation{
		Title:  title,
		UserID: userId,
	}
	db.DB.Create(&conv)
}
```

**Step 3: Update ListConversations**

```go
func (h *QueryHandler) ListConversations(c echo.Context) error {
	userId := GetCurrentUserId(c)
	var convs []db.Conversation
	db.DB.Where("user_id = ?", userId).Order("updated_at desc").Find(&convs)
	return c.JSON(200, convs)
}
```

**Step 4: Update GetConversationMessages**

```go
func (h *QueryHandler) GetConversationMessages(c echo.Context) error {
	userId := GetCurrentUserId(c)
	id := c.Param("id")
	
	// Verify conversation belongs to user
	var conv db.Conversation
	result := db.DB.Where("id = ? AND user_id = ?", id, userId).First(&conv)
	if result.Error != nil {
		return c.JSON(404, echo.Map{"error": "对话不存在或无权访问"})
	}
	// ... rest unchanged
}
```

**Step 5: Update DeleteConversation**

```go
func (h *QueryHandler) DeleteConversation(c echo.Context) error {
	userId := GetCurrentUserId(c)
	id := c.Param("id")
	
	result := db.DB.Where("id = ? AND user_id = ?", id, userId).Delete(&db.Conversation{})
	if result.RowsAffected == 0 {
		return c.JSON(404, echo.Map{"error": "对话不存在或无权删除"})
	}
}
```

**Step 6: Commit**

```bash
cd /Users/bruceding\ 1/Projects/llm_knowledge
git add backend/api/query.go
git commit -m "feat(api): add UserID filtering to query/conversation handlers"
```

---

## Task 13: Modify Settings Handler with UserID Filtering

**Files:**
- Modify: `backend/api/settings.go`

**Step 1: Read current file**

```bash
cat /Users/bruceding\ 1/Projects/llm_knowledge/backend/api/settings.go
```

**Step 2: Update GetSettings**

```go
func (h *SettingsHandler) GetSettings(c echo.Context) error {
	userId := GetCurrentUserId(c)
	var settings db.UserSettings
	db.DB.Where("user_id = ?", userId).FirstOrCreate(&settings)
	return c.JSON(200, settings)
}
```

**Step 3: Update UpdateSettings**

```go
func (h *SettingsHandler) UpdateSettings(c echo.Context) error {
	userId := GetCurrentUserId(c)
	var settings db.UserSettings
	db.DB.Where("user_id = ?", userId).FirstOrCreate(&settings)
	// ... bind and update, add UserID:
	settings.UserID = userId
	db.DB.Save(&settings)
	return c.JSON(200, settings)
}
```

**Step 4: Commit**

```bash
cd /Users/bruceding\ 1/Projects/llm_knowledge
git add backend/api/settings.go
git commit -m "feat(api): add UserID filtering to settings handler"
```

---

## Task 14: Modify RSS Handler with UserID Filtering

**Files:**
- Modify: `backend/api/rss.go`

**Step 1: Update AddFeed**

```go
func (h *RSSHandler) AddFeed(c echo.Context) error {
	userId := GetCurrentUserId(c)
	// ... create feed with UserID:
	feed := db.RSSFeed{
		Name:   req.Name,
		URL:    req.URL,
		UserID: userId,
	}
}
```

**Step 2: Update ListFeeds**

```go
func (h *RSSHandler) ListFeeds(c echo.Context) error {
	userId := GetCurrentUserId(c)
	var feeds []db.RSSFeed
	db.DB.Where("user_id = ?", userId).Find(&feeds)
}
```

**Step 3: Update DeleteFeed**

```go
func (h *RSSHandler) DeleteFeed(c echo.Context) error {
	userId := GetCurrentUserId(c)
	id := c.Param("id")
	result := db.DB.Where("id = ? AND user_id = ?", id, userId).Delete(&db.RSSFeed{})
	if result.RowsAffected == 0 {
		return c.JSON(404, echo.Map{"error": "订阅不存在或无权删除"})
	}
}
```

**Step 4: Update SyncFeed**

```go
func (h *RSSHandler) SyncFeed(c echo.Context) error {
	userId := GetCurrentUserId(c)
	id := c.Param("id")
	
	var feed db.RSSFeed
	result := db.DB.Where("id = ? AND user_id = ?", id, userId).First(&feed)
	if result.Error != nil {
		return c.JSON(404, echo.Map{"error": "订阅不存在或无权访问"})
	}
	// ... rest unchanged
}
```

**Step 5: Commit**

```bash
cd /Users/bruceding\ 1/Projects/llm_knowledge
git add backend/api/rss.go
git commit -m "feat(api): add UserID filtering to RSS handler"
```

---

## Task 15: Modify DocNotes Handler with UserID Filtering

**Files:**
- Modify: `backend/api/docnotes.go`

**Step 1: Update all handlers with UserID**

Add `GetCurrentUserId(c)` to:
- ListNotes: filter by user_id
- CreateNote: set user_id
- UpdateNote: check user_id ownership
- DeleteNote: check user_id ownership
- PushToWiki: check user_id ownership

**Step 2: Commit**

```bash
cd /Users/bruceding\ 1/Projects/llm_knowledge
git add backend/api/docnotes.go
git commit -m "feat(api): add UserID filtering to doc notes handler"
```

---

## Task 16: Modify Images Handler with UserID

**Files:**
- Modify: `backend/api/images.go`

**Step 1: Add UserID context check**

Images are associated with documents, so we need to validate the user can upload.

**Step 2: Commit**

```bash
cd /Users/bruceding\ 1/Projects/llm_knowledge
git add backend/api/images.go
git commit -m "feat(api): add auth check to images handler"
```

---

## Task 17: Update main.go Route Configuration

**Files:**
- Modify: `backend/main.go`

**Step 1: Add AuthHandler initialization**

After other handler initializations:

```go
// Auth API
authH := &api.AuthHandler{}
```

**Step 2: Add public auth routes**

Before the protected routes section:

```go
// Public auth routes (no middleware)
e.GET("/api/auth/captcha", authH.GetCaptcha)
e.POST("/api/auth/register", authH.Register)
e.POST("/api/auth/login", authH.Login)
e.GET("/api/auth/status", authH.Status)
```

**Step 3: Create protected route group**

Replace individual route registrations with a protected group:

```go
// Protected routes (require auth)
apiGroup := e.Group("/api")
apiGroup.Use(api.AuthMiddleware)

// Auth routes requiring middleware
apiGroup.POST("/auth/logout", authH.Logout)
apiGroup.PUT("/auth/password", authH.ChangePassword)

// Document API
apiGroup.GET("/documents/inbox", docH.ListInbox)
apiGroup.GET("/documents", docH.ListAll)
apiGroup.GET("/documents/:id", docH.GetDoc)
apiGroup.PUT("/documents/:id", docH.UpdateDoc)
apiGroup.POST("/documents/:id/publish", docH.Publish)
apiGroup.POST("/documents/:id/re-extract", docH.ReExtract)
apiGroup.POST("/documents/:id/llm-extract", docH.LLMExtract)
apiGroup.POST("/documents/:id/html-extract", docH.HTMLExtract)
apiGroup.POST("/documents/:id/regenerate-summary", docH.RegenerateSummary)
apiGroup.DELETE("/documents/:id", docH.DeleteDoc)

// ... continue for all other protected routes
```

**Step 4: Keep public routes outside the group**

```go
// Public routes (no auth required)
e.GET("/api/health", func(c echo.Context) error {
	return c.JSON(200, map[string]string{"status": "ok"})
})
e.GET("/api/dependencies/status", depsH.GetStatus)
e.POST("/api/dependencies/check", depsH.Check)
```

**Step 5: Commit**

```bash
cd /Users/bruceding\ 1/Projects/llm_knowledge
git add backend/main.go
git commit -m "feat: configure auth routes with middleware protection"
```

---

## Task 18: Add Frontend Auth Types

**Files:**
- Modify: `frontend/src/types.ts`

**Step 1: Add auth-related types**

```typescript
export interface AuthState {
  isLoggedIn: boolean
  userId: number | null
  username: string | null
  mustChangePassword: boolean
  token: string | null
}

export interface LoginResponse {
  success: boolean
  token: string
  userId: number
  username: string
  mustChangePassword: boolean
  message?: string
}

export interface RegisterResponse {
  success: boolean
  userId: number
}

export interface CaptchaResponse {
  captchaKey: string
  captchaImage: string
}
```

**Step 2: Commit**

```bash
cd /Users/bruceding\ 1/Projects/llm_knowledge
git add frontend/src/types.ts
git commit -m "feat(frontend): add auth-related TypeScript types"
```

---

## Task 19: Add Frontend Auth API Functions

**Files:**
- Modify: `frontend/src/api.ts`

**Step 1: Add auth API functions**

```typescript
// Auth API
export async function getCaptcha(): Promise<CaptchaResponse> {
  const res = await fetch(`${API_BASE}/auth/captcha`)
  if (!res.ok) throw new Error('Failed to get captcha')
  return res.json()
}

export async function register(
  username: string,
  password: string,
  email: string,
  captchaKey: string,
  captchaAnswer: string
): Promise<RegisterResponse> {
  const res = await fetch(`${API_BASE}/auth/register`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, password, email, captchaKey, captchaAnswer }),
  })
  const data = await res.json()
  if (!res.ok) throw new Error(data.error || 'Registration failed')
  return data
}

export async function login(
  username: string,
  password: string,
  captchaKey: string,
  captchaAnswer: string
): Promise<LoginResponse> {
  const res = await fetch(`${API_BASE}/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, password, captchaKey, captchaAnswer }),
  })
  const data = await res.json()
  if (!res.ok) throw new Error(data.error || 'Login failed')
  return data
}

export async function logout(): Promise<void> {
  const token = localStorage.getItem('token')
  const res = await fetch(`${API_BASE}/auth/logout`, {
    method: 'POST',
    headers: { 'Authorization': `Bearer ${token}` },
  })
  if (!res.ok) throw new Error('Logout failed')
}

export async function checkAuthStatus(): Promise<{ loggedIn: boolean; userId?: number; username?: string }> {
  const token = localStorage.getItem('token')
  if (!token) return { loggedIn: false }
  
  const res = await fetch(`${API_BASE}/auth/status`, {
    headers: { 'Authorization': `Bearer ${token}` },
  })
  return res.json()
}

export async function changePassword(currentPassword: string, newPassword: string): Promise<void> {
  const token = localStorage.getItem('token')
  const res = await fetch(`${API_BASE}/auth/password`, {
    method: 'PUT',
    headers: {
      'Content-Type': 'application/json',
      'Authorization': `Bearer ${token}`,
    },
    body: JSON.stringify({ currentPassword, newPassword }),
  })
  const data = await res.json()
  if (!res.ok) throw new Error(data.error || 'Password change failed')
}
```

**Step 2: Add token to all existing API calls**

Create a helper function:

```typescript
function getAuthHeaders(): HeadersInit {
  const token = localStorage.getItem('token')
  const headers: HeadersInit = { 'Content-Type': 'application/json' }
  if (token) {
    headers['Authorization'] = `Bearer ${token}`
  }
  return headers
}
```

Update existing fetch calls to use auth headers (except for public endpoints).

**Step 3: Commit**

```bash
cd /Users/bruceding\ 1/Projects/llm_knowledge
git add frontend/src/api.ts
git commit -m "feat(frontend): add auth API functions and token handling"
```

---

## Task 20: Create Frontend Auth Store (Zustand)

**Files:**
- Create: `frontend/src/store/authStore.ts`

**Step 1: Install Zustand**

```bash
cd /Users/bruceding\ 1/Projects/llm_knowledge/frontend
npm install zustand
```

**Step 2: Create auth store**

```typescript
import { create } from 'zustand'
import { persist } from 'zustand/middleware'

interface AuthState {
  isLoggedIn: boolean
  userId: number | null
  username: string | null
  mustChangePassword: boolean
  token: string | null
  
  setAuth: (token: string, userId: number, username: string, mustChangePassword: boolean) => void
  clearAuth: () => void
  setMustChangePassword: (value: boolean) => void
}

export const useAuthStore = create<AuthState>()(
  persist(
    (set) => ({
      isLoggedIn: false,
      userId: null,
      username: null,
      mustChangePassword: false,
      token: null,
      
      setAuth: (token, userId, username, mustChangePassword) => {
        localStorage.setItem('token', token)
        set({
          isLoggedIn: true,
          token,
          userId,
          username,
          mustChangePassword,
        })
      },
      
      clearAuth: () => {
        localStorage.removeItem('token')
        set({
          isLoggedIn: false,
          token: null,
          userId: null,
          username: null,
          mustChangePassword: false,
        })
      },
      
      setMustChangePassword: (value) => set({ mustChangePassword: value }),
    }),
    {
      name: 'auth-storage',
    }
  )
)
```

**Step 3: Commit**

```bash
cd /Users/bruceding\ 1/Projects/llm_knowledge
git add frontend/src/store/authStore.ts frontend/package.json frontend/package-lock.json
git commit -m "feat(frontend): add Zustand auth store with persistence"
```

---

## Task 21: Create Login Page Component

**Files:**
- Create: `frontend/src/components/LoginPage.tsx`

**Step 1: Create login component**

```tsx
import { useState, useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import { getCaptcha, login } from '../api'
import { useAuthStore } from '../store/authStore'

export default function LoginPage() {
  const navigate = useNavigate()
  const setAuth = useAuthStore((s) => s.setAuth)
  
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [captchaKey, setCaptchaKey] = useState('')
  const [captchaAnswer, setCaptchaAnswer] = useState('')
  const [captchaImage, setCaptchaImage] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  
  useEffect(() => {
    refreshCaptcha()
  }, [])
  
  async function refreshCaptcha() {
    try {
      const data = await getCaptcha()
      setCaptchaKey(data.captchaKey)
      setCaptchaImage(data.captchaImage)
    } catch (e) {
      setError('获取验证码失败')
    }
  }
  
  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    setError('')
    setLoading(true)
    
    try {
      const data = await login(username, password, captchaKey, captchaAnswer)
      setAuth(data.token, data.userId, data.username, data.mustChangePassword)
      
      if (data.mustChangePassword) {
        navigate('/change-password')
      } else {
        navigate('/')
      }
    } catch (e: any) {
      setError(e.message)
      refreshCaptcha()
    } finally {
      setLoading(false)
    }
  }
  
  return (
    <div className="min-h-screen flex items-center justify-center bg-gray-50">
      <div className="max-w-md w-full p-8 bg-white rounded-lg shadow-md">
        <h2 className="text-2xl font-bold text-center mb-6">登录</h2>
        
        {error && (
          <div className="mb-4 p-3 bg-red-100 text-red-700 rounded">{error}</div>
        )}
        
        <form onSubmit={handleSubmit}>
          <div className="mb-4">
            <label className="block text-sm font-medium mb-2">用户名</label>
            <input
              type="text"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              className="w-full p-2 border rounded focus:outline-none focus:ring-2 focus:ring-blue-500"
              required
            />
          </div>
          
          <div className="mb-4">
            <label className="block text-sm font-medium mb-2">密码</label>
            <input
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              className="w-full p-2 border rounded focus:outline-none focus:ring-2 focus:ring-blue-500"
              required
            />
          </div>
          
          <div className="mb-4">
            <label className="block text-sm font-medium mb-2">验证码</label>
            <div className="flex gap-2">
              <input
                type="text"
                value={captchaAnswer}
                onChange={(e) => setCaptchaAnswer(e.target.value.toUpperCase())}
                className="flex-1 p-2 border rounded focus:outline-none focus:ring-2 focus:ring-blue-500"
                maxLength={4}
                required
              />
              <img
                src={captchaImage}
                alt="captcha"
                onClick={refreshCaptcha}
                className="h-10 cursor-pointer border rounded"
                title="点击刷新"
              />
            </div>
          </div>
          
          <button
            type="submit"
            disabled={loading}
            className="w-full py-2 bg-blue-600 text-white rounded hover:bg-blue-700 disabled:opacity-50"
          >
            {loading ? '登录中...' : '登录'}
          </button>
        </form>
        
        <p className="mt-4 text-center text-sm">
          <a href="/register" className="text-blue-600 hover:underline">
            没有账号？注册
          </a>
        </p>
      </div>
    </div>
  )
}
```

**Step 2: Commit**

```bash
cd /Users/bruceding\ 1/Projects/llm_knowledge
git add frontend/src/components/LoginPage.tsx
git commit -m "feat(frontend): add login page component"
```

---

## Task 22: Create Register Page Component

**Files:**
- Create: `frontend/src/components/RegisterPage.tsx`

**Step 1: Create register component**

Similar structure to LoginPage with additional fields for email and confirm password.

**Step 2: Commit**

```bash
cd /Users/bruceding\ 1/Projects/llm_knowledge
git add frontend/src/components/RegisterPage.tsx
git commit -m "feat(frontend): add register page component"
```

---

## Task 23: Create Change Password Page Component

**Files:**
- Create: `frontend/src/components/ChangePasswordPage.tsx`

**Step 1: Create change password component**

```tsx
import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { changePassword } from '../api'
import { useAuthStore } from '../store/authStore'

export default function ChangePasswordPage() {
  const navigate = useNavigate()
  const setMustChangePassword = useAuthStore((s) => s.setMustChangePassword)
  
  const [currentPassword, setCurrentPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  
  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    setError('')
    
    if (newPassword !== confirmPassword) {
      setError('新密码两次输入不一致')
      return
    }
    
    if (!/[a-zA-Z]/.test(newPassword) || !/\d/.test(newPassword)) {
      setError('密码必须包含字母和数字')
      return
    }
    
    if (newPassword.length < 6 || newPassword.length > 32) {
      setError('密码长度需在6-32字符之间')
      return
    }
    
    setLoading(true)
    try {
      await changePassword(currentPassword, newPassword)
      setMustChangePassword(false)
      navigate('/')
    } catch (e: any) {
      setError(e.message)
    } finally {
      setLoading(false)
    }
  }
  
  return (
    <div className="min-h-screen flex items-center justify-center bg-gray-50">
      <div className="max-w-md w-full p-8 bg-white rounded-lg shadow-md">
        <h2 className="text-2xl font-bold text-center mb-2">修改密码</h2>
        <p className="text-center text-gray-500 mb-6">请设置您的个人密码</p>
        
        {error && (
          <div className="mb-4 p-3 bg-red-100 text-red-700 rounded">{error}</div>
        )}
        
        <form onSubmit={handleSubmit}>
          <div className="mb-4">
            <label className="block text-sm font-medium mb-2">当前密码</label>
            <input
              type="password"
              value={currentPassword}
              onChange={(e) => setCurrentPassword(e.target.value)}
              className="w-full p-2 border rounded focus:outline-none focus:ring-2 focus:ring-blue-500"
              required
            />
          </div>
          
          <div className="mb-4">
            <label className="block text-sm font-medium mb-2">新密码</label>
            <input
              type="password"
              value={newPassword}
              onChange={(e) => setNewPassword(e.target.value)}
              className="w-full p-2 border rounded focus:outline-none focus:ring-2 focus:ring-blue-500"
              required
            />
            <p className="text-xs text-gray-500 mt-1">密码需6-32字符，包含字母和数字</p>
          </div>
          
          <div className="mb-4">
            <label className="block text-sm font-medium mb-2">确认新密码</label>
            <input
              type="password"
              value={confirmPassword}
              onChange={(e) => setConfirmPassword(e.target.value)}
              className="w-full p-2 border rounded focus:outline-none focus:ring-2 focus:ring-blue-500"
              required
            />
          </div>
          
          <button
            type="submit"
            disabled={loading}
            className="w-full py-2 bg-blue-600 text-white rounded hover:bg-blue-700 disabled:opacity-50"
          >
            {loading ? '提交中...' : '提交'}
          </button>
        </form>
      </div>
    </div>
  )
}
```

**Step 2: Commit**

```bash
cd /Users/bruceding\ 1/Projects/llm_knowledge
git add frontend/src/components/ChangePasswordPage.tsx
git commit -m "feat(frontend): add change password page component"
```

---

## Task 24: Create Route Guard Component

**Files:**
- Create: `frontend/src/components/PrivateRoute.tsx`

**Step 1: Create route guard**

```tsx
import { Navigate, useLocation } from 'react-router-dom'
import { useAuthStore } from '../store/authStore'

interface PrivateRouteProps {
  children: React.ReactNode
}

export default function PrivateRoute({ children }: PrivateRouteProps) {
  const location = useLocation()
  const isLoggedIn = useAuthStore((s) => s.isLoggedIn)
  const mustChangePassword = useAuthStore((s) => s.mustChangePassword)
  
  if (!isLoggedIn) {
    return <Navigate to="/login" state={{ from: location }} replace />
  }
  
  if (mustChangePassword && location.pathname !== '/change-password') {
    return <Navigate to="/change-password" replace />
  }
  
  return <>{children}</>
}
```

**Step 2: Commit**

```bash
cd /Users/bruceding\ 1/Projects/llm_knowledge
git add frontend/src/components/PrivateRoute.tsx
git commit -m "feat(frontend): add private route guard component"
```

---

## Task 25: Update App.tsx with Auth Routes

**Files:**
- Modify: `frontend/src/App.tsx`

**Step 1: Import new components**

```tsx
import LoginPage from './components/LoginPage'
import RegisterPage from './components/RegisterPage'
import ChangePasswordPage from './components/ChangePasswordPage'
import PrivateRoute from './components/PrivateRoute'
```

**Step 2: Update Routes**

```tsx
function App() {
  const { i18n } = useTranslation()

  useEffect(() => {
    fetchSettings()
      .then((settings) => i18n.changeLanguage(settings.language))
      .catch(() => {})
  }, [i18n])

  return (
    <BrowserRouter>
      <Routes>
        {/* Public routes */}
        <Route path="/login" element={<LoginPage />} />
        <Route path="/register" element={<RegisterPage />} />
        <Route path="/change-password" element={<ChangePasswordPage />} />
        
        {/* Protected routes */}
        <Route element={<PrivateRoute><Layout /></PrivateRoute>}>
          <Route path="/" element={<Inbox />} />
          <Route path="/documents" element={<DocumentsList />} />
          <Route path="/documents/:id" element={<DocDetail />} />
          <Route path="/wiki/*" element={<WikiView />} />
          <Route path="/chat/:id?" element={<ChatView />} />
          <Route path="/import" element={<ImportView />} />
          <Route path="/tags" element={<TagsView />} />
          <Route path="/settings" element={<SettingsPage />} />
        </Route>
      </Routes>
    </BrowserRouter>
  )
}
```

**Step 3: Commit**

```bash
cd /Users/bruceding\ 1/Projects/llm_knowledge
git add frontend/src/App.tsx
git commit -m "feat(frontend): integrate auth routes with route guards"
```

---

## Task 26: Add Logout Button to Sidebar

**Files:**
- Modify: `frontend/src/components/Sidebar.tsx`

**Step 1: Add logout functionality**

```tsx
import { useNavigate } from 'react-router-dom'
import { useAuthStore } from '../store/authStore'
import { logout } from '../api'

// In the Sidebar component:
const navigate = useNavigate()
const username = useAuthStore((s) => s.username)
const clearAuth = useAuthStore((s) => s.clearAuth)

async function handleLogout() {
  try {
    await logout()
  } catch (e) {}
  clearAuth()
  navigate('/login')
}
```

**Step 2: Add logout button UI**

Add at the bottom of sidebar:

```tsx
<div className="mt-auto p-4 border-t">
  <div className="flex items-center justify-between">
    <span className="text-sm text-gray-600">{username}</span>
    <button
      onClick={handleLogout}
      className="text-sm text-blue-600 hover:underline"
    >
      登出
    </button>
  </div>
</div>
```

**Step 3: Commit**

```bash
cd /Users/bruceding\ 1/Projects/llm_knowledge
git add frontend/src/components/Sidebar.tsx
git commit -m "feat(frontend): add logout button to sidebar"
```

---

## Task 27: Test Backend Auth APIs

**Files:**
- Create: `backend/api/auth_test.go`

**Step 1: Write tests for password validation**

```go
package api

import (
	"testing"
)

func TestValidatePassword(t *testing.T) {
	tests := []struct {
		password string
		valid    bool
		errMsg   string
	}{
		{"abc123", true, ""},
		{"123456", false, "密码必须包含至少一个字母"},
		{"abcdef", false, "密码必须包含至少一个数字"},
		{"ab1", false, "密码长度必须在6-32字符之间"},
		{"a123456789012345678901234567890123", false, "密码长度必须在6-32字符之间"},
	}

	for _, tt := range tests {
		err := validatePassword(tt.password)
		if tt.valid && err != nil {
			t.Errorf("password %s should be valid, got error: %v", tt.password, err)
		}
		if !tt.valid && err == nil {
			t.Errorf("password %s should be invalid", tt.password)
		}
		if !tt.valid && err != nil && err.Error() != tt.errMsg {
			t.Errorf("password %s error message mismatch: got %s, want %s", tt.password, err.Error(), tt.errMsg)
		}
	}
}

func TestValidateUsername(t *testing.T) {
	tests := []struct {
		username string
		valid    bool
	}{
		{"abc", true},
		{"abcd", true},
		{"ab", false},
		{"abcdefghijklmnopqrstu", false},
	}

	for _, tt := range tests {
		err := validateUsername(tt.username)
		if tt.valid && err != nil {
			t.Errorf("username %s should be valid, got error: %v", tt.username, err)
		}
		if !tt.valid && err == nil {
			t.Errorf("username %s should be invalid", tt.username)
		}
	}
}
```

**Step 2: Run tests**

```bash
cd /Users/bruceding\ 1/Projects/llm_knowledge/backend
go test ./api -run TestValidate -v
```

**Step 3: Commit**

```bash
cd /Users/bruceding\ 1/Projects/llm_knowledge
git add backend/api/auth_test.go
git commit -m "test(api): add password and username validation tests"
```

---

## Task 28: Build and Run Integration Test

**Step 1: Build backend**

```bash
cd /Users/bruceding\ 1/Projects/llm_knowledge/backend
go build -o llm-knowledge
```

**Step 2: Start server**

```bash
cd /Users/bruceding\ 1/Projects/llm_knowledge
./backend/llm-knowledge
```

**Step 3: Test captcha API**

```bash
curl http://localhost:8080/api/auth/captcha
```

Expected: JSON with captchaKey and captchaImage

**Step 4: Test register flow**

Use the captcha key from step 3:

```bash
curl -X POST http://localhost:8080/api/auth/register \
  -H "Content-Type: application/json" \
  -d '{"username":"testuser","password":"test123","email":"test@example.com","captchaKey":"<key>","captchaAnswer":"<answer>"}'
```

**Step 5: Test login flow**

```bash
# Get new captcha first
curl http://localhost:8080/api/auth/captcha

# Login
curl -X POST http://localhost:8080/api/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"testuser","password":"test123","captchaKey":"<key>","captchaAnswer":"<answer>"}'
```

**Step 6: Test protected API**

```bash
curl http://localhost:8080/api/documents \
  -H "Authorization: Bearer <token>"
```

---

## Task 29: Build and Test Frontend

**Step 1: Build frontend**

```bash
cd /Users/bruceding\ 1/Projects/llm_knowledge/frontend
npm run build
```

**Step 2: Start development server**

```bash
npm run dev
```

**Step 3: Test login flow manually**

1. Open browser to http://localhost:5173
2. Should redirect to /login
3. Enter credentials and captcha
4. Should redirect to main page or change password

---

## Task 30: Final Integration Commit

**Step 1: Run all tests**

```bash
cd /Users/bruceding\ 1/Projects/llm_knowledge/backend
go test ./... -v
```

**Step 2: Final commit**

```bash
cd /Users/bruceding\ 1/Projects/llm_knowledge
git add -A
git commit -m "feat: complete user authentication system integration

- Add User, Session, Captcha models with migration
- Implement auth handlers (register, login, logout, captcha)
- Add auth middleware with sliding expiration
- Add UserID filtering to all handlers
- Create frontend auth pages (login, register, change password)
- Add Zustand auth store with route guards

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Summary

This plan covers:
- **Backend (Tasks 1-17):** Models, handlers, middleware, route configuration
- **Frontend (Tasks 18-26):** Types, API, store, pages, routes
- **Testing (Tasks 27-30):** Unit tests, integration tests, final build

Each task follows TDD principles with small, atomic commits.