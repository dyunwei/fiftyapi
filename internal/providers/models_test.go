package providers

import "testing"

func TestParseModels(t *testing.T) {
	for _, body := range []string{`{"data":[{"id":"b"},{"id":"a"},{"id":"a"}]}`, `{"models":[{"slug":"b"},{"slug":"a"}]}`, `{"models":{"a":{},"b":{}}}`} {
		ids, err := ParseModels([]byte(body))
		if err != nil || len(ids) != 2 || ids[0] != "a" || ids[1] != "b" {
			t.Fatalf("%s: %v %v", body, ids, err)
		}
	}
	for _, body := range []string{`{}`, `null`, `{"data":null}`, `{"data":{}}`, `{"models":[{}]}`, `{"models":{"a":null}}`, `{"error":"failed","data":[]}`} {
		if _, err := ParseModels([]byte(body)); err == nil {
			t.Fatalf("accepted malformed catalog %s", body)
		}
	}
	ids, err := ParseModels([]byte(`{"data":[]}`))
	if err != nil || len(ids) != 0 {
		t.Fatal("empty authoritative catalog rejected")
	}
}
