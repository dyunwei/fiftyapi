package server

import (
	"context"
	"fiftyapi/internal/store"
	"log"
	"net/http"
	"strconv"
	"time"
)

func (a *API) testAccountModel(w http.ResponseWriter, r *http.Request, _ identity) {
	if a.gateway == nil {
		writeError(w, 503, "账号服务未配置")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, 400, "账号 ID 无效")
		return
	}
	var in struct {
		Model    string `json:"model"`
		Protocol string `json:"protocol"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.Model) == 0 || len(in.Model) > 255 {
		writeError(w, 400, "模型 ID 无效")
		return
	}
	started := time.Now()
	result, err := a.gateway.Probe(r.Context(), id, in.Model, in.Protocol, r.Host)
	entry := store.RuntimeLog{Level: "info", Module: "accounts", Event: "manual_probe", AccountID: id, Message: "手动模型测试成功", Status: result.Status, DurationMS: time.Since(started).Milliseconds()}
	if err != nil {
		entry.Level = "warn"
		entry.Message = "手动模型测试未完成，请检查账号、模型协议或并发占用"
	} else if result.Error != "" || result.Status < 200 || result.Status >= 300 {
		entry.Level = "error"
		entry.Message = "手动模型测试失败，请检查上游状态"
	}
	a.store.RecordRuntime(r.Context(), entry)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, result)
}

func nextMorning(now time.Time) time.Time {
	next := time.Date(now.Year(), now.Month(), now.Day(), 7, 0, 0, 0, now.Location())
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

// RunMorningProbes runs until shutdown. Each enabled OpenAI account receives one
// request, since quota windows belong to upstream accounts rather than models.
func (a *API) RunMorningProbes(ctx context.Context) {
	for {
		next := nextMorning(time.Now())
		log.Printf("OpenAI 每日测试下次执行：%s", next.Format(time.RFC3339))
		a.store.RecordRuntime(ctx, store.RuntimeLog{Level: "info", Module: "scheduler", Event: "morning_scheduled", Message: "每日 OpenAI 测试下次执行：" + next.Format(time.RFC3339)})
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		a.runMorningProbeBatch(ctx)
	}
}

func (a *API) runMorningProbeBatch(ctx context.Context) {
	start := time.Now()
	a.store.RecordRuntime(ctx, store.RuntimeLog{Level: "info", Module: "scheduler", Event: "morning_start", Message: "每日 OpenAI 测试开始"})
	defer func() {
		message := "每日 OpenAI 测试结束"
		level := "info"
		if ctx.Err() != nil {
			message = "每日 OpenAI 测试因服务停止而中断"
			level = "warn"
		}
		a.store.RecordRuntime(ctx, store.RuntimeLog{Level: level, Module: "scheduler", Event: "morning_end", Message: message, DurationMS: time.Since(start).Milliseconds()})
	}()
	rows, err := a.store.Accounts(ctx)
	if err != nil {
		log.Print("OpenAI 每日测试：读取账号失败")
		a.store.RecordRuntime(ctx, store.RuntimeLog{Level: "error", Module: "scheduler", Event: "morning_batch", Message: "每日测试无法读取账号列表"})
		return
	}
	for _, row := range rows {
		if ctx.Err() != nil {
			return
		}
		if !row.Enabled || row.Provider != "openai" || row.ModelCount == 0 || row.State == "reauth_required" {
			continue
		}
		page, err := a.store.AccountModelsPage(ctx, row.ID, 1, 1, "")
		if err != nil || len(page.Items) == 0 {
			a.store.RecordRuntime(ctx, store.RuntimeLog{Level: "warn", Module: "scheduler", Event: "morning_probe", AccountID: row.ID, Message: "每日测试未执行：无法读取已同步模型"})
			continue
		}
		m := page.Items[0]
		result, err := a.gateway.Probe(ctx, row.ID, m.ID, "responses", "localhost")
		if err != nil {
			log.Printf("OpenAI 每日测试 account=%d 未执行", row.ID)
			a.store.RecordRuntime(ctx, store.RuntimeLog{Level: "warn", Module: "scheduler", Event: "morning_probe", AccountID: row.ID, Message: "每日测试未执行，请检查账号凭据、模型协议或并发占用"})
			continue
		}
		level, message := "info", "每日 OpenAI 测试成功"
		if result.Error != "" || result.Status < 200 || result.Status >= 300 {
			level = "error"
			message = "每日 OpenAI 测试失败，请检查上游状态"
		}
		a.store.RecordRuntime(ctx, store.RuntimeLog{Level: level, Module: "scheduler", Event: "morning_probe", AccountID: row.ID, Message: message, Status: result.Status, DurationMS: result.DurationMS})
		log.Printf("OpenAI 每日测试 account=%d model=%s status=%d duration_ms=%d success=%t", row.ID, m.ID, result.Status, result.DurationMS, result.Error == "" && result.Status >= 200 && result.Status < 300)
	}
}
