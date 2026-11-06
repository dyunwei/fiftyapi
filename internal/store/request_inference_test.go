package store

import (
	"context"
	"reflect"
	"testing"
)

func TestRequestInferenceLogRoundTrip(t *testing.T) {
	s, user, key := quotaStore(t)
	ctx := context.Background()
	budget := int64(8192)
	want := RequestInference{ReasoningEffort: "high", ThinkingType: "enabled", ThinkingBudgetTokens: &budget, Speed: "fast", ServiceTier: "auto"}
	for _, tc := range []struct {
		id        string
		inference RequestInference
	}{
		{"with-settings", want}, {"without-settings", RequestInference{}},
	} {
		if err := s.LogRequest(ctx, RequestLog{ID: tc.id, UserID: user, KeyID: key, Model: "model", RequestInference: tc.inference}); err != nil {
			t.Fatal(err)
		}
		logs, err := s.LogsPage(ctx, user, false, 0, "", 0, "", 0, tc.id)
		if err != nil || len(logs) != 1 || !reflect.DeepEqual(logs[0].RequestInference, tc.inference) {
			t.Fatalf("legacy logs: %+v, %v", logs, err)
		}
		page, err := s.LogListPage(ctx, LogFilter{User: user, RequestID: tc.id}, 1, 20)
		if err != nil || len(page.Items) != 1 || !reflect.DeepEqual(page.Items[0].RequestInference, tc.inference) {
			t.Fatalf("paginated logs: %+v, %v", page, err)
		}
	}
}
