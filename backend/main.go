package main

import (
	"llm-knowledge/api"
	"llm-knowledge/config"
	"llm-knowledge/db"
	"llm-knowledge/fs"
	"log"
	"path/filepath"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

func main() {
	cfg := config.Load()

	// Initialize directory structure
	if err := fs.InitDirs(cfg.DataDir); err != nil {
		log.Fatalf("Failed to initialize directories: %v", err)
	}

	// Initialize database
	dbPath := filepath.Join(cfg.DataDir, "data", "knowledge.db")
	if err := db.Init(dbPath); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}

	e := echo.New()
	e.Use(middleware.Logger())
	e.Use(middleware.CORS())

	e.GET("/api/health", func(c echo.Context) error {
		return c.JSON(200, map[string]string{"status": "ok"})
	})

	// Raw file storage API
	rawH := &api.RawHandler{
		DataDir:   cfg.DataDir,
		ClaudeBin: cfg.ClaudeBin,
	}
	e.POST("/api/raw/pdf", rawH.UploadPDF, middleware.BodyLimit("50M"))

	// Document CRUD API
	docH := &api.DocHandler{
		DataDir: cfg.DataDir,
	}
	e.GET("/api/documents/inbox", docH.ListInbox)
	e.GET("/api/documents", docH.ListAll)
	e.GET("/api/documents/:id", docH.GetDoc)
	e.PUT("/api/documents/:id", docH.UpdateDoc)
	e.POST("/api/documents/:id/publish", docH.Publish)
	e.DELETE("/api/documents/:id", docH.DeleteDoc)

	e.Logger.Fatal(e.Start(":" + cfg.Port))
}
