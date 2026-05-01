package db

import (
	"log"
	"math/rand"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var DB *gorm.DB

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
		&UserSettings{}, &RSSFeed{}, &DocNote{},
	)
	if err != nil {
		return err
	}

	// Check if default user needed (migration for existing data)
	var userCount int64
	DB.Model(&User{}).Count(&userCount)
	if userCount == 0 {
		// Create default user for existing data
		defaultPassword := generateRandomPassword(12)
		hashedPassword, _ := bcrypt.GenerateFromPassword([]byte(defaultPassword), bcrypt.DefaultCost)

		defaultUser := User{
			Username:          "admin",
			PasswordHash:      string(hashedPassword),
			Email:             "admin@localhost",
			MustChangePassword: true,
		}
		DB.Create(&defaultUser)

		// Update all existing data to UserID=1
		DB.Model(&Document{}).Where("user_id IS NULL OR user_id = 0").Update("user_id", 1)
		DB.Model(&Conversation{}).Where("user_id IS NULL OR user_id = 0").Update("user_id", 1)
		DB.Model(&UserSettings{}).Where("user_id IS NULL OR user_id = 0").Update("user_id", 1)
		DB.Model(&RSSFeed{}).Where("user_id IS NULL OR user_id = 0").Update("user_id", 1)
		DB.Model(&DocNote{}).Where("user_id IS NULL OR user_id = 0").Update("user_id", 1)

		log.Printf("Created default user 'admin', password: %s", defaultPassword)
	}

	// Start session cleanup scheduler
	go startSessionCleanup()

	return nil
}

func generateRandomPassword(length int) string {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, length)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}

func startSessionCleanup() {
	ticker := time.NewTicker(1 * time.Hour)
	for range ticker.C {
		DB.Where("expires_at < ?", time.Now()).Delete(&Session{})
		DB.Where("expires_at < ?", time.Now()).Delete(&Captcha{})
	}
}