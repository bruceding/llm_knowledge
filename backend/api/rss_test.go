package api

import "testing"

func TestRSSHandlerExists(t *testing.T) {
	h := RSSHandler{DataDir: "/tmp"}
	if h.DataDir != "/tmp" {
		t.Error("Expected DataDir")
	}
}