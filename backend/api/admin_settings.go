package api

import (
	"llm-knowledge/db"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
)

const (
	defaultTranslationApiBase = "https://dashscope.aliyuncs.com/compatible-mode/v1"
	defaultTranslationModel   = "deepseek-v4-flash"

	// apiKeyMask is the placeholder returned in GET responses to mask the real key
	apiKeyMask = "••••••••"
)

// maskApiKey returns a masked version of the API key for display.
// Shows last 4 chars if key is long enough, otherwise returns the mask placeholder.
func maskApiKey(key string) string {
	if key == "" {
		return ""
	}
	if len(key) > 4 {
		return "****" + key[len(key)-4:]
	}
	return apiKeyMask
}

// ensureGlobalSettings loads the singleton GlobalSettings row.
// Uses empty-condition FirstOrCreate to avoid the GORM bug where
// non-zero search conditions create duplicate rows when values differ from defaults.
func ensureGlobalSettings() (db.GlobalSettings, error) {
	var settings db.GlobalSettings
	result := db.DB.FirstOrCreate(&settings)
	if result.Error != nil {
		return settings, result.Error
	}
	if settings.ID == 0 {
		// Row was just created with zero values; set defaults
		settings.TranslationApiBase = defaultTranslationApiBase
		settings.TranslationModel = defaultTranslationModel
		db.DB.Save(&settings)
	}
	return settings, nil
}

type AdminSettingsHandler struct{}

// globalSettingsResponse builds the JSON response for global settings,
// including a masked API key instead of the real one.
func globalSettingsResponse(settings db.GlobalSettings) echo.Map {
	return echo.Map{
		"id":                 settings.ID,
		"translationEnabled": settings.TranslationEnabled,
		"translationApiBase": settings.TranslationApiBase,
		"translationApiKey":  maskApiKey(settings.TranslationApiKey),
		"translationModel":   settings.TranslationModel,
		"createdAt":          settings.CreatedAt,
		"updatedAt":          settings.UpdatedAt,
	}
}

// GetGlobalSettings returns global settings (admin only)
// GET /api/admin/settings
func (h *AdminSettingsHandler) GetGlobalSettings(c echo.Context) error {
	settings, err := ensureGlobalSettings()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to get global settings"})
	}
	return c.JSON(http.StatusOK, globalSettingsResponse(settings))
}

// UpdateGlobalSettings updates global settings (admin only)
// PUT /api/admin/settings
func (h *AdminSettingsHandler) UpdateGlobalSettings(c echo.Context) error {
	settings, err := ensureGlobalSettings()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to get global settings"})
	}

	var input struct {
		TranslationEnabled *bool   `json:"translationEnabled"`
		TranslationApiBase string  `json:"translationApiBase"`
		TranslationApiKey  string  `json:"translationApiKey"`
		TranslationModel   string  `json:"translationModel"`
	}
	if err := c.Bind(&input); err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "invalid input"})
	}

	// Apply partial updates
	if input.TranslationEnabled != nil {
		settings.TranslationEnabled = *input.TranslationEnabled
	}
	if input.TranslationApiBase != "" {
		settings.TranslationApiBase = input.TranslationApiBase
	}
	if input.TranslationModel != "" {
		settings.TranslationModel = input.TranslationModel
	}
	// Only update API key if a real value is provided (not the mask placeholder)
	if input.TranslationApiKey != "" && input.TranslationApiKey != apiKeyMask && !strings.HasPrefix(input.TranslationApiKey, "****") {
		settings.TranslationApiKey = input.TranslationApiKey
	}

	// Validate when translation is enabled
	if settings.TranslationEnabled {
		if settings.TranslationApiKey == "" {
			return c.JSON(http.StatusBadRequest, echo.Map{"error": "API key required when translation is enabled"})
		}
		if settings.TranslationApiBase == "" {
			return c.JSON(http.StatusBadRequest, echo.Map{"error": "API base URL required when translation is enabled"})
		}
		if settings.TranslationModel == "" {
			return c.JSON(http.StatusBadRequest, echo.Map{"error": "Model name required when translation is enabled"})
		}
	}

	if err := db.DB.Save(&settings).Error; err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to save global settings"})
	}

	return c.JSON(http.StatusOK, globalSettingsResponse(settings))
}
