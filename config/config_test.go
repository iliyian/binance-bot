package config

import (
	"os"
	"strings"
	"sync"
	"testing"
)

// chdirTemp changes the working directory to a fresh temp dir for the duration
// of the test and writes an optional .env file there.
func chdirTemp(t *testing.T, envContent string) {
	t.Helper()
	dir := t.TempDir()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	if envContent != "" {
		if err := os.WriteFile(".env", []byte(envContent), 0644); err != nil {
			t.Fatalf("write .env: %v", err)
		}
	}
}

// ---------- UpdateTradeAmount ----------

func TestUpdateTradeAmount_ValidUpdate(t *testing.T) {
	chdirTemp(t, "# comment\nTRADE_AMOUNTS=10,5\nOTHER=value\n")

	cfg := &Config{
		TradePairs:   []string{"BTCUSDT", "ETHUSDT"},
		TradeAmounts: []string{"10", "5"},
	}

	if err := cfg.UpdateTradeAmount("BTCUSDT", "20"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.TradeAmounts[0] != "20" {
		t.Errorf("in-memory value: want 20, got %s", cfg.TradeAmounts[0])
	}
	if cfg.TradeAmounts[1] != "5" {
		t.Errorf("second pair should be unchanged: want 5, got %s", cfg.TradeAmounts[1])
	}

	data, _ := os.ReadFile(".env")
	if !strings.Contains(string(data), "TRADE_AMOUNTS=20,5") {
		t.Errorf("file not updated correctly: %s", string(data))
	}
}

func TestUpdateTradeAmount_InvalidAmount(t *testing.T) {
	cfg := &Config{
		TradePairs:   []string{"BTCUSDT"},
		TradeAmounts: []string{"10"},
	}

	for _, amount := range []string{"abc", "-1", "0", ""} {
		if err := cfg.UpdateTradeAmount("BTCUSDT", amount); err == nil {
			t.Errorf("expected error for amount %q, got nil", amount)
		}
	}
}

func TestUpdateTradeAmount_PairNotFound(t *testing.T) {
	cfg := &Config{
		TradePairs:   []string{"BTCUSDT"},
		TradeAmounts: []string{"10"},
	}

	if err := cfg.UpdateTradeAmount("XYZUSDT", "20"); err == nil {
		t.Error("expected error for unknown pair, got nil")
	}
}

func TestUpdateTradeAmount_CaseInsensitive(t *testing.T) {
	chdirTemp(t, "TRADE_AMOUNTS=10\n")

	cfg := &Config{
		TradePairs:   []string{"BTCUSDT"},
		TradeAmounts: []string{"10"},
	}

	if err := cfg.UpdateTradeAmount("btcusdt", "20"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.TradeAmounts[0] != "20" {
		t.Errorf("want 20, got %s", cfg.TradeAmounts[0])
	}
}

func TestUpdateTradeAmount_Concurrent(t *testing.T) {
	chdirTemp(t, "TRADE_AMOUNTS=10,5\n")

	cfg := &Config{
		TradePairs:   []string{"BTCUSDT", "ETHUSDT"},
		TradeAmounts: []string{"10", "5"},
	}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = cfg.UpdateTradeAmount("BTCUSDT", "20")
		}()
	}
	wg.Wait()

	if cfg.TradeAmounts[0] != "20" {
		t.Errorf("after concurrent updates: want 20, got %s", cfg.TradeAmounts[0])
	}
}

// ---------- saveToEnvFile ----------

