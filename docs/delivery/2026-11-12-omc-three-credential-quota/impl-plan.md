---
type: impl-plan
slug: 2026-11-12-omc-three-credential-quota
status: draft
updated: 2026-11-12
prd: 无独立 prd.md；需求原文见 E:/works/Homan-context/runs/msgs/2026-11-12-2026-11-12-T18-0.md，AC 草案见本文
summary: "复用 OMC 已有插件额度通路（GET /credentials → POST /plugins/:id/quota）：后端补 summary 余额、窗口描述/stale 与旧宿主回退，前端补 Command Code / OpenCode Go 的 provider 身份与 ollama 特判收窄；拆 3 个包，P0 后端与 P1 前端可并行，P2 测试基建随后"
---

# 实现计划：OMC 显示 ollama / Command Code / OpenCode Go 三家凭证额度

> 依据：浩文 2026-11-12 傍晚需求（`runs/msgs/2026-11-12-2026-11-12-T18-0.md`）、T18 派发说明、S1/S2/S3 三份调研与 `交付/2026-11-12-cpa-key录入方式.md`。工作区基线 `Newoahil/11-12-t18-s7-claude`（= `origin/dev` `a12ed88a`）。本稿只写方案，不写业务代码、不跑构建测试。

## 1. 背景与目标

CPA 管理台 2026-11-12 出现 403（IP 被临时封禁），浩文进不去看额度，要求把 CPA 插件已经能读到的 ollama / Command Code（CMC）/ OpenCode Go（OPC）三家额度，同样显示在 OMC 的额度页，风格与现有额度页一致；只做这三家，先出方案再动手。

目标：

1. 三家凭证按 OMC 现有“凭证额度行 + 抽屉”的形式展示，不另起页面：套餐名、余额、窗口（5 小时 / 周 / 月 / rolling）、重置时间、状态。
2. 数据与 CPA 插件读到的是同一份：复用 OMC 已有的插件额度通路，不在 OMC 重写采集器，不新增 OMC 直连上游。
3. 三家数据结构不同的地方（CMC 有套餐+积分余额，OPC 只有滚动/周/月窗口，Ollama 有 USD 余额但 api_key 没有窗口）能统一到现有槽位，缺失字段如实显示缺失，不造数、不写 0。
4. 新增/沿用的 CPA 管理接口调用不加重 403 失败计数；把“代码完成但线上看不到”的外部前提写清楚。

非目标：

- 不做第四家 provider；不改 CPA 插件（插件 v0.3.0 已是基准）；不做 CPA 管理台页面；不做 OMC 自有 Ollama 账号采集器的改造；不在 OMC 实现上游直连或凭证明文读取。
- 不新增 OMC 对外接口，不改数据库结构（新字段随现有 `plan_json` / `windows_json` 持久化）。

## 2. 现状与约束（读代码核实）

### 2.1 OMC 侧

| 事实 | 位置 |
|---|---|
| 额度页是 `/oauth-management`；并行读 `management-auth-files`（60s）、`management-quota`（60s）、`management-plugins`、OMC 自有 Ollama 账号与额度 | `web/src/pages/oauthManagement/OAuthManagementPage.tsx:109-158` |
| 凭证行与额度按“唯一非空 `auth_index`”合并；CPA 凭证与 OMC 自有 Ollama 合成行（`auth_index=ollama:<id>`、`runtime_only=true`）并列 | `web/src/pages/oauthManagement/oauthWorkspaceLogic.ts:280-488` |
| 行内额度只有：状态标签、套餐、Credit 余额、重置券/授权、首组两个最短窗口 | `web/src/pages/quota/CompactQuotaView.tsx:52-184` |
| 抽屉有套餐/到期/Credit/额外用量/`plan.balances`/全部窗口/诊断 | `web/src/pages/quota/CredentialQuotaBody.tsx:35-396` |
| 插件凭证识别：非内置 provider + `supports_quota`/`quota_provider`/`quota_probe` 之一 | `internal/quota/plugin.go:35-43` |
| 插件选择：优先 CPA 返回的 `quota_plugin_id`，否则 `GET /plugins` 按 quota identifier → 插件 id → oauth provider 匹配 | `internal/quota/plugin.go:93-108`、`internal/quota/plugin.go:56-85` |
| 取数：`POST /v8/management/plugins/<id>/quota`，body `{"auth_index": …}` | `internal/cpa/management/client_plugins.go:206-223` |
| 插件载荷投影：只解 `groups[].buckets[]`（套用 Antigravity 解析）与 `subscription.{plan,tierName,tierId}`；**`summary` 整段丢弃** | `internal/quota/plugin.go:113-156`、`internal/quota/antigravity.go:32-34,104-204` |
| 读取成功但没有窗口 → `unpublished`（“上游已应答但未返回配额窗口”） | `internal/quota/service.go:441-446` |
| 额度概览只读 CPA `/credentials` + 本地快照，不触发上游读取；CPA 401/403 时概览整体 502，无本地回退 | `internal/api/management_quota.go:802-983`、`internal/api/management_cpa_facade.go` |
| 自动刷新每分钟一拍、每轮 ≤16，按 `CapabilitiesForFile(file).RefreshSupported` 选目标，插件凭证已经在内 | `internal/api/quota_auto_refresh.go:104-167`、`internal/quota/service.go:179-184` |
| 视图层对 `ollama` 的两个特判：无 `raw_signals.ollama_balance_source=settings_balance` 就丢弃 `plan.credits`；`provider=ollama` 固定 30 分钟判 stale | `web/src/utils/quotaCredits.ts:3-8`、`web/src/utils/quotaStatus.ts:25-30` |
| provider 元数据只有 `ollama`，没有 Command Code / OpenCode Go；未知 key 只做首字母大写、图标靠 lobe 目录模糊匹配 | `web/src/components/common/providerMetadata.ts:15-58,79-82`、`web/src/types/providerIconIds.ts:13-39,151-218` |
| 前端没有任何 plugin 凭证渲染用例；fake CPA 没有 `/plugins/:id/quota` 路由 | `internal/quota/plugin_test.go`、`scripts/fake-cpa.mjs:295-330,1012-1075` |

### 2.2 插件 v0.3.0（`Newoahil/cpa-plugin-ollama-quota` tag `v0.3.0`，commit `a72fa4b`）

