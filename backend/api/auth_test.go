package api

import (
	"testing"
)

func TestValidatePassword(t *testing.T) {
	tests := []struct {
		password string
		valid    bool
		errMsg   string
	}{
		{"abc123", true, ""},
		{"123456", false, "密码必须包含至少一个字母"},
		{"abcdef", false, "密码必须包含至少一个数字"},
		{"ab1", false, "密码长度必须在6-32字符之间"},
		{"a123456789012345678901234567890123", false, "密码长度必须在6-32字符之间"},
		{"Abc123", true, ""},
		{"test1234", true, ""},
	}

	for _, tt := range tests {
		err := validatePassword(tt.password)
		if tt.valid && err != nil {
			t.Errorf("password %s should be valid, got error: %v", tt.password, err)
		}
		if !tt.valid && err == nil {
			t.Errorf("password %s should be invalid", tt.password)
		}
		if !tt.valid && err != nil && err.Error() != tt.errMsg {
			t.Errorf("password %s error message mismatch: got %s, want %s", tt.password, err.Error(), tt.errMsg)
		}
	}
}

func TestValidateUsername(t *testing.T) {
	tests := []struct {
		username string
		valid    bool
	}{
		{"abc", true},
		{"abcd", true},
		{"testuser", true},
		{"ab", false},
		{"abcdefghijklmnopqrstu", false},
		{"a", false},
	}

	for _, tt := range tests {
		err := validateUsername(tt.username)
		if tt.valid && err != nil {
			t.Errorf("username %s should be valid, got error: %v", tt.username, err)
		}
		if !tt.valid && err == nil {
			t.Errorf("username %s should be invalid", tt.username)
		}
	}
}