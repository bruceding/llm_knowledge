package api

import (
	"errors"
	"strings"
	"time"
	"unicode"

	"llm-knowledge/db"

	"github.com/labstack/echo/v4"
	"github.com/mojocn/base64Captcha"
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