| 事实 | 位置 |
|---|---|
| 一个插件实例服务三个 provider key，逐字：`ollama`、`openai-compatible-command code`（中间**有空格**）、`openai-compatible-opencode` | `v0.3.0:internal/quotaplugin/providers.go:15-24` |
| 能力声明 `quota_provider: true`；`QuotaProvider.Identifier()` = `ollama`；`DescribeQuota.SupportedProviders` 返回上述三个 key | `v0.3.0:internal/quotaplugin/rpc.go:86-113`、`provider.go:92-112` |
| 返回结构即 CPA `pluginapi.QuotaFetchResponse`：`subscription{plan,tierName,tierId}`、`summary[]{key,label,value,format,currency,unit}`、`groups[]{displayName,buckets[]{window,remainingFraction,resetTime,description}}` | `v0.3.0:internal/quotaplugin/fetch.go:410-467`；CPA v8.0.47 `sdk/pluginapi/types.go:1737-1857` |
| 每个凭证每次 POST 实时读上游（无 TTL 缓存）；429 带 Retry-After（无头时默认退避），失败但有旧读数时回 200 + stale 标记，从未成功则回错误（CPA 转 502） | `v0.3.0:internal/quotaplugin/fetch.go:120-230` |
| 窗口只发 `window` 名（`5h`/`7d`/`rolling`/`weekly`/`monthly`），显示文案在 `description` 第一段；`description` 还带 `read at <ts>`，stale 时为 `stale · last read at <ts> · <reason>`，另有窗口 note | `v0.3.0:internal/quotaplugin/fetch.go:480-501` |
| Ollama 余额 = USD（`format=currency,currency=USD`）；CMC 余额 = 积分（`format=number,unit=credits`）；OPC 无余额；CMC 有 plan，OPC 无 | `v0.3.0:internal/ollama/balance.go`、`internal/cmc/parser.go`、`internal/opc/parser.go` |

### 2.3 CPA 宿主

| 事实 | 位置 |
|---|---|
| v8.0.47：`GET /v8/management/credentials` 对磁盘凭证写 `supports_quota: true`、`quota_provider`（= 凭证自己的 provider key）、`quota_plugin_id`（CPA 选中的插件 id，v8.0.25+） | v8.0.47 `internal/api/handlers/management/auth_files.go:689-695` |
| v8.0.47：插件额度路由 `POST /v8/management/plugins/:id/quota`，200 原样返回插件 JSON，插件报错转 502，需管理 key | v8.0.47 `internal/api/handlers/management/plugin_quota.go:251-299`、`internal/api/server_management_v8.go:66-68` |
| v8.0.47 + 本机 fork 都有 `GET /v8/management/credentials/quota/providers`，返回 `{providers:[{plugin_id,provider,display_name,supported_providers,supports_reset}]}` | v8.0.47 `server_management_v8.go:43`；本机 `E:/works/CLIProxyAPI@c3ce22a2` `internal/api/server_management_v8.go:43`、`internal/pluginhost/quota_provider.go:32-99` |
| 本机 fork（约 v8.0.4）：凭证列表有 `supports_quota`/`quota_provider`，**没有 `quota_plugin_id`**；`GET /plugins` 的 `quota_provider` 只有 identifier（`ollama`） | 本机 fork `internal/api/handlers/management/auth_files.go:681-693`、`internal/pluginhost/snapshot.go:70-104` |
| 管理鉴权失败：同一 ClientIP 连续 5 次失败封 30 分钟；只计缺 key/错 key 的鉴权失败（401），封禁期的 403 不再累加；成功调用清零；插件 502 不计 | 本机 fork `internal/api/handlers/management/handler.go:299-398`；v8.0.47 同逻辑 |
| 凭证 `type` 转 `provider`：`strings.ToLower(strings.TrimSpace(type))`，内部空格不折叠 | v8.0.47 `internal/watcher/synthesizer/file.go:109-110`；插件 `providers.go` 注释同述 |

### 2.4 已有工作与约束

- 没有开着的 PR/Issue、分支或验收在做“OMC 显示三家额度”；#86（Ollama 美元额度）已由 #87/#98/#99 完成，作用于 OMC 自有账号，不覆盖 CPA 插件凭证；#82（StepFun）已关。CMC/OPC 的图标与 provider 元数据没人做过（S3）。
- dev 基线 `a12ed88a`；master 停在 `739ee409`（不含 11-06 后能力）；线上 OMC 跑在 `deploy/prebuilt` `a3cd9b26` 档——**合进 dev 不等于上线**。
- 插件 v0.3.0 已合 dev/tag，但“装进线上 CPA”的拍板卡 N1112-28 仍未闭环，线上 11-10 检查时 `registered=false`。
- 三家凭证必须录成 CPA **磁盘认证文件**（type 逐字、`api_key`，后两家加 `base_url`）；“AI 提供商”里的兼容 key 没有 `path`，不进凭证列表、不出额度卡。
- Ollama 只有 Cookie 路径才有 5 小时/周窗口；只填 `api_key` 时插件只有余额。

## 3. 数据从哪来：逐跳说明

```
运营者                CPA                                    插件(ollama-quota v0.3.0)          上游
  │  上传 auths/*.json  │  GET /v8/management/credentials        │                              │
  │───────────────────>│  POST /v8/management/plugins/:id/quota │  FetchQuota(credential)      │
  │                    │───────────────────────────────────────>│─────────────────────────────>│
  │                    │  200 {subscription,summary,groups}     │  200/429/错误                 │
  │                    │<───────────────────────────────────────│<─────────────────────────────│
OMC                      │                                        │
  │ GET /v8/management/credentials （管理 key；每分钟）             │
  │ POST /v8/management/plugins/<id>/quota {"auth_index": …}      │
  │        （管理 key；自动刷新每凭证约 30 分钟一次 / 手动刷新）        │
  ▼                                                               │
quota_snapshots(plan_json/windows_json) → GET /api/v1/management/quota → 浏览器 60s 轮询
```

