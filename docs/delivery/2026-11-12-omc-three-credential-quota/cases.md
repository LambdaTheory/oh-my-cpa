---
type: cases
slug: 2026-11-12-omc-three-credential-quota
status: draft
updated: 2026-11-12
prd: impl-plan.md
summary: "32 条用例（P0 13 / P1 17 / P2 2），覆盖 AC-1～AC-11；浏览器 14、接口 11、e2e/自动化 7"
---

# 验收用例：OMC 显示 ollama / Command Code / OpenCode Go 三家凭证额度

依据：需求原文（`E:/works/Homan-context/runs/msgs/2026-11-12-2026-11-12-T18-0.md`）与实现计划 `docs/delivery/2026-11-12-omc-three-credential-quota/impl-plan.md`（commit `602f636e`，AC 见 §8，字段映射见 §4.4，回退见 §4.5，载荷契约见 §6.1，测试方法见 §7，代码外前提见 §9.1）。

判定口径：结论只有「通过 / 范围内要修 / 范围外观察」。预期写用户可见结果；涉及接口字段的，字段名以当前已发布的 API 形状为准（`internal/quota/types.go`），P0 新增的 `stale/note` 字段不写进接口断言，只用界面结果验证。

## 测试环境

### 基线与工具链

- 分支：验收对象所在分支（P2 开发分支），基线 `602f636e` 之后。
- Node 20+、pnpm；首次执行 `pnpm install`。
- Go 1.22+（或按项目约定用 Docker 跑 Go 测试）。前端与 fake CPA 不需要 Go。
- 浏览器用例用 Orca 内置浏览器或本机 Chrome，不用真实账号、不连生产 CPA。

### 自动化入口

| 层 | 命令 | 覆盖用例 |
|---|---|---|
| 浏览器 e2e | `pnpm verify:browser -- --scenario plugin-quota`（场景名待 P2 注册，见 `scripts/acceptance/browser-selection.mjs:3`） | C-26，以及 C-01/C-03/C-04/C-05/C-06/C-13 的自动断言 |
| fake CPA 单测 | `node --test scripts/fake-cpa.test.mjs` | C-27 |
| Go 额度投影 | `go test ./internal/quota ./internal/cpa/management ./internal/api`、`go vet ./...` | C-28、C-29 |
| 前端逻辑/组件 | `pnpm type-check && pnpm test:logic && pnpm test:components` | C-30 |
| 回归总门 | `pnpm verify:static` + `pnpm verify:browser`（既有场景） | C-31 |

`pnpm verify:browser` 默认无头运行，失败产物在 `tmp/browser-acceptance-failure/`。需要人工看界面时按 1.5 起本地环境，另存截图。

### fake CPA 夹具（三家）

当前 `scripts/fake-cpa.mjs` 只有通用 fixture，**没有**三家插件夹具，也没有 `GET /v8/management/credentials/quota/providers` 与 `POST /v8/management/plugins/:id/quota` 路由；以下夹具与路由**待 P2 提供**（建议由 `createFakeCpaServer({ fixtures: 'plugin-quota' })` 或等价开关装载）。P2 同时把 `plugin-quota` 注册进 `scripts/acceptance/scenarios.mjs` 与 `browser-selection.mjs`。

| 夹具 | auth_index | 磁盘 type（逐字） | 插件载荷要点 |
|---|---|---|---|
| F-OLC | `fixture-ollama-cookie` | `ollama` | cookie 形态；`summary` balance `12.34` USD；groups `Ollama`：`5h` 剩余 60%、`7d` 剩余 80%，均带 `resetTime` |
| F-OLK | `fixture-ollama-key` | `ollama` | 仅 `summary` balance `8.75` USD；无 groups |
| F-CMC | `fixture-cmc` | `openai-compatible-command code` | `subscription.plan="Pro"`；`summary` balance `321`（number/credits）；groups `Command Code`：`5h` 剩余 80%、`7d` 剩余 55% |
| F-OPC | `fixture-opc` | `openai-compatible-opencode` | 无 `subscription`/`summary`；groups `OpenCode Go`：`rolling` 剩余 0%（窗口 note：`upstream reports this window rate limited`）、`weekly` 剩余 42%、`monthly` 剩余 21% |

三条插件凭证均带 `supports_quota:true`、`quota_provider` 等于上表 type，新宿主夹具带 `quota_plugin_id:"ollama-quota"`。fake CPA 的 `GET /v8/management/plugins` 返回 `id:"ollama-quota"`、`metadata.version:"v0.3.0"`、`supports_quota:true`、`quota_provider:"ollama"`。完整载荷形状见 impl-plan §6.1；数值刻意选非整数、非整齐值，避免格式化歧义。

夹具变体（P2 提供切换方式，名称以实现为准）：

