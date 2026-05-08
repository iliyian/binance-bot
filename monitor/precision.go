package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	futuresExchangeInfoURL  = "https://fapi.binance.com/fapi/v1/exchangeInfo"
	defaultPricePrecision   = 2
	precisionRequestTimeout = 10 * time.Second
)

type futuresExchangeInfo struct {
	Symbols []struct {
		Symbol  string `json:"symbol"`
		Filters []struct {
			FilterType string `json:"filterType"`
			TickSize   string `json:"tickSize"`
		} `json:"filters"`
	} `json:"symbols"`
}

func (m *Monitor) loadPricePrecisions() error {
	m.precisionMu.RLock()
	if len(m.pricePrecisions) > 0 {
		m.precisionMu.RUnlock()
		return nil
	}
	m.precisionMu.RUnlock()

	ctx, cancel := context.WithTimeout(context.Background(), precisionRequestTimeout)
	defer cancel()

	precisions, err := fetchFuturesPricePrecisions(ctx, m.client)
	if err != nil {
		return err
	}

	m.precisionMu.Lock()
	if len(m.pricePrecisions) == 0 {
		m.pricePrecisions = precisions
	}
	m.precisionMu.Unlock()

	return nil
}

func fetchFuturesPricePrecisions(ctx context.Context, client *http.Client) (map[string]int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, futuresExchangeInfoURL, nil)
	if err != nil {
		return nil, fmt.Errorf("创建 exchangeInfo 请求失败: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 exchangeInfo 失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取 exchangeInfo 响应失败: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("exchangeInfo 返回 %d: %s", resp.StatusCode, string(body))
	}

	var info futuresExchangeInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, fmt.Errorf("解析 exchangeInfo 失败: %w", err)
	}

	precisions := make(map[string]int, len(info.Symbols))
	for _, sym := range info.Symbols {
		precision := defaultPricePrecision
		for _, filter := range sym.Filters {
			if filter.FilterType != "PRICE_FILTER" {
				continue
			}
			if p, ok := precisionFromTickSize(filter.TickSize); ok {
				precision = p
			}
			break
		}
		precisions[sym.Symbol] = precision
	}

	return precisions, nil
}

func precisionFromTickSize(tickSize string) (int, bool) {
	tickSize = strings.TrimSpace(tickSize)
	if tickSize == "" {
		return 0, false
	}

	if !strings.Contains(tickSize, ".") {
		if tickSize == "0" {
			return 0, false
		}
		return 0, true
	}

	trimmed := strings.TrimRight(tickSize, "0")
	if trimmed == "" {
		return 0, false
	}
	if strings.HasSuffix(trimmed, ".") {
		return 0, true
	}

	dot := strings.IndexByte(trimmed, '.')
	if dot < 0 {
		return 0, true
	}

	precision := len(trimmed) - dot - 1
	if precision < 0 {
		return 0, false
	}
	return precision, true
}

func (m *Monitor) pricePrecision(symbol string) int {
	m.precisionMu.RLock()
	precision, ok := m.pricePrecisions[symbol]
	m.precisionMu.RUnlock()
	if !ok {
		return defaultPricePrecision
	}
	return precision
}

func (m *Monitor) formatPrice(symbol string, value float64) string {
	return strconv.FormatFloat(value, 'f', m.pricePrecision(symbol), 64)
}
