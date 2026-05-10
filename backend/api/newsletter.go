package api

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"llm-knowledge/crypto"
	"llm-knowledge/db"
	"llm-knowledge/ingest"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-message/mail"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

var syncLocks sync.Map

// resolveMailboxName attempts to select a mailbox by name. If the exact name
// fails (Gmail IMAP is case-sensitive for non-INBOX folders), it falls back to
// a case-insensitive LIST match and returns the canonical name from the server.
func resolveMailboxName(client *imapclient.Client, folderName string) (canonical string, selectData *imap.SelectData, err error) {
	selectData, err = client.Select(folderName, nil).Wait()
	if err == nil {
		return folderName, selectData, nil
	}

	// Folder not found — try case-insensitive LIST match
	listCmd := client.List("", "*", nil)
	var matched string
	for {
		data := listCmd.Next()
		if data == nil {
			break
		}
		if strings.EqualFold(data.Mailbox, folderName) {
			matched = data.Mailbox
			break
		}
	}
	listCmd.Close()

	if matched != "" {
		selectData, err2 := client.Select(matched, nil).Wait()
		if err2 != nil {
			return "", nil, fmt.Errorf("folder '%s' not found (tried case-insensitive match '%s'): %v", folderName, matched, err2)
		}
		return matched, selectData, nil
	}

	return "", nil, fmt.Errorf("folder '%s' not found", folderName)
}

type NewsletterHandler struct {
	DataDir   string
	ClaudeBin string
}

type IMAPConfigRequest struct {
	Host       string `json:"host"`
	Port       int    `json:"port"`
	Username   string `json:"username"`
	Password   string `json:"password"`
	FolderName string `json:"folderName"`
	AutoSync   bool   `json:"autoSync"`
}

type IMAPConfigResponse struct {
	ID         uint      `json:"id"`
	Host       string    `json:"host"`
	Port       int       `json:"port"`
	Username   string    `json:"username"`
	FolderName string    `json:"folderName"`
	AutoSync   bool      `json:"autoSync"`
	LastSyncAt time.Time `json:"lastSyncAt"`
	CreatedAt  time.Time `json:"createdAt"`
}

type NewsletterSyncResult struct {
	NewArticles    int    `json:"newArticles"`
	Total          int    `json:"total"`
	DownloadErrors int    `json:"downloadErrors"`
	Message        string `json:"message"`
	Error          string `json:"error,omitempty"`
}

// syncStatus tracks in-progress sync state per user
type syncStatus struct {
	Running bool                  `json:"running"`
	Result  *NewsletterSyncResult `json:"result,omitempty"`
}

var syncStatuses sync.Map // map[uint]*syncStatus

func toIMAPConfigResponse(cfg *db.IMAPConfig) IMAPConfigResponse {
	return IMAPConfigResponse{
		ID:         cfg.ID,
		Host:       cfg.Host,
		Port:       cfg.Port,
		Username:   cfg.Username,
		FolderName: cfg.FolderName,
		AutoSync:   cfg.AutoSync,
		LastSyncAt: cfg.LastSyncAt,
		CreatedAt:  cfg.CreatedAt,
	}
}

func (h *NewsletterHandler) GetConfig(c echo.Context) error {
	userId := GetCurrentUserId(c)

	var cfg db.IMAPConfig
	if err := db.DB.Where("user_id = ?", userId).First(&cfg).Error; err != nil {
		return c.JSON(http.StatusOK, echo.Map{"configured": false})
	}

	return c.JSON(http.StatusOK, echo.Map{"configured": true, "config": toIMAPConfigResponse(&cfg)})
}

func (h *NewsletterHandler) UpdateConfig(c echo.Context) error {
	userId := GetCurrentUserId(c)

	var req IMAPConfigRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "invalid request"})
	}

	if req.Host == "" || req.Username == "" {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "host and username are required"})
	}
	if req.Port == 0 {
		req.Port = 993
	}
	if req.FolderName == "" {
		req.FolderName = "Newsletter"
	}

	var cfg db.IMAPConfig
	err := db.DB.Where("user_id = ?", userId).First(&cfg).Error
	isNew := errors.Is(err, gorm.ErrRecordNotFound)
	if err != nil && !isNew {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "database error"})
	}

	cfg.UserID = userId
	cfg.Host = req.Host
	cfg.Port = req.Port
	cfg.Username = req.Username
	cfg.FolderName = req.FolderName
	cfg.AutoSync = req.AutoSync

	if req.Password != "" {
		encrypted, err := crypto.Encrypt(req.Password)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to encrypt password"})
		}
		cfg.EncryptedPass = encrypted
	} else if isNew {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "password is required"})
	}

	if err := db.DB.Save(&cfg).Error; err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to save config"})
	}

	return c.JSON(http.StatusOK, echo.Map{"configured": true, "config": toIMAPConfigResponse(&cfg)})
}