| 变体 | 用途 | 内容 |
|---|---|---|
| F-MISSING | C-06 | CMC 无 `subscription`/`summary`；Ollama api_key 无 groups；OPC 只有 `weekly` |
| F-STALE | C-07 | CMC `summary.label="Balance · stale"`；`5h` description 含 `stale · last read at 2026-11-12T02:00:00Z · upstream timeout` |
| F-429 | C-08/C-18 | 插件额度路由 429 + `Retry-After: 120`，无缓存 |
| F-502 | C-09/C-25 | 插件额度路由 502，无缓存（C-25 先成功再切 502） |
| F-OLD-PLUGINS | C-19 | `/credentials` 不含 `quota_plugin_id`；`/plugins` 只按 identifier `ollama` 可匹配 |
| F-OLD-PROVIDERS | C-20 | `/credentials` 不含 `quota_plugin_id`；`/plugins` 匹配不到 CMC/OPC；`/credentials/quota/providers` 返回三家 `supported_providers` |
| F-UNMATCHED | C-21 | 凭证的 `quota_provider` 不在任何插件与 providers 列表里 |
| F-DECOY | C-22 | 新宿主 `quota_plugin_id:"ollama-quota"`，另有一个 `decoy-quota` 也声明支持 CMC |
| F-OWN-OLLAMA | C-10 | OMC 自有 Ollama 账号 `auth_index:"ollama:fixture-own"`，快照来源不是 `settings_balance`（沿用现有行为） |
| F-META | C-24 | 一张 Meta 凭证，用于确认自动刷新轮排除 Meta |

fake CPA 已把每个请求记进 `createFakeCpaServer()` 返回的 `requests` 数组（`scripts/fake-cpa.mjs:1088`），P2 场景直接读它断言；人工排查时由启动脚本退出前打印。

### 手工起本地环境（浏览器用例）

```bash
# 终端 1：fake CPA。P2 完成前，createFakeCpaServer 只接受 managementKey，
# 三家夹具开关待 P2 提供；完成后按 P2 的脚本/参数启动。
node --input-type=module -e "import('./scripts/fake-cpa.mjs').then(m=>{const f=m.createFakeCpaServer();f.server.listen(18080,'127.0.0.1',()=>console.log('fake CPA: http://127.0.0.1:18080'))})"

# 终端 2：OMC 开发服务器（环境变量与 scripts/browser-acceptance.mjs 保持一致）
OMCPA_CPA_BASE_URL=http://127.0.0.1:18080 \
OMCPA_CPA_MANAGEMENT_KEY=omc-e2e-management-key \
OMCPA_DATA_DIR=tmp/acceptance-plugin-quota \
OMCPA_MASTER_KEY=acceptance-master-key-0123456789abcdef \
OMCPA_CPA_USAGE_ADDR='' \
OMCPA_USAGE_INGEST_ENABLED=false \
OMCPA_UPDATE_CHECK_ENABLED=false \
pnpm dev
```

Windows PowerShell 用 `$env:NAME='value'` 逐条设置后执行 `pnpm dev`。打开 `http://127.0.0.1:5173/omc/`，登录密码是 CPA 管理密钥 `omc-e2e-management-key`。额度页路径 `/omc/oauth-management`。切换夹具或做到「无缓存」类用例时，换一个新的 `OMCPA_DATA_DIR` 并重启 OMC，避免旧快照干扰。

### 接口调用

```bash
BASE=http://127.0.0.1:5173/omc
curl -s -c /tmp/omc-cookie -H 'Content-Type: application/json' \
  -d '{"password":"omc-e2e-management-key"}' $BASE/api/auth/login
curl -s -b /tmp/omc-cookie $BASE/api/v1/management/quota | jq
curl -s -b /tmp/omc-cookie -H 'Content-Type: application/json' \
  -d '{"auth_index":"fixture-cmc"}' $BASE/api/v1/management/quota/refresh | jq

# 隔离用：关闭自动刷新，避免后台轮次污染「页面加载不读上游」类用例
curl -s -b /tmp/omc-cookie -X PUT -H 'Content-Type: application/json' \
  -d '{"interval_minutes":0,"refresh_on_reset":false}' \
  $BASE/api/v1/preferences/omc_quota_auto_refresh
```

Windows 下用 `curl.exe`；`jq` 可换成 `python -m json.tool`。

### 证据

截图、fake CPA 请求日志、curl 输出放 `inbox/agents/验收/2026-11-12-omc-three-credential-quota/`，不进 git。每条用例记录：用例号、命令/点击路径、关键输出或截图文件名。

## 用例

类型说明：**浏览器**＝人工看界面并截图（标「可自动化」的断言同由 C-26 脚本复核）；**接口**＝curl 或脚本断言；**e2e**＝自动化测试套件。优先级：P0 主流程、P1 边界/异常、P2 兼容。

### 浏览器（额度页行 / 抽屉）

#### C-01 三家凭证各自成行、名称与图标正确
- AC：AC-1 · 类型：浏览器 · 优先级：P0 · 执行方式：人工截图；行/标签断言可自动化
- 前置：F-OLC/F-OLK/F-CMC/F-OPC 已装载；OMC 已登录；额度页打开。
- 步骤：
  1. 搜索或滚动到四条夹具凭证。
  2. 查看每行标题、图标与 provider 过滤标签。
- 预期：
  - 四张凭证是独立行，分别标注 `Ollama`（两条）、`Command Code`、`OpenCode Go`；没有合并成一行或归到「其他」。
  - 过滤标签出现 `Ollama` / `Command Code` / `OpenCode Go`，点击每个标签只剩对应凭证。
  - 三家用各自品牌图标，不是通用问号/首字母占位；Ollama 沿用现有图标。
  - Command Code 与 OpenCode Go 行的内部 provider 名称保留原样（`openai-compatible-command code` 中间的空格未被改成连字符），页面不显示成 `openai-compatible-command-code`。

#### C-02 页面只经 OMC 读额度，不直连上游
- AC：AC-2 · 类型：浏览器 · 优先级：P0 · 执行方式：人工看 DevTools + fake CPA 请求记录
- 前置：同 C-01；关闭自动刷新（见 1.6）；清空 fake CPA `requests`。
- 步骤：
  1. 打开 DevTools → Network，过滤 XHR/fetch，记录域名。
  2. 刷新页面并等待 10 秒，点一次 CMC 行的刷新按钮（`quota.btn_refresh_quota`）。
