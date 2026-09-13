package db

import (
	"time"

	"gorm.io/gorm"
)

type Document struct {
	ID         uint           `gorm:"primaryKey" json:"id"`
	Title      string         `json:"title"`
	Slug       string         `json:"slug"`       // Filesystem-safe name derived from Title
	SourceType string         `json:"sourceType"` // pdf, rss, web, manual
	RawPath    string         `json:"rawPath"`
	WikiPath   string         `json:"wikiPath"`
	Summary    string         `json:"summary"` // AI生成的短摘要（50-100字）
	Language   string         `json:"language"`
	Status     string         `gorm:"default:inbox" json:"status"` // inbox, later, published, archived
	Metadata   string         `json:"metadata"`                    // JSON string
	SourceURL  string         `json:"sourceUrl"`                   // Original URL for web/rss/blog
	SourceGUID string         `json:"sourceGuid"`                  // RSS item GUID for dedup
	ChatSessionID string      `json:"chatSessionId"`               // agent session ID (claude or pi) for resume
	UserID     uint           `gorm:"index;not null;default:1" json:"userId"`
	RSSFeedID  uint           `json:"rssFeedId"`  // Associated RSS feed
	BlogFeedID uint           `json:"blogFeedId"` // Associated Blog feed
	CreatedAt  time.Time      `json:"createdAt"`
	UpdatedAt  time.Time      `json:"updatedAt"`
	DeletedAt  gorm.DeletedAt `gorm:"index" json:"deletedAt,omitempty"`
	Tags       []Tag          `gorm:"many2many:document_tags;" json:"tags"`
}

type Tag struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Name      string    `gorm:"uniqueIndex:idx_name_user" json:"name"`
	Color     string    `json:"color"`
	UserID    uint      `gorm:"uniqueIndex:idx_name_user;index;not null;default:1" json:"userId"`
	CreatedAt time.Time `json:"createdAt"`
}

type DocumentTag struct {
	DocumentID uint `gorm:"primaryKey" json:"documentId"`
	TagID      uint `gorm:"primaryKey" json:"tagId"`
}

type Conversation struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Title     string    `json:"title"`
	SessionID string    `json:"sessionId"` // agent session ID (claude or pi) for resume
	UserID    uint      `gorm:"index;not null;default:1" json:"userId"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type ConversationMessage struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	ConversationID uint      `json:"conversationId"`
	Role           string    `json:"role"` // user, assistant, system
	Content        string    `json:"content"`
	ContextDocIDs  string    `json:"contextDocIds"`            // JSON array
	Images         string    `gorm:"default:''" json:"images"` // JSON array of image paths
	CreatedAt      time.Time `json:"createdAt"`
}

type UserSettings struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	UserID    uint      `gorm:"index;not null;default:1" json:"userId"`
	Language  string    `gorm:"default:en" json:"language"` // 'en' or 'zh'
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type GlobalSettings struct {
	ID                 uint   `gorm:"primaryKey" json:"id"`
	TranslationEnabled bool   `gorm:"default:false" json:"translationEnabled"`
	TranslationApiBase string `gorm:"default:https://dashscope.aliyuncs.com/compatible-mode/v1" json:"translationApiBase"`
	TranslationApiKey  string `gorm:"" json:"-"` // never expose in API responses
	TranslationModel   string `gorm:"default:deepseek-v4-flash" json:"translationModel"`
	// LLMBackend 选择 agent 后端:"claude"(默认) 或 "pi"。
	// AutoMigrate 自动加列,无需数据迁移;存量行拿到默认值 claude,即改造前的行为。
	// 读取方是 agent.Current()(5s TTL 缓存),写入方是 PUT /api/admin/settings。
	// 取值校验与探测在 API 层(Task 7),本字段本身不加 CHECK 约束 ——
	// agent.normalizeBackendName 对未知值一律回退 claude,故写坏也不会让链路全挂。
	LLMBackend string    `gorm:"default:claude" json:"llmBackend"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

type RSSFeed struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	UserID     uint      `gorm:"uniqueIndex:idx_rss_feeds_user_url;index;not null;default:1" json:"userId"`
	Name       string    `json:"name"`
	URL        string    `gorm:"uniqueIndex:idx_rss_feeds_user_url" json:"url"`
	AutoSync   bool      `gorm:"default:false" json:"autoSync"`
	LastSyncAt time.Time `json:"lastSyncAt"`
	CreatedAt  time.Time `json:"createdAt"`
}

type DocNote struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	UserID       uint      `gorm:"index;not null;default:1" json:"userId"`
	DocumentID   uint      `gorm:"index" json:"documentId"`
	Content      string    `json:"content"`
	SourceMsgID  string    `json:"sourceMsgId"` // frontend message ID for dedup
	WikiPushed   bool      `gorm:"default:false" json:"wikiPushed"`
	WikiPushedAt time.Time `json:"wikiPushedAt,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type User struct {
	ID                 uint      `gorm:"primaryKey" json:"id"`
	Username           string    `gorm:"unique;not null" json:"username"`
	PasswordHash       string    `gorm:"not null" json:"-"`
	Email              string    `gorm:"unique;not null" json:"email"`
	Role               string    `gorm:"default:user" json:"role"` // admin, user
	MustChangePassword bool      `gorm:"default:false" json:"mustChangePassword"`
	CreatedAt          time.Time `json:"createdAt"`
	UpdatedAt          time.Time `json:"updatedAt"`
}

type Session struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	UserID     uint      `gorm:"index;not null;default:1" json:"userId"`
	Token      string    `gorm:"unique;not null" json:"token"`
	ExpiresAt  time.Time `gorm:"not null" json:"expiresAt"`
	LastAccess time.Time `json:"lastAccess"`
	CreatedAt  time.Time `json:"createdAt"`
}

type Captcha struct {
	ID        uint      `gorm:"primaryKey"`
	Key       string    `gorm:"unique;not null"`
	Answer    string    `gorm:"not null"`
	ExpiresAt time.Time `gorm:"not null"`
	CreatedAt time.Time
}

type IMAPConfig struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	UserID        uint      `gorm:"uniqueIndex;not null" json:"userId"`
	Host          string    `json:"host"`
	Port          int       `gorm:"default:993" json:"port"`
	Username      string    `json:"username"`
	EncryptedPass string    `json:"-"`
	FolderName    string    `gorm:"default:Newsletter" json:"folderName"`
	AutoSync      bool      `gorm:"default:false" json:"autoSync"`
	LastSyncAt    time.Time `json:"lastSyncAt"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}
