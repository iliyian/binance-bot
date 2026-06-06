package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/iliyian/binance-bot/config"
	"github.com/iliyian/binance-bot/telegram"
)

const (
	futuresWSBaseURL = "wss://fstream.binance.com/stream"
	wsReconnectDelay = 5 * time.Second
	wsPingInterval   = 30 * time.Second
	checkThrottleMs   = 500
	bollPeriodDefault = 20
)

var intervalToMs = map[string]int64{
	"1m":  60000,
	"3m":  180000,
	"5m":  300000,
	"15m": 900000,
	"30m": 1800000,
	"1h":  3600000,
	"2h":  7200000,
	"4h":  14400000,
	"6h":  21600000,
	"8h":  28800000,
	"12h": 43200000,
	"1d":  86400000,
	"3d":  259200000,
	"1w":  604800000,
	"1M":  2592000000,
}

type aggTradeEvent struct {
	EventType string `json:"e"`
	EventTime int64  `json:"E"`
	Symbol    string `json:"s"`
	Price     string `json:"p"`
	Quantity  string `json:"q"`
	TradeTime int64  `json:"T"`
}

type streamWrapper struct {
	Stream string          `json:"stream"`
	Data   json.RawMessage `json:"data"`
}

type intervalTracker struct {
	interval   string
	intervalMs int64
	pairSymbol string

	candleStart int64
	open        float64
	high        float64
	low         float64
	close       float64

	period int
	stddev float64
	closes []float64
	boll   *BollResult

	wasBreaking bool
	breakDir    BreakType
	lastCheckMs int64
}

type pairTracker struct {
	symbol    string
	synthetic bool
	numSymbol string
	denSymbol string

	intervals map[string]*intervalTracker
}

// streamBinding maps a raw Binance symbol to the trackers that need it
type streamBinding struct {
	pairSymbol string
	synthetic  bool
	numSymbol  string
	denSymbol  string
	intervals  []*intervalTracker
}

type pendingAlert struct {
	symbol    string
	bt        BreakType
	it        *intervalTracker
	precision int
	// synthetic pair underlying prices (zero for normal pairs)
	synthetic bool
	numSymbol string
	denSymbol string
	numPrice  float64
	denPrice  float64
}

// WSClient manages Binance WebSocket aggTrade streams and real-time Bollinger Band detection
type WSClient struct {
	cfg      *config.Config
	notifier *telegram.Notifier
	httpDoer *http.Client

	mu          sync.RWMutex
	trackers    map[string]*pairTracker
	streamIndex map[string][]*streamBinding // raw symbol → bindings
	latest      map[string]float64

	conn     *websocket.Conn
	connDone chan struct{}
	stopCh   chan struct{}
	doneCh   chan struct{}
}

