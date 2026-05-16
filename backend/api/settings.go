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
		UserID:   userId,
		Language: "en",
	})
	if result.Error != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to get settings"})
	}

	// Also return whether user is admin and translation status
	isAdmin := IsAdmin(c)
	globalSettings, _ := ensureGlobalSettings()

	return c.JSON(http.StatusOK, echo.Map{
		"id":                 settings.ID,
		"userId":             settings.UserID,
		"language":           settings.Language,
		"createdAt":          settings.CreatedAt,
		"updatedAt":          settings.UpdatedAt,
		"isAdmin":            isAdmin,
		"translationEnabled": globalSettings.TranslationEnabled,
	})
}

func (h *SettingsHandler) UpdateSettings(c echo.Context) error {
	userId := GetCurrentUserId(c)
	var settings db.UserSettings
	result := db.DB.Where("user_id = ?", userId).FirstOrCreate(&settings, db.UserSettings{
		UserID:   userId,
		Language: "en",
	})
	if result.Error != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to get settings"})
	}

	var input struct {
		Language string `json:"language"`
	}
	if err := c.Bind(&input); err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "invalid input"})
	}

	if input.Language != "en" && input.Language != "zh" {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "language must be 'en' or 'zh'"})
	}

	settings.UserID = userId
	settings.Language = input.Language

	if err := db.DB.Save(&settings).Error; err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to save settings"})
	}

	isAdmin := IsAdmin(c)
	globalSettings, _ := ensureGlobalSettings()

	return c.JSON(http.StatusOK, echo.Map{
		"id":                 settings.ID,
		"userId":             settings.UserID,
		"language":           settings.Language,
		"createdAt":          settings.CreatedAt,
		"updatedAt":          settings.UpdatedAt,
		"isAdmin":            isAdmin,
		"translationEnabled": globalSettings.TranslationEnabled,
	})
}
