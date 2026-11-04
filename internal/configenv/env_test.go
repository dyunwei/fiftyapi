package configenv

import (
	"os"
	"testing"
)

func TestLegacyAliasAndExplicitCanonicalPrecedence(t *testing.T) {
	t.Setenv("ZTOKENMGR_PUBLIC_URL", "https://old.example")
	t.Setenv("FIFTYAPI_PUBLIC_URL", "")
	if err := os.Unsetenv("FIFTYAPI_PUBLIC_URL"); err != nil {
		t.Fatal(err)
	}
	if Value("FIFTYAPI_PUBLIC_URL") != "https://old.example" {
		t.Fatal("old configuration lost")
	}
	t.Setenv("FIFTYAPI_PUBLIC_URL", "https://new.example")
	if Value("FIFTYAPI_PUBLIC_URL") != "https://new.example" {
		t.Fatal("canonical configuration did not win")
	}
	t.Setenv("FIFTYAPI_PUBLIC_URL", "")
	if Value("FIFTYAPI_PUBLIC_URL") != "" {
		t.Fatal("cleared canonical setting resurrected old value")
	}
}