func TestSaveToEnvFile_PreservesCommentsAndOrder(t *testing.T) {
	envContent := "# === Config ===\nBINANCE_API_KEY=key\n\n# DCA\nTRADE_AMOUNTS=10,5\nCRON_SCHEDULE=0 0 9 * * *\n"
	chdirTemp(t, envContent)

	cfg := &Config{
		TradePairs:   []string{"BTCUSDT", "ETHUSDT"},
		TradeAmounts: []string{"20", "15"},
	}

	if err := cfg.saveToEnvFile(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(".env")
	if err != nil {
		t.Fatalf("read .env: %v", err)
	}
	result := string(data)

	// Comments preserved
	if !strings.Contains(result, "# === Config ===") {
		t.Error("header comment not preserved")
	}
	if !strings.Contains(result, "# DCA") {
		t.Error("DCA comment not preserved")
	}
	// Other variables preserved
	if !strings.Contains(result, "BINANCE_API_KEY=key") {
		t.Error("BINANCE_API_KEY not preserved")
	}
	if !strings.Contains(result, "CRON_SCHEDULE=0 0 9 * * *") {
		t.Error("CRON_SCHEDULE not preserved")
	}
	// TRADE_AMOUNTS updated
	if !strings.Contains(result, "TRADE_AMOUNTS=20,15") {
		t.Errorf("TRADE_AMOUNTS not updated correctly: %s", result)
	}
	// Original TRADE_AMOUNTS value gone
	if strings.Contains(result, "TRADE_AMOUNTS=10,5") {
		t.Errorf("old TRADE_AMOUNTS value still present: %s", result)
	}
	// Variable order preserved: BINANCE_API_KEY < TRADE_AMOUNTS < CRON_SCHEDULE
	idxKey := strings.Index(result, "BINANCE_API_KEY")
	idxAmounts := strings.Index(result, "TRADE_AMOUNTS")
	idxCron := strings.Index(result, "CRON_SCHEDULE")
	if idxKey >= idxAmounts || idxAmounts >= idxCron {
		t.Errorf("variable order not preserved: %s", result)
	}
}

func TestSaveToEnvFile_AppendsIfMissing(t *testing.T) {
	chdirTemp(t, "# no amounts here\nOTHER=val\n")

	cfg := &Config{
		TradePairs:   []string{"BTCUSDT"},
		TradeAmounts: []string{"30"},
	}

	if err := cfg.saveToEnvFile(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(".env")
	result := string(data)
	if !strings.Contains(result, "TRADE_AMOUNTS=30") {
		t.Errorf("TRADE_AMOUNTS not appended: %s", result)
	}
	if !strings.Contains(result, "# no amounts here") {
		t.Error("existing comment not preserved")
	}
	if !strings.Contains(result, "OTHER=val") {
		t.Error("existing variable not preserved")
	}
}

func TestSaveToEnvFile_TrailingNewlinePreserved(t *testing.T) {
	chdirTemp(t, "TRADE_AMOUNTS=10\n")

	cfg := &Config{
		TradePairs:   []string{"BTCUSDT"},
		TradeAmounts: []string{"20"},
	}

	if err := cfg.saveToEnvFile(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(".env")
	if !strings.HasSuffix(string(data), "\n") {
		t.Errorf("trailing newline not preserved, got: %q", string(data))
	}
}

func TestSaveToEnvFile_NoTrailingNewline(t *testing.T) {
	chdirTemp(t, "TRADE_AMOUNTS=10") // no trailing newline

	cfg := &Config{
		TradePairs:   []string{"BTCUSDT"},
		TradeAmounts: []string{"20"},
	}

	if err := cfg.saveToEnvFile(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(".env")
	if strings.HasSuffix(string(data), "\n") {
		t.Errorf("unexpected trailing newline added, got: %q", string(data))
	}
}

func TestSaveToEnvFile_CreatesFileIfMissing(t *testing.T) {
	chdirTemp(t, "") // no .env file

	cfg := &Config{
		TradePairs:   []string{"BTCUSDT"},
		TradeAmounts: []string{"50"},
	}

	if err := cfg.saveToEnvFile(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(".env")
	if err != nil {
		t.Fatalf("expected .env to be created: %v", err)
	}
	if !strings.Contains(string(data), "TRADE_AMOUNTS=50") {
		t.Errorf("expected TRADE_AMOUNTS=50, got: %s", string(data))
	}
}

func TestSaveToEnvFile_CommentWithKeyNotReplaced(t *testing.T) {
	// A comment line that contains the key name must not be replaced
	envContent := "# TRADE_AMOUNTS is set below\nTRADE_AMOUNTS=10\n"
	chdirTemp(t, envContent)

	cfg := &Config{
		TradePairs:   []string{"BTCUSDT"},
		TradeAmounts: []string{"99"},
	}

	if err := cfg.saveToEnvFile(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(".env")
	result := string(data)

	// The comment line should be unchanged
	if !strings.Contains(result, "# TRADE_AMOUNTS is set below") {
		t.Error("comment line was modified")
	}
	// The assignment line should be updated
	if !strings.Contains(result, "\nTRADE_AMOUNTS=99\n") {
		t.Errorf("assignment not updated correctly: %s", result)
	}
}
