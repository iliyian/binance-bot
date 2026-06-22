1. Update `telegram/bot.go` to add `/addmonitor` and `/delmonitor` commands.
2. In `telegram/bot.go`, add a handler `handleAddMonitor` and `handleDelMonitor`.
3. Add `MonitorAdder` and `MonitorRemover` callback types to `telegram/bot.go`.
4. In `main.go`, hook `bot.SetMonitorAdder(...)` to a function that adds the monitor.
5. In `monitor/monitor.go`, add methods `AddSymbol` and `RemoveSymbol`, or just `UpdateSymbols([]config.BollMonitorSymbolConfig)` and `Reload()`. 
6. Ensure that when adding/deleting, `BollMonitorSymbols` are sorted by `Symbol` lexicographically.
7. Also update the `BOLL_MONITOR_SYMBOLS` string in `.env` by calling `config.UpdateEnvKey` with the sorted symbols.
