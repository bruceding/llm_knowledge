package db

import (
	"testing"
	"time"
)

func TestDocumentSourceURL(t *testing.T) {
	doc := Document{
		Title:      "Test",
		SourceType: "web",
		SourceURL:  "https://example.com/article",
		Status:     "inbox",
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	if doc.SourceURL != "https://example.com/article" {
		t.Errorf("Expected SourceURL to be set, got %s", doc.SourceURL)
	}
	if doc.SourceType != "web" {
		t.Errorf("Expected SourceType to be web, got %s", doc.SourceType)
	}
}

func TestRSSFeedModel(t *testing.T) {
	feed := RSSFeed{
		Name:     "Go Blog",
		URL:      "https://go.dev/blog/feed.atom",
		AutoSync: false,
	}
	if feed.Name != "Go Blog" {
		t.Error("Expected Name to be set")
	}
	if feed.AutoSync != false {
		t.Error("Expected AutoSync to be false")
	}
}