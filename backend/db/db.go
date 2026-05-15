package db

import (
	"context"
	"crypto/rand"
	"log"
	"math/big"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var DB *gorm.DB

var cleanupCancel context.CancelFunc

func Init(path string) error {
	var err error
	DB, err = gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		return err
	}

	// AutoMigrate all tables including new auth models
	err = DB.AutoMigrate(
		&User{}, &Session{}, &Captcha{},
		&Document{}, &Tag{}, &DocumentTag{},
		&Conversation{}, &ConversationMessage{},
		&UserSettings{}, &RSSFeed{}, &BlogFeed{}, &DocNote{}, &IMAPConfig{},
	)
	if err != nil {
		return err
	}

	// Check if default user needed (migration for existing data)
	var userCount int64
	DB.Model(&User{}).Count(&userCount)
	if userCount == 0 {
		err = DB.Transaction(func(tx *gorm.DB) error {
			// Create default user for existing data
			defaultPassword := generateRandomPassword(12)
			hashedPassword, err := bcrypt.GenerateFromPassword([]byte(defaultPassword), bcrypt.DefaultCost)
			if err != nil {
				return err
			}

			defaultUser := User{
				Username:          "admin",
				PasswordHash:      string(hashedPassword),
				Email:             "admin@localhost",
				MustChangePassword: true,
			}
			if err := tx.Create(&defaultUser).Error; err != nil {
				return err
			}

			// Update all existing data to UserID=1
			tx.Model(&Document{}).Where("user_id IS NULL OR user_id = 0").Update("user_id", 1)
			tx.Model(&Conversation{}).Where("user_id IS NULL OR user_id = 0").Update("user_id", 1)
			tx.Model(&UserSettings{}).Where("user_id IS NULL OR user_id = 0").Update("user_id", 1)
			tx.Model(&RSSFeed{}).Where("user_id IS NULL OR user_id = 0").Update("user_id", 1)
			tx.Model(&DocNote{}).Where("user_id IS NULL OR user_id = 0").Update("user_id", 1)

			log.Printf("Created default user 'admin', password: %s", defaultPassword)
			return nil
		})
		if err != nil {
			return err
		}
	}

	// Migrate existing tags to have user_id: assign each tag to the user who owns the document
	if DB.Migrator().HasColumn(&Tag{}, "UserID") {
		var orphanTags []Tag
		DB.Where("user_id IS NULL OR user_id = 0").Find(&orphanTags)
		for _, tag := range orphanTags {
			// Find the owner via document_tags → documents
			var docID uint
			DB.Table("document_tags").Where("tag_id = ?", tag.ID).Select("document_id").Scan(&docID)
			if docID > 0 {
				var doc Document
				DB.First(&doc, docID)
				if doc.UserID > 0 {
					DB.Model(&tag).Update("user_id", doc.UserID)
					continue
				}
			}
			// Fallback: assign to user 1
			DB.Model(&tag).Update("user_id", 1)
		}
	}

	// Drop old unique index on name if it exists, new composite index will be created by AutoMigrate
	if DB.Migrator().HasIndex(&Tag{}, "idx_tags_name") {
		DB.Migrator().DropIndex(&Tag{}, "idx_tags_name")
	}

	// Start session cleanup scheduler
	startSessionCleanup()

	return nil
}

func Close() {
	if cleanupCancel != nil {
		cleanupCancel()
	}
}

func generateRandomPassword(length int) string {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, length)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(letters))))
		if err != nil {
			// Fallback to timestamp-based on crypto rand failure (should not happen)
			b[i] = letters[byte(time.Now().UnixNano())%byte(len(letters))]
			continue
		}
		b[i] = letters[n.Int64()]
	}
	return string(b)
}

func startSessionCleanup() {
	ctx, cancel := context.WithCancel(context.Background())
	cleanupCancel = cancel

	ticker := time.NewTicker(1 * time.Hour)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				DB.Where("expires_at < ?", time.Now()).Delete(&Session{})
				DB.Where("expires_at < ?", time.Now()).Delete(&Captcha{})
			}
		}
	}()
}

// MigrateDocumentPaths updates raw_path and wiki_path to include user prefix
// Old: "raw/papers/foo" -> New: "users/1/raw/papers/foo"
// This should be called AFTER file migration is complete.
func MigrateDocumentPaths() {
	// Check if migration needed by looking for paths without "users/" prefix
	var needsMigration int64
	DB.Model(&Document{}).Where("raw_path != '' AND raw_path NOT LIKE 'users/%'").Count(&needsMigration)
	if needsMigration == 0 {
		return
	}

	log.Printf("[migration] Migrating %d document paths to user-partitioned structure", needsMigration)

	// Update raw_path: prepend "users/{user_id}/"
	if result := DB.Exec(`
		UPDATE documents
		SET raw_path = 'users/' || user_id || '/' || raw_path
		WHERE raw_path != '' AND raw_path NOT LIKE 'users/%'
	`); result.Error != nil {
		log.Printf("[migration] Failed to update raw_path: %v", result.Error)
	}

	// Update wiki_path: prepend "users/{user_id}/"
	if result := DB.Exec(`
		UPDATE documents
		SET wiki_path = 'users/' || user_id || '/' || wiki_path
		WHERE wiki_path != '' AND wiki_path NOT LIKE 'users/%'
	`); result.Error != nil {
		log.Printf("[migration] Failed to update wiki_path: %v", result.Error)
	}

	log.Printf("[migration] Document path migration completed")
}