1. **运营者 → CPA**：在 CPA「认证文件」上传 JSON（文件名必须 `.json`），`type` 为 `ollama` / `openai-compatible-command code` / `openai-compatible-opencode`，填 `api_key`（后两家加 `base_url`）。CPA 用管理 key 保护该上传接口；这一步不经过 OMC。
2. **OMC → CPA 凭证列表**：OMC 用库里加密保存的 CPA 管理 key，带 `Authorization: Bearer` 调 `GET /v8/management/credentials`。CPA 按 provider 查插件主机，给磁盘凭证写 `supports_quota`/`quota_provider`，v8.0.25+ 再写 `quota_plugin_id`。失败：401/403 → 概览 502 `cpa_authentication_failed`；网络/5xx → 本轮刷新失败，页面保留旧快照。
3. **OMC → CPA 插件额度**：仅刷新路径（自动刷新轮、手动刷新、MCP `quota_refresh`）对每张到期凭证发 `POST /v8/management/plugins/<quota_plugin_id>/quota`，body 只有 `{"auth_index": "<CPA 不透明索引>"}`。CPA 校验管理 key 后转给插件主机；404=插件/额度提供者不在，502=插件读到上游失败，200=插件载荷。
4. **CPA → 插件**：进程内调用 `FetchQuota`，把该凭证的 `Metadata`/`Attributes`（磁盘文件里的 `api_key`、`base_url`、`cookie`）交给插件；插件不读 CPA 配置库，OMC 进程也从不接触这些密钥。
5. **插件 → 上游**：CMC 以 `Bearer api_key` 调 `GET https://api.commandcode.ai/alpha/billing/credits`，另尽力读 `GET /alpha/billing/subscriptions` 取套餐名（凭证 `base_url` 若带 `/provider/v1`，插件先剥到账户 API 根）；OPC `GET https://opencode.ai/zen/go/v1/usage`（Bearer api_key）；Ollama 有 api_key 时 `GET https://ollama.com/api/balance`，否则用 cookie 抓 `https://ollama.com/settings` 的 HTML。429 由插件按 Retry-After/默认值退避；有旧读数时回 200 + stale 标记，无旧读数时回错误。
6. **OMC 存储**：`RefreshCredentialQuota` 把载荷投影成 `NormalizedQuota`（窗口 + 套餐 + 余额），成功才写 `quota_snapshots`（`windows_json`、`plan_json`；每凭证保留最近 50 条）。失败/`stale` 不覆盖旧快照。
7. **OMC API → 浏览器**：`GET /api/v1/management/quota` 读凭证列表 + 最新快照 + 冷却/刷新尝试，返回 `QuotaItemDTO`；页面 60 秒轮询，**这条读不触发任何上游读取**。
8. **浏览器**：按 `auth_index` 把额度并入凭证行；行内 `CompactQuotaView`，抽屉 `CredentialQuotaBody`。

**为什么不新增直连**：三家密钥只存在于 CPA 磁盘认证文件里，OMC 从不解密或读取凭证内容；插件已经实现三家采集、缓存、429 退避和 stale 语义。若 OMC 自己再写采集，需要新增密钥托管/解密、三套解析器与失败语义，还会与 CPA 页面出现两份可能不一致的数。现有 `internal/ollama` 是 OMC 自己管理的 Ollama 账号（cookie/API key 存在 OMC 库，monitor-only），与 CPA 凭证生命周期解耦，不能拿来复用（`docs/ollama-monitoring.md:19-22`）。因此本方案**不新增直连、不新增采集器**，只补 OMC 对插件载荷的投影与展示。

## 4. 方案

### 4.1 后端改动（逐文件）

1. `internal/quota/antigravity.go`
   - 保留现有 `ParseAntigravityUsage` 行为不变（内置 Antigravity 读数的回归面为零），把公共循环抽成内部函数；新增 `ParsePluginQuotaWindows(raw, nowMS, serverOffsetMS) (windows []QuotaWindow, observedAtMS int64, stale bool, err error)` 供插件路径使用。
   - 插件形状下：
     - `bucket.displayName` 为空时，用 `description` 的第一段（` · ` 分隔）作为窗口名，解决插件只发 `window`、不发显示名导致 `Ollama · bucket_0_0` 的问题；
     - `description` 里出现 `stale` 段时置 `QuotaWindow.Stale=true`，其余非 `read at …` 段（如 “upstream reports this window rate limited”、stale 原因）收进 `QuotaWindow.Note`；
     - `description` 的 `read at <RFC3339>` / `last read at <RFC3339>` 解析为观测时间，用于 stale 时回填 `observed_at_ms`；
     - `resetTime` 不是 RFC3339 时不再丢弃，落 `ResetLabel`（Ollama 页面相对文案如 “Resets in 3 hours” 能原样显示）；
     - `window` 识别：`5h`/`7d`/`weekly` 保持 5/168 小时；`rolling`、`monthly` **不猜周期**（`PeriodHours` 置 nil），标签由 description 提供（`Rolling` / `Monthly`）。
2. `internal/quota/types.go`
   - `QuotaWindow` 增加 `Stale bool json:"stale,omitempty"`、`Note string json:"note,omitempty"`（随 `windows_json` 持久化，旧快照天然兼容）。
   - `QuotaCredits` 增加 `Stale bool json:"stale,omitempty"`（随 `plan_json` 持久化）。
3. `internal/quota/plugin.go`
   - `parsePluginQuotaPlan` 扩展：除 `subscription` 外解析 `summary[]`；取 `key=="balance"` 的第一条，按 `format` 落 `Plan.Credits.Balance`：
     - `format=="currency" && currency!=""` → `"<金额> <CURRENCY>"`（Ollama：`12.34 USD`）；金额保留 2 位、去掉多余的 0；
     - 否则 → `"<金额>"` +（`unit` 非空时）`" <unit>"`（CMC：`321 credits`）；
     - `metric.label` 含 `stale` → `Credits.Stale=true`；
     - 只有余额、没有套餐名时仍返回 plan（`PlanType/Tier="unknown"`、`PlanLabel=""`），否则 `planFromSnapshot` 会丢掉余额。
   - `fetchPluginQuota` 改用 `ParsePluginQuotaWindows`，返回 `(windows, plan, stale, observedAtMS, err)`。
   - `pluginQuotaTarget` 增加旧宿主回退：`quota_plugin_id` 为空 → 先走现有 `GET /plugins` 匹配；匹配不到/列表读取失败 → `GET /credentials/quota/providers`，在 `supported_providers` 里找目标 key 对应条目取 `plugin_id`（只选 `QuotaProvider != ""` 的已注册插件）；两条都失败才报错。回退不会把凭证发给无关插件。