- 预期：
  - 浏览器请求只打到 OMC 自己的 origin（`127.0.0.1:5173`），没有直接请求 `api.commandcode.ai`、`opencode.ai`、`ollama.com` 或 fake CPA 的 origin。
  - fake CPA `requests` 里，额度读取是 `POST /v8/management/plugins/ollama-quota/quota`，带 OMC 已存的 CPA 管理 key；没有携带凭证 api_key/cookie 的请求体。
  - OMC 额度响应与页面中都不出现凭证密钥、cookie、`base_url` 原文。

#### C-03 CMC：套餐、积分余额、5 小时/周窗口
- AC：AC-3 · 类型：浏览器 · 优先级：P0 · 执行方式：人工截图；字段断言可自动化
- 前置：F-CMC 行可见；无旧快照。
- 步骤：
  1. 看 F-CMC 行头：套餐、余额、两个窗口的标题/百分比/重置文案。
  2. 点「详情」→「配额」抽屉，复核同样的字段。
- 预期：
  - 套餐显示 `Pro`；没有编造 tier 或「未知套餐」。
  - 余额显示为 `321 credits`（单位随文本保留），不是 `$321`、`321 USD` 或裸数字。
  - 窗口标题含 `Command Code · 5 小时` 与 `Command Code · 周`（英文界面为 5-hour / Weekly），两个窗口各有百分比（80%、55%）和重置时间；重置时刻与夹具 RFC3339 一致（按本机时区显示）。
  - 不出现 `bucket_0_0` 之类内部名，也不把缺失字段显示成 `0`。

#### C-04 OPC：三个窗口、无套餐/余额、周期不硬套
- AC：AC-4 · 类型：浏览器 · 优先级：P0 · 执行方式：人工截图；窗口/标签断言可自动化
- 前置：F-OPC 行可见。
- 步骤：
  1. 看行内两个窗口与行头。
  2. 点「详情」→「配额」抽屉，查看全部窗口。
- 预期：
  - 行内最多两个窗口，其中必有 `OpenCode Go · Weekly`（42%）；另一个是 `Rolling` 或 `Monthly` 之一，两个都不显示「5 小时」「30 天」这类未经确认的周期。
  - 抽屉列出 `Rolling` / `Weekly` / `Monthly` 三个窗口，各有标签与重置时间；`Rolling` 显示 0% 剩余并带 rate limited 说明（C-11）。
  - 行头不显示套餐，也不显示「Credit 余额」或 `0 USD`。
  - 不出现 `bucket_0_0`；`rolling`/`monthly` 不被写成 5h/30d。

#### C-05 Ollama：cookie 有窗口，api_key 只有余额
- AC：AC-5 · 类型：浏览器 · 优先级：P0 · 执行方式：人工截图；字段断言可自动化
- 前置：F-OLC、F-OLK 两行可见；无旧快照。
- 步骤：
  1. 分别查看两行的 Credit 余额与窗口区。
  2. 打开两行的抽屉「配额」页。
- 预期：
  - F-OLC：显示 `12.34 USD`；窗口 `Ollama · 5 小时`（60%）与 `Ollama · 周`（80%），带重置时间。
  - F-OLK：显示 `8.75 USD`；窗口区显示空态「上游已应答但未返回配额窗口」，不显示 0% 或假进度条；余额在行和抽屉都可见，没有被隐藏。
  - 两条都不出现 `bucket_0_0`。

#### C-06 缺失字段不写 0、不造占位
- AC：AC-3 / AC-4 / AC-5 · 类型：浏览器 · 优先级：P1 · 执行方式：人工截图；断言可自动化
- 前置：装载 F-MISSING（CMC 无套餐/余额；Ollama api_key 无窗口；OPC 只有 weekly）。
- 步骤：
  1. 逐行查看行头与抽屉。
- 预期：
  - CMC 不显示套餐名、不显示 `0 credits`；无窗口时显示空态而不是 0%。
  - Ollama api_key 不显示 `0 USD`；空态文案是「上游已应答但未返回配额窗口」。
  - OPC 只有 `Weekly` 一个窗口，不补出 Rolling/Monthly，也不显示套餐/余额。
  - 任何缺失的百分比显示为 `--` 或整块省略，不写 0；缺失的重置时间不写成当前时间。

#### C-07 stale 200：缓存读数、原因与观测时间回填
- AC：AC-7 · 类型：浏览器 · 优先级：P1 · 执行方式：人工截图 + 悬停提示
- 前置：F-STALE（CMC 返回 200，`last read at 2026-11-12T02:00:00Z`，原因 `upstream timeout`）。
- 步骤：
  1. 打开额度页，看 F-CMC 行头。
  2. 悬停「缓存读数」标记；打开抽屉「配额」页。
- 预期：
  - 行头出现本地化「缓存读数」标记（英文 Cached reading），提示里说明这是上次成功读取、不是本次刷新结果。
  - 显示 stale 原因 `upstream timeout`；观测时间回填为 `2026-11-12T02:00:00Z` 对应的本机时刻（+08 为 10:00），显示为「约 2 小时前」一类，不得显示「刚刚」或当前时间。
  - 窗口百分比仍显示夹具值，不因 stale 变成 0；stale 标记与「数据过期」状态可同时出现，不冲突。

