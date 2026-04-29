package db

import (
	"time"

	"gorm.io/gorm"
)

type Document struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	Title      string    `json:"title"`
	SourceType string    `json:"sourceType"` // pdf, rss, web, manual
	RawPath    string    `json:"rawPath"`
	WikiPath   string    `json:"wikiPath"`
	Summary    string    `json:"summary"`                    // AI生成的短摘要（50-100字）
	Language   string    `json:"language"`
	Status     string    `gorm:"default:inbox" json:"status"` // inbox, published, archived
	Metadata   string    `json:"metadata"`                    // JSON string
	SourceURL  string          `json:"sourceUrl"`                    // Original URL for web/rss
	SourceGUID string          `json:"sourceGuid"`                   // RSS item GUID for dedup
	RSSFeedID  uint            `json:"rssFeedId"`                    // Associated RSS feed
	CreatedAt  time.Time       `json:"createdAt"`
	UpdatedAt  time.Time       `json:"updatedAt"`
	DeletedAt  gorm.DeletedAt  `gorm:"index" json:"deletedAt,omitempty"`
	Tags       []Tag           `gorm:"many2many:document_tags;" json:"tags"`
}

type Tag struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Name      string    `gorm:"unique" json:"name"`
	Color     string    `json:"color"`
	CreatedAt time.Time `json:"createdAt"`
}

type DocumentTag struct {
	DocumentID uint `gorm:"primaryKey" json:"documentId"`
	TagID      uint `gorm:"primaryKey" json:"tagId"`
}

type Conversation struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Title     string    `json:"title"`
	SessionID string    `json:"sessionId"` // Claude session ID for --resume
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type ConversationMessage struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	ConversationID uint      `json:"conversationId"`
	Role           string    `json:"role"` // user, assistant, system
	Content        string    `json:"content"`
	ContextDocIDs  string    `json:"contextDocIds"` // JSON array
	Images         string    `gorm:"default:''" json:"images"` // JSON array of image paths
	CreatedAt      time.Time `json:"createdAt"`
}

type UserSettings struct {
	ID                 uint      `gorm:"primaryKey" json:"id"`
	Language           string    `gorm:"default:en" json:"language"` // 'en' or 'zh'
	TranslationEnabled bool      `gorm:"default:false" json:"translationEnabled"`
	TranslationApiBase string    `gorm:"default:https://dashscope.aliyuncs.com/compatible-mode/v1" json:"translationApiBase"`
	TranslationApiKey  string    `gorm:"" json:"translationApiKey"`
	TranslationModel   string    `gorm:"default:deepseek-v4-flash" json:"translationModel"`
	CreatedAt          time.Time `json:"createdAt"`
	UpdatedAt          time.Time `json:"updatedAt"`
}

type RSSFeed struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	Name       string    `json:"name"`
	URL        string    `gorm:"unique" json:"url"`
	AutoSync   bool      `gorm:"default:false" json:"autoSync"`
	LastSyncAt time.Time `json:"lastSyncAt"`
	CreatedAt  time.Time `json:"createdAt"`
}