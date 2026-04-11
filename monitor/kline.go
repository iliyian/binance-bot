package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
)

const futuresKlineURL = "https://fapi.binance.com/fapi/v1/klines"

// Kline K 线数据
type Kline struct {
	OpenTime  int64
	Open      float64
	High      float64
	Low       float64
	Close     float64
	Volume    float64
	CloseTime int64
}

// FetchKlines 从币安合约 REST API 获取 K 线数据
// symbol: 交易对 (如 BTCUSDT)
// interval: K 线周期 (如 1m, 5m, 15m, 1h, 4h, 1d)
// limit: 获取数量
func FetchKlines(ctx context.Context, client *http.Client, symbol, interval string, limit int) ([]Kline, error) {
	url := fmt.Sprintf("%s?symbol=%s&interval=%s&limit=%d", futuresKlineURL, symbol, interval, limit)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 K 线数据失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API 返回 %d: %s", resp.StatusCode, string(body))
	}

	// 币安 K 线返回格式: [[openTime, open, high, low, close, volume, closeTime, ...], ...]
	var raw [][]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("解析 JSON 失败: %w", err)
	}

	klines := make([]Kline, 0, len(raw))
	for _, item := range raw {
		if len(item) < 7 {
			continue
		}
		k, err := parseKline(item)
		if err != nil {
			continue
		}
		klines = append(klines, k)
	}

	return klines, nil
}

// GetCloses 从 K 线数组中提取收盘价
func GetCloses(klines []Kline) []float64 {
	closes := make([]float64, len(klines))
	for i, k := range klines {
		closes[i] = k.Close
	}
	return closes
}

func parseKline(item []json.RawMessage) (Kline, error) {
	var k Kline

	// openTime (index 0) - int64
	if err := json.Unmarshal(item[0], &k.OpenTime); err != nil {
		return k, err
	}

	// open (index 1) - string
	k.Open = parseFloat(item[1])
	// high (index 2) - string
	k.High = parseFloat(item[2])
	// low (index 3) - string
	k.Low = parseFloat(item[3])
	// close (index 4) - string
	k.Close = parseFloat(item[4])
	// volume (index 5) - string
	k.Volume = parseFloat(item[5])

	// closeTime (index 6) - int64
	if err := json.Unmarshal(item[6], &k.CloseTime); err != nil {
		return k, err
	}

	return k, nil
}

func parseFloat(raw json.RawMessage) float64 {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return 0
	}
	v, _ := strconv.ParseFloat(s, 64)
	return v
}
