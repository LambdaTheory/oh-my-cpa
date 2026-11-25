# Re-runs the whole capabilities-navigation acceptance against fresh environments, one script after the other:
#   pwsh scripts/acceptance/capabilities-nav/run-all.ps1 [-BaseRoot <baseline worktree with its built binary>]
# Needs: tmp/omc-nav.exe built from the head (pnpm build, then the container cross-compile), the same in <BaseRoot>/tmp for the
# baseline, Edge, and ports 18481-18499 free. Output: logs and result files under the delivery's acceptance/net/s8 directory.
# Output of every script goes to a file, never to a pipe: the environments started here outlive this shell.
param(
  [string]$BaseRoot = 'C:/Users/lhw/AppData/Local/Temp/omc-t9-s8-base'
)
$ErrorActionPreference = 'Continue'
$repo = (Resolve-Path (Join-Path $PSScriptRoot '../../..')).Path
Set-Location $repo
$out = Join-Path $repo 'docs/delivery/2026-11-23-omc-capabilities-nav/acceptance/net/s8'
New-Item -ItemType Directory -Force $out | Out-Null
$nav = 'scripts/acceptance/capabilities-nav'
$summary = Join-Path $out 'run-all-summary.txt'
"script | exit | seconds" | Set-Content $summary

function Invoke-Step([string]$Name, [hashtable]$Environment = @{}) {
  $start = Get-Date
  foreach ($key in $Environment.Keys) { Set-Item "Env:$key" $Environment[$key] }
  node "$nav/$Name.mjs" *> (Join-Path $out "run-$Name.log")
  $code = $LASTEXITCODE
  foreach ($key in $Environment.Keys) { Remove-Item "Env:$key" -ErrorAction SilentlyContinue }
  "EXIT=$code" | Add-Content (Join-Path $out "run-$Name.log")
  "$Name | $code | $([int]((Get-Date) - $start).TotalSeconds)" | Add-Content $summary
}

# 1. Two pairs of environments from one seeded data directory.
Invoke-Step '00-setup'
# 2. The hidden-entry setting reads what the baseline stored, so it runs first.
Invoke-Step '05-hidden'
# 3. Rail, presentation, addresses, pages, key management page, panel side by side (stub layer), network.
Invoke-Step '01-structure'
Invoke-Step '02-presentation'
Invoke-Step '03-routes'
Invoke-Step '04-pages'
Invoke-Step '06-apikeys'
Invoke-Step '07-repokeys-parity'
Invoke-Step '09-network'
# 4. The real layer on the second pair (its data is still the seeded copy).
Invoke-Step '08-repokeys-real' @{ T9_APP = 'http://127.0.0.1:18494/omc'; T9_BASE_APP = 'http://127.0.0.1:18497/omc' }
# 5. Demonstration mode: the head's and the baseline's binaries, each on its own port.
$data = 'C:/Users/lhw/AppData/Local/Temp/omc-t9-s8-work/data'
node "$nav/env.mjs" demo-start demo-h --binary "$repo/tmp/omc-nav.exe" --data "$data/demo-h" --port 18487 *>> (Join-Path $out 'demo-start.log')
node "$nav/env.mjs" demo-start demo-b --binary "$BaseRoot/tmp/omc-nav.exe" --data "$data/demo-b" --port 18488 *>> (Join-Path $out 'demo-start.log')
Invoke-Step '10-demo'
# 6. Other pages, dark and phone, lazy loading.
Invoke-Step '11-visual'
# 7. Experience-environment self-check of the earlier delivery (needs its three ports free).
$env:OMCPA_BROWSER_BINARY = "$repo/tmp/omc-nav.exe"
$env:OMC_LENDING_APP_PORT = '18489'; $env:OMC_LENDING_CPA_PORT = '18490'; $env:OMC_LENDING_CONTROL_PORT = '18492'
$env:OMC_LENDING_DATA_DIR = "$data/lending-check"
node scripts/acceptance/lending-experience-env.mjs --check *> (Join-Path $out 'run-lending-check.log')
"lending-experience-env --check | $LASTEXITCODE | -" | Add-Content $summary
Remove-Item Env:OMCPA_BROWSER_BINARY, Env:OMC_LENDING_APP_PORT, Env:OMC_LENDING_CPA_PORT, Env:OMC_LENDING_CONTROL_PORT, Env:OMC_LENDING_DATA_DIR
# 8. Tear down what this run started.
foreach ($role in 'b1', 'h1', 'b2', 'h2', 'demo-h', 'demo-b', 'hfresh') { node "$nav/env.mjs" stop $role *>> (Join-Path $out 'run-all-stop.log') }
"DONE $(Get-Date)" | Add-Content $summary
