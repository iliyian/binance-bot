import re

with open("config/config.go", "r") as f:
    content = f.read()

# Add "sort" to imports if not there
imports_pattern = re.compile(r'import \((.*?)\)', re.DOTALL)
match = imports_pattern.search(content)
if match:
    imports = match.group(1)
    if '"sort"' not in imports:
        new_imports = imports + '\t"sort"\n'
        content = content.replace(imports, new_imports)

# Find where config.BollMonitorSymbols is parsed and add sorting
target = "cfg.BollMonitorStdDev = f\n\t\t}\n\n\t}"
new_target = """cfg.BollMonitorStdDev = f
		}

		// 按字典序排序
		sort.Slice(cfg.BollMonitorSymbols, func(i, j int) bool {
			return cfg.BollMonitorSymbols[i].Symbol < cfg.BollMonitorSymbols[j].Symbol
		})
	}"""

if target in content:
    content = content.replace(target, new_target)
else:
    print("Could not find target in config.go")

with open("config/config.go", "w") as f:
    f.write(content)