func NewWSClient(cfg *config.Config, notifier *telegram.Notifier, httpClient *http.Client) *WSClient {
	return &WSClient{
		cfg:      cfg,
		notifier: notifier,
		httpDoer: httpClient,
		trackers: make(map[string]*pairTracker),
		latest:   make(map[string]float64),
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
}

func (w *WSClient) Start() error {
	if err := w.initTrackers(); err != nil {
		return fmt.Errorf("初始化 tracker 失败: %w", err)
	}
	go w.run()
	return nil
}

func (w *WSClient) Stop() {
	close(w.stopCh)
	w.mu.RLock()
	c := w.conn
	w.mu.RUnlock()
	if c != nil {
		c.Close()
	}
	<-w.doneCh
	log.Println("⏹ WebSocket 已停止")
}

func (w *WSClient) initTrackers() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.trackers = make(map[string]*pairTracker)
	w.streamIndex = make(map[string][]*streamBinding)

	for _, symCfg := range w.cfg.BollMonitorSymbols {
		symbol := symCfg.Symbol
		intervals := symCfg.Intervals

		syn := resolveSynthetic(w.cfg.SyntheticPairs, symbol)

		pt := &pairTracker{
			symbol:    symbol,
			synthetic: syn.synthetic,
			intervals: make(map[string]*intervalTracker),
		}
		if syn.synthetic {
			pt.numSymbol = syn.num
			pt.denSymbol = syn.den
		}

		period := w.cfg.BollMonitorPeriod
		if period <= 0 {
			period = bollPeriodDefault
		}
		stddev := w.cfg.BollMonitorStdDev
		if stddev <= 0 {
			stddev = 2.0
		}

		var its []*intervalTracker
		for _, interval := range intervals {
			intervalMs := intervalToMs[interval]
			if intervalMs == 0 {
				return fmt.Errorf("不支持的 K 线间隔: %s", interval)
			}

			it := &intervalTracker{
				interval:   interval,
				intervalMs: intervalMs,
				pairSymbol: symbol,
				period:     period,
				stddev:     stddev,
			}

			if err := w.loadBollBaseline(it, symbol, interval, syn.synthetic, syn.expr); err != nil {
				log.Printf("⚠️ %s [%s] 初始布林带加载失败: %v", symbol, interval, err)
			}

			pt.intervals[interval] = it
			its = append(its, it)
		}

		w.trackers[symbol] = pt

		// build reverse index: raw stream symbols → streamBindings
		sb := &streamBinding{
			pairSymbol: symbol,
			synthetic:  syn.synthetic,
			intervals:  its,
		}
		if syn.synthetic {
			sb.numSymbol = syn.num
			sb.denSymbol = syn.den
			w.streamIndex[syn.num] = append(w.streamIndex[syn.num], sb)
			w.streamIndex[syn.den] = append(w.streamIndex[syn.den], sb)
		} else {
			w.streamIndex[symbol] = append(w.streamIndex[symbol], sb)
		}
	}
	return nil
}

func (w *WSClient) loadBollBaseline(it *intervalTracker, symbol, interval string, isSynthetic bool, ratioExpr string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	limit := it.period + 1
	var klines []Kline
	var err error

	if isSynthetic {
		klines, err = FetchSyntheticKlines(ctx, w.httpDoer, ratioExpr, interval, limit)
	} else {
		klines, err = FetchKlines(ctx, w.httpDoer, symbol, interval, limit)
	}
	if err != nil {
		return err
	}

	if len(klines) == 0 {
		return fmt.Errorf("K 线数据为空")
	}

	last := klines[len(klines)-1]
	it.candleStart = last.OpenTime
	it.open = last.Open
	it.high = last.High
	it.low = last.Low
	it.close = last.Close

	klines = klines[:len(klines)-1]
	closes := GetCloses(klines)
	if len(closes) >= it.period {
		it.closes = closes[len(closes)-it.period:]
		it.boll = CalcBoll(it.closes, it.period, it.stddev)
	}

	return nil
}

func (w *WSClient) run() {
	defer close(w.doneCh)
	for {
		select {
		case <-w.stopCh:
			return
		default:
		}
		if err := w.connectAndRead(); err != nil {
			if isStopped(w.stopCh) {
				return
			}
			log.Printf("⚠️ WebSocket 断开: %v，%s 后重连", err, wsReconnectDelay)
			time.Sleep(wsReconnectDelay)
		}
	}
}

func (w *WSClient) connectAndRead() error {
	streams := w.buildStreamList()
	if len(streams) == 0 {
		return fmt.Errorf("没有可订阅的 stream")
	}

	url := fmt.Sprintf("%s?streams=%s", futuresWSBaseURL, strings.Join(streams, "/"))
	log.Printf("🔗 WebSocket 连接: %d streams", len(streams))

	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		return fmt.Errorf("拨号失败: %w", err)
	}

	connDone := make(chan struct{})
	defer close(connDone)

	w.mu.Lock()
	w.conn = conn
	w.connDone = connDone
	w.mu.Unlock()

	log.Println("✅ WebSocket 已连接，实时接收 aggTrade")

	go w.pingLoop(conn, connDone)

	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("读取消息失败: %w", err)
		}
		w.handleMessage(msg)
	}
}

