# Read-only inventory/bridge checks; repository source only, no user configuration.
from pathlib import Path
import re, subprocess
root=Path(__file__).resolve().parents[3]
files=sorted((root/'internal/wslsupply').glob('*'))
print('INVENTORY',len(files))
for p in files: print(p.relative_to(root),len(p.read_text().splitlines()))
names=[]
for rel in ['app_wsl_supply.go','app_wsl_supply_s4.go','app_wsl_plugins.go']:
 text=(root/rel).read_text()
 names+=re.findall(r'func \(a \*App\) ([A-Z]\w*)\(',text)
js=(root/'frontend/wailsjs/go/main/App.js').read_text(); dt=(root/'frontend/wailsjs/go/main/App.d.ts').read_text()
print('APP_EXPORTED',len(names))
print('MISSING_JS',[n for n in names if f'export function {n}(' not in js])
print('MISSING_DTS',[n for n in names if f'export function {n}(' not in dt])
for rel in ['app_wsl_supply.go','app_wsl_supply_s4.go','app_wsl_plugins.go']:
 for n in re.findall(r'func \(a \*App\) ([A-Z]\w*)\(', (root/rel).read_text()):
  print(n, next((l for l in dt.splitlines() if l.startswith(f'export function {n}(')),''))
print('PROVENANCE')
subprocess.run(['git','log','--format=%h %s','16f707d^..58e7577','--','internal/wslsupply','app_wsl_supply.go','app_wsl_supply_s4.go','app_wsl_plugins.go'],cwd=root)
