package main

import (
	"context"
	"io"
	"io/fs"
	"llm-knowledge/api"
	"llm-knowledge/browser"
	"llm-knowledge/claude"
	"llm-knowledge/config"
	"llm-knowledge/db"
	"llm-knowledge/dependencies"
	embedfs "llm-knowledge/fs"
	"llm-knowledge/pdf2zh"
	"log"
	"net/http"
	"net/url"
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

	// Migrate existing files to user-partitioned structure (if needed)
	migrateFilesToUserPartition(cfg.DataDir)

	// Set global data directory for middleware to compute userDir
	api.SetDataDir(cfg.DataDir)

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

	// Serve data directory files (wiki, raw, etc.) - now protected with auth
	apiGroup.GET("/data/*", func(c echo.Context) error {
		userDir := api.GetUserDir(c)
		if userDir == "" {
			return c.String(http.StatusInternalServerError, "user context error")
		}

		// Remove /data prefix and serve from userDir
		relPath := c.Param("*")
		// Decode URL-encoded path (frontend uses encodeURIComponent)
		if decoded, err := url.PathUnescape(relPath); err == nil {
			relPath = decoded
		}
		fullPath := filepath.Join(userDir, relPath)

		// Security check: ensure path is within userDir
		absUserDir, err := filepath.Abs(userDir)
		if err != nil {
			return c.String(http.StatusInternalServerError, "path error")
		}
		absFullPath, err := filepath.Abs(fullPath)
		if err != nil {
			return c.String(http.StatusInternalServerError, "path error")
		}

		// Check if path starts with userDir (prevents path traversal)
		if !strings.HasPrefix(absFullPath, absUserDir) {
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
	browserPool := browser.NewPool(2)
	defer browserPool.Close()

	webH := &api.WebHandler{
		DataDir:     cfg.DataDir,
		ClaudeBin:   cfg.ClaudeBin,
		BrowserPool: browserPool,
	}
	apiGroup.POST("/raw/web", webH.UploadWeb)
	apiGroup.POST("/raw/web-clip", webH.ClipWeb, middleware.BodyLimit("10M"))

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
	apiGroup.GET("/query/status", queryH.Status)
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

	// Newsletter IMAP API (protected)
	newsletterH := &api.NewsletterHandler{
		DataDir:   cfg.DataDir,
		ClaudeBin: cfg.ClaudeBin,
	}
	apiGroup.GET("/imap/config", newsletterH.GetConfig)
	apiGroup.PUT("/imap/config", newsletterH.UpdateConfig)
	apiGroup.DELETE("/imap/config", newsletterH.DeleteConfig)
	apiGroup.POST("/imap/test", newsletterH.TestConnection)
	apiGroup.GET("/imap/folders", newsletterH.ListFolders)
	apiGroup.POST("/imap/sync", newsletterH.Sync)
	apiGroup.GET("/imap/sync-status", newsletterH.SyncStatus)

	newsletterH.StartAutoSyncScheduler()

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

// migrateFilesToUserPartition moves existing files from shared raw/wiki directories
// to user-partitioned structure based on document ownership in database.
func migrateFilesToUserPartition(dataDir string) {
	usersPath := filepath.Join(dataDir, "users")

	// If users/ already exists, migration already done
	if _, err := os.Stat(usersPath); err == nil {
		return
	}

	// Check if old structure exists
	oldRawPath := filepath.Join(dataDir, "raw")
	oldWikiPath := filepath.Join(dataDir, "wiki")

	rawExists := false
	if _, err := os.Stat(oldRawPath); err == nil {
		rawExists = true
	}
	wikiExists := false
	if _, err := os.Stat(oldWikiPath); err == nil {
		wikiExists = true
	}
	if !rawExists && !wikiExists {
		return
	}

	log.Printf("[migration] Migrating files to user-partitioned structure")

	// Get all users from database
	var users []db.User
	if err := db.DB.Find(&users).Error; err != nil {
		log.Printf("[migration] Failed to get users: %v", err)
		return
	}

	// Initialize directory structure for each user
	for _, user := range users {
		embedfs.InitUserDirs(dataDir, user.ID)
	}

	// Migrate raw files based on document ownership
	if rawExists {
		migrateRawFiles(dataDir, oldRawPath)
	}

	// Migrate wiki files based on document ownership
	if wikiExists {
		migrateWikiFiles(dataDir, oldWikiPath)
	}

	log.Printf("[migration] File migration completed")
}

// migrateRawFiles moves raw files to user directories based on document ownership
func migrateRawFiles(dataDir string, oldRawPath string) {
	// Walk through all files in raw/ directory
	err := filepath.Walk(oldRawPath, func(oldPath string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}

		// Get relative path from raw/
		relPath, err := filepath.Rel(oldRawPath, oldPath)
		if err != nil {
			return nil
		}

		// Find document that owns this file
		// Match against both old path format and possible variations
		oldRawRelPath := "raw/" + relPath

		var doc db.Document
		// Try exact match first
		result := db.DB.Where("raw_path = ?", oldRawRelPath).First(&doc)
		if result.Error != nil {
			// Try with paper.md suffix for PDF directories
			if !strings.HasSuffix(relPath, ".md") {
				paperRelPath := "raw/" + relPath + "/paper.md"
				result = db.DB.Where("raw_path = ? OR raw_path LIKE ?", paperRelPath, "raw/"+relPath+"%").First(&doc)
			}
		}

		if result.Error != nil {
			// File not tracked in database, move to user 1 as fallback
			log.Printf("[migration] Untracked file %s, moving to user 1", relPath)
			doc.UserID = 1
		}

		// Build new path
		userDir := config.GetUserDir(dataDir, doc.UserID)
		newPath := filepath.Join(userDir, "raw", relPath)

		// Ensure target directory exists
		os.MkdirAll(filepath.Dir(newPath), 0755)

		// Move file
		log.Printf("[migration] Moving %s -> users/%d/raw/%s", oldPath, doc.UserID, relPath)
		if err := os.Rename(oldPath, newPath); err != nil {
			log.Printf("[migration] Warning: failed to move %s: %v", oldPath, err)
		}

		return nil
	})
	if err != nil {
		log.Printf("[migration] Error walking raw directory: %v", err)
	}

	// Remove empty old raw directory
	os.RemoveAll(oldRawPath)
}

// migrateWikiFiles moves wiki files to user directories based on document ownership
func migrateWikiFiles(dataDir string, oldWikiPath string) {
	// Wiki files are structured differently - sources/, entities/, topics/, index.md, log.md
	// Sources files are linked to documents, others are shared metadata

	// Walk through all files in wiki/ directory
	err := filepath.Walk(oldWikiPath, func(oldPath string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}

		// Get relative path from wiki/
		relPath, err := filepath.Rel(oldWikiPath, oldPath)
		if err != nil {
			return nil
		}

		// Determine owner:
		// - sources/*.md: find document with matching wiki_path
		// - Other files (entities, topics, index, log): move to user 1 as primary owner

		var ownerID uint = 1

		if strings.HasPrefix(relPath, "sources/") {
			// Find document that owns this wiki file
			oldWikiRelPath := "wiki/" + relPath
			var doc db.Document
			if db.DB.Where("wiki_path = ?", oldWikiRelPath).First(&doc).Error == nil {
				ownerID = doc.UserID
			}
		}

		// Build new path
		userDir := config.GetUserDir(dataDir, ownerID)
		newPath := filepath.Join(userDir, "wiki", relPath)

		// Ensure target directory exists
		os.MkdirAll(filepath.Dir(newPath), 0755)

		// Move file
		log.Printf("[migration] Moving %s -> users/%d/wiki/%s", oldPath, ownerID, relPath)
		if err := os.Rename(oldPath, newPath); err != nil {
			log.Printf("[migration] Warning: failed to move %s: %v", oldPath, err)
		}

		return nil
	})
	if err != nil {
		log.Printf("[migration] Error walking wiki directory: %v", err)
	}

	// Remove empty old wiki directory
	os.RemoveAll(oldWikiPath)
}
