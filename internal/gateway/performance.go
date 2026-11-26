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
	mu             sync.Mutex
	accountWait    timingSamples
	firstOutput    timingSamples
	sse, websocket latencyWindow
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
	AccountQueue TimingSummary      `json:"account_queue"`
	FirstOutput  TimingSummary      `json:"first_output"`
	SSE          LatencyPercentiles `json:"sse_last_hour"`
	WebSocket    LatencyPercentiles `json:"websocket_last_hour"`
}

func (h *Handler) Performance() Performance {
	h.performance.mu.Lock()
	defer h.performance.mu.Unlock()
	queue, first := h.performance.accountWait, h.performance.firstOutput
	return Performance{
		AccountQueue: SummarizeTiming(queue.count, queue.total),
		FirstOutput:  SummarizeTiming(first.count, first.total),
		SSE:          h.performance.sse.summary(time.Now()),
		WebSocket:    h.performance.websocket.summary(time.Now()),
	}
}

// Each minute uses fixed latency buckets. Counts are bounded by time, not traffic.
var latencyBounds = [...]int64{10, 25, 50, 100, 200, 400, 750, 1000, 2000, 4000, 8000, 15000, 30000, 60000, 120000}

type latencyMinute struct {
	minute int64
	counts [len(latencyBounds) + 1]int64
}
type latencyWindow [60]latencyMinute
type LatencyPercentiles struct {
	Samples int64 `json:"samples"`
	P50     int64 `json:"p50_ms"`
	P95     int64 `json:"p95_ms"`
}

func (w *latencyWindow) observe(at time.Time, elapsed time.Duration) {
	minute := at.Unix() / 60
	slot := &w[minute%60]
	if slot.minute != minute {
		*slot = latencyMinute{minute: minute}
	}
	ms := elapsed.Milliseconds()
	for i, b := range latencyBounds {
		if ms <= b {
			slot.counts[i]++
			return
		}
	}
	slot.counts[len(latencyBounds)]++
}
func (w *latencyWindow) summary(at time.Time) LatencyPercentiles {
	minute := at.Unix() / 60
	var bins [len(latencyBounds) + 1]int64
	var out LatencyPercentiles
	for _, slot := range w {
		if slot.minute > minute || slot.minute <= minute-60 {
			continue
		}
		for i, n := range slot.counts {
			bins[i] += n
			out.Samples += n
		}
	}
	if out.Samples == 0 {
		return out
	}
	quantile := func(p int64) int64 {
		rank := (out.Samples*p + 99) / 100
		for i, n := range bins {
			rank -= n
			if rank <= 0 {
				if i == len(latencyBounds) {
					return -1
				}
				return latencyBounds[i]
			}
		}
		return -1
	}
	out.P50, out.P95 = quantile(50), quantile(95)
	return out
}
