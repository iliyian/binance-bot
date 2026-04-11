package monitor

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/iliyian/binance-bot/config"
	"github.com/iliyian/binance-bot/telegram"
)

// IntervalResult 单个 interval 的检测结果
type IntervalResult struct {
	Interval  string
	Boll      *BollResult
	BreakType BreakType
}

// Monitor 价格监控服务
type Monitor struct {
	cfg          *config.Config
	notifier     *telegram.Notifier
	client       *http.Client
	stop         chan struct{}
	done         chan struct{}
	prevAllBreak sync.Map // map[string]bool 上次检查各交易对是否全级别突破
}

// New 创建监控服务
func New(cfg *config.Config, notifier *telegram.Notifier) *Monitor {
	return &Monitor{
		cfg:      cfg,
		notifier: notifier,
		client:   &http.Client{Timeout: 15 * time.Second},
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
}

// Start 启动监控
func (m *Monitor) Start() {
	go m.run()
	log.Printf("📡 价格监控已启动，检查间隔: %s", m.cfg.BollMonitorCheckInterval)
	for _, sym := range m.cfg.BollMonitorSymbols {
		log.Printf("📡 监控 %s，K 线: %s，布林带(%d, %.1f)",
			sym.Symbol, strings.Join(sym.Intervals, "&"), m.cfg.BollMonitorPeriod, m.cfg.BollMonitorStdDev)
	}
}

// Stop 停止监控
func (m *Monitor) Stop() {
	close(m.stop)
	<-m.done
	log.Println("⏹ 价格监控已停止")
}

// GetStatus 获取当前监控状态描述
func (m *Monitor) GetStatus() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("检查间隔: %s\n", m.cfg.BollMonitorCheckInterval))
	sb.WriteString(fmt.Sprintf("布林带参数: period=%d, stddev=%.1f\n\n", m.cfg.BollMonitorPeriod, m.cfg.BollMonitorStdDev))
	for _, sym := range m.cfg.BollMonitorSymbols {
		sb.WriteString(fmt.Sprintf("• %s — %s\n", sym.Symbol, strings.Join(sym.Intervals, "&")))
	}
	return sb.String()
}

func (m *Monitor) run() {
	defer close(m.done)

	m.checkAll()

	ticker := time.NewTicker(m.cfg.BollMonitorCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.stop:
			return
		case <-ticker.C:
			m.checkAll()
		}
	}
}

// checkIntervals 检查一个交易对所有 interval 的布林带状态
func (m *Monitor) checkIntervals(ctx context.Context, symbol string, intervals []string) ([]IntervalResult, bool, BreakType) {
	limit := m.cfg.BollMonitorPeriod + 1 // +1 排除当前未闭合K线

	results := make([]IntervalResult, 0, len(intervals))
	allBreak := true
	var breakDir BreakType

	for _, interval := range intervals {
		klines, err := FetchKlines(ctx, m.client, symbol, interval, limit)
		if err != nil {
			log.Printf("⚠️ 获取 %s %s K 线失败: %v", symbol, interval, err)
			allBreak = false
			continue
		}

		// 最后一根未闭合K线的最高/最低价用于突破判断
		lastKline := klines[len(klines)-1]
		klines = klines[:len(klines)-1]

		closes := GetCloses(klines)
		boll := CalcBoll(closes, m.cfg.BollMonitorPeriod, m.cfg.BollMonitorStdDev)
		if boll == nil {
			log.Printf("⚠️ %s %s K 线数据不足，无法计算布林带", symbol, interval)
			allBreak = false
			continue
		}
		boll.High = lastKline.High
		boll.Low = lastKline.Low
		boll.Close = lastKline.Close

		bt := boll.Break()
		results = append(results, IntervalResult{
			Interval:  interval,
			Boll:      boll,
			BreakType: bt,
		})

		if bt == BreakNone {
			allBreak = false
		} else if breakDir == BreakNone {
			breakDir = bt
		} else if breakDir != bt {
			allBreak = false
		}
	}

	if len(results) < len(intervals) {
		allBreak = false
	}

	return results, allBreak, breakDir
}

func (m *Monitor) checkAll() {
	for _, sym := range m.cfg.BollMonitorSymbols {
		m.checkSymbol(sym.Symbol, sym.Intervals)
	}
}

func (m *Monitor) checkSymbol(symbol string, intervals []string) []IntervalResult {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	results, allBreak, breakDir := m.checkIntervals(ctx, symbol, intervals)

	for _, r := range results {
		log.Printf("📊 %s [%s] 上轨=%.4f 均值=%.4f 下轨=%.4f 最高=%.4f 最低=%.4f",
			symbol, r.Interval, r.Boll.Upper, r.Boll.Middle, r.Boll.Lower, r.Boll.High, r.Boll.Low)
	}

	if allBreak {
		log.Printf("🔔 %s 所有 K 线级别布林带突破！方向: %s", symbol, breakTypeName(breakDir))
	}

	prev, _ := m.prevAllBreak.Swap(symbol, allBreak)
	wasBreak, _ := prev.(bool)

	if allBreak && !wasBreak {
		m.sendAlert(symbol, breakDir, results)
	}

	return results
}

// CheckNow 立即执行一次检查并返回格式化结果
func (m *Monitor) CheckNow() string {
	var sb strings.Builder
	for _, sym := range m.cfg.BollMonitorSymbols {
		results := m.checkSymbol(sym.Symbol, sym.Intervals)
		sb.WriteString(fmt.Sprintf("<b>%s</b>", sym.Symbol))
		if len(results) > 0 {
			sb.WriteString(fmt.Sprintf("  实时价格: %.2f", results[0].Boll.Close))
		}
		sb.WriteString("\n")
		for _, r := range results {
			status := "—"
			if r.BreakType == BreakUpper {
				status = "⬆️ 突破上轨"
			} else if r.BreakType == BreakLower {
				status = "⬇️ 突破下轨"
			}
			sb.WriteString(fmt.Sprintf("  [%s] 上轨=%.2f 均值=%.2f 下轨=%.2f\n", r.Interval, r.Boll.Upper, r.Boll.Middle, r.Boll.Lower))
			sb.WriteString(fmt.Sprintf("  最高=%.2f 最低=%.2f %s\n", r.Boll.High, r.Boll.Low, status))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func (m *Monitor) sendAlert(symbol string, breakDir BreakType, results []IntervalResult) {
	if m.notifier == nil {
		return
	}

	// 构建通知所需的数据（避免循环依赖，传递简单结构）
	details := make([]telegram.BollAlertDetail, len(results))
	for i, r := range results {
		details[i] = telegram.BollAlertDetail{
			Interval: r.Interval,
			High:     r.Boll.High,
			Low:      r.Boll.Low,
			Upper:    r.Boll.Upper,
			Middle:   r.Boll.Middle,
			Lower:    r.Boll.Lower,
		}
	}
	m.notifier.SendBollAlert(symbol, breakTypeName(breakDir), breakDir == BreakUpper, details)
}

func breakTypeName(bt BreakType) string {
	switch bt {
	case BreakUpper:
		return "突破上轨"
	case BreakLower:
		return "突破下轨"
	default:
		return "未突破"
	}
}
