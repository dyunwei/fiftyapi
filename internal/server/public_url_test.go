package server

import "testing"

func TestPublicURLAllowsHTTPOrigins(t *testing.T) {
	for _, value := range []string{"http://192.168.1.10:8080", "http://gateway.internal:8080/", "http://[fd00::1]:8080", "http://localhost:8080", "https://gateway.example"} {
		if !validPublicURL(value) {
			t.Errorf("rejected origin %q", value)
		}
		if err := (emailSettings{PublicURL: value}).validate(); err != nil {
			t.Errorf("settings rejected %q: %v", value, err)
		}
	}
	for _, value := range []string{"", "ftp://gateway.internal", "//gateway.internal", "http://", "http://:8080", "http://user:password@gateway.internal", "http://gateway.internal/path", "http://gateway.internal/?a=b", "http://gateway.internal/?", "http://gateway.internal/#fragment"} {
		if validPublicURL(value) {
			t.Errorf("accepted invalid origin %q", value)
		}
	}
}
