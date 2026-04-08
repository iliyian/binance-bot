package telegram

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// mockTransport captures HTTP requests and returns a canned OK response,
// so tests never make real network calls.
type mockTransport struct {
	bodies []string
}

func (m *mockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	body, _ := io.ReadAll(req.Body)
	m.bodies = append(m.bodies, string(body))
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader([]byte(`{"ok":true}`))),
		Header:     make(http.Header),
	}, nil
}

// newMockBot creates a Bot wired to a mockTransport so no real HTTP is made.
func newMockBot(chatID string) (*Bot, *mockTransport) {
	transport := &mockTransport{}
	bot := &Bot{
		botToken: "testtoken",
		chatID:   chatID,
		client:   &http.Client{Transport: transport},
	}
	return bot, transport
}

// lastSentText returns the "text" field of the last message sent via sendReply.
func lastSentText(transport *mockTransport) string {
	if len(transport.bodies) == 0 {
		return ""
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(transport.bodies[len(transport.bodies)-1]), &payload); err != nil {
		return ""
	}
	if text, ok := payload["text"].(string); ok {
		return text
	}
	return ""
}

// ---------- handleSetAmount ----------

func TestHandleSetAmount_NoArgs(t *testing.T) {
	bot, transport := newMockBot("123")
	bot.handleSetAmount([]string{})
	reply := lastSentText(transport)
	if !strings.Contains(reply, "/setamount") {
		t.Errorf("expected usage message containing /setamount, got: %s", reply)
	}
}

func TestHandleSetAmount_OneArg(t *testing.T) {
	bot, transport := newMockBot("123")
	bot.handleSetAmount([]string{"BTCUSDT"})
	reply := lastSentText(transport)
	if !strings.Contains(reply, "/setamount") {
		t.Errorf("expected usage message containing /setamount, got: %s", reply)
	}
}

func TestHandleSetAmount_UpdaterNil(t *testing.T) {
	bot, transport := newMockBot("123")
	bot.amountUpdater = nil
	bot.handleSetAmount([]string{"BTCUSDT", "20"})
	reply := lastSentText(transport)
	if !strings.Contains(reply, "不可用") {
		t.Errorf("expected unavailable message, got: %s", reply)
	}
}

func TestHandleSetAmount_UpdaterError(t *testing.T) {
	bot, transport := newMockBot("123")
	bot.amountUpdater = func(pair, amount string) error {
		return fmt.Errorf("交易对 %s 未找到", pair)
	}
	bot.handleSetAmount([]string{"XYZUSDT", "20"})
	reply := lastSentText(transport)
	if !strings.Contains(reply, "更新失败") {
		t.Errorf("expected error message containing 更新失败, got: %s", reply)
	}
}

func TestHandleSetAmount_Success(t *testing.T) {
	bot, transport := newMockBot("123")
	var calledPair, calledAmount string
	bot.amountUpdater = func(pair, amount string) error {
		calledPair = pair
		calledAmount = amount
		return nil
	}

	bot.handleSetAmount([]string{"BTCUSDT", "25.5"})

	if calledPair != "BTCUSDT" {
		t.Errorf("expected pair BTCUSDT, got %s", calledPair)
	}
	if calledAmount != "25.5" {
		t.Errorf("expected amount 25.5, got %s", calledAmount)
	}
	reply := lastSentText(transport)
	if !strings.Contains(reply, "BTCUSDT") {
		t.Errorf("expected success reply to contain pair, got: %s", reply)
	}
	if !strings.Contains(reply, "25.5") {
		t.Errorf("expected success reply to contain amount, got: %s", reply)
	}
}

func TestHandleSetAmount_PairUppercased(t *testing.T) {
	bot, _ := newMockBot("123")
	var calledPair string
	bot.amountUpdater = func(pair, amount string) error {
		calledPair = pair
		return nil
	}
	// Send lowercase pair; handleSetAmount should uppercase it
	bot.handleSetAmount([]string{"btcusdt", "10"})
	if calledPair != "BTCUSDT" {
		t.Errorf("expected pair uppercased to BTCUSDT, got %s", calledPair)
	}
}

