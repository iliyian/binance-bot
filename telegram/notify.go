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

// Notifier Telegram 通知器
type Notifier struct {
	botToken string
	chatID   string
	client   *http.Client
}

// NewNotifier 创建 Telegram 通知器
func NewNotifier(botToken, chatID string) *Notifier {
	return &Notifier{
		botToken: botToken,
		chatID:   chatID,
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
func (n *Notifier) SendTradeReport(results []*binance.TradeResult, balance string, redeemResults, purchaseResults []*binance.EarnTransferResult) {
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

	for _, r := range results {
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
			sb.WriteString(fmt.Sprintf("   🔖 订单ID: %d\n\n", r.OrderID))
		}
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

	msg := sb.String()

	if err := n.sendMessage(msg); err != nil {
		log.Printf("❌ 发送 Telegram 通知失败: %v", err)
	} else {
		log.Printf("📨 Telegram 通知发送成功")
	}
}

// SendStartupNotice 发送启动通知
func (n *Notifier) SendStartupNotice(pairs []string, amounts []string, cronExpr string) {
	var sb strings.Builder

	sb.WriteString("🚀 <b>币安自动定投已启动</b>\n\n")
	sb.WriteString("📋 <b>定投计划:</b>\n")

	for i, pair := range pairs {
		amount := "N/A"
		if i < len(amounts) {
			amount = amounts[i]
		}
		sb.WriteString(fmt.Sprintf("  • %s — %s USDT\n", pair, amount))
	}

	sb.WriteString(fmt.Sprintf("\n⏰ 定时规则: <code>%s</code>\n", cronExpr))
	sb.WriteString(fmt.Sprintf("📅 启动时间: %s\n", time.Now().Format("2006-01-02 15:04:05")))

	if err := n.sendMessage(sb.String()); err != nil {
		log.Printf("❌ 发送启动通知失败: %v", err)
	}
}

// SendErrorNotice 发送错误通知
func (n *Notifier) SendErrorNotice(errMsg string) {
	msg := fmt.Sprintf("🚨 <b>币安定投异常</b>\n\n⚠️ %s\n\n📅 %s",
		html.EscapeString(errMsg), time.Now().Format("2006-01-02 15:04:05"))

	if err := n.sendMessage(msg); err != nil {
		log.Printf("❌ 发送错误通知失败: %v", err)
	}
}
