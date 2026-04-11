package telegram

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/iliyian/binance-bot/binance"
)

// PoolInfo 记录每个交易对的 pool 变化信息（从 scheduler 包导入会循环依赖，这里定义接口）
type PoolInfo struct {
	Pair       string  // 交易对
	PoolBefore float64 // 交易前的 pool 值
	PoolAfter  float64 // 交易后的 pool 值
	Intended   float64 // 本次意图交易金额 (配置金额 + pool)
	Actual     float64 // 实际花费金额
}

// BollAlertDetail 布林带提醒中单个 K 线级别的详情
type BollAlertDetail struct {
	Interval string
	Close    float64
	Upper    float64
	Middle   float64
	Lower    float64
}

// Notifier Telegram 通知器
type Notifier struct {
	botToken   string
	chatID     string
	commitHash string
	client     *http.Client
}

// NewNotifier 创建 Telegram 通知器
func NewNotifier(botToken, chatID, commitHash string) *Notifier {
	return &Notifier{
		botToken:   botToken,
		chatID:     chatID,
		commitHash: commitHash,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// sendMessage 发送消息到 Telegram
func (n *Notifier) sendMessage(text string) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", n.botToken)

	payload := map[string]interface{}{
		"chat_id":    n.chatID,
		"text":       text,
		"parse_mode": "HTML",
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("序列化消息失败: %w", err)
	}

	resp, err := n.client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("发送 Telegram 消息失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Telegram API 返回错误 %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// SendTradeReport 发送定投交易报告
func (n *Notifier) SendTradeReport(results []*binance.TradeResult, balance string, redeemResults, purchaseResults []*binance.EarnTransferResult, poolInfos []PoolInfo) {
	var sb strings.Builder

	sb.WriteString("🤖 <b>币安自动定投报告</b>\n")
	sb.WriteString(fmt.Sprintf("📅 %s\n\n", time.Now().Format("2006-01-02 15:04:05")))

	// 理财赎回信息
	if len(redeemResults) > 0 {
		sb.WriteString("📥 <b>活期理财赎回:</b>\n")
		for _, r := range redeemResults {
			if r.Error != nil {
				sb.WriteString(fmt.Sprintf("   ❌ %s — %s\n", r.Asset, html.EscapeString(r.Error.Error())))
			} else {
				sb.WriteString(fmt.Sprintf("   ✅ 赎回 %.4f %s\n", r.Amount, r.Asset))
			}
		}
		sb.WriteString("\n")
	}

	successCount := 0
	failCount := 0

	for i, r := range results {
		if r.Error != nil {
			failCount++
			sb.WriteString(fmt.Sprintf("❌ <b>%s</b> — 失败\n", r.Symbol))
			sb.WriteString(fmt.Sprintf("   原因: %s\n\n", html.EscapeString(r.Error.Error())))
		} else {
			successCount++
			sb.WriteString(fmt.Sprintf("✅ <b>%s</b> — 成功\n", r.Symbol))
			sb.WriteString(fmt.Sprintf("   💰 花费: %s USDT\n", r.QuoteAmount))
			sb.WriteString(fmt.Sprintf("   📦 获得: %s\n", r.FilledQty))
			sb.WriteString(fmt.Sprintf("   📈 均价: %s\n", r.AvgPrice))
			if r.Commission != "" {
				sb.WriteString(fmt.Sprintf("   💸 手续费: %s\n", r.Commission))
			}
			sb.WriteString(fmt.Sprintf("   🕐 成交时间: %s\n", r.TransactTime.Format("2006-01-02 15:04:05")))
			sb.WriteString(fmt.Sprintf("   🔖 订单ID: %d\n", r.OrderID))
		}
		// 显示 pool 信息
		if i < len(poolInfos) {
			pi := poolInfos[i]
			if pi.PoolBefore > 0 || pi.PoolAfter > 0 {
				sb.WriteString(fmt.Sprintf("   🏊 Pool: %.8f → %.8f USDT\n", pi.PoolBefore, pi.PoolAfter))
			}
			if pi.Intended > 0 && r.Error == nil {
				sb.WriteString(fmt.Sprintf("   🎯 目标: %.8f, 实际: %.8f, 差额: %.8f\n",
					pi.Intended, pi.Actual, pi.Intended-pi.Actual))
			}
		}
		sb.WriteString("\n")
	}

	// 理财申购信息
	if len(purchaseResults) > 0 {
		sb.WriteString("📤 <b>存入活期理财:</b>\n")
		for _, r := range purchaseResults {
			if r.Error != nil {
				sb.WriteString(fmt.Sprintf("   ⚠️ %s — %s\n", r.Asset, html.EscapeString(r.Error.Error())))
			} else {
				sb.WriteString(fmt.Sprintf("   ✅ 存入 %.8f %s\n", r.Amount, r.Asset))
			}
		}
		sb.WriteString("\n")
	}

	sb.WriteString("━━━━━━━━━━━━━━━━━━\n")
	sb.WriteString(fmt.Sprintf("📊 成功: %d | 失败: %d\n", successCount, failCount))
	if balance != "" {
		sb.WriteString(fmt.Sprintf("💳 USDT 余额: %s\n", balance))
	}

	// 汇总 pool 信息
	hasPool := false
	for _, pi := range poolInfos {
		if pi.PoolAfter > 0 {
			hasPool = true
			break
		}
	}
	if hasPool {
		sb.WriteString("\n🏊 <b>Pool 累积:</b>\n")
		for _, pi := range poolInfos {
			if pi.PoolAfter > 0 {
				sb.WriteString(fmt.Sprintf("   • %s: %.8f USDT\n", pi.Pair, pi.PoolAfter))
			}
		}
	}

	sb.WriteString(fmt.Sprintf("\n🔗 <code>%s</code>", n.commitHash))

	msg := sb.String()

	if err := n.sendMessage(msg); err != nil {
		log.Printf("❌ 发送 Telegram 通知失败: %v", err)
	} else {
		log.Printf("📨 Telegram 通知发送成功")
	}
}

// SendStartupNotice 发送启动通知
func (n *Notifier) SendStartupNotice(pairs []string, amounts []string, cronExpr string, poolAmounts []float64) {
	var sb strings.Builder

	sb.WriteString("🚀 <b>币安自动定投已启动</b>\n\n")
	sb.WriteString("📋 <b>定投计划:</b>\n")

	for i, pair := range pairs {
		amount := "N/A"
		if i < len(amounts) {
			amount = amounts[i]
		}
		poolStr := ""
		if i < len(poolAmounts) && poolAmounts[i] > 0 {
			poolStr = fmt.Sprintf(" (Pool: %.8f)", poolAmounts[i])
		}
		sb.WriteString(fmt.Sprintf("  • %s — %s USDT%s\n", pair, amount, poolStr))
	}

	// 如果有任何非零 pool，汇总显示
	hasPool := false
	for _, p := range poolAmounts {
		if p > 0 {
			hasPool = true
			break
		}
	}
	if hasPool {
		sb.WriteString("\n🏊 <b>初始 Pool:</b>\n")
		for i, pair := range pairs {
			if i < len(poolAmounts) && poolAmounts[i] > 0 {
				sb.WriteString(fmt.Sprintf("  • %s — %.8f USDT\n", pair, poolAmounts[i]))
			}
		}
	}

	sb.WriteString(fmt.Sprintf("\n⏰ 定时规则: <code>%s</code>\n", cronExpr))
	sb.WriteString(fmt.Sprintf("📅 启动时间: %s\n", time.Now().Format("2006-01-02 15:04:05")))
	sb.WriteString(fmt.Sprintf("🔗 版本: <code>%s</code>", n.commitHash))

	if err := n.sendMessage(sb.String()); err != nil {
		log.Printf("❌ 发送启动通知失败: %v", err)
	}
}

// SendErrorNotice 发送错误通知
func (n *Notifier) SendErrorNotice(errMsg string) {
	msg := fmt.Sprintf("🚨 <b>币安定投异常</b>\n\n⚠️ %s\n\n📅 %s\n🔗 <code>%s</code>",
		html.EscapeString(errMsg), time.Now().Format("2006-01-02 15:04:05"), n.commitHash)

	if err := n.sendMessage(msg); err != nil {
		log.Printf("❌ 发送错误通知失败: %v", err)
	}
}

// SendBollAlert 发送布林带突破提醒
func (n *Notifier) SendBollAlert(symbol, direction string, isUpper bool, details []BollAlertDetail) {
	var sb strings.Builder

	icon := "📈"
	if !isUpper {
		icon = "📉"
	}

	sb.WriteString("🔔 <b>布林带突破提醒</b>\n\n")
	sb.WriteString(fmt.Sprintf("📊 <b>%s</b>\n", symbol))
	sb.WriteString(fmt.Sprintf("%s %s\n\n", icon, direction))

	for _, d := range details {
		sb.WriteString(fmt.Sprintf("⏱ <b>%s</b>\n", d.Interval))
		sb.WriteString(fmt.Sprintf("   收盘价: <code>%.2f</code>\n", d.Close))
		sb.WriteString(fmt.Sprintf("   上轨:   <code>%.2f</code>\n", d.Upper))
		sb.WriteString(fmt.Sprintf("   中轨:   <code>%.2f</code>\n", d.Middle))
		sb.WriteString(fmt.Sprintf("   下轨:   <code>%.2f</code>\n\n", d.Lower))
	}

	sb.WriteString(fmt.Sprintf("📅 %s", time.Now().Format("2006-01-02 15:04:05")))

	if err := n.sendMessage(sb.String()); err != nil {
		log.Printf("❌ 发送布林带提醒失败: %v", err)
	} else {
		log.Printf("📨 布林带提醒发送成功: %s %s", symbol, direction)
	}
}