func (w *WSClient) buildStreamList() []string {
	w.mu.RLock()
	defer w.mu.RUnlock()

	seen := make(map[string]bool)
	for rawSymbol := range w.streamIndex {
		seen[strings.ToLower(rawSymbol)+"@aggTrade"] = true
	}

	streams := make([]string, 0, len(seen))
	for s := range seen {
		streams = append(streams, s)
	}
	return streams
}

func (w *WSClient) handleMessage(msg []byte) {
	var wrapper streamWrapper
	if err := json.Unmarshal(msg, &wrapper); err != nil {
		var event aggTradeEvent
		if err := json.Unmarshal(msg, &event); err != nil {
			return
		}
		if event.EventType == "aggTrade" {
			w.processAggTrade(&event)
		}
		return
	}

	var event aggTradeEvent
	if err := json.Unmarshal(wrapper.Data, &event); err != nil {
		return
	}
	if event.EventType == "aggTrade" {
		w.processAggTrade(&event)
	}
}

func (w *WSClient) processAggTrade(e *aggTradeEvent) {
	price, _ := strconv.ParseFloat(e.Price, 64)
	symbol := strings.ToUpper(e.Symbol)
	tradeTime := e.TradeTime

	w.mu.Lock()
	w.latest[symbol] = price

	bindings := w.streamIndex[symbol]

	// snapshot counterpart prices for synthetic pairs while under lock
	snap := make(map[string]float64, len(bindings)*2)
	for _, sb := range bindings {
		if sb.synthetic {
			if _, exists := snap[sb.numSymbol]; !exists {
				if p, ok := w.latest[sb.numSymbol]; ok {
					snap[sb.numSymbol] = p
				}
			}
			if _, exists := snap[sb.denSymbol]; !exists {
				if p, ok := w.latest[sb.denSymbol]; ok {
					snap[sb.denSymbol] = p
				}
			}
		}
	}
	w.mu.Unlock()

	var alerts []pendingAlert
	for _, sb := range bindings {
		if sb.synthetic {
			numPrice, numOk := snap[sb.numSymbol]
			denPrice, denOk := snap[sb.denSymbol]
			if !numOk || !denOk || denPrice == 0 {
				continue
			}
			ratio := numPrice / denPrice
			for _, it := range sb.intervals {
				w.updateCandle(it, ratio, tradeTime)
				if a, ok := w.checkBreakout(it, sb.pairSymbol, true); ok {
					a.synthetic = true
					a.numSymbol = sb.numSymbol
					a.denSymbol = sb.denSymbol
					a.numPrice = numPrice
					a.denPrice = denPrice
					alerts = append(alerts, a)
				}
			}
		} else {
			for _, it := range sb.intervals {
				w.updateCandle(it, price, tradeTime)
				if a, ok := w.checkBreakout(it, sb.pairSymbol, false); ok {
					alerts = append(alerts, a)
				}
			}
		}
	}

	for _, a := range alerts {
		w.sendWSAlert(a)
	}
}

func (w *WSClient) updateCandle(it *intervalTracker, price float64, tradeTime int64) {
	candleStart := (tradeTime / it.intervalMs) * it.intervalMs

	if candleStart <= it.candleStart {
		if it.candleStart == 0 {
			it.candleStart = candleStart
			it.open = price
		}
		it.close = price
		if price > it.high {
			it.high = price
		}
		if price < it.low || it.low == 0 {
			it.low = price
		}
		return
	}

	// new candle: close previous and reset
	if it.candleStart > 0 && it.close > 0 {
		it.closes = append(it.closes, it.close)
		if len(it.closes) > it.period {
			it.closes = it.closes[len(it.closes)-it.period:]
		}
		if len(it.closes) >= it.period {
			it.boll = CalcBoll(it.closes, it.period, it.stddev)
			log.Printf("📊 %s [%s] 上轨=%.4f 均值=%.4f 下轨=%.4f",
				it.pairSymbol, it.interval,
				it.boll.Upper, it.boll.Middle, it.boll.Lower)
		}
	}
	it.candleStart = candleStart
	it.open = price
	it.high = price
	it.low = price
	it.close = price
	it.wasBreaking = false
	it.breakDir = BreakNone
}