4. `internal/quota/service.go`
   - `CPAClient` 接口增加 `QuotaProviders(ctx) ([]management.QuotaProviderInfo, error)`。
   - 插件分支接收 stale/observedAt：插件报 stale 且带读取时间时，把 `result.ObservedAtMS` 回填为真实读取时间（不把旧数当新读数）；窗口/余额上的 stale 标记随结果持久化。
5. `internal/cpa/management/client_plugins.go`
   - 新增 `QuotaProviderInfo` 类型与 `Client.QuotaProviders`：`GET /credentials/quota/providers`，兼容 `{providers:[…]}` 与裸数组两种形状。
6. 测试：`plugin_test.go` 增加三家真实形状夹具（见 §6）、summary/plan、stale、回退解析（`quota_plugin_id` 优先不列插件；回退命中；无关插件不收到 auth index）；`antigravity_test.go` 增加插件形状的 description/重置文案/rolling/monthly；`service_test.go` 的 mock 补 `QuotaProviders`。新增 `internal/cpa/management/client_plugins_test.go` 覆盖新路由的传输与响应解码。
7. 文档：`docs/cpamc-parity.md` 的 Plugin quota 行补“summary→Credits、description→窗口名/stale、旧宿主回退”；`docs/architecture.md` 的 “Quota resolution” 节补同一句；`docs/testing.md` 的 plugin_test 归属补新用例。

不需要改：`internal/api/management_quota.go`（`QuotaItemDTO` 内嵌 `NormalizedQuota`，`plan_json`/`windows_json` 原样往返）；数据库迁移；`internal/ollama/*`；CPA/插件仓库。

### 4.2 前端改动（逐文件）

1. `web/src/components/common/providerMetadata.ts`
   - 新增三条（键必须逐字、含空格）：
     - `'openai-compatible-command code'` → `{ id: 'commandcode', label: 'Command Code', iconId: 'CommandCode' }`
     - `'openai-compatible-opencode'` → `{ id: 'opencode', label: 'OpenCode Go', iconId: 'OpenCode' }`
     - 别名 `commandcode`、`opencode` → 同上（`canonicalDisplayProviderKey` 会把展示键收敛到 metadata.id，后续再查元数据时不能落空）。
2. `web/src/types/providerIconIds.ts`
   - `PROVIDER_ICON_IDS` 增加 `commandcode: 'CommandCode'`、`opencode: 'OpenCode'`、`ollama: 'Ollama'`，以及两个逐字 raw key（用量事件行按 provider 直接查这张表）。图标 id 已核实存在于 `web/src/generated/lobeIconCatalog.json`（`CommandCode` / `OpenCode` / `Ollama`）。
3. `web/src/utils/quotaCredits.ts`
   - 收窄 ollama 特判：只在 **OMC 自有 Ollama 行**（`auth_index` 以 `ollama:` 开头）且没有 `raw_signals.ollama_balance_source=settings_balance` 时丢弃 `plan.credits`；CPA 插件 ollama 凭证（auth_index 是 CPA 不透明索引）必须显示余额。
4. `web/src/utils/quotaStatus.ts`
   - `quotaFreshnessMS` 的 30 分钟新鲜度同样只作用于 `auth_index` 以 `ollama:` 开头的行；插件 ollama 走通用窗口算法（有窗口按最短周期 1/4，最低 2 小时；无窗口回落 4 小时）。
5. `web/src/types/quota.ts`
   - `QuotaWindow` 加 `stale?: boolean`、`note?: string`；`QuotaCredits` 加 `stale?: boolean`。
6. `web/src/pages/quota/CompactQuotaView.tsx`
   - 行头在 `credits.stale` 或任一 `window.stale` 时显示本地化“缓存读数”标记（tooltip 带 note）；其余显示逻辑不变（余额仍走 `verifiedQuotaCredits` 的 “Credit 余额” 槽）。
7. `web/src/pages/quota/CredentialQuotaBody.tsx` + `QuotaProgressBar.tsx`
   - 抽屉状态行加同一标记；窗口行显示 stale 标记和 `note`（有 note 时作为次行/tooltip），让插件 stale 的原因不被吞掉。
8. `web/src/i18n/index.tsx`
   - 新增 `quota.plugin_stale`（如“缓存读数 / Cached reading”）与 `quota.plugin_stale_hint`（“插件返回的是上次成功读取，不是本次刷新结果”），中英各一条。
9. 测试：
   - `web/tests/credential-quota.component.test.tsx`：更新第 159 行的 ollama 新鲜度断言（改为 `auth_index: 'ollama:x'`），新增“插件 ollama 余额显示、OMC 自有 ollama 无信号仍隐藏”的用例。
   - 新增 `web/tests/plugin-quota.component.test.tsx`：三家 `QuotaItem` 夹具渲染行/抽屉——provider 标签、CMC credits、OPC 三个窗口、Ollama api_key 只有余额、stale 标记与 note。
   - `scripts/test-oauth-workspace.ts`：补 `canonicalDisplayProviderKey` 对两个 raw key 的收敛与 filter 选项断言。

不需要改：`OAuthManagementPage.tsx`（合并逻辑按 auth_index 已通用）、`OAuthCredentialRecord.tsx`/凭证面板（`displayProvider` 与 `credentialProviderIconId` 都从 provider 元数据取，新增条目后自动生效）、`oauthWorkspaceLogic.ts` 的合并逻辑、`providerFilterTabs` 的排序规则（新增 provider 按字母序出现在固定七家之后，这与现有未知插件 provider 的行为一致）、`OllamaCreditsView`（只服务 OMC 自有账号）。

### 4.3 三个 provider key 的映射（空格不能改）

| CPA 凭证 `type`（逐字） | OMC `DetectProvider` | 前端展示键 | 标签 | 图标 | CPA 插件识别 key |
|---|---|---|---|---|---|
| `ollama` | `ollama` | `ollama` | Ollama | `Ollama` | `ollama`（QuotaProvider.Identifier） |
| `openai-compatible-command code` | 原样返回（default 分支） | `commandcode` | Command Code | `CommandCode` | `openai-compatible-command code`（SupportedProviders） |
| `openai-compatible-opencode` | 原样返回（default 分支） | `opencode` | OpenCode Go | `OpenCode` | `openai-compatible-opencode`（SupportedProviders） |

