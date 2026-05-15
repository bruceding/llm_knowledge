package ssrf

import (
	"bytes"
	"fmt"
	"net"
	"net/url"
)

// IsPrivateIP checks if an IP address belongs to a private/reserved range
func IsPrivateIP(ip net.IP) bool {
	privateRanges := []struct{ start, end net.IP }{
		{net.ParseIP("10.0.0.0"), net.ParseIP("10.255.255.255")},
		{net.ParseIP("172.16.0.0"), net.ParseIP("172.31.255.255")},
		{net.ParseIP("192.168.0.0"), net.ParseIP("192.168.255.255")},
		{net.ParseIP("127.0.0.0"), net.ParseIP("127.255.255.255")},
		{net.ParseIP("169.254.0.0"), net.ParseIP("169.254.255.255")},
	}
	for _, r := range privateRanges {
		if bytes.Compare(ip, r.start) >= 0 && bytes.Compare(ip, r.end) <= 0 {
			return true
		}
	}
	return false
}

// ValidateURLHost resolves the host in the given URL and rejects private IPs
func ValidateURLHost(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL")
	}
	host := parsed.Hostname()
	if host == "" {
		return fmt.Errorf("URL has no host")
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("cannot resolve host")
	}
	for _, ip := range ips {
		if IsPrivateIP(ip.To4()) || IsPrivateIP(ip.To16()) {
			return fmt.Errorf("connection to private networks is not allowed")
		}
	}
	return nil
}