func (w *WSClient) checkBreakout(it *intervalTracker, symbol string, synthetic bool) (pendingAlert, bool) {
	if it.boll == nil {
		return pendingAlert{}, false
	}

	nowMs := time.Now().UnixMilli()
	if nowMs-it.lastCheckMs < checkThrottleMs {
		return pendingAlert{}, false
	}
	it.lastCheckMs = nowMs

	boll := *it.boll
	boll.High = it.high
	boll.Low = it.low
	boll.Close = it.close

	bt := boll.Break()
	isBreaking := bt != BreakNone

	if isBreaking && !it.wasBreaking {
		it.wasBreaking = true
		it.breakDir = bt

		precision := defaultPricePrecision
		if synthetic {
			precision = 4
		}
		return pendingAlert{symbol: symbol, bt: bt, it: it, precision: precision}, true
	}
	if !isBreaking {
		it.wasBreaking = false
		it.breakDir = BreakNone
	}
	return pendingAlert{}, false
}

func (w *WSClient) sendWSAlert(a pendingAlert) {
	if a.synthetic {
		log.Printf("🔔 %s(%s/%s) [%s] %s！比值=%.*f %s=%.4f %s=%.4f 上轨=%.*f 均值=%.*f 下轨=%.*f 最高=%.*f 最低=%.*f",
			a.symbol, a.numSymbol, a.denSymbol, a.it.interval, breakTypeName(a.bt),
			a.precision, a.it.close,
			a.numSymbol, a.numPrice,
			a.denSymbol, a.denPrice,
			a.precision, a.it.boll.Upper,
			a.precision, a.it.boll.Middle,
			a.precision, a.it.boll.Lower,
			a.precision, a.it.high,
			a.precision, a.it.low)
	} else {
		log.Printf("🔔 %s [%s] %s！价格=%.*f 上轨=%.*f 均值=%.*f 下轨=%.*f 最高=%.*f 最低=%.*f",
			a.symbol, a.it.interval, breakTypeName(a.bt),
			a.precision, a.it.close,
			a.precision, a.it.boll.Upper,
			a.precision, a.it.boll.Middle,
			a.precision, a.it.boll.Lower,
			a.precision, a.it.high,
			a.precision, a.it.low)
	}

	if w.notifier == nil {
		return
	}

	isUpper := a.bt == BreakUpper
	detail := telegram.BollAlertDetail{
		Interval:  a.it.interval,
		High:      a.it.high,
		Low:       a.it.low,
		Upper:     a.it.boll.Upper,
		Middle:    a.it.boll.Middle,
		Lower:     a.it.boll.Lower,
		Synthetic: a.synthetic,
		NumSymbol: a.numSymbol,
		DenSymbol: a.denSymbol,
		NumPrice:  a.numPrice,
		DenPrice:  a.denPrice,
	}

	w.notifier.SendBollAlert(a.symbol, a.it.close, breakTypeName(a.bt), isUpper,
		[]telegram.BollAlertDetail{detail}, a.precision)
}

func (w *WSClient) LatestPrice(symbol string) (float64, bool) {
	w.mu.RLock()
	p, ok := w.latest[symbol]
	w.mu.RUnlock()
	return p, ok
}

func (w *WSClient) pingLoop(conn *websocket.Conn, done chan struct{}) {
	ticker := time.NewTicker(wsPingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			conn.WriteMessage(websocket.PingMessage, nil)
		}
	}
}

func isStopped(ch chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}