约束：CPA 侧只做 `ToLower+TrimSpace`，**不折叠内部空格**；OMC 的 `DetectProvider` 同样只 lower/trim；插件键是逐字常量。任何把空格改成连字符的“规范化”都会让凭证匹配不到插件，三处都必须保持原样。

### 4.4 三家字段映射表（插件载荷 → OMC 展示槽位）

| 插件载荷字段（v0.3.0 实际发出的形状） | 来源/provider | OMC 槽位 | 展示方式 | 缺失/异常时 |
|---|---|---|---|---|
| `subscription.plan` / `tierName` / `tierId` | CMC（plan 有值）、Ollama（多为空）、OPC（无） | `QuotaPlan.PlanLabel`（首个非空）+ `PlanType/Tier="unknown"` | 行/抽屉“套餐” | 全空则不建套餐名，不写“未知套餐”占位 |
| `summary[]` 中 `key="balance"`，`format="currency", currency="USD", value` | Ollama（api_key 或 cookie） | `QuotaPlan.Credits.Balance = "<金额> USD"` | 行/抽屉“Credit 余额”（现有文本槽） | 无 metric → 不显示余额，不写 0 |
| `summary[]` 中 `key="balance"`，`format="number", unit="credits", value` | CMC | `QuotaPlan.Credits.Balance = "<金额> credits"` | 同上，单位随文本保留，不冒充美元 | 同上 |
| `summary[].label` 含 `stale` | 插件缓存读数 | `QuotaCredits.Stale=true` | “缓存读数”标记 | — |
| `groups[].displayName` | `Ollama` / `Command Code` / `OpenCode Go` | 窗口 `Label` 前缀（`<组名> · <周期>`） | 行内窗口标题、抽屉分组 | 组名为空回落 `Group N`（现有逻辑） |
| `buckets[].window` | `5h`/`7d`（Ollama、CMC）、`rolling`/`weekly`/`monthly`（OPC） | `PeriodHours`（5/168）；`rolling`/`monthly` 置 nil，不猜周期 | 行按最短周期取两个窗口；抽屉全列 | 未识别周期原样显示标签 |
| `buckets[].remainingFraction` | 0..1 剩余比例 | `RemainingPercent` / `UsedPercent`（×100，夹 0..100） | 进度条 + 百分比 | 缺失显示 `--`，不写 0 |
| `buckets[].resetTime` | RFC3339 或页面原文 | RFC3339 → `ResetAtMS`+`ResetLabel`；原文 → `ResetLabel` | “09/24 20:11 · 4 hours left”或原文 | 两者都无 → “边界未知” |
| `buckets[].description` | 插件为每个窗口拼的描述 | 无 `displayName` 时作窗口名；`stale` → `QuotaWindow.Stale`；其余说明 → `Note`；`read at <ts>` → stale 时回填 `observed_at_ms` | 窗口标签/“缓存读数”/note | 无 description → 现有行为 |
| 插件 `stale` 200 | 上次成功读数 | `Stale` 标记 + 回填观测时间 | 行头/抽屉“缓存读数”，note 说明原因 | — |
| 插件 502 / 429（无缓存） | 上游失败 | 刷新失败记录 + `last_refresh` 错误，保留旧快照 | 行/抽屉错误文案 | 不造数、不写 0 |
| OPC 无 `summary` / 无 `subscription` | OPC | 只有窗口，无余额/套餐 | 正常显示窗口 | 不显示空余额 |

说明：货币与积分数值都走 `plan.credits` 文本槽，而不是 `plan.balances`。`QuotaBalance` 是 `limit_usd/used_usd/remaining_usd` 的美元专用结构，用它装 CMC 积分会把积分误标成美元；Ollama 的 USD 若放进去，抽屉会渲染出上游从未给出的 `used/limit`（显示成 `— of — spent`）。文本槽让单位和币种随值走，行内与抽屉都能直接显示，不需要为三家再发明一套金额组件。

### 4.5 旧宿主回退：代价与风险

- 现状：宿主 ≥ v8.0.25 直接给 `quota_plugin_id`，OMC 现有代码零改动即可解析；本机 fork c3ce22a2（本地验证环境）只给 `supports_quota`/`quota_provider`，现有回退只按 `GET /plugins` 的 `quota_provider`（只有 `ollama`）匹配，**CMC/OPC 会报 “no enabled plugin provides quota”**。
- 方案：新增 `GET /credentials/quota/providers` 回退（该路由在本机 fork 与 v8.0.47 都存在）。只在 `quota_plugin_id` 为空且 `GET /plugins` 匹配失败时调用；每次失败读取最多一次额外管理调用（自动刷新下每凭证约 30 分钟一次，量级远小于现有每分钟 `/credentials`）。
- 代价：新增一个 client 方法 + `CPAClient` 接口一处 + mock；风险是线上若版本更旧、该路由不存在，回退失败并如实报错（此时通常也没有插件额度能力，本来就不出卡）。
- 不选“把 `GET /plugins` 的匹配放宽到模糊子串”：会把凭证交给无关插件，违反现有安全约束（`plugin.go` 注释与测试 `TestRefreshCredentialQuotaRejectsUnrelatedPlugin`）。

### 4.6 403 的教训与结论

本方案新增/沿用的 CPA 管理接口：

| 调用 | 触发 | 鉴权 | 失败计数影响 |
|---|---|---|---|
| `GET /v8/management/credentials`（已有） | 页面概览 60s、自动刷新每分钟、手动刷新目标选择 | OMC 库中加密的管理 key，`Authorization: Bearer` | 成功清零；只有缺 key/错 key 的鉴权失败（401）才计数 |
| `POST /v8/management/plugins/:id/quota`（已有路径，三家首次真正使用） | 到期凭证的自动刷新（约 30 分钟/凭证）、手动刷新 | 同上 | 插件 502/429 是应用层结果，不进入鉴权失败计数 |
| `GET /v8/management/credentials/quota/providers`（新增回退） | 仅旧宿主、且 `quota_plugin_id` 为空时，每凭证读取至多一次 | 同上 | 成功调用，清零；失败属于 401/403 时和现有 `/credentials` 同性质 |

结论：**这个方案不会加重 CPA 的失败计数**。CPA 的 5 次/30 分钟封禁只统计缺 key/错 key 的管理鉴权失败（本机 fork `handler.go:299-398`，v8.0.47 同逻辑），本方案所有调用都复用现有正确 key；封禁期的 403 不再累加，插件取数失败（502）也不计。真正会触发封禁的是 key 错误或同 IP 的失败登录，这与本功能无关。OMC 目前没有 401/403 熔断，属于既有缺口；加熔断/退避需要跨所有管理调用统一设计，**列为范围外观察**，不在本需求里做。

