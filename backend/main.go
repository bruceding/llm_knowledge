package main

import (
	"llm-knowledge/api"
	"llm-knowledge/config"
	"llm-knowledge/db"
	"llm-knowledge/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

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

	// Serve frontend static files from embedded dist
	e.GET("/", func(c echo.Context) error {
		data, err := fs.DistFS.ReadFile("dist/index.html")
		if err != nil {
			return c.String(http.StatusNotFound, "index.html not found")
		}
		return c.HTML(http.StatusOK, string(data))
	})

	// Serve frontend static assets
	e.GET("/assets/*", func(c echo.Context) error {
		// Get the path from request
		reqPath := c.Request().URL.Path
		path := "dist" + reqPath
		data, err := fs.DistFS.ReadFile(path)
		if err != nil {
			return c.String(http.StatusNotFound, "asset not found")
		}
		// Determine content type based on extension
		ext := filepath.Ext(path)
		contentType := "application/octet-stream"
		switch ext {
		case ".js":
			contentType = "application/javascript"
		case ".css":
			contentType = "text/css"
		case ".svg":
			contentType = "image/svg+xml"
		}
		return c.Blob(http.StatusOK, contentType, data)
	})

	// Serve favicon and icons
	e.GET("/favicon.svg", func(c echo.Context) error {
		data, err := fs.DistFS.ReadFile("dist/favicon.svg")
		if err != nil {
			return c.String(http.StatusNotFound, "favicon not found")
		}
		return c.Blob(http.StatusOK, "image/svg+xml", data)
	})

	e.GET("/icons.svg", func(c echo.Context) error {
		data, err := fs.DistFS.ReadFile("dist/icons.svg")
		if err != nil {
			return c.String(http.StatusNotFound, "icons not found")
		}
		return c.Blob(http.StatusOK, "image/svg+xml", data)
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
	e.DELETE("/api/documents/:id", docH.DeleteDoc)

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

	e.Logger.Fatal(e.Start(":" + cfg.Port))
}