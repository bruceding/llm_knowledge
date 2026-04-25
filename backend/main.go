package main

import (
	"io"
	"io/fs"
	"llm-knowledge/api"
	"llm-knowledge/config"
	"llm-knowledge/db"
	embedfs "llm-knowledge/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

func main() {
	cfg := config.Load()

	// Initialize directory structure
	if err := embedfs.InitDirs(cfg.DataDir); err != nil {
		log.Fatalf("Failed to initialize directories: %v", err)
	}

	// Setup log file
	logDir := cfg.LogDir
	if err := os.MkdirAll(logDir, 0755); err != nil {
		log.Fatalf("Failed to create log directory: %v", err)
	}

	// Open log file with daily rotation naming
	logFileName := filepath.Join(logDir, "app-"+time.Now().Format("2006-01-02")+".log")
	logFile, err := os.OpenFile(logFileName, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log.Fatalf("Failed to open log file: %v", err)
	}
	// Write to both file and stdout
	log.SetOutput(io.MultiWriter(os.Stdout, logFile))

	// Initialize database
	dbPath := filepath.Join(cfg.DataDir, "data", "knowledge.db")
	if err := db.Init(dbPath); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}

	e := echo.New()

	// Configure Echo logger to write to file
	e.Use(middleware.LoggerWithConfig(middleware.LoggerConfig{
		Output: io.MultiWriter(os.Stdout, logFile),
	}))
	e.Use(middleware.CORS())

	e.GET("/api/health", func(c echo.Context) error {
		return c.JSON(200, map[string]string{"status": "ok"})
	})

	// Serve data directory files (wiki, raw, etc.)
	e.GET("/data/*", func(c echo.Context) error {
		// Remove /data prefix and serve from cfg.DataDir
		relPath := c.Param("*")
		fullPath := filepath.Join(cfg.DataDir, relPath)

		// Security check: ensure path is within DataDir
		absDataDir, err := filepath.Abs(cfg.DataDir)
		if err != nil {
			return c.String(http.StatusInternalServerError, "path error")
		}
		absFullPath, err := filepath.Abs(fullPath)
		if err != nil {
			return c.String(http.StatusInternalServerError, "path error")
		}

		// Check if path starts with DataDir
		if !strings.HasPrefix(absFullPath, absDataDir) {
			return c.String(http.StatusForbidden, "access denied")
		}

		// Check if file exists
		if _, err := os.Stat(absFullPath); err != nil {
			return c.String(http.StatusNotFound, "file not found")
		}

		// Serve the file
		return c.File(absFullPath)
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
	e.POST("/api/documents/:id/re-extract", docH.ReExtract)
	e.POST("/api/documents/:id/llm-extract", docH.LLMExtract)
	e.POST("/api/documents/:id/html-extract", docH.HTMLExtract)
	e.POST("/api/documents/:id/regenerate-summary", docH.RegenerateSummary)
	e.DELETE("/api/documents/:id", docH.DeleteDoc)

	// Pages API (page image generation for bilingual view)
	pagesH := &api.PagesHandler{
		DataDir: cfg.DataDir,
	}
	e.POST("/api/documents/:id/generate-pages", pagesH.GeneratePages)
	e.GET("/api/documents/:id/pages-status", pagesH.CheckPages)

	// Query API (SSE streaming)
	queryH := &api.QueryHandler{
		DataDir:   cfg.DataDir,
		ClaudeBin: cfg.ClaudeBin,
	}
	e.POST("/api/query/ask", queryH.Ask)

	// Translate API (SSE streaming)
	translateH := &api.TranslateHandler{
		DataDir:   cfg.DataDir,
		ClaudeBin: cfg.ClaudeBin,
	}
	e.POST("/api/translate", translateH.Translate)

	// Settings API
	settingsH := &api.SettingsHandler{}
	e.GET("/api/settings", settingsH.GetSettings)
	e.PUT("/api/settings", settingsH.UpdateSettings)

	// Serve frontend static files from embedded filesystem
	// Create a sub filesystem from the embedded dist directory
	distSubFS, err := fs.Sub(embedfs.DistFS, "dist")
	if err != nil {
		log.Fatalf("Failed to create sub filesystem: %v", err)
	}
	e.StaticFS("/", distSubFS)

	// SPA fallback: serve index.html for unmatched frontend routes
	// This handles client-side routing for paths like /inbox, /documents, etc.
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			// First try the next handler (static file serving)
			err := next(c)
			if err == nil {
				return nil
			}

			// Check if it's a 404 error and not an API/data route
			path := c.Request().URL.Path
			if strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/data/") {
				return err // Return the original error for API routes
			}

			// For frontend routes, serve index.html for SPA routing
			if he, ok := err.(*echo.HTTPError); ok && he.Code == http.StatusNotFound {
				// Serve index.html from embedded filesystem
				data, err := embedfs.DistFS.ReadFile("dist/index.html")
				if err != nil {
					return c.String(http.StatusInternalServerError, "index.html not found")
				}
				return c.HTML(http.StatusOK, string(data))
			}

			return err
		}
	})

	e.Logger.Fatal(e.Start(":" + cfg.Port))
}
