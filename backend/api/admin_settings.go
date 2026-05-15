package api

import (
	"llm-knowledge/db"
	"net/http"

	"github.com/labstack/echo/v4"
)

type AdminSettingsHandler struct{}

// GetGlobalSettings returns global settings (admin only)
// GET /api/admin/settings
func (h *AdminSettingsHandler) GetGlobalSettings(c echo.Context) error {
	var settings db.GlobalSettings
	result := db.DB.FirstOrCreate(&settings, db.GlobalSettings{
		TranslationApiBase: "https://dashscope.aliyuncs.com/compatible-mode/v1",
		TranslationModel:   "deepseek-v4-flash",
	})
	if result.Error != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to get global settings"})
	}
	return c.JSON(http.StatusOK, settings)
}

// UpdateGlobalSettings updates global settings (admin only)
// PUT /api/admin/settings
func (h *AdminSettingsHandler) UpdateGlobalSettings(c echo.Context) error {
	var settings db.GlobalSettings
	result := db.DB.FirstOrCreate(&settings, db.GlobalSettings{
		TranslationApiBase: "https://dashscope.aliyuncs.com/compatible-mode/v1",
		TranslationModel:   "deepseek-v4-flash",
	})
	if result.Error != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to get global settings"})
	}

	var input struct {
		TranslationEnabled bool   `json:"translationEnabled"`
		TranslationApiBase string `json:"translationApiBase"`
		TranslationApiKey  string `json:"translationApiKey"`
		TranslationModel   string `json:"translationModel"`
	}
	if err := c.Bind(&input); err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "invalid input"})
	}

	if input.TranslationEnabled && input.TranslationApiKey == "" {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "API key required when translation is enabled"})
	}

	settings.TranslationEnabled = input.TranslationEnabled
	settings.TranslationApiBase = input.TranslationApiBase
	settings.TranslationApiKey = input.TranslationApiKey
	settings.TranslationModel = input.TranslationModel

	if err := db.DB.Save(&settings).Error; err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to save global settings"})
	}

	return c.JSON(http.StatusOK, settings)
}

// GetGlobalTranslationStatus returns whether translation is globally enabled (for all users)
// GET /api/admin/translation-status
func (h *AdminSettingsHandler) GetGlobalTranslationStatus(c echo.Context) error {
	var settings db.GlobalSettings
	if err := db.DB.First(&settings).Error; err != nil {
		return c.JSON(http.StatusOK, echo.Map{"enabled": false})
	}
	return c.JSON(http.StatusOK, echo.Map{"enabled": settings.TranslationEnabled})
}
