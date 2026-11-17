package gateway

import (
	"context"
	"encoding/json"
	"fiftyapi/internal/store"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPremiumPriceAdmissionAndActualTierJSONAndSSE(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, tier := range []string{"fast", "default"} {
			t.Run(map[bool]string{true: "sse", false: "json"}[stream]+"/"+tier, func(t *testing.T) {
				calls := 0
				up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					var request map[string]json.RawMessage
					_ = json.NewDecoder(r.Body).Decode(&request)
					if string(request["future_option"]) != `{"new":true}` || string(request["temperature"]) != "0.25" {
						t.Error("native extension or sampling field lost")
					}
					response := `{"id":"fixture","status":"completed","service_tier":"` + tier + `","usage":{"input_tokens":10,"output_tokens":2,"input_tokens_details":{"cached_tokens":4}}}`
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":"+response+"}\n\n")
					} else {
						w.Header().Set("Content-Type", "application/json")
						io.WriteString(w, response)
					}
				}))
				defer up.Close()
				g := setupGateway(t, "openai", up)
				ctx := context.Background()
				cached, fastCache := int64(100000), int64(300000)
				p := store.Price{Input: 1000000, Output: 2000000, Rates: []store.PriceRate{{Tier: "default", Input: 1000000, Output: 2000000, Cached: &cached}, {Tier: "fast", Input: 3000000, Output: 6000000, Cached: &fastCache}}}
				if err := g.s.SetModel(ctx, g.user, "model", true, &p); err != nil {
					t.Fatal(err)
				}
				flag := "false"
				if stream {
					flag = "true"
				}
				out := g.request("/v1/responses", `{"model":"model","input":"hi","service_tier":"priority","stream":`+flag+`,"temperature":0.25,"future_option":{"new":true}}`)
				if out.Code != 200 {
					t.Fatalf("premium request rejected: %d %s", out.Code, out.Body.String())
				}
				want := int64(32)
				if tier == "default" {
					want = 11
				}
				quota, _ := g.s.Quota(ctx, g.user)
				logs, _ := g.s.Logs(ctx, g.user, false, 0, "", 0)
				if quota[0].Used != want || quota[0].Reserved != 0 || len(logs) != 1 || !logs[0].BillingUsage.Confirmed || logs[0].BillingUsage.Tier != tier {
					t.Fatal("actual tier settlement wrong", quota, logs)
				}
				denied := g.request("/v1/responses", `{"model":"model","input":"hi","service_tier":"ultrafast"}`)
				if denied.Code != 400 || calls != 1 {
					t.Fatal("unpriced tier dispatched")
				}
				if !strings.Contains(out.Body.String(), "service_tier") {
					t.Fatal("upstream tier stripped from response")
				}
			})
		}
	}
}
