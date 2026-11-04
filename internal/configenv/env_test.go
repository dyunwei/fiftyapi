package configenv

import "testing"

func TestConfiguredAndEmptyValues(t *testing.T) {
	t.Setenv("FIFTYAPI_PUBLIC_URL", "https://example.test")
	if Value("FIFTYAPI_PUBLIC_URL") != "https://example.test" {
		t.Fatal("configured value ignored")
	}
	t.Setenv("FIFTYAPI_PUBLIC_URL", "")
	if Value("FIFTYAPI_PUBLIC_URL") != "" {
		t.Fatal("empty value ignored")
	}
}
