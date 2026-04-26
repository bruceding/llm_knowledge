package db

import "time"

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
	SourceURL  string    `json:"sourceUrl"`                   // NEW: Original URL for web/rss
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
	Tags       []Tag     `gorm:"many2many:document_tags;" json:"tags"`
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
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type ConversationMessage struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	ConversationID uint      `json:"conversationId"`
	Role           string    `json:"role"` // user, assistant, system
	Content        string    `json:"content"`
	ContextDocIDs  string    `json:"contextDocIds"` // JSON array
	CreatedAt      time.Time `json:"createdAt"`
}

type UserSettings struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Language  string    `gorm:"default:en" json:"language"` // 'en' or 'zh'
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}