func (h *NewsletterHandler) DeleteConfig(c echo.Context) error {
	userId := GetCurrentUserId(c)

	var cfg db.IMAPConfig
	if err := db.DB.Where("user_id = ?", userId).First(&cfg).Error; err != nil {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "config not found"})
	}

	var inboxDocs []db.Document
	db.DB.Where("source_type = ? AND user_id = ? AND status = ?", "newsletter", userId, "inbox").Find(&inboxDocs)
	for _, doc := range inboxDocs {
		if doc.RawPath != "" {
			os.Remove(filepath.Join(h.DataDir, doc.RawPath))
		}
	}
	db.DB.Where("source_type = ? AND user_id = ? AND status = ?", "newsletter", userId, "inbox").Delete(&db.Document{})

	db.DB.Delete(&cfg)
	return c.JSON(http.StatusOK, echo.Map{"message": "config deleted"})
}

func isPrivateIP(ip net.IP) bool {
	privateRanges := []struct{ start, end net.IP }{
		{net.ParseIP("10.0.0.0"), net.ParseIP("10.255.255.255")},
		{net.ParseIP("172.16.0.0"), net.ParseIP("172.31.255.255")},
		{net.ParseIP("192.168.0.0"), net.ParseIP("192.168.255.255")},
		{net.ParseIP("127.0.0.0"), net.ParseIP("127.255.255.255")},
		{net.ParseIP("169.254.0.0"), net.ParseIP("169.254.255.255")},
	}
	for _, r := range privateRanges {
		if bytes.Compare(ip, r.start) >= 0 && bytes.Compare(ip, r.end) <= 0 {
			return true
		}
	}
	return false
}

func validateIMAPHost(host string, port int) error {
	if port != 993 && port != 143 {
		return fmt.Errorf("only port 993 (IMAPS) and 143 (IMAP) are allowed")
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("cannot resolve host")
	}
	for _, ip := range ips {
		if isPrivateIP(ip.To4()) || isPrivateIP(ip.To16()) {
			return fmt.Errorf("connection to private networks is not allowed")
		}
	}
	return nil
}

func dialIMAP(host string, port int, timeout time.Duration) (*imapclient.Client, error) {
	if err := validateIMAPHost(host, port); err != nil {
		return nil, err
	}
	addr := fmt.Sprintf("%s:%d", host, port)
	dialer := &net.Dialer{Timeout: timeout}
	conn, err := tls.DialWithDialer(dialer, "tcp", addr, nil)
	if err != nil {
		return nil, fmt.Errorf("connection failed")
	}
	conn.SetDeadline(time.Now().Add(timeout))
	return imapclient.New(conn, nil), nil
}

func (h *NewsletterHandler) TestConnection(c echo.Context) error {
	userId := GetCurrentUserId(c)

	var cfg db.IMAPConfig
	if err := db.DB.Where("user_id = ?", userId).First(&cfg).Error; err != nil {
		return c.JSON(http.StatusOK, echo.Map{"success": false, "message": "IMAP not configured"})
	}

	password, err := crypto.Decrypt(cfg.EncryptedPass)
	if err != nil {
		return c.JSON(http.StatusOK, echo.Map{"success": false, "message": "failed to decrypt password"})
	}

	client, err := dialIMAP(cfg.Host, cfg.Port, 30*time.Second)
	if err != nil {
		fmt.Printf("[newsletter] test connection failed for %s:%d: %v\n", cfg.Host, cfg.Port, err)
		return c.JSON(http.StatusOK, echo.Map{"success": false, "message": "connection failed"})
	}
	defer client.Close()

	if err := client.Login(cfg.Username, password).Wait(); err != nil {
		fmt.Printf("[newsletter] login failed for %s: %v\n", cfg.Username, err)
		return c.JSON(http.StatusOK, echo.Map{"success": false, "message": "login failed, check credentials"})
	}

	canonicalName, selectData, err := resolveMailboxName(client, cfg.FolderName)
	if err != nil {
		// Collect available folders for the error message
		listCmd := client.List("", "*", nil)
		var available []string
		for {
			data := listCmd.Next()
			if data == nil {
				break
			}
			available = append(available, data.Mailbox)
		}
		listCmd.Close()

		return c.JSON(http.StatusOK, echo.Map{
			"success":          false,
			"folderExists":     false,
			"message":          fmt.Sprintf("folder '%s' not found", cfg.FolderName),
			"availableFolders": available,
		})
	}

	// Auto-correct case mismatch in DB
	if canonicalName != cfg.FolderName {
		db.DB.Model(cfg).Update("folder_name", canonicalName)
		cfg.FolderName = canonicalName
	}

	return c.JSON(http.StatusOK, echo.Map{
		"success":      true,
		"folderExists": true,
		"messageCount": selectData.NumMessages,
		"message":      fmt.Sprintf("Connection successful, folder '%s' accessible", cfg.FolderName),
	})
}

