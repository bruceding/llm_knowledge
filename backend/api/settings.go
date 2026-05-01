package api

import (
	"llm-knowledge/db"
	"net/http"

	"github.com/labstack/echo/v4"
)

type SettingsHandler struct{}

func (h *SettingsHandler) GetSettings(c echo.Context) error {
	userId := GetCurrentUserId(c)
	var settings db.UserSettings
	result := db.DB.Where("user_id = ?", userId).FirstOrCreate(&settings, db.UserSettings{
		UserID:             userId,
		Language:           "en",
		TranslationEnabled: false,
		TranslationApiBase: "https://dashscope.aliyuncs.com/compatible-mode/v1",
		TranslationModel:   "deepseek-v4-flash",
	})
	if result.Error != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to get settings"})
	}
	return c.JSON(http.StatusOK, settings)
}

func (h *SettingsHandler) UpdateSettings(c echo.Context) error {
	userId := GetCurrentUserId(c)
	var settings db.UserSettings
	result := db.DB.Where("user_id = ?", userId).FirstOrCreate(&settings, db.UserSettings{
		UserID:             userId,
		Language:           "en",
		TranslationApiBase: "https://dashscope.aliyuncs.com/compatible-mode/v1",
		TranslationModel:   "deepseek-v4-flash",
	})
	if result.Error != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to get settings"})
	}

	var input struct {
		Language           string `json:"language"`
		TranslationEnabled bool   `json:"translationEnabled"`
		TranslationApiBase string `json:"translationApiBase"`
		TranslationApiKey  string `json:"translationApiKey"`
		TranslationModel   string `json:"translationModel"`
	}
	if err := c.Bind(&input); err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "invalid input"})
	}

	if input.Language != "en" && input.Language != "zh" {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "language must be 'en' or 'zh'"})
	}

	// 验证翻译配置
	if input.TranslationEnabled && input.TranslationApiKey == "" {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "API key required when translation is enabled"})
	}

	settings.UserID = userId
	settings.Language = input.Language
	settings.TranslationEnabled = input.TranslationEnabled
	settings.TranslationApiBase = input.TranslationApiBase
	settings.TranslationApiKey = input.TranslationApiKey
	settings.TranslationModel = input.TranslationModel

	if err := db.DB.Save(&settings).Error; err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to save settings"})
	}

	return c.JSON(http.StatusOK, settings)
}