### 4.7 复用了什么

- 后端：`isPluginQuotaFile` / `pluginQuotaTarget` / `fetchPluginQuota` 整条通路、`CapabilitiesForFile`（自动刷新/手动刷新/能力展示三处一致）、`quota_snapshots` 存储、`buildQuotaOverview`、`plan_json`/`windows_json`。
- 前端：`buildOAuthWorkspaceProjection` 的 auth_index 合并、`CompactQuotaView`/`CredentialQuotaBody`/`QuotaProgressBar`、`plan.credits` 与 `plan.balances` 渲染槽、provider metadata/icon 机制、`verifiedQuotaCredits`。
- 测试基建：`internal/quota/plugin_test.go` 的 mock 与夹具风格、`scripts/test-oauth-workspace.ts`、`web/tests/credential-quota.component.test.tsx`、`scripts/fake-cpa.mjs`、`scripts/browser-acceptance.mjs`。
- `quotaCredits.ts` / `quotaStatus.ts` 的 ollama 特判不删除：改成只约束 OMC 自有账号（`auth_index` 前缀），插件 ollama 的余额和新鲜度走通用逻辑。

## 5. 工作包

> 文件归属互不重叠；P0 与 P1 依本文件 §6 的契约同时开工，P2 依赖两者合并后跑通端到端。

| 包 | 内容 | 对应 AC | 依赖 | 文件归属 | 预计 |
|---|---|---|---|---|---|
| P0 | 后端：插件载荷投影扩展（summary→Credits、description→窗口名/stale/note/观测时间、重置文案）+ 旧宿主 `quota providers` 回退 + Go 单测 + 文档 | AC-2, AC-3, AC-4, AC-5, AC-7, AC-8, AC-9 | — | `internal/quota/plugin.go`、`internal/quota/antigravity.go`、`internal/quota/types.go`、`internal/quota/service.go`、`internal/quota/plugin_test.go`、`internal/quota/antigravity_test.go`、`internal/quota/service_test.go`、`internal/cpa/management/client_plugins.go`、`internal/cpa/management/client_plugins_test.go`、`docs/cpamc-parity.md`、`docs/architecture.md`、`docs/testing.md` | 360 分钟 |
| P1 | 前端：Command Code / OpenCode Go 的 provider 元数据与图标、ollama 特判收窄、余额/stale/note 渲染、i18n、逻辑与组件测试 | AC-1, AC-3, AC-4, AC-5, AC-6, AC-7, AC-10 | — | `web/src/components/common/providerMetadata.ts`、`web/src/types/providerIconIds.ts`、`web/src/utils/quotaCredits.ts`、`web/src/utils/quotaStatus.ts`、`web/src/types/quota.ts`、`web/src/pages/quota/CompactQuotaView.tsx`、`web/src/pages/quota/CredentialQuotaBody.tsx`、`web/src/pages/quota/QuotaProgressBar.tsx`、`web/src/i18n/index.tsx`、`web/tests/credential-quota.component.test.tsx`、`web/tests/plugin-quota.component.test.tsx`、`scripts/test-oauth-workspace.ts` | 240 分钟 |
| P2 | 测试基建与端到端：fake CPA 三家磁盘凭证 + 额度插件条目 + `POST /plugins/:id/quota` + `GET /credentials/quota/providers`；浏览器 e2e（含刷新路径断言）；fake-cpa 单测 | AC-2, AC-9, AC-10, AC-11 | P0, P1 | `scripts/fake-cpa.mjs`、`scripts/fake-cpa.test.mjs`、`scripts/browser-acceptance.mjs`、`scripts/acceptance/plugin-quota.mjs` | 180 分钟 |

并行关系：P0、P1 无文件重叠，可同时开发；P2 要等两者合入后跑通真实端到端（预期路径：P0/P1 并行 → P2 → 合并）。关键路径 P0 → P2（约 9 小时）。

## 6. 接口约定

### 6.1 插件载荷契约（以 v0.3.0 实际发出为准）

Ollama（cookie 路径有窗口；api_key 路径只有 summary）：

```json
{
  "subscription": {"plan": ""},
  "summary": [{"key": "balance", "label": "Balance", "value": 12.34, "format": "currency", "currency": "USD"}],
  "groups": [{"displayName": "Ollama", "buckets": [
    {"window": "5h", "remainingFraction": 0.6, "resetTime": "2026-11-12T13:00:00Z", "description": "5-hour session · read at 2026-11-12T08:00:00Z"},
    {"window": "7d", "remainingFraction": 0.8, "resetTime": "2026-11-15T00:00:00Z", "description": "Weekly · read at 2026-11-12T08:00:00Z"}
  ]}]
}
```

Command Code：

```json
{
  "subscription": {"plan": "Pro"},
  "summary": [{"key": "balance", "label": "Balance", "value": 321, "format": "number", "unit": "credits"}],
  "groups": [{"displayName": "Command Code", "buckets": [
    {"window": "5h", "remainingFraction": 0.8, "resetTime": "2026-11-12T13:00:00Z", "description": "5-hour session · read at 2026-11-12T08:00:00Z"},
    {"window": "7d", "remainingFraction": 0.55, "resetTime": "2026-11-15T00:00:00Z", "description": "Weekly · read at 2026-11-12T08:00:00Z"}
  ]}]
}
```

OpenCode Go（无套餐、无余额）：

```json
{
  "groups": [{"displayName": "OpenCode Go", "buckets": [
    {"window": "rolling", "remainingFraction": 0.0, "resetTime": "2026-11-12T09:00:00Z", "description": "Rolling · read at 2026-11-12T08:00:00Z · upstream reports this window rate limited"},
    {"window": "weekly", "remainingFraction": 0.42, "resetTime": "2026-11-15T00:00:00Z", "description": "Weekly · read at 2026-11-12T08:00:00Z"},
    {"window": "monthly", "remainingFraction": 0.21, "resetTime": "2026-12-01T00:00:00Z", "description": "Monthly · read at 2026-11-12T08:00:00Z"}
  ]}]
}
```

