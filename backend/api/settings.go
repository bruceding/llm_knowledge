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
			settings = db.UserSettings{Language: "en"}
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
		settings = db.UserSettings{Language: "en"}
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

	settings.Language = input.Language
	if err := db.DB.Save(&settings).Error; err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to save settings"})
	}

	return c.JSON(http.StatusOK, settings)
}
