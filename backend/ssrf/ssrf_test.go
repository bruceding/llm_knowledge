package ssrf

import (
	"net"
	"testing"
)

func TestIsPrivateIP(t *testing.T) {
	tests := []struct {
		ip   string
		want bool
	}{
		// Private ranges
		{"10.0.0.1", true},
		{"10.255.255.254", true},
		{"172.16.0.1", true},
		{"172.31.255.254", true},
		{"192.168.1.1", true},
		{"192.168.0.100", true},
		{"127.0.0.1", true},
		{"169.254.1.1", true},
		// Public ranges
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"172.15.255.254", false},
		{"172.32.0.1", false},
		{"11.0.0.1", false},
		{"128.0.0.1", false},
	}

	for _, tt := range tests {
		t.Run(tt.ip, func(t *testing.T) {
			got := IsPrivateIP(net.ParseIP(tt.ip))
			if got != tt.want {
				t.Errorf("IsPrivateIP(%s) = %v, want %v", tt.ip, got, tt.want)
			}
		})
	}
}

func TestValidateURLHost(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"public URL", "https://example.com/page", false},
		{"invalid URL", "://invalid", true},
		{"empty host", "http://", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateURLHost(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateURLHost(%s) error = %v, wantErr %v", tt.url, err, tt.wantErr)
			}
		})
	}
}
