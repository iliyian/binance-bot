sed -i 's/type MonitorCheckNow func() string/type MonitorCheckNow func() string\n\ntype MonitorAdder func(args []string) (string, error)\n\ntype MonitorRemover func(args []string) (string, error)/' telegram/bot.go

sed -i 's/monitorCheck  MonitorCheckNow/monitorCheck  MonitorCheckNow\n\tmonitorAdder  MonitorAdder\n\tmonitorRemover MonitorRemover/' telegram/bot.go

sed -i 's/func (b \*Bot) SetMonitorCheckNow(checker MonitorCheckNow) {/func (b \*Bot) SetMonitorAdder(adder MonitorAdder) {\n\tb.monitorAdder = adder\n}\n\nfunc (b \*Bot) SetMonitorRemover(remover MonitorRemover) {\n\tb.monitorRemover = remover\n}\n\nfunc (b \*Bot) SetMonitorCheckNow(checker MonitorCheckNow) {/' telegram/bot.go
