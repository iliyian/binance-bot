package telegram

import (
	"bytes"
	"context"
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

// PoolGetter 获取当前 pool 状态的回调函数类型
type PoolGetter func() map[string]float64

// Bot Telegram Bot 命令处理器
type Bot struct {
	botToken   string
	chatID     string
	client     *http.Client
	binance    *binance.Client
	cancel     context.CancelFunc
	poolGetter PoolGetter
}

// TelegramUpdate Telegram 更新结构
type TelegramUpdate struct {
	UpdateID int             `json:"update_id"`
	Message  *TelegramMessage `json:"message"`
}

// TelegramMessage Telegram 消息结构
type TelegramMessage struct {
	MessageID int             `json:"message_id"`
	From      *TelegramUser   `json:"from"`
	Chat      *TelegramChat   `json:"chat"`
	Text      string          `json:"text"`
	Entities  []MessageEntity `json:"entities"`
}

// TelegramUser Telegram 用户
type TelegramUser struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
}

// TelegramChat Telegram 聊天
type TelegramChat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

// MessageEntity 消息实体（用于识别命令）
type MessageEntity struct {
	Type   string `json:"type"`
	Offset int    `json:"offset"`
	Length int    `json:"length"`
}

// BotCommand Telegram Bot 命令定义
type BotCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

// GetUpdatesResponse getUpdates 响应
type GetUpdatesResponse struct {
	OK     bool             `json:"ok"`
	Result []TelegramUpdate `json:"result"`
}

// NewBot 创建 Telegram Bot
func NewBot(botToken, chatID string, binanceClient *binance.Client) *Bot {
	return &Bot{
		botToken: botToken,
		chatID:   chatID,
		client: &http.Client{
			Timeout: 60 * time.Second, // 长轮询超时
		},
		binance: binanceClient,
	}
}

// SetPoolGetter 设置 pool 数据获取回调
func (b *Bot) SetPoolGetter(getter PoolGetter) {
	b.poolGetter = getter
}

// Start 启动 Bot 长轮询
func (b *Bot) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	b.cancel = cancel

	// 注册命令
	if err := b.registerCommands(); err != nil {
		log.Printf("⚠️ Telegram Bot 命令注册失败: %v", err)
	} else {
		log.Println("✅ Telegram Bot 命令注册成功")
	}

	// 启动长轮询
	go b.pollUpdates(ctx)
	log.Println("🤖 Telegram Bot 命令监听已启动")
}

// Stop 停止 Bot
func (b *Bot) Stop() {
	if b.cancel != nil {
		b.cancel()
	}
	log.Println("⏹ Telegram Bot 命令监听已停止")
}

