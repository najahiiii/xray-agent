package metrics

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/najahiiii/xray-agent/internal/model"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/net"
)

type Collector struct {
	log *slog.Logger

	mu      sync.Mutex
	lastNet *net.IOCountersStat
	lastAt  time.Time
}

func New(log *slog.Logger) *Collector {
	return &Collector{log: log}
}

func (c *Collector) Sample(ctx context.Context) *model.ServerMetricPush {
	sample := &model.ServerMetricPush{
		ServerTime: time.Now().UTC(),
	}
	var hasData bool

	if cpuPct, err := cpu.PercentWithContext(ctx, 0, false); err != nil {
		c.log.Debug("metrics cpu sample failed", "err", err)
	} else if len(cpuPct) > 0 {
		sample.CPUPercent = floatPtr(cpuPct[0])
		hasData = true
	}

	if vm, err := mem.VirtualMemoryWithContext(ctx); err != nil {
		c.log.Debug("metrics memory sample failed", "err", err)
	} else if vm != nil {
		sample.MemoryPercent = floatPtr(vm.UsedPercent)
		hasData = true
	}

	if up, down, ok := c.netThroughput(ctx); ok {
		sample.BandwidthUpMbps = floatPtr(up)
		sample.BandwidthDownMbps = floatPtr(down)
		hasData = true
	}

	if !hasData {
		return nil
	}
	return sample
}

func (c *Collector) netThroughput(ctx context.Context) (float64, float64, bool) {
	stats, err := net.IOCountersWithContext(ctx, false)
	if err != nil || len(stats) == 0 {
		if err != nil {
			c.log.Debug("metrics net sample failed", "err", err)
		}
		return 0, 0, false
	}

	now := time.Now()
	total := stats[0]

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.lastNet == nil {
		c.lastNet = &net.IOCountersStat{}
		*c.lastNet = total
		c.lastAt = now
		return 0, 0, false
	}

	elapsed := now.Sub(c.lastAt).Seconds()
	if elapsed <= 0 {
		*c.lastNet = total
		c.lastAt = now
		return 0, 0, false
	}

	upDelta := diffUint64(total.BytesSent, c.lastNet.BytesSent)
	downDelta := diffUint64(total.BytesRecv, c.lastNet.BytesRecv)

	*c.lastNet = total
	c.lastAt = now

	upMbps := bytesToMbps(upDelta, elapsed)
	downMbps := bytesToMbps(downDelta, elapsed)
	return upMbps, downMbps, true
}

func diffUint64(curr, prev uint64) uint64 {
	if curr >= prev {
		return curr - prev
	}
	return 0
}

func bytesToMbps(delta uint64, seconds float64) float64 {
	if seconds <= 0 {
		return 0
	}
	return (float64(delta) * 8) / (seconds * 1_000_000)
}

func floatPtr(value float64) *float64 {
	v := value
	return &v
}