// ---------- handleUpdate ----------

func TestHandleUpdate_UnauthorizedChat(t *testing.T) {
	bot, transport := newMockBot("999")
	update := TelegramUpdate{
		UpdateID: 1,
		Message: &TelegramMessage{
			Chat: &TelegramChat{ID: 123}, // wrong chat
			Text: "/help",
		},
	}
	bot.handleUpdate(update)
	if len(transport.bodies) > 0 {
		t.Error("expected no reply for unauthorized chat")
	}
}

func TestHandleUpdate_NilMessage(t *testing.T) {
	bot, transport := newMockBot("123")
	bot.handleUpdate(TelegramUpdate{UpdateID: 1, Message: nil})
	if len(transport.bodies) > 0 {
		t.Error("expected no reply for nil message")
	}
}

func TestHandleUpdate_NonCommand(t *testing.T) {
	bot, transport := newMockBot("123")
	update := TelegramUpdate{
		UpdateID: 1,
		Message: &TelegramMessage{
			Chat: &TelegramChat{ID: 123},
			Text: "hello world",
		},
	}
	bot.handleUpdate(update)
	if len(transport.bodies) > 0 {
		t.Error("expected no reply for non-command message")
	}
}

func TestHandleUpdate_RoutesSetAmount(t *testing.T) {
	bot, _ := newMockBot("123")
	var called bool
	bot.amountUpdater = func(pair, amount string) error {
		called = true
		return nil
	}
	update := TelegramUpdate{
		UpdateID: 1,
		Message: &TelegramMessage{
			Chat: &TelegramChat{ID: 123},
			Text: "/setamount BTCUSDT 20",
		},
	}
	bot.handleUpdate(update)
	if !called {
		t.Error("expected amountUpdater to be called via /setamount command")
	}
}

func TestHandleUpdate_RoutesHelp(t *testing.T) {
	bot, transport := newMockBot("123")
	update := TelegramUpdate{
		UpdateID: 1,
		Message: &TelegramMessage{
			Chat: &TelegramChat{ID: 123},
			Text: "/help",
		},
	}
	bot.handleUpdate(update)
	reply := lastSentText(transport)
	if !strings.Contains(reply, "/setamount") {
		t.Errorf("expected help text to mention /setamount, got: %s", reply)
	}
}

func TestHandleUpdate_StripsBotName(t *testing.T) {
	bot, _ := newMockBot("123")
	var called bool
	bot.amountUpdater = func(pair, amount string) error {
		called = true
		return nil
	}
	// Telegram sends commands as /command@botname when in groups
	update := TelegramUpdate{
		UpdateID: 1,
		Message: &TelegramMessage{
			Chat: &TelegramChat{ID: 123},
			Text: "/setamount@mybot BTCUSDT 30",
		},
	}
	bot.handleUpdate(update)
	if !called {
		t.Error("expected amountUpdater to be called when @botname suffix present")
	}
}

// ---------- SetAmountUpdater ----------

func TestSetAmountUpdater(t *testing.T) {
	bot, _ := newMockBot("123")
	if bot.amountUpdater != nil {
		t.Fatal("amountUpdater should be nil initially")
	}
	bot.SetAmountUpdater(func(pair, amount string) error { return nil })
	if bot.amountUpdater == nil {
		t.Error("amountUpdater should be set after SetAmountUpdater call")
	}
}

// ---------- boolToEmoji ----------

func TestBoolToEmoji(t *testing.T) {
	if !strings.Contains(boolToEmoji(true), "✅") {
		t.Error("expected ✅ for true")
	}
	if !strings.Contains(boolToEmoji(false), "❌") {
		t.Error("expected ❌ for false")
	}
}