func (h *NewsletterHandler) ListFolders(c echo.Context) error {
	userId := GetCurrentUserId(c)

	var cfg db.IMAPConfig
	if err := db.DB.Where("user_id = ?", userId).First(&cfg).Error; err != nil {
		return c.JSON(http.StatusOK, echo.Map{"folders": []string{}})
	}

	password, err := crypto.Decrypt(cfg.EncryptedPass)
	if err != nil {
		return c.JSON(http.StatusOK, echo.Map{"folders": []string{}})
	}

	client, err := dialIMAP(cfg.Host, cfg.Port, 30*time.Second)
	if err != nil {
		return c.JSON(http.StatusOK, echo.Map{"folders": []string{}})
	}
	defer client.Close()

	if err := client.Login(cfg.Username, password).Wait(); err != nil {
		return c.JSON(http.StatusOK, echo.Map{"folders": []string{}})
	}

	listCmd := client.List("", "*", nil)
	var folders []string
	for {
		data := listCmd.Next()
		if data == nil {
			break
		}
		folders = append(folders, data.Mailbox)
	}
	listCmd.Close()

	return c.JSON(http.StatusOK, echo.Map{"folders": folders})
}

func (h *NewsletterHandler) Sync(c echo.Context) error {
	userId := GetCurrentUserId(c)

	// Check if already running
	if st, ok := syncStatuses.Load(userId); ok {
		s := st.(*syncStatus)
		if s.Running {
			return c.JSON(http.StatusOK, echo.Map{"status": "running"})
		}
	}

	var cfg db.IMAPConfig
	if err := db.DB.Where("user_id = ?", userId).First(&cfg).Error; err != nil {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "IMAP not configured"})
	}

	// Mark as running
	status := &syncStatus{Running: true}
	syncStatuses.Store(userId, status)

	go func() {
		result := h.syncInternal(&cfg)
		status.Running = false
		status.Result = &result
	}()

	return c.JSON(http.StatusOK, echo.Map{"status": "started"})
}

func (h *NewsletterHandler) SyncStatus(c echo.Context) error {
	userId := GetCurrentUserId(c)

	st, ok := syncStatuses.Load(userId)
	if !ok {
		return c.JSON(http.StatusOK, echo.Map{"running": false})
	}
	s := st.(*syncStatus)
	if s.Running {
		return c.JSON(http.StatusOK, echo.Map{"running": true})
	}
	return c.JSON(http.StatusOK, echo.Map{
		"running": false,
		"result":  s.Result,
	})
}