（以上字段名/语义按 `v0.3.0:internal/quotaplugin/fetch.go:410-501` 推导；数值仅为形状示例，不是线上样本。）

### 6.2 后端类型与函数

```go
// internal/quota/types.go
type QuotaWindow struct {
    // …现有字段不变…
    Stale bool   `json:"stale,omitempty"`
    Note  string `json:"note,omitempty"`
}
type QuotaCredits struct {
    Balance     string `json:"balance,omitempty"`
    IsUnlimited bool   `json:"unlimited,omitempty"`
    Stale       bool   `json:"stale,omitempty"`
}

// internal/quota/antigravity.go
// 插件形状的解析；stale=true 表示插件返回的是上次成功读数。
// observedAtMS 来自 description 的 read at；无则 0。
func ParsePluginQuotaWindows(raw []byte, nowMS int64, serverOffsetMS int64) (windows []QuotaWindow, observedAtMS int64, stale bool, err error)

// internal/quota/plugin.go
// 契约：Plan.Credits.Balance 是带单位的文本，前端原样展示在“Credit 余额”槽。
func parsePluginQuotaPlan(raw []byte) *QuotaPlan

// internal/cpa/management/client_plugins.go
type QuotaProviderInfo struct {
    PluginID           string   `json:"plugin_id"`
    Provider           string   `json:"provider"`
    DisplayName        string   `json:"display_name"`
    SupportedProviders []string `json:"supported_providers"`
    SupportsReset      bool     `json:"supports_reset"`
}
func (c *Client) QuotaProviders(ctx context.Context) ([]QuotaProviderInfo, error)

// internal/quota/service.go
type CPAClient interface {
    // …现有方法…
    QuotaProviders(ctx context.Context) ([]management.QuotaProviderInfo, error)
}
```

### 6.3 前端类型

```ts
// web/src/types/quota.ts
interface QuotaWindow { /* … */ stale?: boolean; note?: string }
interface QuotaCredits { balance?: string; unlimited?: boolean; stale?: boolean }
```

Provider 元数据（`providerMetadata.ts`）：`'openai-compatible-command code' → {id:'commandcode', label:'Command Code', iconId:'CommandCode'}`；`'openai-compatible-opencode' → {id:'opencode', label:'OpenCode Go', iconId:'OpenCode'}`。

## 7. 测试与验收方法

### 7.1 P0（后端）

- `go test ./internal/quota ./internal/cpa/management ./internal/api`：三家载荷夹具、summary（currency/credits/零值/缺失）、stale 标记与回填时间、description 标签、重置原文、回退解析（有 `quota_plugin_id` 时不再列插件；无时命中 providers 路由；无匹配/无关插件不发请求）、Antigravity 既有用例回归。
- `go vet ./...`。本机无 Go，按既有配方用 docker（`golang:1.27.1` + `omc-go-mod`/`omc-gocache` 卷，`GOPROXY=off`，`TZ=UTC`）跑四包。
- 冒烟：不接真 CPA；投影层由单测覆盖，端到端留给 P2。真实 key 的本地 CPA 验证归验收（S9 结果未出，见 §9）。

### 7.2 P1（前端）

- `pnpm install --frozen-lockfile`（新 worktree 没有 `node_modules`）→ `pnpm type-check`、`pnpm test:logic`、`pnpm test:components`。
- 重点断言：插件 ollama 余额可见 vs OMC 自有 ollama 无信号隐藏；30 分钟新鲜度只对 `ollama:` 前缀；两个 raw key 收敛到 `commandcode`/`opencode` 且标签/图标正确；OPC 三窗口、CMC credits+套餐、余额缺失不出现 0；stale 标记与 note。
- 手工冒烟：`pnpm dev`（连接 fake CPA 或本地 CPA）→ `/oauth-management`，看三家行、抽屉、刷新按钮。

### 7.3 P2（测试基建）

- `node --test scripts/fake-cpa.test.mjs`：新路由返回形状、管理鉴权、未知 auth_index 行为。
- `pnpm verify:browser`：新增场景断言三家行出现、CMC/OPC/Ollama 字段、行内刷新触发 `POST /v8/management/plugins/ollama-quota/quota`、旧宿主回退走 `GET /credentials/quota/providers`（fixture 不返回 `quota_plugin_id`）。
- 回归：`pnpm test:components`、`pnpm test:logic`；涉及页面改动再跑 `pnpm check:ui` 对应场景。

### 7.4 安全与性能检查点

- 不在日志/页面新增任何凭证内容；插件载荷本身不含 key，OMC 错误文本仍走 `security.RedactText`。
- 新增管理调用只发生在旧宿主回退路径，次数 = 失败读取的凭证数（远小于现有每分钟 `/credentials`）；页面轮询不产生新上游读取。
- 前端只多渲染文本与标记，无新增轮询/计时器。

## 8. AC 草案

- **AC-1** 三家凭证在 OMC 额度页显示为独立行；provider 过滤标签分别为 Ollama / Command Code / OpenCode Go，图标为品牌图标；两个 raw key 的内部空格未被改写。
- **AC-2** 额度读取只经 CPA 管理接口（`GET /v8/management/credentials`、`POST /v8/management/plugins/:id/quota`，旧宿主加 `GET /v8/management/credentials/quota/providers`），使用 OMC 已存的 CPA 管理 key；OMC 不直连三家上游，不读取凭证密钥。
- **AC-3** CMC：套餐名显示；余额显示为带 `credits` 单位的文本（不显示成美元/$）；5 小时/周窗口与重置时间显示；缺失字段不写 0。
- **AC-4** OPC：`rolling` / `weekly` / `monthly` 三个窗口都能显示标签与重置时间；无套餐/余额时不显示伪造值；未确认周期的窗口不被硬套成 5 小时/30 天。
- **AC-5** Ollama：Cookie 凭证显示 5 小时/周窗口；仅 api_key 凭证（只有余额）显示 Credit 余额，窗口空态如实提示“上游未返回配额窗口”，余额不被隐藏。
- **AC-6** 插件 ollama 凭证的 `plan.credits` 不被 `verifiedQuotaCredits` 隐藏；30 分钟 stale 判定只作用于 `auth_index` 以 `ollama:` 开头的 OMC 自有账号。
- **AC-7** 插件返回 stale（200 + description/label 标记）时，界面有“缓存读数”标记与原因，观测时间回填为插件读取时间；插件错误/429 无缓存时显示刷新失败，不显示 0 或旧数据以外的东西。
- **AC-8** 宿主不返回 `quota_plugin_id` 时，OMC 通过 `GET /v8/management/credentials/quota/providers` 的 `supported_providers` 解析到正确插件；匹配不到时报错，且不把凭证发给无关插件。
- **AC-9** 三家插件凭证进入自动刷新轮与手动刷新（`refresh_supported=true`）；刷新失败按现有规则记录，不新增 CPA 管理鉴权失败面。
- **AC-10** 现有七家内置额度与 OMC 自有 Ollama 账号的显示、新鲜度、筛选行为不变；既有前端/后端测试不回归。
- **AC-11** 文档更新（`docs/cpamc-parity.md`、`docs/architecture.md`、`docs/testing.md`）；Go 单测、前端逻辑/组件测试、fake CPA 的端到端脚本覆盖上述路径。

