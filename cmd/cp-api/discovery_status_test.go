package main

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/OraCache/oracache/internal/discovery"
)

func TestDiscoveryStatusEmpty(t *testing.T) {
	cat, err := discovery.New("a", "g", 20*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	discoveryStatusHandler(cat).ServeHTTP(w, httptest.NewRequest("GET", "/heartbeats", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"group_id":"g"`) {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}
