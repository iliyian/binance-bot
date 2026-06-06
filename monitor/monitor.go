package monitor

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"
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
	cfg      *config.Config
	notifier *telegram.Notifier
	client   *http.Client

	ws *WSClient

	precisionMu     sync.RWMutex
	pricePrecisions map[string]int
}

// New 创建监控服务
func New(cfg *config.Config, notifier *telegram.Notifier) *Monitor {
	return &Monitor{
		cfg:      cfg,
		notifier: notifier,
		client:   &http.Client{Timeout: 15 * time.Second},
	}
}

// Start 启动实时价格监控 (WebSocket)
func (m *Monitor) Start() {
	if err := m.loadPricePrecisions(); err != nil {
		log.Printf("⚠️ 获取币种价格精度失败，改用默认小数位: %v", err)
	}

	m.ws = NewWSClient(m.cfg, m.notifier, m.client)
	if err := m.ws.Start(); err != nil {
		log.Fatalf("❌ WebSocket 启动失败: %v", err)
	}

	log.Printf("📡 价格监控已启动（WebSocket 实时模式）")
	for _, sym := range m.cfg.BollMonitorSymbols {
		log.Printf("📡 监控 %s，K 线: %s，布林带(%d, %.1f)",
			sym.Symbol, strings.Join(sym.Intervals, "&"), m.cfg.BollMonitorPeriod, m.cfg.BollMonitorStdDev)
	}
}

// Stop 停止监控
func (m *Monitor) Stop() {
	if m.ws != nil {
		m.ws.Stop()
	}
	log.Println("⏹ 价格监控已停止")
}

// GetStatus 获取当前监控状态描述
func (m *Monitor) GetStatus() string {
	var sb strings.Builder
	sb.WriteString("模式: WebSocket 实时\n")
	sb.WriteString(fmt.Sprintf("布林带参数: period=%d, stddev=%.1f\n\n", m.cfg.BollMonitorPeriod, m.cfg.BollMonitorStdDev))
	for _, sym := range m.cfg.BollMonitorSymbols {
		sb.WriteString(fmt.Sprintf("• %s — %s\n", sym.Symbol, strings.Join(sym.Intervals, "&")))
	}
	return sb.String()
}

// checkIntervals 检查一个交易对所有 interval 的布林带状态
func (m *Monitor) checkIntervals(ctx context.Context, symbol string, intervals []string) ([]IntervalResult, bool, BreakType) {
	limit := m.cfg.BollMonitorPeriod + 1 // +1 排除当前未闭合K线

	results := make([]IntervalResult, 0, len(intervals))
	allBreak := true
	var breakDir BreakType

	syn := resolveSynthetic(m.cfg.SyntheticPairs, symbol)

	for _, interval := range intervals {
		var klines []Kline
		var err error
		if syn.synthetic {
			klines, err = FetchSyntheticKlines(ctx, m.client, syn.expr, interval, limit)
		} else {
			klines, err = FetchKlines(ctx, m.client, symbol, interval, limit)
		}
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

func (m *Monitor) checkSymbol(symbol string, intervals []string) []IntervalResult {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	results, allBreak, breakDir := m.checkIntervals(ctx, symbol, intervals)

	for _, r := range results {
		log.Printf("📊 %s [%s] 上轨=%s 均值=%s 下轨=%s 最高=%s 最低=%s",
			symbol, r.Interval,
			m.formatPrice(symbol, r.Boll.Upper),
			m.formatPrice(symbol, r.Boll.Middle),
			m.formatPrice(symbol, r.Boll.Lower),
			m.formatPrice(symbol, r.Boll.High),
			m.formatPrice(symbol, r.Boll.Low))
	}

	if allBreak {
		log.Printf("🔔 %s 所有 K 线级别布林带突破！方向: %s", symbol, breakTypeName(breakDir))
	}

	return results
}

// CheckNow 立即执行一次检查并返回格式化结果
func (m *Monitor) CheckNow() string {
	var sb strings.Builder
	for _, sym := range m.cfg.BollMonitorSymbols {
		syn := resolveSynthetic(m.cfg.SyntheticPairs, sym.Symbol)

		results := m.checkSymbol(sym.Symbol, sym.Intervals)

		precision := m.pricePrecision(sym.Symbol)
		if syn.synthetic {
			precision = 4
		}
		format := func(v float64) string {
			return strconv.FormatFloat(v, 'f', precision, 64)
		}

		sb.WriteString(fmt.Sprintf("<b>%s</b>", sym.Symbol))
		if len(results) > 0 {
			sb.WriteString(fmt.Sprintf("  实时价格: <code>%s</code>", format(results[0].Boll.Close)))
		}
		if syn.synthetic && m.ws != nil {
			if numP, numOk := m.ws.LatestPrice(syn.num); numOk {
				if denP, denOk := m.ws.LatestPrice(syn.den); denOk {
					sb.WriteString(fmt.Sprintf("\n   └ %s: <code>%.4f</code>  %s: <code>%.4f</code>",
						syn.num, numP, syn.den, denP))
				}
			}
		}
		sb.WriteString("\n")
		for _, r := range results {
			status := "—"
			if r.BreakType == BreakUpper {
				status = "⬆️ 突破上轨"
			} else if r.BreakType == BreakLower {
				status = "⬇️ 突破下轨"
			}
			sb.WriteString(fmt.Sprintf("  [%s] 上轨=%s 均值=%s 下轨=%s\n", r.Interval,
				format(r.Boll.Upper), format(r.Boll.Middle), format(r.Boll.Lower)))
			sb.WriteString(fmt.Sprintf("  最高=%s 最低=%s %s\n",
				format(r.Boll.High), format(r.Boll.Low), status))
		}
		sb.WriteString("\n")
	}
	return sb.String()
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
