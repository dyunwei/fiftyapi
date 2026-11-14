package clientsetup

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDownload(t *testing.T) {
	w := httptest.NewRecorder()
	Handler(w, httptest.NewRequest("GET", "/setup.py", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "def configure(") || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("helper download missing or incorrectly typed")
	}
}