#### C-08 429 无缓存：刷新失败而非 0
- AC：AC-7 · 类型：浏览器 · 优先级：P1 · 执行方式：人工截图
- 前置：F-429，全新 `OMCPA_DATA_DIR`（无旧快照）；关闭自动刷新。
- 步骤：
  1. 打开额度页，点 CMC 行刷新。
  2. 等待请求结束，查看行与抽屉。
- 预期：
  - 行状态显示「已限流」（`quota.status_rate_limited`）或「刷新失败」，并显示原因 `HTTP 429`；如有重试时间，显示为未来时刻。
  - 没有出现 0% 进度、`0 credits`、伪造窗口或假的重置时间。
  - 抽屉不展示编造的套餐/余额；只展示失败原因或空态，与行一致。

#### C-09 502 无缓存：刷新失败而非旧数据冒充
- AC：AC-7 · 类型：浏览器 · 优先级：P1 · 执行方式：人工截图
- 前置：F-502，全新 `OMCPA_DATA_DIR`；关闭自动刷新。
- 步骤：
  1. 打开额度页，点 CMC 行刷新。
  2. 查看行与抽屉。
- 预期：
  - 显示「刷新失败」+ `HTTP 502`；没有成功观测时间（或明确显示从未成功读取）。
  - 不显示任何数字型额度、0% 或「暂无额度」之类会被误读为 0 的文案。

#### C-10 Ollama 特判收窄：插件凭证余额不被隐藏，自有账号行为不变
- AC：AC-6 · 类型：浏览器 · 优先级：P1 · 执行方式：人工截图
- 前置：F-OLC/F-OLK + F-OWN-OLLAMA（`auth_index:"ollama:fixture-own"`，快照来源非 `settings_balance`）。
- 步骤：
  1. 看 F-OLC/F-OLK 行的 Credit 余额。
  2. 看 OMC 自有 Ollama 账号区（同一页面或对应区块）中 F-OWN-OLLAMA 的余额/额度显示。
- 预期：
  - 两条插件 Ollama 凭证显示 `12.34 USD` / `8.75 USD`，没有被 `verifiedQuotaCredits` 的旧特判隐藏。
  - F-OWN-OLLAMA 沿用现有行为：来源不是 `settings_balance` 的旧快照不显示余额，不因本次改动被放开。
  - 30 分钟 stale 判定的前缀收窄由 C-30 的单元测试验证；本用例只确认界面上的余额显隐没有互相污染。

#### C-11 窗口 note 可见且不冒充窗口名
- AC：AC-4 / AC-7 · 类型：浏览器 · 优先级：P1 · 执行方式：人工截图
- 前置：F-OPC（rolling 窗口 note：`upstream reports this window rate limited`）。
- 步骤：
  1. 打开 F-OPC 抽屉「配额」页，定位 Rolling 窗口。
- 预期：
  - Rolling 窗口标题仍是 `OpenCode Go · Rolling`，不是 note 原文。
  - note 文本在窗口下方或提示中可见，说明该窗口被上游限流；不把 note 当百分比或重置时间。
  - 其余两个窗口不显示这条 note。

#### C-12 抽屉列全量窗口，行只取两个
- AC：AC-4 · 类型：浏览器 · 优先级：P1 · 执行方式：人工截图
- 前置：F-OPC（三个窗口）、F-CMC（两个窗口）。
- 步骤：
  1. 对比 F-OPC 行内窗口数与抽屉窗口数；再看 F-CMC。
- 预期：
  - 行内最多两个窗口；抽屉列出 F-OPC 全部三个、F-CMC 全部两个。
  - 抽屉里的窗口顺序稳定、标题不重复；没有因行内截断而丢失 monthly。

#### C-13 既有七家与 OMC 自有 Ollama 不回归
- AC：AC-10 · 类型：浏览器 · 优先级：P0 · 执行方式：人工点检 + 既有 acceptance 脚本
- 前置：fake CPA 既有凭证夹具（claude/antigravity/codex/xai/kimi/devin/meta）与 OMC 自有 Ollama 账号；三家新夹具同时存在。
- 步骤：
  1. 打开额度页，逐类查看既有七家行：状态、窗口、余额、刷新按钮。
  2. 使用搜索与既有 provider 过滤标签。
  3. 打开一个 Antigravity 行抽屉，确认窗口分组与既有行为一致。
- 预期：
  - 既有七家的显示、新鲜度文案、筛选、刷新行为与改动前一致；Meta 仍只支持手动刷新。
  - OMC 自有 Ollama 账号区仍正常显示，未被三家插件凭证挤掉或改道。
  - 既有 `scripts/acceptance/auth-files.mjs` 的场景断言仍通过（C-31 自动复核）。

#### C-14 英文界面文案完整
- AC：AC-1 / AC-3 / AC-4 / AC-5 / AC-7 · 类型：浏览器 · 优先级：P2 · 执行方式：人工截图
- 前置：同 C-01 + F-STALE；在设置页把界面语言切到 English（`omc.language`）。
- 步骤：
  1. 重新打开额度页，重复 C-01/C-03/C-04/C-05/C-07 的查看点。
- 预期：
  - 过滤标签为 `Ollama` / `Command Code` / `OpenCode Go`；余额为 `321 credits` / `12.34 USD`。
  - 空态为 `Upstream answered without a quota window`；stale 标记为 `Cached reading`，提示为英文。
  - 新文案没有漏译的中文，也没有把 provider 内部 key 直接当标签显示。

### 接口（刷新路径与旧宿主回退）

