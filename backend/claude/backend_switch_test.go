package claude

import (
	"os"
	"path/filepath"
	"testing"

	"llm-knowledge/agent"
	"llm-knowledge/db"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// writeFakePiBinary 造一个假的 pi 可执行文件,让 PiProtocol.Probe 的
// LookPath + `pi --version` 能成功。不造假二进制的话 Current() 会因探测失败
// 返回错误,用例就会「通过」在一个根本没走到 pi 分支的路径上。
func writeFakePiBinary(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pi")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho 0.99.9\n"), 0o755); err != nil {
		t.Fatalf("write fake pi: %v", err)
	}
	return path
}

// setPiBackendInDB 把 GlobalSettings.LLMBackend 设为 "pi" 并失效 resolver 缓存。
//
// 必须真的建一张表:resolver 的 readBackendName() 直接读 db.DB(为 nil 时返回 ""
// 即 claude),所以只改 resolver 选项或环境变量都不会让它选中 pi。
func setPiBackendInDB(t *testing.T) {
	t.Helper()
	testDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := testDB.AutoMigrate(&db.GlobalSettings{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	if err := testDB.Create(&db.GlobalSettings{LLMBackend: "pi"}).Error; err != nil {
		t.Fatalf("create settings: %v", err)
	}
	prev := db.DB
	db.DB = testDB
	t.Cleanup(func() { db.DB = prev })
	agent.Invalidate()
	t.Cleanup(agent.Invalidate)
}
