sed -i '/bot.SetMonitorStatusGetter(mon.GetStatus)/a\			bot.SetMonitorAdder(mon.AddSymbol)\n\t\t\tbot.SetMonitorRemover(mon.RemoveSymbol)' main.go
