package monitor

import "math"

// BollResult 布林带计算结果
type BollResult struct {
	Upper  float64 // 上轨
	Middle float64 // 中轨 (SMA)
	Lower  float64 // 下轨
	Close  float64 // 最新收盘价
}

// BreakType 突破类型
type BreakType int

const (
	BreakNone  BreakType = iota // 未突破
	BreakUpper                  // 突破上轨
	BreakLower                  // 突破下轨
)

// Break 判断收盘价是否突破布林带
func (b *BollResult) Break() BreakType {
	if b.Close > b.Upper {
		return BreakUpper
	}
	if b.Close < b.Lower {
		return BreakLower
	}
	return BreakNone
}

// CalcBoll 计算布林带
// closes: 收盘价序列（从旧到新），长度必须 >= period
// period: SMA 周期（通常为 20）
// stddev: 标准差倍数（通常为 2）
func CalcBoll(closes []float64, period int, stddev float64) *BollResult {
	if len(closes) < period {
		return nil
	}

	// 取最近 period 根 K 线计算 SMA 和标准差
	data := closes[len(closes)-period:]

	// SMA
	sum := 0.0
	for _, v := range data {
		sum += v
	}
	sma := sum / float64(period)

	// 标准差
	variance := 0.0
	for _, v := range data {
		diff := v - sma
		variance += diff * diff
	}
	sd := math.Sqrt(variance / float64(period))

	return &BollResult{
		Upper:  sma + stddev*sd,
		Middle: sma,
		Lower:  sma - stddev*sd,
		Close:  closes[len(closes)-1],
	}
}
