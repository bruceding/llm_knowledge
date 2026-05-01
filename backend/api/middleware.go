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