func (h *NewsletterHandler) syncInternal(cfg *db.IMAPConfig) NewsletterSyncResult {
	mu := &sync.Mutex{}
	actual, _ := syncLocks.LoadOrStore(cfg.UserID, mu)
	mu = actual.(*sync.Mutex)
	if !mu.TryLock() {
		return NewsletterSyncResult{Message: "Sync already in progress"}
	}
	defer mu.Unlock()

	password, err := crypto.Decrypt(cfg.EncryptedPass)
	if err != nil {
		return NewsletterSyncResult{Error: "failed to decrypt password: " + err.Error()}
	}

	client, err := dialIMAP(cfg.Host, cfg.Port, 5*time.Minute)
	if err != nil {
		return NewsletterSyncResult{Error: "connection failed: " + err.Error()}
	}
	defer client.Close()

	if err := client.Login(cfg.Username, password).Wait(); err != nil {
		return NewsletterSyncResult{Error: "login failed: " + err.Error()}
	}

	canonicalName, _, err := resolveMailboxName(client, cfg.FolderName)
	if err != nil {
		return NewsletterSyncResult{Error: fmt.Sprintf("folder '%s' not found: %v", cfg.FolderName, err)}
	}

	// Auto-correct case mismatch in DB
	if canonicalName != cfg.FolderName {
		db.DB.Model(cfg).Update("folder_name", canonicalName)
		cfg.FolderName = canonicalName
	}

	isFirstSync := cfg.LastSyncAt.IsZero()

	criteria := &imap.SearchCriteria{}
	if !isFirstSync {
		// After first sync, only fetch unseen messages
		criteria.NotFlag = []imap.Flag{imap.FlagSeen}
	}
	if !cfg.LastSyncAt.IsZero() {
		criteria.Since = cfg.LastSyncAt
	}

	searchData, err := client.UIDSearch(criteria, nil).Wait()
	if err != nil {
		return NewsletterSyncResult{Error: "search failed: " + err.Error()}
	}

	uids := searchData.AllUIDs()
	if len(uids) == 0 {
		return NewsletterSyncResult{Message: "No new newsletters"}
	}

	// Phase 1: fetch envelopes only for all UIDs
	envelopeOptions := &imap.FetchOptions{
		Envelope: true,
		UID:      true,
	}

	uidSet := imap.UIDSetNum(uids...)
	fetchCmd := client.Fetch(uidSet, envelopeOptions)

	type fetchedMsg struct {
		uid      imap.UID
		envelope *imap.Envelope
		body     []byte
	}
	var messages []fetchedMsg

	for {
		msgData := fetchCmd.Next()
		if msgData == nil {
			break
		}

		buf, err := msgData.Collect()
		if err != nil {
			continue
		}

		if buf.Envelope != nil {
			messages = append(messages, fetchedMsg{
				uid:      buf.UID,
				envelope: buf.Envelope,
			})
		}
	}

	if err := fetchCmd.Close(); err != nil {
		return NewsletterSyncResult{Error: "fetch envelopes failed: " + err.Error()}
	}

	sort.Slice(messages, func(i, j int) bool {
		return messages[i].envelope.Date.After(messages[j].envelope.Date)
	})

	if isFirstSync && len(messages) > 10 {
		messages = messages[:10]
	}

	// Phase 2: fetch body only for the selected messages
	if len(messages) > 0 {
		topUIDs := make([]imap.UID, len(messages))
		for i, m := range messages {
			topUIDs[i] = m.uid
		}
		bodySet := imap.UIDSetNum(topUIDs...)
		bodyOptions := &imap.FetchOptions{
			UID:         true,
			BodySection: []*imap.FetchItemBodySection{{}},
		}
		bodyCmd := client.Fetch(bodySet, bodyOptions)

		bodyMap := make(map[imap.UID][]byte)
		for {
			msgData := bodyCmd.Next()
			if msgData == nil {
				break
			}
			buf, err := msgData.Collect()
			if err != nil {
				continue
			}
			if len(buf.BodySection) > 0 {
				bodyMap[buf.UID] = buf.BodySection[0].Bytes
			}
		}
		if err := bodyCmd.Close(); err != nil {
			return NewsletterSyncResult{Error: "fetch bodies failed: " + err.Error()}
		}

		for i := range messages {
			messages[i].body = bodyMap[messages[i].uid]
		}
	}

	total := len(messages)
	newArticles := 0
	downloadErrors := 0
	var processedUIDs []imap.UID

	for _, m := range messages {
		messageID := m.envelope.MessageID
		if messageID == "" {
			messageID = fmt.Sprintf("%s-%s-%d", m.envelope.Subject, m.envelope.Date.Format(time.RFC3339), m.uid)
		}

		var existing db.Document
		if db.DB.Unscoped().Where("source_guid = ? AND user_id = ?", messageID, cfg.UserID).First(&existing).Error == nil {
			processedUIDs = append(processedUIDs, m.uid)
			continue
		}

		fromName := ""
		fromAddr := ""
		if len(m.envelope.From) > 0 {
			fromName = m.envelope.From[0].Name
			fromAddr = m.envelope.From[0].Addr()
			if fromName == "" {
				fromName = fromAddr
			}
		}

		subject := m.envelope.Subject
		if subject == "" {
			subject = "untitled"
		}

		htmlContent := extractHTMLFromEmail(m.body)
		if htmlContent == "" {
			fmt.Printf("[newsletter] skipping uid=%d: no HTML content\n", m.uid)
			continue
		}

		viewURL := extractViewInBrowserLink(htmlContent)

		senderDir := sanitizeFilename(fromName)
		feedDir := filepath.Join(h.DataDir, "raw", "newsletter", senderDir)
		assetsDir := filepath.Join(feedDir, "assets")
		os.MkdirAll(assetsDir, 0755)

		filteredHTML := filterDecorativeImages(htmlContent)
		markdown, imgCount, imgErrors := processHTMLToMarkdown(filteredHTML, assetsDir, viewURL)
		downloadErrors += imgErrors

		var content strings.Builder
		content.WriteString(fmt.Sprintf("# %s\n\n", subject))
		content.WriteString(fmt.Sprintf("**From:** %s\n", fromName))
		content.WriteString(fmt.Sprintf("**Date:** %s\n", m.envelope.Date.Format("2006-01-02 15:04")))
		if viewURL != "" {
			content.WriteString(fmt.Sprintf("**Link:** %s\n", viewURL))
		}
		content.WriteString("\n## Content\n\n")
		content.WriteString(markdown)

		slug := fmt.Sprintf("%s-%d", sanitizeFilename(subject), m.uid)
		articlePath := filepath.Join(feedDir, slug+".md")
		if err := os.WriteFile(articlePath, []byte(content.String()), 0644); err != nil {
			continue
		}

		metadata := map[string]string{
			"from":      fromName,
			"fromAddr":  fromAddr,
			"subject":   subject,
			"date":      m.envelope.Date.Format(time.RFC3339),
			"messageId": messageID,
			"images":    strconv.Itoa(imgCount),
		}
		metadataJSON, _ := json.Marshal(metadata)

		doc := db.Document{
			UserID:     cfg.UserID,
			Title:      subject,
			Slug:       slug,
			SourceType: "newsletter",
			RawPath:    filepath.Join("raw", "newsletter", senderDir, slug+".md"),
			SourceURL:  viewURL,
			SourceGUID: messageID,
			Language:   "en",
			Status:     "inbox",
			Metadata:   string(metadataJSON),
			CreatedAt:  m.envelope.Date,
			UpdatedAt:  time.Now(),
		}

		if err := db.DB.Create(&doc).Error; err != nil {
			continue
		}

		if fromName != "" {
			var tag db.Tag
			result := db.DB.Where("name = ? AND user_id = ?", fromName, cfg.UserID).First(&tag)
			if result.Error != nil {
				tag = db.Tag{Name: fromName, Color: "#808080", UserID: cfg.UserID}
				db.DB.Create(&tag)
			}
			db.DB.Create(&db.DocumentTag{DocumentID: doc.ID, TagID: tag.ID})
		}

		if h.ClaudeBin != "" {
			docID := doc.ID
			rawPath := doc.RawPath
			go func() {
				summary, err := ingest.GenerateSummary(h.DataDir, rawPath, h.ClaudeBin)
				if err != nil {
					fmt.Printf("[newsletter] summary generation failed for %d: %v\n", docID, err)
				} else {
					db.DB.Model(&db.Document{}).Where("id = ?", docID).Update("summary", summary)
				}
			}()
		}

		processedUIDs = append(processedUIDs, m.uid)
		newArticles++
	}

	if len(processedUIDs) > 0 {
		storeFlags := imap.StoreFlags{
			Op:    imap.StoreFlagsAdd,
			Flags: []imap.Flag{imap.FlagSeen},
		}
		uidSet := imap.UIDSetNum(processedUIDs...)
		if err := client.Store(uidSet, &storeFlags, nil).Close(); err != nil {
			fmt.Printf("[newsletter] failed to mark messages as seen: %v\n", err)
		}
	}

	// When first sync is truncated, set LastSyncAt to the oldest processed
	// message's date so the next sync picks up remaining older messages.
	syncTime := time.Now()
	if isFirstSync && total > 0 {
		oldest := messages[len(messages)-1].envelope.Date
		if !oldest.IsZero() {
			syncTime = oldest
		}
	}
	db.DB.Model(cfg).Update("last_sync_at", syncTime)

	msg := fmt.Sprintf("Synced %d new newsletters", newArticles)
	if downloadErrors > 0 {
		msg += fmt.Sprintf(" (%d image download errors)", downloadErrors)
	}

	return NewsletterSyncResult{
		NewArticles:    newArticles,
		Total:          total,
		DownloadErrors: downloadErrors,
		Message:        msg,
	}
}