以下接口用例可脚本化，建议 P2 把 C-16～C-25 合并进 `scripts/fake-cpa.test.mjs` 或 `plugin-quota` 场景；手工执行时按 1.6 的 curl 顺序。

#### C-15 概览返回三家原始 key，且不含凭证秘密
- AC：AC-1 / AC-2 · 类型：接口 · 优先级：P0 · 执行方式：可自动化
- 前置：四张夹具凭证；关闭自动刷新；已登录取得 cookie。
- 步骤：
  1. `GET /api/v1/management/quota`。
  2. 检查三条插件凭证的 `provider`、`type`、`capabilities`、`plan`、`windows`。
  3. 检查响应中是否出现密钥类字段。
- 预期：
  - `provider` 逐字为 `ollama`、`openai-compatible-command code`、`openai-compatible-opencode`，内部空格保留；`type` 同。
  - 三条的 `capabilities.refresh_supported` 均为 `true`。
  - CMC `plan.plan_label="Pro"`、`plan.credits.balance="321 credits"`；Ollama 为 `"12.34 USD"` / `"8.75 USD"`；OPC 无 `plan` 或 `plan` 无套餐/余额。
  - 所有窗口 `label` 不含 `bucket_0_0`；OPC 的 rolling/monthly 不带 `period_hours`。
  - 响应里没有凭证的 api_key、cookie、`base_url`、access token 等秘密字段（对 JSON 文本搜索这些键）。
  - fake CPA `requests` 中额度读取带 OMC 保存的管理 key；OMC 没有向外发起直连（上游域名不出现在 fake CPA 记录之外）。

#### C-16 单条手动刷新打到正确插件且写回观测时间
- AC：AC-9 · 类型：接口 · 优先级：P0 · 执行方式：可自动化
- 前置：新宿主夹具（带 `quota_plugin_id:"ollama-quota"`）；清空 fake CPA `requests`。
- 步骤：
  1. `POST /api/v1/management/quota/refresh`，body `{"auth_index":"fixture-cmc"}`。
  2. 再 `GET /api/v1/management/quota`。
- 预期：
  - 刷新响应中该条 `outcome` 为成功，`quota.observed_at_ms > 0`，套餐/积分/窗口字段与 C-15 一致。
  - fake CPA 恰好收到一次 `POST /v8/management/plugins/ollama-quota/quota`，body 为 `{"auth_index":"fixture-cmc"}`，带管理 key；没有打到别的插件。
  - 概览中该条 `observed_at_ms` 与刷新响应一致，旧快照被新读数替换。

#### C-17 批量刷新逐条执行，单条失败不拖垮其他
- AC：AC-9 · 类型：接口 · 优先级：P0 · 执行方式：可自动化
- 前置：CMC/OPC/Ollama 成功夹具 + 一条 F-502 变体；清空 fake CPA `requests`。
- 步骤：
  1. `POST /api/v1/management/quota/refresh`，body `{"auth_indexes":["fixture-cmc","fixture-opc","fixture-ollama-cookie","fixture-502"]}`。
  2. 检查每个结果与 fake CPA 请求次数。
- 预期：
  - 返回四条结果，顺序与请求一致；成功条目有配额，失败条目有错误与 `outcome`，不因一条失败整体报错。
  - 每条成功凭证各一次插件 quota POST；同一 auth_index 重复出现只刷新一次。
  - 失败条目没有写入 0 值快照（C-25 复核旧快照保留）。

#### C-18 429 + Retry-After：记限流与重试时间，不写快照
- AC：AC-7 / AC-9 · 类型：接口 · 优先级：P1 · 执行方式：可自动化
- 前置：F-429（无缓存）；关闭自动刷新；清空 fake CPA `requests`。
- 步骤：
  1. 刷新 F-429 凭证（`auth-index-e2e-rate-limited`）。
  2. 立即再刷新一次。
- 预期：
  - 首次结果 `outcome` 为限流/失败，`retry_after_ms` 约 120000 或 `retry_at_ms` 为未来时刻；错误信息含 429。
  - `GET /management/quota` 中该条 `last_refresh` 记录本次失败（`outcome=rate_limited`、`http_status=429`、`retry_at_ms` 为未来时刻），`last_success_at_ms` 为 0/缺省（从未成功），无伪造额度；本次失败不写 `observed_at_ms` 快照。（2026-11-13 PM 决策：不改代码，保留所有 provider 现有 observed_at 语义，本子项改断言 `last_success_at_ms`。）
  - 重试窗口内第二次刷新不再向 CPA 发插件 quota POST（fake CPA 次数不增加），返回被退避的结果。
  - 若 P2 按 v8.0.47 真实行为把插件错误统一成 502（`impl-plan.md:67`），429 子项以 502 表现记，OMC 仍须是失败而非 0，并在记录中注明这一宿主差异。

#### C-19 旧宿主回退：`GET /plugins` 能匹配时的路径
- AC：AC-8 · 类型：接口 · 优先级：P1 · 执行方式：可自动化
- 前置：F-OLD-PLUGINS（`/credentials` 无 `quota_plugin_id`；`/plugins` 的 `quota_provider:"ollama"` 可匹配 Ollama）；关闭自动刷新；清空 `requests`。
- 步骤：
  1. 刷新 `fixture-ollama-cookie`。
  2. 刷新 `fixture-cmc`。
