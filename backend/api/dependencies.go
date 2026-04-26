package api

import (
	"llm-knowledge/dependencies"
	"net/http"

	"github.com/labstack/echo/v4"
)

type DependenciesHandler struct{}

// GetStatus returns all dependency statuses
func (h *DependenciesHandler) GetStatus(c echo.Context) error {
	statuses := dependencies.GetStatuses()
	log := dependencies.GetInstallLog()

	return c.JSON(http.StatusOK, echo.Map{
		"statuses": statuses,
		"log":      log,
		"ready":    dependencies.IsReady(),
	})
}

// CheckDependencies triggers a re-check of all dependencies
func (h *DependenciesHandler) Check(c echo.Context) error {
	dependencies.CheckAll()

	return c.JSON(http.StatusOK, echo.Map{
		"message": "Dependency check started",
	})
}