func extractHTMLFromEmail(body []byte) string {
	mr, err := mail.CreateReader(bytes.NewReader(body))
	if err != nil {
		bodyStr := string(body)
		if strings.Contains(bodyStr, "<html") || strings.Contains(bodyStr, "<HTML") {
			return bodyStr
		}
		return ""
	}
	defer mr.Close()

	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue
		}

		h, ok := p.Header.(*mail.InlineHeader)
		if !ok {
			continue
		}

		contentType, _, _ := h.ContentType()
		if contentType != "text/html" {
			continue
		}

		b, err := io.ReadAll(p.Body)
		if err != nil {
			continue
		}
		return string(b)
	}

	return ""
}

func extractViewInBrowserLink(htmlContent string) string {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
	if err != nil {
		return ""
	}

	patterns := []string{
		"view in browser",
		"view online",
		"view this email",
		"view in your browser",
		"在浏览器中查看",
		"view it in your browser",
		"read online",
		"open in browser",
	}

	var viewURL string
	doc.Find("a").Each(func(i int, s *goquery.Selection) {
		if viewURL != "" {
			return
		}
		text := strings.ToLower(strings.TrimSpace(s.Text()))
		for _, pattern := range patterns {
			if strings.Contains(text, pattern) {
				if href, exists := s.Attr("href"); exists && href != "" {
					viewURL = href
					return
				}
			}
		}
	})

	return viewURL
}

