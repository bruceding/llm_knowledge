package api

import (
	"llm-knowledge/config"
	"llm-knowledge/db"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
)

// GlobalDataDir is set during initialization in main.go
var GlobalDataDir string

// SetDataDir sets the global data directory for middleware use
func SetDataDir(dataDir string) {
	GlobalDataDir = dataDir
}

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

		// Inject userId and userDir into context
		c.Set("userId", session.UserID)
		userDir := config.GetUserDir(GlobalDataDir, session.UserID)
		c.Set("userDir", userDir)
		c.Set("userIdStr", strconv.FormatUint(uint64(session.UserID), 10))

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

// IsAdmin checks if the current user has admin role
func IsAdmin(c echo.Context) bool {
	userId := GetCurrentUserId(c)
	if userId == 0 {
		return false
	}
	var user db.User
	if err := db.DB.First(&user, userId).Error; err != nil {
		return false
	}
	return user.Role == "admin"
}

// AdminMiddleware requires the authenticated user to have admin role
func AdminMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		if !IsAdmin(c) {
			return c.JSON(403, echo.Map{"error": "admin access required"})
		}
		return next(c)
	}
}

// GetUserDir extracts userDir from context
func GetUserDir(c echo.Context) string {
	userDir, ok := c.Get("userDir").(string)
	if !ok || userDir == "" {
		return ""
	}
	return userDir
}

// GetUserIdStr extracts userId as string from context
func GetUserIdStr(c echo.Context) string {
	userIdStr, ok := c.Get("userIdStr").(string)
	if !ok {
		return ""
	}
	return userIdStr
}

// StripUserPrefix removes the "users/{userId}/" prefix from a path.
// Returns the path relative to userDir for use with Claude CLI.
// E.g., "users/1/raw/papers/foo" -> "raw/papers/foo"
// Legacy paths without "users/" prefix are returned unchanged.
func StripUserPrefix(path string) string {
	if strings.HasPrefix(path, "users/") {
		parts := strings.SplitN(path, "/", 3)
		if len(parts) >= 3 && parts[2] != "" {
			return parts[2]
		}
	}
	return path
}