- 预期：
  - Ollama 通过 `GET /v8/management/plugins` 的 identifier 匹配到 `ollama-quota`，随后 POST 正确；不调用 `credentials/quota/providers`。
  - CMC 在 `/plugins` 匹配不到时不再报死，转 C-20 的 providers 回退；若该变体未提供 providers 路由，则明确报「找不到提供额度的插件」，不把凭证发给 `ollama-quota` 以外的插件。

#### C-20 旧宿主回退：`credentials/quota/providers` 解析三家
- AC：AC-8 · 类型：接口 · 优先级：P1 · 执行方式：可自动化
- 前置：F-OLD-PROVIDERS（providers 路由返回 `supported_providers` 含三家 key）；关闭自动刷新；清空 `requests`。
- 步骤：
  1. 刷新 `fixture-cmc` 与 `fixture-opc`。
  2. 检查 fake CPA 调用序列。
- 预期：
  - 每条凭证先经历一次 `GET /v8/management/credentials/quota/providers`（失败读取最多一次额外调用），按 `supported_providers` 逐字匹配到 `ollama-quota`。
  - 随后各发一次正确的插件 quota POST，body 的 auth_index 对应该凭证。
  - 不再出现「no enabled plugin provides quota」类错误；匹配过程不修改 CPA 配置。

#### C-21 匹配不到：明确报错，不发给无关插件
- AC：AC-8 · 类型：接口 · 优先级：P1 · 执行方式：可自动化
- 前置：F-UNMATCHED（`quota_provider` 不在 `/plugins` 与 providers 列表）；清空 `requests`。
- 步骤：
  1. 刷新该凭证。
  2. 查看结果与 fake CPA 记录。
- 预期：
  - 刷新结果带明确错误（找不到提供额度的插件 / provider 不受支持），该条 `outcome` 为失败。
  - fake CPA 没有任何插件 quota POST；凭证没有被发给 `ollama-quota` 或任何别的插件。
  - 概览该条无新快照、无 0 值。

#### C-22 `quota_plugin_id` 优先，且不再走发现回退
- AC：AC-8 · 类型：接口 · 优先级：P2 · 执行方式：可自动化
- 前置：F-DECOY（凭证声明 `quota_plugin_id:"ollama-quota"`；`decoy-quota` 也声明支持 CMC）；清空 `requests`。
- 步骤：
  1. 刷新 `fixture-cmc`。
- 预期：
  - 只 POST 到 `ollama-quota`，`decoy-quota` 收到 0 次。
  - 不调用 `credentials/quota/providers`（有 `quota_plugin_id` 时不需要发现）。
  - 结果成功，额度来自 `ollama-quota` 的响应。

#### C-23 概览与页面轮询不触发上游读取
- AC：AC-2 / AC-9 · 类型：接口 · 优先级：P1 · 执行方式：可自动化
- 前置：自动刷新设为 `interval_minutes:0`；清空 `requests`；无手动刷新。
- 步骤：
  1. `GET /api/v1/management/quota` 两次，间隔 60 秒；期间保持额度页打开。
- 预期：
  - fake CPA 只收到 `/v8/management/credentials` 一类读请求；插件 quota POST 次数为 0。
  - 没有直连三家上游的请求；没有因页面轮询写快照（`observed_at_ms` 不变）。

#### C-24 自动刷新轮覆盖三家，Meta 排除
- AC：AC-9 · 类型：接口 · 优先级：P1 · 执行方式：可自动化（约 2 分钟）
- 前置：全新 `OMCPA_DATA_DIR`（无快照）；自动刷新 `{"interval_minutes":5,"refresh_on_reset":false}`；不打开页面；清空 `requests`。
- 步骤：
  1. 启动 OMC，等待 1～2 分钟（服务每分钟一个 tick，首个 tick 在 60 秒）。
  2. 查 fake CPA `requests` 与 `GET /management/quota`。
- 预期：
  - 三家插件凭证各收到一次插件 quota POST（本轮上限 16 条，不会漏掉三家）；`observed_at_ms` 均大于启动时刻。
  - F-META 没有任何自动读取；失败条目按既有规则记录，不中断其他凭证。
  - 5 分钟内的后续 tick 不重复读取（attempt 时间已更新，未到间隔）。

#### C-25 刷新失败保留旧快照，不被 0 覆盖
- AC：AC-7 / AC-9 · 类型：接口 · 优先级：P1 · 执行方式：可自动化
- 前置：先让 F-CMC 成功刷新一次（记录 `observed_at_ms=T1`、余额 `321 credits`）；再把 fake CPA 切成 F-502；关闭自动刷新。
- 步骤：
  1. 刷新 `fixture-cmc`，确认失败。
  2. `GET /management/quota` 对比旧值。
- 预期：
  - 刷新结果失败；概览里余额仍为 `321 credits`，`observed_at_ms` 仍为 T1，窗口值不变。
  - `last_refresh` 记录失败原因（HTTP 502）；界面按 C-09 的失败/stale 语义显示，旧读数有标记，不与新失败混淆。
  - 没有写入 0、没有把观测时间改成当前时间。

### e2e 与自动化套件（fake CPA 三家夹具）

#### C-26 `plugin-quota` 浏览器场景覆盖主流程
- AC：AC-11；连带 AC-1/3/4/5/6/7/8/9 · 类型：e2e · 优先级：P0 · 执行方式：自动化
- 前置：P2 已把 `plugin-quota` 注册进 `scripts/acceptance/scenarios.mjs` 与 `scripts/acceptance/browser-selection.mjs`；夹具装载。
- 步骤：
  1. `pnpm verify:browser -- --scenario plugin-quota`。