func filterDecorativeImages(htmlContent string) string {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
	if err != nil {
		return htmlContent
	}

	sizePattern := regexp.MustCompile(`(\d+)`)

	doc.Find("img").Each(func(i int, s *goquery.Selection) {
		shouldRemove := false

		width, hasWidth := s.Attr("width")
		height, hasHeight := s.Attr("height")

		if hasWidth {
			if w := sizePattern.FindString(width); w != "" {
				if val, err := strconv.Atoi(w); err == nil && val < 50 {
					shouldRemove = true
				}
			}
		}
		if hasHeight {
			if h := sizePattern.FindString(height); h != "" {
				if val, err := strconv.Atoi(h); err == nil && val < 50 {
					shouldRemove = true
				}
			}
		}

		if hasWidth && hasHeight {
			w := sizePattern.FindString(width)
			h := sizePattern.FindString(height)
			if w == "1" && h == "1" {
				shouldRemove = true
			}
		}

		if style, exists := s.Attr("style"); exists {
			styleLower := strings.ToLower(style)
			if strings.Contains(styleLower, "width:1px") || strings.Contains(styleLower, "height:1px") ||
				strings.Contains(styleLower, "width: 1px") || strings.Contains(styleLower, "height: 1px") {
				shouldRemove = true
			}
			if strings.Contains(styleLower, "display:none") || strings.Contains(styleLower, "display: none") {
				shouldRemove = true
			}
		}

		if shouldRemove {
			s.Remove()
		}
	})

	result, err := doc.Html()
	if err != nil {
		return htmlContent
	}
	return result
}

func (h *NewsletterHandler) StartAutoSyncScheduler() {
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()

		fmt.Println("[newsletter] Auto-sync scheduler started, checking every hour")

		for range ticker.C {
			h.syncAutoSyncConfigs()
		}
	}()
}

func (h *NewsletterHandler) syncAutoSyncConfigs() {
	var configs []db.IMAPConfig
	if err := db.DB.Where("auto_sync = ?", true).Find(&configs).Error; err != nil {
		fmt.Printf("[newsletter] Failed to query auto-sync configs: %v\n", err)
		return
	}

	if len(configs) == 0 {
		return
	}

	for _, cfg := range configs {
		if !cfg.LastSyncAt.IsZero() && time.Since(cfg.LastSyncAt) < 1*time.Hour {
			continue
		}

		fmt.Printf("[newsletter] Auto-syncing for user %d (%s)\n", cfg.UserID, cfg.Username)
		result := h.syncInternal(&cfg)
		if result.Error != "" {
			fmt.Printf("[newsletter] Auto-sync failed for user %d: %s\n", cfg.UserID, result.Error)
		} else {
			fmt.Printf("[newsletter] Auto-sync completed for user %d: %s\n", cfg.UserID, result.Message)
		}
	}
}
