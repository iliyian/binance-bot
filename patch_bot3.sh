sed -i 's/case "\/monitor":/case "\/addmonitor":\n\t\tb.handleAddMonitor(args)\n\tcase "\/delmonitor":\n\t\tb.handleDelMonitor(args)\n\tcase "\/monitor":/' telegram/bot.go