- 预期：
  - 场景启动 fake CPA + Go 二进制 + 浏览器，自动断言：三家行/过滤标签、CMC 套餐与 credits、OPC 三窗口与无套餐/余额、Ollama 两形态、缺失字段非 0、stale 标记、429/502 失败文案、旧宿主回退。
  - 退出码 0；失败时产物落在 `tmp/browser-acceptance-failure/`，含截图与请求日志。
  - 场景不改真实 CPA、不读 `private/`。

#### C-27 fake CPA 单测覆盖新路由与夹具
- AC：AC-11 · 类型：e2e · 优先级：P0 · 执行方式：自动化
- 前置：P2 已扩展 `scripts/fake-cpa.mjs` 与 `scripts/fake-cpa.test.mjs`。
- 步骤：
  1. `node --test scripts/fake-cpa.test.mjs`。
- 预期：
  - 覆盖 `/v8/management/plugins/:id/quota` 的请求体校验、200 载荷、429+Retry-After、502、stale 200。
  - 覆盖 `/v8/management/credentials/quota/providers` 与 `/plugins` 的旧宿主组合、无 `quota_plugin_id` 的凭证列表。
  - 覆盖 `requests` 记录内容，能让上层断言插件 POST 次数与 body。

#### C-28 Go 单测覆盖三家载荷投影
- AC：AC-3 / AC-4 / AC-5 / AC-7 · 类型：e2e · 优先级：P0 · 执行方式：自动化
- 前置：P0 已改 `internal/quota/plugin.go` 及相关测试。
- 步骤：
  1. `go test ./internal/quota`。
- 预期：
  - subscription → 套餐名；summary currency → `"12.34 USD"`；summary number/credits → `"321 credits"`；无 summary 不建余额。
  - group + window key → 窗口标签与 `period_hours`（5h/7d 有值，rolling/monthly 为 nil）；description 的窗口名/stale/note/`read at` 投影正确；缺失 remaining 不写 0。
  - stale 200 时观测时间回填、`stale` 标记置位；502/429 返回错误且不产生成功快照。
  - 既有七家投影测试不回归。

#### C-29 Go 单测覆盖插件发现与回退
- AC：AC-8 · 类型：e2e · 优先级：P0 · 执行方式：自动化
- 前置：P0 已改 `internal/quota/plugin.go`、`internal/cpa/management/client_plugins.go` 及测试。
- 步骤：
  1. `go test ./internal/cpa/management ./internal/api`。
- 预期：
  - `quota_plugin_id` 优先；缺失时先 `/plugins`，失败再 `credentials/quota/providers`；每次失败读取最多一次额外调用。
  - `supported_providers` 逐字匹配（含 `openai-compatible-command code` 的空格）；匹配不到返回错误，不向无关插件发送凭证。
  - 自动刷新候选包含插件凭证（`refresh_supported=true`），Meta 排除；分页/上限逻辑照旧。

#### C-30 前端逻辑/组件测试覆盖特判与窗口选择
- AC：AC-4 / AC-6 / AC-10 · 类型：e2e · 优先级：P1 · 执行方式：自动化
- 前置：P1 已补前端测试。
- 步骤：
  1. `pnpm test:logic && pnpm test:components`。
- 预期：
  - `verifiedQuotaCredits`：provider=`ollama` 但 `auth_index` 不以 `ollama:` 开头的插件凭证，credits 正常返回；以 `ollama:` 开头的旧快照仍按来源隐藏。
  - `quotaFreshnessMS`：30 分钟阈值只用于 `auth_index` 前缀 `ollama:`；插件 Ollama 按窗口/默认新鲜度计算。
  - 行内两窗口选择覆盖 OPC（rolling/weekly/monthly，无 period 的窗口不被硬排序成 5h/30d）；空态与缺失字段渲染为 `--`/空态，不是 0。
  - 既有组件测试不回归。

#### C-31 回归总门与既有浏览器场景
- AC：AC-10 / AC-11 · 类型：e2e · 优先级：P1 · 执行方式：自动化
- 前置：P2 全部改动就位。
- 步骤：
  1. `pnpm verify:static`。
  2. `pnpm verify:browser`（既有全部场景）+ `pnpm verify:browser:harness`。
- 预期：
  - Go 测试/vet、type-check、逻辑/组件测试、repository 检查全绿；已知基线红项按项目约定记录，不把既有失败算成本次回归（当前 master 的 `test-oauth-sort` requests-desc 是既有失败，dev 基线以交付时核对为准）。
  - 既有额度页/认证文件场景、插件管理场景全通过；三家新场景不改变其断言。

#### C-32 文档与测试入口更新
- AC：AC-11 · 类型：e2e · 优先级：P1 · 执行方式：人工核对 + `pnpm check-docs`
- 前置：P2 完成。
- 步骤：
  1. 读 `docs/cpamc-parity.md`、`docs/architecture.md`、`docs/testing.md` 中被改动的段落。
  2. 运行 `pnpm check-docs`、`pnpm test:docs`。
- 预期：
  - 三份文档说明三家 provider 的映射、旧宿主回退、stale 语义与测试入口；命令与脚本名可复制执行。
  - `plugin-quota` 场景已登记在测试文档与 `package.json`/场景注册处，`pnpm verify:browser -- --scenario plugin-quota` 是文档中的正式入口。
  - 文档检查通过，没有死链或与实现不符的命令。

## AC 覆盖

