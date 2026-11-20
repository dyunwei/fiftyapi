package gateway

import (
	"sync"
	"time"
)

type timingSamples struct {
	count int64
	total time.Duration
}

func (t *timingSamples) observe(elapsed time.Duration) {
	t.count++
	t.total += elapsed
}

type performanceMetrics struct {
	mu          sync.Mutex
	accountWait timingSamples
	firstOutput timingSamples
}

type TimingSummary struct {
	Samples   int64   `json:"samples"`
	AverageMS float64 `json:"average_ms"`
}

func SummarizeTiming(samples int64, elapsed time.Duration) TimingSummary {
	out := TimingSummary{Samples: samples}
	if samples > 0 {
		out.AverageMS = float64(elapsed) / float64(time.Millisecond) / float64(samples)
	}
	return out
}

type Performance struct {
	AccountQueue TimingSummary `json:"account_queue"`
	FirstOutput  TimingSummary `json:"first_output"`
}

func (h *Handler) Performance() Performance {
	h.performance.mu.Lock()
	defer h.performance.mu.Unlock()
	queue, first := h.performance.accountWait, h.performance.firstOutput
	return Performance{
		AccountQueue: SummarizeTiming(queue.count, queue.total),
		FirstOutput:  SummarizeTiming(first.count, first.total),
	}
}