// registerCommands 注册 Bot 命令到 Telegram
func (b *Bot) registerCommands() error {
	commands := []BotCommand{
		{Command: "balance", Description: "查询所有账户余额总览"},
		{Command: "spot", Description: "查询现货账户余额"},
		{Command: "earn", Description: "查询活期理财持仓"},
		{Command: "asset", Description: "查询指定币种余额，用法: /asset BTC"},
		{Command: "pool", Description: "查询各交易对的 Pool 累积金额"},
		{Command: "help", Description: "显示帮助信息"},
	}

	url := fmt.Sprintf("https://api.telegram.org/bot%s/setMyCommands", b.botToken)

	payload := map[string]interface{}{
		"commands": commands,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("序列化命令失败: %w", err)
	}

	resp, err := b.client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("注册命令失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
		return fmt.Errorf("注册命令 API 错误 %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// pollUpdates 长轮询获取更新
func (b *Bot) pollUpdates(ctx context.Context) {
	offset := 0

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		updates, err := b.getUpdates(offset, 30)
		if err != nil {
			if ctx.Err() != nil {
				return // 上下文已取消
			}
			log.Printf("⚠️ 获取 Telegram 更新失败: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}

		for _, update := range updates {
			offset = update.UpdateID + 1
			b.handleUpdate(update)
		}
	}
}

// getUpdates 获取更新
func (b *Bot) getUpdates(offset, timeout int) ([]TelegramUpdate, error) {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?offset=%d&timeout=%d&allowed_updates=[\"message\"]",
		b.botToken, offset, timeout)

	resp, err := b.client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, err
	}

	var result GetUpdatesResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	if !result.OK {
		return nil, fmt.Errorf("getUpdates 返回失败: %s", string(body))
	}

	return result.Result, nil
}

// handleUpdate 处理更新
func (b *Bot) handleUpdate(update TelegramUpdate) {
	if update.Message == nil {
		return
	}

	msg := update.Message

	// 验证聊天 ID（安全检查）
	chatID := fmt.Sprintf("%d", msg.Chat.ID)
	if chatID != b.chatID {
		log.Printf("⚠️ 忽略来自未授权聊天的消息: chatID=%s", chatID)
		return
	}

	// 检查是否是命令
	if !strings.HasPrefix(msg.Text, "/") {
		return
	}

	// 解析命令和参数
	parts := strings.Fields(msg.Text)
	command := strings.Split(parts[0], "@")[0] // 去除 @botname
	args := parts[1:]

	log.Printf("🤖 收到命令: %s, 参数: %v", command, args)

	switch command {
	case "/balance":
		b.handleBalance()
	case "/spot":
		b.handleSpot()
	case "/earn":
		b.handleEarn()
	case "/asset":
		b.handleAsset(args)
	case "/pool":
		b.handlePool()
	case "/help", "/start":
		b.handleHelp()
	default:
		log.Printf("🤖 未知命令: %s", command)
	}
}

// sendReply 发送回复消息
func (b *Bot) sendReply(text string) {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", b.botToken)

	payload := map[string]interface{}{
		"chat_id":    b.chatID,
		"text":       text,
		"parse_mode": "HTML",
	}

	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("❌ 序列化回复失败: %v", err)
		return
	}

	resp, err := b.client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("❌ 发送回复失败: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
		log.Printf("❌ 发送回复 API 错误 %d: %s", resp.StatusCode, string(respBody))
	}
}

// handleBalance 处理 /balance 命令 — 查询所有账户余额总览
func (b *Bot) handleBalance() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var sb strings.Builder
	sb.WriteString("💰 <b>账户余额总览</b>\n")
	sb.WriteString(fmt.Sprintf("📅 %s\n\n", time.Now().Format("2006-01-02 15:04:05")))

	// 查询现货余额
	spotBalances, spotErr := b.binance.GetAllSpotBalances(ctx)
	if spotErr != nil {
		sb.WriteString(fmt.Sprintf("❌ 现货账户查询失败: %s\n\n", html.EscapeString(spotErr.Error())))
	} else {
		sb.WriteString("📊 <b>现货账户:</b>\n")
		if len(spotBalances) == 0 {
			sb.WriteString("   (空)\n")
		}
		for _, bal := range spotBalances {
			sb.WriteString(fmt.Sprintf("   • <b>%s</b>: %.8g", bal.Asset, bal.Total))
			if bal.Locked > 0 {
				sb.WriteString(fmt.Sprintf(" (可用: %.8g, 冻结: %.8g)", bal.Free, bal.Locked))
			}
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	// 查询活期理财持仓
	earnPositions, earnErr := b.binance.GetFlexiblePositions(ctx, "")
	if earnErr != nil {
		sb.WriteString(fmt.Sprintf("❌ 活期理财查询失败: %s\n", html.EscapeString(earnErr.Error())))
	} else {
		sb.WriteString("🏦 <b>活期理财:</b>\n")
		if len(earnPositions) == 0 {
			sb.WriteString("   (空)\n")
		}
		for _, pos := range earnPositions {
			redeemable := pos.GetRedeemableAmount()
			sb.WriteString(fmt.Sprintf("   • <b>%s</b>: %s", pos.Asset, pos.TotalAmount))
			if redeemable != pos.TotalAmount {
				sb.WriteString(fmt.Sprintf(" (可赎回: %s)", redeemable))
			}
			sb.WriteString("\n")
		}
	}

	b.sendReply(sb.String())
}

// handleSpot 处理 /spot 命令 — 查询现货账户余额
func (b *Bot) handleSpot() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var sb strings.Builder
	sb.WriteString("📊 <b>现货账户余额</b>\n")
	sb.WriteString(fmt.Sprintf("📅 %s\n\n", time.Now().Format("2006-01-02 15:04:05")))

	balances, err := b.binance.GetAllSpotBalances(ctx)
	if err != nil {
		sb.WriteString(fmt.Sprintf("❌ 查询失败: %s\n", html.EscapeString(err.Error())))
		b.sendReply(sb.String())
		return
	}

	if len(balances) == 0 {
		sb.WriteString("(无持仓)\n")
		b.sendReply(sb.String())
		return
	}

	for _, bal := range balances {
		sb.WriteString(fmt.Sprintf("• <b>%s</b>\n", bal.Asset))
		sb.WriteString(fmt.Sprintf("   总计: %.8g\n", bal.Total))
		sb.WriteString(fmt.Sprintf("   可用: %.8g\n", bal.Free))
		if bal.Locked > 0 {
			sb.WriteString(fmt.Sprintf("   冻结: %.8g\n", bal.Locked))
		}
	}

	b.sendReply(sb.String())
}

// handleEarn 处理 /earn 命令 — 查询活期理财持仓
func (b *Bot) handleEarn() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var sb strings.Builder
	sb.WriteString("🏦 <b>活期理财持仓</b>\n")
	sb.WriteString(fmt.Sprintf("📅 %s\n\n", time.Now().Format("2006-01-02 15:04:05")))

	positions, err := b.binance.GetFlexiblePositions(ctx, "")
	if err != nil {
		sb.WriteString(fmt.Sprintf("❌ 查询失败: %s\n", html.EscapeString(err.Error())))
		b.sendReply(sb.String())
		return
	}

	if len(positions) == 0 {
		sb.WriteString("(无持仓)\n")
		b.sendReply(sb.String())
		return
	}

	for _, pos := range positions {
		redeemable := pos.GetRedeemableAmount()
		sb.WriteString(fmt.Sprintf("• <b>%s</b>\n", pos.Asset))
		sb.WriteString(fmt.Sprintf("   总量: %s\n", pos.TotalAmount))
		sb.WriteString(fmt.Sprintf("   可赎回: %s\n", redeemable))
		sb.WriteString(fmt.Sprintf("   可赎回状态: %s\n", boolToEmoji(pos.CanRedeem)))
		if pos.LatestAnnualPercentageRate != "" {
			sb.WriteString(fmt.Sprintf("   年化利率: %s%%\n", pos.LatestAnnualPercentageRate))
		}
		if pos.CumulativeTotalRewards != "" && pos.CumulativeTotalRewards != "0" {
			sb.WriteString(fmt.Sprintf("   累计收益: %s\n", pos.CumulativeTotalRewards))
		}
		sb.WriteString(fmt.Sprintf("   产品ID: <code>%s</code>\n", pos.ProductId))
	}

	b.sendReply(sb.String())
}

// handleAsset 处理 /asset 命令 — 查询指定币种在所有账户的余额
func (b *Bot) handleAsset(args []string) {
	if len(args) == 0 {
		b.sendReply("⚠️ 请指定币种，例如: <code>/asset BTC</code>")
		return
	}

	asset := strings.ToUpper(strings.TrimSpace(args[0]))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🔍 <b>%s 余额查询</b>\n", html.EscapeString(asset)))
	sb.WriteString(fmt.Sprintf("📅 %s\n\n", time.Now().Format("2006-01-02 15:04:05")))

	// 现货余额
	spotBal, spotErr := b.binance.GetSpotBalance(ctx, asset)
	if spotErr != nil {
		sb.WriteString(fmt.Sprintf("❌ 现货查询失败: %s\n\n", html.EscapeString(spotErr.Error())))
	} else {
		sb.WriteString("📊 <b>现货账户:</b>\n")
		sb.WriteString(fmt.Sprintf("   总计: %.8g\n", spotBal.Total))
		sb.WriteString(fmt.Sprintf("   可用: %.8g\n", spotBal.Free))
		if spotBal.Locked > 0 {
			sb.WriteString(fmt.Sprintf("   冻结: %.8g\n", spotBal.Locked))
		}
		sb.WriteString("\n")
	}

	// 活期理财持仓
	earnPositions, earnErr := b.binance.GetFlexiblePositions(ctx, asset)
	if earnErr != nil {
		sb.WriteString(fmt.Sprintf("❌ 活期理财查询失败: %s\n", html.EscapeString(earnErr.Error())))
	} else {
		sb.WriteString("🏦 <b>活期理财:</b>\n")
		if len(earnPositions) == 0 {
			sb.WriteString("   (无持仓)\n")
		}
		for _, pos := range earnPositions {
			redeemable := pos.GetRedeemableAmount()
			sb.WriteString(fmt.Sprintf("   总量: %s\n", pos.TotalAmount))
			sb.WriteString(fmt.Sprintf("   可赎回: %s\n", redeemable))
		}
	}

	b.sendReply(sb.String())
}

// handlePool 处理 /pool 命令 — 查询各交易对的 Pool 累积金额
func (b *Bot) handlePool() {
	var sb strings.Builder
	sb.WriteString("🏊 <b>Pool 累积金额</b>\n")
	sb.WriteString(fmt.Sprintf("📅 %s\n\n", time.Now().Format("2006-01-02 15:04:05")))

	if b.poolGetter == nil {
		sb.WriteString("⚠️ Pool 功能未初始化\n")
		b.sendReply(sb.String())
		return
	}

	pool := b.poolGetter()
	if len(pool) == 0 {
		sb.WriteString("(所有交易对 Pool 均为 0)\n")
		b.sendReply(sb.String())
		return
	}

	totalPool := 0.0
	hasNonZero := false
	for pair, amount := range pool {
		if amount > 0 {
			hasNonZero = true
		}
		totalPool += amount
		sb.WriteString(fmt.Sprintf("• <b>%s</b>: %.8f USDT\n", pair, amount))
	}

	if !hasNonZero {
		sb.WriteString("\n(所有交易对 Pool 均为 0)\n")
	} else {
		sb.WriteString(fmt.Sprintf("\n💰 Pool 合计: %.8f USDT\n", totalPool))
	}

	sb.WriteString("\n💡 <i>Pool 会在每次定投时自动叠加到定投金额上</i>")

	b.sendReply(sb.String())
}

// handleHelp 处理 /help 命令
func (b *Bot) handleHelp() {
	text := `🤖 <b>币安定投机器人 — 命令列表</b>

/balance — 查询所有账户余额总览
/spot — 查询现货账户余额
/earn — 查询活期理财持仓
/asset &lt;币种&gt; — 查询指定币种余额
  例: <code>/asset BTC</code>
  例: <code>/asset USDT</code>
/pool — 查询各交易对的 Pool 累积金额
/help — 显示此帮助信息`

	b.sendReply(text)
}

// boolToEmoji 布尔值转 emoji
func boolToEmoji(b bool) string {
	if b {
		return "✅ 是"
	}
	return "❌ 否"
}