| AC | 用例 |
|---|---|
| AC-1 | C-01、C-14、C-15、C-26 |
| AC-2 | C-02、C-15、C-23、C-26 |
| AC-3 | C-03、C-06、C-14、C-26、C-28 |
| AC-4 | C-04、C-06、C-11、C-12、C-14、C-26、C-28、C-30 |
| AC-5 | C-05、C-06、C-14、C-26、C-28 |
| AC-6 | C-10、C-30 |
| AC-7 | C-07、C-08、C-09、C-11、C-18、C-25、C-28 |
| AC-8 | C-19、C-20、C-21、C-22、C-29 |
| AC-9 | C-16、C-17、C-18、C-24、C-25、C-26 |
| AC-10 | C-13、C-30、C-31 |
| AC-11 | C-26、C-27、C-28、C-29、C-30、C-31、C-32 |

## 代码外前提与线上核验

### 代码外前提（不满足时线上看不到，不算产品缺陷）

| 前提 | 现状/判断方式 | 对验收的影响 |
|---|---|---|
| 线上 CPA ≥ v8.0.25（有 `quota_plugin_id`） | 授权环境读 `GET /v8/management/plugins` 的 `metadata.version`，或比对 Dokploy 镜像；`X-CPA-VERSION` 不可靠（11-02 实测 dev/none/unknown） | 低于该版本走 `credentials/quota/providers` 回退；回退路由只在本机 fork c3ce22a2 核实，线上未核实 |
| 插件 v0.3.0 已装进线上 CPA | 拍板卡 N1112-28 未闭环；11-10 检查线上 `registered=false`（倾向 `.so` 被重部署清掉） | 未装时凭证没有 `supports_quota`，三家不出卡；本功能线上不可见，不能据此判失败 |
| 三家凭证是磁盘认证文件 | 授权环境看 `GET /v8/management/credentials` 是否出现三条目标 type；「AI 提供商」里的兼容 key 无 `path`，不会出现在该接口 | 不是磁盘文件时额度页看不到，需先补认证文件 |
| Ollama 窗口依赖 cookie | 凭证形态看 CPA 认证文件：只有 api_key 时只有余额 | api_key 形态不显示 5 小时/周窗口，属预期 |
| 线上部署路径 | dev 合并 ≠ 上线；prebuilt + Dokploy 部署后入口资源哈希变化才生效（当前线上 prebuilt `a3cd9b26`） | 部署前用线上结果判 bug 会误判；先核对资源哈希 |
| 真实 key 验证结论（S9） | 本地验证 CMC×3、OLLAMA_API_KEY；OPC 无 key，结果在验收前给出 | 未出结论时，线上真实数值只能记为观察，不能判「与夹具不符」 |

### 环境划分

- **fake CPA / 本地可跑**：C-01～C-32 全部。旧宿主回退用 F-OLD-* 模拟；429/502 由 fake CPA 路由模拟。
- **必须线上环境**：O-1～O-5 的补充核验（真实插件安装、真实凭证、线上版本与部署）。线上核验只读管理接口与页面，不做写操作、不输真实凭证密钥。

### 线上补充核验（不计入 32 条，结果记为观察）

- **O-1 前置核验**：`GET /v8/management/plugins` 中 `ollama-quota` 的 `metadata.version` 为 v0.3.0 且 `registered=true`；凭证列表三条目标 type 带 `supports_quota:true`。任一不满足，记录前提失败并停，不判产品失败。
- **O-2 真实额度**：线上额度页能看到三家行；CMC 套餐/积分、Ollama 余额与窗口、OPC 三窗口与页面值合理。真实值与上限有关，不要求等于夹具；只判「有值、单位对、缺失不写 0、无 `bucket_0_0`」。
- **O-3 回退路径**：若线上 `GET /v8/management/credentials` 不含 `quota_plugin_id`，刷新三家应走 `credentials/quota/providers` 成功；若含该字段，本项记为不适用（由 C-22 本地覆盖）。
- **O-4 自动刷新观察**：线上等一轮自动刷新（默认 30 分钟，可调至 5 分钟），从服务日志/观测时间确认三家被读取；不强制改线上策略，未观察到记「未验证」。
- **O-5 部署核对**：部署后用入口页资源哈希确认线上已换到新 prebuilt；未换版前不跑 O-2/O-3，避免把旧版本当回归。

## 不测的

- 第四家 provider：需求只三家，不扩。
- CPA 插件自身的采集/解析/退避实现，以及 CPA 管理台页面：插件 v0.3.0 是既定外部依赖，只验 OMC 对载荷的投影。
- 插件安装、上线、Dokploy 部署操作：属于代码外前提，不写进用例步骤。
- OMC 直连三家上游、读取/解密凭证密钥：明确非目标；只用 C-02/C-15 反证没有发生。
- OMC 自有 Ollama 账号采集器改造：本期不改，回归由 C-13/C-31 覆盖。
- 401/403 管理鉴权熔断/退避：方案列为范围外观察（`impl-plan.md` §9.3），本需求不做。
- CMC 积分的 included/purchased/free 分项：插件载荷已合并为总数，OMC 无法拆分，不测。
- OPC `rolling`/`monthly` 的真实周期语义（是否 5 小时/自然月）：未确认前只验标签与重置时间，不判断周期长度。
- 飞书额度报告因新增 provider 快照产生的变化：预期观察，不在本需求 AC。
- demo 数据与 demo 模式的三家卡片：明确不生成（`impl-plan.md` §9.3）。
- 像素级视觉走查、品牌图标版权与动画细节：只验图标存在、可辨识、不是通用占位。
- 真实账号/生产数据的写操作与登录：验收不使用真实凭证。
