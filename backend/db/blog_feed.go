package db

import "time"

type BlogFeed struct {
	ID              uint      `gorm:"primaryKey" json:"id"`
	UserID          uint      `gorm:"index;not null;default:1" json:"userId"`
	Name            string    `json:"name"`
	IndexURL        string    `gorm:"unique" json:"indexUrl"`
	PlatformType    string    `json:"platformType"`    // claude, webflow, wordpress, ghost, medium, custom
	LinkSelector    string    `json:"linkSelector"`    // CSS selector for article links
	ContentSelector string    `json:"contentSelector"` // CSS selector for article content
	LinkExclude     string    `json:"linkExclude"`     // CSS selector to exclude links
	AutoSync        bool      `gorm:"default:false" json:"autoSync"`
	LastArticleDate time.Time `json:"lastArticleDate"` // Max date of fetched articles
	LastSyncAt      time.Time `json:"lastSyncAt"`
	CreatedAt       time.Time `json:"createdAt"`
}
