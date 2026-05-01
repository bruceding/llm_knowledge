package main

import (
	"context"
	"io"
	"io/fs"
	"llm-knowledge/api"
	"llm-knowledge/claude"
	"llm-knowledge/config"
	"llm-knowledge/db"
	"llm-knowledge/dependencies"
	embedfs "llm-knowledge/fs"
	"llm-knowledge/pdf2zh"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
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

	// Check and install pdf2zh asynchronously
	pdf2zh.CheckAndInstall(cfg.PDF2ZhVenvDir)

	// Check all dependencies (Claude CLI, plugins) asynchronously
	dependencies.CheckAll()

	e := echo.New()

	// Configure Echo logger to write to file
	e.Use(middleware.LoggerWithConfig(middleware.LoggerConfig{
		Output: io.MultiWriter(os.Stdout, logFile),
	}))
	e.Use(middleware.CORS())

	e.GET("/api/health", func(c echo.Context) error {
		return c.JSON(200, map[string]string{"status": "ok"})
	})

	// Auth API (public routes - no middleware)
	authH := &api.AuthHandler{}
	e.GET("/api/auth/captcha", authH.GetCaptcha)
	e.POST("/api/auth/register", authH.Register)
	e.POST("/api/auth/login", authH.Login)
	e.GET("/api/auth/status", authH.Status)

	// Dependencies API (public routes - for setup/initial checks)
	depsH := &api.DependenciesHandler{}
	e.GET("/api/dependencies/status", depsH.GetStatus)
	e.POST("/api/dependencies/check", depsH.Check)

	// Protected routes (require auth)
	apiGroup := e.Group("/api")
	apiGroup.Use(api.AuthMiddleware)

	// Auth routes requiring authentication
	apiGroup.POST("/auth/logout", authH.Logout)
	apiGroup.PUT("/auth/password", authH.ChangePassword)

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

	// Raw file storage API (protected)
	rawH := &api.RawHandler{
		DataDir:   cfg.DataDir,
		ClaudeBin: cfg.ClaudeBin,
	}
	apiGroup.POST("/raw/pdf", rawH.UploadPDF, middleware.BodyLimit("50M"))
	apiGroup.POST("/raw/pdf-url", rawH.UploadPDFFromURL)

	// Web clipping API (protected)
	webH := &api.WebHandler{
		DataDir:   cfg.DataDir,
		ClaudeBin: cfg.ClaudeBin,
	}
	apiGroup.POST("/raw/web", webH.UploadWeb)

	// Document CRUD API (protected)
	docH := &api.DocHandler{
		DataDir:   cfg.DataDir,
		ClaudeBin: cfg.ClaudeBin,
	}
	apiGroup.GET("/documents/inbox", docH.ListInbox)
	apiGroup.GET("/documents", docH.ListAll)
	apiGroup.GET("/documents/:id", docH.GetDoc)
	apiGroup.PUT("/documents/:id", docH.UpdateDoc)
	apiGroup.POST("/documents/:id/publish", docH.Publish)
	apiGroup.POST("/documents/:id/re-extract", docH.ReExtract)
	apiGroup.POST("/documents/:id/llm-extract", docH.LLMExtract)
	apiGroup.POST("/documents/:id/html-extract", docH.HTMLExtract)
	apiGroup.POST("/documents/:id/regenerate-summary", docH.RegenerateSummary)
	apiGroup.DELETE("/documents/:id", docH.DeleteDoc)

	// Pages API (page image generation for bilingual view) (protected)
	pagesH := &api.PagesHandler{
		DataDir: cfg.DataDir,
	}
	apiGroup.POST("/documents/:id/generate-pages", pagesH.GeneratePages)
	apiGroup.GET("/documents/:id/pages-status", pagesH.CheckPages)

	// Query API (SSE streaming with session pool) (protected)
	querySessionPool := claude.NewQuerySessionPool(cfg.DataDir, cfg.ClaudeBin)
	queryH := &api.QueryHandler{
		DataDir:   cfg.DataDir,
		ClaudeBin: cfg.ClaudeBin,
		Pool:      querySessionPool,
	}
	apiGroup.POST("/query/conversation", queryH.CreateConversation)
	apiGroup.GET("/query/stream", queryH.Stream)
	apiGroup.POST("/query/message", queryH.Message)
	apiGroup.POST("/query/interrupt", queryH.Interrupt)
	apiGroup.GET("/conversations", queryH.ListConversations)
	apiGroup.GET("/conversations/:id/messages", queryH.GetConversationMessages)
	apiGroup.DELETE("/conversations/:id", queryH.DeleteConversation)

	// Translate API (SSE streaming) (protected)
	translateH := &api.TranslateHandler{
		DataDir:   cfg.DataDir,
		ClaudeBin: cfg.ClaudeBin,
	}
	apiGroup.POST("/translate", translateH.Translate)

	// Settings API (protected)
	settingsH := &api.SettingsHandler{}
	apiGroup.GET("/settings", settingsH.GetSettings)
	apiGroup.PUT("/settings", settingsH.UpdateSettings)

	// PDF Translation API (protected)
	pdfTranslateH := &api.PDFTranslateHandler{
		DataDir:       cfg.DataDir,
		PDF2ZhVenvDir: cfg.PDF2ZhVenvDir,
	}
	apiGroup.GET("/documents/:id/translation-status", pdfTranslateH.CheckTranslationStatus)
	apiGroup.POST("/pdf-translate", pdfTranslateH.TranslatePDF)

	// Markdown Translation API (SSE streaming) (protected)
	markdownTranslateH := &api.MarkdownTranslateHandler{
		DataDir: cfg.DataDir,
	}
	apiGroup.POST("/markdown-translate", markdownTranslateH.TranslateMarkdown)
	apiGroup.GET("/documents/:id/markdown-translation-status", markdownTranslateH.CheckMarkdownTranslationStatus)

	// Image Upload API (protected)
	imagesH := &api.ImagesHandler{
		DataDir: cfg.DataDir,
	}
	apiGroup.POST("/images/upload", imagesH.Upload, middleware.BodyLimit("15M"))

	// Document Chat API (SSE streaming with session pool) (protected)
	sessionPool := claude.NewSessionPool(cfg.DataDir, cfg.ClaudeBin)
	docChatH := &api.DocChatHandler{
		Pool:    sessionPool,
		DataDir: cfg.DataDir,
	}
	apiGroup.GET("/doc-chat/stream", docChatH.Stream)
	apiGroup.POST("/doc-chat/message", docChatH.Message)
	apiGroup.GET("/doc-chat/reconnect", docChatH.Reconnect)

	// Doc Notes API (CRUD + wiki push) (protected)
	docNotesH := &api.DocNoteHandler{
		DataDir: cfg.DataDir,
	}
	apiGroup.GET("/documents/:id/notes", docNotesH.ListNotes)
	apiGroup.POST("/documents/:id/notes", docNotesH.CreateNote)
	apiGroup.PUT("/documents/:id/notes/:noteId", docNotesH.UpdateNote)
	apiGroup.DELETE("/documents/:id/notes/:noteId", docNotesH.DeleteNote)
	apiGroup.POST("/documents/:id/notes/:noteId/wiki-push", docNotesH.PushToWiki)

	// RSS API (protected)
	rssH := &api.RSSHandler{
		DataDir:   cfg.DataDir,
		ClaudeBin: cfg.ClaudeBin,
	}
	apiGroup.POST("/rss/feeds", rssH.AddFeed)
	apiGroup.GET("/rss/feeds", rssH.ListFeeds)
	apiGroup.DELETE("/rss/feeds/:id", rssH.DeleteFeed)
	apiGroup.POST("/rss/feeds/:id/sync", rssH.SyncFeed)

	// Start RSS auto-sync scheduler
	rssH.StartAutoSyncScheduler()

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

	// Graceful shutdown: listen for OS signals and clean up session pools
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// Start server in a goroutine
	serverErr := make(chan error, 1)
	go func() {
		if err := e.Start(":" + cfg.Port); err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
	}()

	select {
	case err := <-serverErr:
		log.Fatalf("Server error: %v", err)
	case sig := <-quit:
		log.Printf("Received signal %v, shutting down gracefully...", sig)
	}

	// Give outstanding requests 10 seconds to complete
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := e.Shutdown(ctx); err != nil {
		log.Printf("HTTP server shutdown error: %v", err)
	}

	// Close all Claude session pools to kill child processes
	querySessionPool.Close()
	sessionPool.Close()

	// Close database connection (cleanup sessions)
	db.Close()

	log.Println("Server exited cleanly")
}
