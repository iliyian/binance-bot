package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
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

// syntheticResult holds a parsed synthetic pair definition
type syntheticResult struct {
	synthetic bool
	expr      string
	num       string
	den       string
}

// resolveSynthetic resolves a symbol to its synthetic pair definition.
// It checks the configured synthetic pairs first, then falls back to inline "/" notation.
func resolveSynthetic(syntheticPairs map[string]string, symbol string) syntheticResult {
	if expr, ok := syntheticPairs[symbol]; ok {
		parts := strings.Split(expr, "/")
		return syntheticResult{synthetic: true, expr: expr, num: strings.TrimSpace(parts[0]), den: strings.TrimSpace(parts[1])}
	}
	if strings.Contains(symbol, "/") {
		parts := strings.Split(symbol, "/")
		return syntheticResult{synthetic: true, expr: symbol, num: strings.TrimSpace(parts[0]), den: strings.TrimSpace(parts[1])}
	}
	return syntheticResult{}
}

// FetchKlines 从币安合约 REST API 获取 K 线数据
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

// FetchSyntheticKlines 获取合成交易对 K 线
// ratioExpr 格式: NUM/DEN, 如 XAUUSDT/XAGUSDT
// 返回的 K 线各字段为 NUM/DEN 的比值
func FetchSyntheticKlines(ctx context.Context, client *http.Client, ratioExpr, interval string, limit int) ([]Kline, error) {
	parts := strings.Split(ratioExpr, "/")
	if len(parts) != 2 {
		return nil, fmt.Errorf("合成交易对表达式格式错误: %s", ratioExpr)
	}
	num := strings.TrimSpace(parts[0])
	den := strings.TrimSpace(parts[1])

	var (
		numKlines, denKlines []Kline
		numErr, denErr       error
		wg                   sync.WaitGroup
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		numKlines, numErr = FetchKlines(ctx, client, num, interval, limit)
	}()
	go func() {
		defer wg.Done()
		denKlines, denErr = FetchKlines(ctx, client, den, interval, limit)
	}()
	wg.Wait()

	if numErr != nil {
		return nil, fmt.Errorf("获取分子 %s K 线失败: %w", num, numErr)
	}
	if denErr != nil {
		return nil, fmt.Errorf("获取分母 %s K 线失败: %w", den, denErr)
	}

	denByTime := make(map[int64]Kline, len(denKlines))
	for _, k := range denKlines {
		denByTime[k.OpenTime] = k
	}

	syn := make([]Kline, 0, len(numKlines))
	for _, nk := range numKlines {
		dk, ok := denByTime[nk.OpenTime]
		if !ok {
			continue
		}
		if dk.Open == 0 || dk.High == 0 || dk.Low == 0 || dk.Close == 0 {
			continue
		}
		rawHigh := nk.High / dk.Low
		rawLow := nk.Low / dk.High
		syn = append(syn, Kline{
			OpenTime:  nk.OpenTime,
			Open:      nk.Open / dk.Open,
			High:      max(rawHigh, rawLow),
			Low:       min(rawHigh, rawLow),
			Close:     nk.Close / dk.Close,
			Volume:    0,
			CloseTime: nk.CloseTime,
		})
	}

	if len(syn) == 0 {
		return nil, fmt.Errorf("合成 %s K 线数据为空（分子 %d 条，分母 %d 条，无时间对齐记录）",
			ratioExpr, len(numKlines), len(denKlines))
	}

	return syn, nil
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

	if err := json.Unmarshal(item[0], &k.OpenTime); err != nil {
		return k, err
	}

	k.Open = parseFloat(item[1])
	k.High = parseFloat(item[2])
	k.Low = parseFloat(item[3])
	k.Close = parseFloat(item[4])
	k.Volume = parseFloat(item[5])

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
