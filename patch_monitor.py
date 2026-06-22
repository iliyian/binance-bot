import re

with open("monitor/monitor.go", "r") as f:
    content = f.read()

# Add "sort" to imports
imports_pattern = re.compile(r'import \((.*?)\)', re.DOTALL)
match = imports_pattern.search(content)
if match:
    imports = match.group(1)
    if '"sort"' not in imports:
        new_imports = imports + '\t"sort"\n'
        content = content.replace(imports, new_imports)

# Add the new methods
new_methods = """
// Reload 重启 WebSocket 以应用新的配置
func (m *Monitor) Reload() {
	m.Stop()
	m.Start()
}

// AddSymbol 添加或更新监控交易对
func (m *Monitor) AddSymbol(args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("参数不能为空")
	}

	symbol := strings.ToUpper(strings.TrimSpace(args[0]))
	intervals := args[1:]
	for i := range intervals {
		intervals[i] = strings.TrimSpace(intervals[i])
	}

	// 检查是否已存在
	found := false
	for i, existing := range m.cfg.BollMonitorSymbols {
		if existing.Symbol == symbol {
			if len(intervals) > 0 {
				m.cfg.BollMonitorSymbols[i].Intervals = intervals
			}
			found = true
			break
		}
	}

	if !found {
		if len(intervals) == 0 {
			// 如果没有指定，尝试使用默认配置（取第一个或全局）
			if len(m.cfg.BollMonitorSymbols) > 0 {
				// duplicate slice
				intervals = append([]string{}, m.cfg.BollMonitorSymbols[0].Intervals...)
			}
		}
		m.cfg.BollMonitorSymbols = append(m.cfg.BollMonitorSymbols, config.BollMonitorSymbolConfig{
			Symbol:    symbol,
			Intervals: intervals,
		})
	}

	return m.saveAndReload()
}

// RemoveSymbol 删除监控交易对
func (m *Monitor) RemoveSymbol(args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("参数不能为空")
	}

	symbol := strings.ToUpper(strings.TrimSpace(args[0]))

	foundIdx := -1
	for i, existing := range m.cfg.BollMonitorSymbols {
		if existing.Symbol == symbol {
			foundIdx = i
			break
		}
	}

	if foundIdx == -1 {
		return "", fmt.Errorf("未找到交易对 %s", symbol)
	}

	// 移除
	m.cfg.BollMonitorSymbols = append(m.cfg.BollMonitorSymbols[:foundIdx], m.cfg.BollMonitorSymbols[foundIdx+1:]...)

	return m.saveAndReload()
}

// saveAndReload 排序、保存环境变量并重新加载
func (m *Monitor) saveAndReload() (string, error) {
	// 按照字典序排序
	sort.Slice(m.cfg.BollMonitorSymbols, func(i, j int) bool {
		return m.cfg.BollMonitorSymbols[i].Symbol < m.cfg.BollMonitorSymbols[j].Symbol
	})

	// 构造环境变量字符串
	var parts []string
	for _, sym := range m.cfg.BollMonitorSymbols {
		if len(sym.Intervals) > 0 {
			parts = append(parts, sym.Symbol+":"+strings.Join(sym.Intervals, "&"))
		} else {
			parts = append(parts, sym.Symbol)
		}
	}
	envStr := strings.Join(parts, ",")

	if err := config.UpdateEnvKey("BOLL_MONITOR_SYMBOLS", envStr); err != nil {
		return "", fmt.Errorf("更新 .env 失败: %w", err)
	}

	m.Reload()

	return fmt.Sprintf("已更新并重启监控，当前监控列表:\n%s", m.GetStatus()), nil
}
"""

with open("monitor/monitor.go", "w") as f:
    f.write(content + new_methods)

