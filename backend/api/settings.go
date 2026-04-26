package api

import (
	"errors"
	"llm-knowledge/db"
	"net/http"

	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

type SettingsHandler struct{}

func (h *SettingsHandler) GetSettings(c echo.Context) error {
	var settings db.UserSettings
	result := db.DB.First(&settings)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			// Create default settings if not exists
			settings = db.UserSettings{
				Language:           "en",
				TranslationEnabled: false,
				TranslationApiBase: "https://dashscope.aliyuncs.com/compatible-mode/v1",
				TranslationModel:   "deepseek-v4-flash",
			}
			if err := db.DB.Create(&settings).Error; err != nil {
				return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to create default settings"})
			}
		} else {
			return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to get settings"})
		}
	}
	return c.JSON(http.StatusOK, settings)
}

func (h *SettingsHandler) UpdateSettings(c echo.Context) error {
	var settings db.UserSettings
	result := db.DB.First(&settings)
	if result.Error != nil {
		// Create if not exists
		settings = db.UserSettings{
			Language:           "en",
			TranslationApiBase: "https://dashscope.aliyuncs.com/compatible-mode/v1",
			TranslationModel:   "deepseek-v4-flash",
		}
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