### AC 覆盖

| AC | 包 |
|---|---|
| AC-1 | P1 |
| AC-2 | P0（回退调用）、P2（e2e 断言不直连） |
| AC-3 | P0、P1 |
| AC-4 | P0、P1 |
| AC-5 | P0、P1 |
| AC-6 | P1 |
| AC-7 | P0、P1 |
| AC-8 | P0 |
| AC-9 | P0、P2 |
| AC-10 | P1、P2 |
| AC-11 | P0、P1、P2 |

## 9. 风险、未决与外部的“看不到”前提

### 9.1 代码外前提（不阻塞开发，但决定线上是否看得到）

1. **线上 CPA 版本**：需 ≥ v8.0.25 才有 `quota_plugin_id`；否则依赖 `credentials/quota/providers` 回退（本机 fork c3ce22a2 已核实有该路由，线上版本未核实）。判断方式：授权环境读 `/v8/management/plugins` 的 `metadata.version`，或比对 Dokploy 镜像；`X-CPA-VERSION` 在 11-02 实测是 dev/none/unknown，不可靠。
2. **插件 v0.3.0 是否已装进线上 CPA**：拍板卡 N1112-28 未闭环；11-10 检查时线上 `registered=false`（倾向 `.so` 被重部署清掉）。未装 → 凭证没有 `supports_quota`，不出卡。
3. **凭证形态**：三家必须是 CPA 磁盘认证文件（`api_key`，后两家加 `base_url`）；「AI 提供商」里的兼容 key 无 `path`，不会出现在 `/credentials`，额度页看不到。
4. **Ollama 窗口依赖 Cookie**：只填 `api_key` 只有余额；要 5 小时/周窗口必须上传 cookie 认证文件。
5. **部署滞后**：本功能合进 dev 后，还要走 Mac 构建 → `deploy/prebuilt` → 浩文在 Dokploy 点部署，线上才可见；线上当前停在 `a3cd9b26`。
6. **403 同桶**：OMC 的 CPA `baseUrl` 实值与 CPA `trusted-proxies` 决定是否与浩文共用失败桶；本次不改网络/代理配置。

### 9.2 需要跟进/浩文确认

- 线上 CPA 版本与是否返回 `quota_plugin_id`（需授权环境，读只读接口）。
- N1112-28（插件装线上）的最终答复；在装之前，本功能线上不可见。
- 线上三家凭证是磁盘文件还是只有“AI 提供商”兼容条目（若是后者，先补认证文件才能验收）。
- OPC `rolling` / `monthly` 的周期语义（是否 5 小时 / 自然月）：未确认前不写死周期，只展示标签与重置时间；确认后可再补 `PeriodHours` 与排序。
- 真实 key 验证：S9 的本地验证（CMC×3、OLLAMA_API_KEY；OPC 无 key）结果未出，验收前需要结论。

### 9.3 范围外观察（不进工作包）

- OMC 没有 401/403 熔断/退避；若要防“key 错时轮询自己把自己封了”，需跨所有管理调用统一设计（另开需求）。本方案不加重失败计数（§4.6）。
- `summary` 目前只映射 `key="balance"`；v0.3.0 也只发这一条。若插件后续增加其它 metric，需要单独设计通用指标槽。
- CMC 余额的 `included/purchased/free` 分项在插件载荷里已被合并成一个总数（host `QuotaMetric` 无分项字段），OMC 不会丢“OMC 自己拿到的信息”。
- 飞书额度报告可能因新增 provider 快照而变化（`ListKnownQuotaProviders` 按快照聚合）；属预期观察，不在本需求 AC。
- demo 数据不生成三家夹具；demo 模式看不到这三家卡片，不在本需求范围。

## 10. 数据迁移

本次不涉及数据迁移。新增的 `QuotaWindow.stale/note`、`QuotaCredits.stale` 都是可选 JSON 字段，随现有 `windows_json` / `plan_json` 原样持久化；旧快照反序列化得到零值，前端按缺省处理。
## 11. 方案自检（对应派发说明的 7 点）

| 派发要求 | 在哪一节 |
|---|---|
| 1. 数据从哪来、每一跳、鉴权、失败表现；为何不新增直连 | §3（含逐跳 ASCII 图与失败语义）；“为什么不新增直连”在 §3 末 |
| 2. OMC 额度页改哪些地方、加哪些 provider；三个 key 映射 | §2（现状逐文件）、§4.1（后端逐文件）、§4.2（前端逐文件）、§4.3（三个 key 逐字映射） |
| 3. 要不要新开接口/新写采集、代价 | §4.5（旧宿主回退的代价与风险）、§3“为什么不新增直连”；结论：不新增 OMC 对外接口、不重写采集器，只加一个 CPA 管理读回退 |
| 4. 三家数据结构不同，卡片怎么统一又不丢信息（字段映射/缺失显示/Ollama api_key 只有余额） | §4.4（字段映射表）、§4.2 的 ollama 余额/新鲜度改动、§7.2/AC-5 |
| 5. 已有工作复用；`quotaCredits.ts` / `quotaStatus.ts` 的 ollama 特判怎么处理 | §4.7（复用清单）、§4.2 第 3/4 条（按 `auth_index` 前缀收窄） |
| 6. 403 的教训：管理调用、鉴权、失败计数、是否熔断、会不会加重 | §4.6 |
| 7. 生产前提与边界（线上版本、quota_plugin_id、插件安装、磁盘凭证） | §9.1（代码外前提）、§9.2（需确认项） |

