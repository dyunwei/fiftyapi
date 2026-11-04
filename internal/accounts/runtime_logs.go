package accounts

import (
	"context"
	"errors"
	"fiftyapi/internal/store"
	"time"
)

// Error text can contain provider payloads or credentials. Persist fixed summaries only.
func (s *Service) runtimeOutcome(ctx context.Context, id int64, event, label string, start time.Time, outcome *error) {
	level, message := "info", label+"成功"
	if *outcome != nil {
		level = "error"
		message = label + "失败，请检查账号授权、上游连接或数据库状态"
		if errors.Is(*outcome, context.Canceled) {
			level = "warn"
			message = label + "已取消"
		} else if errors.Is(*outcome, context.DeadlineExceeded) {
			message = label + "超时"
		}
	}
	s.store.RecordRuntime(ctx, store.RuntimeLog{Level: level, Module: "accounts", Event: event, AccountID: id, Message: message, DurationMS: time.Since(start).Milliseconds()})
}
