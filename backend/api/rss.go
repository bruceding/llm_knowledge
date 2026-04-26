package api

import (
	"llm-knowledge/db"
	"time"

	"github.com/labstack/echo/v4"
)

type RSSHandler struct {
	DataDir   string
	ClaudeBin string
}

type AddRSSFeedRequest struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	AutoSync bool   `json:"autoSync"`
}

func (h *RSSHandler) AddFeed(c echo.Context) error {
	var req AddRSSFeedRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(400, echo.Map{"error": "invalid request"})
	}

	if req.URL == "" {
		return c.JSON(400, echo.Map{"error": "URL is required"})
	}

	feed := db.RSSFeed{
		Name:      req.Name,
		URL:       req.URL,
		AutoSync:  req.AutoSync,
		CreatedAt: time.Now(),
	}

	if err := db.DB.Create(&feed).Error; err != nil {
		return c.JSON(500, echo.Map{"error": "failed to create feed"})
	}

	return c.JSON(200, feed)
}

func (h *RSSHandler) ListFeeds(c echo.Context) error {
	var feeds []db.RSSFeed
	result := db.DB.Order("created_at desc").Find(&feeds)
	if result.Error != nil {
		return c.JSON(500, echo.Map{"error": result.Error.Error()})
	}

	// Add article count for each feed
	response := make([]echo.Map, len(feeds))
	for i, feed := range feeds {
		var count int64
		db.DB.Model(&db.Document{}).Where("rss_feed_id = ?", feed.ID).Count(&count)
		response[i] = echo.Map{
			"id":           feed.ID,
			"name":         feed.Name,
			"url":          feed.URL,
			"autoSync":     feed.AutoSync,
			"lastSyncAt":   feed.LastSyncAt,
			"createdAt":    feed.CreatedAt,
			"articleCount": count,
		}
	}

	return c.JSON(200, response)
}

func (h *RSSHandler) DeleteFeed(c echo.Context) error {
	id := c.Param("id")

	result := db.DB.Delete(&db.RSSFeed{}, id)
	if result.Error != nil {
		return c.JSON(500, echo.Map{"error": result.Error.Error()})
	}

	return c.JSON(200, echo.Map{"id": id, "message": "feed deleted"})
}