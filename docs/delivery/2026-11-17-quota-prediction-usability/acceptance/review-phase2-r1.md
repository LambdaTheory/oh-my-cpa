---
type: review
slug: 2026-11-17-quota-prediction-usability-phase2-r1
status: final
updated: 2026-11-18
verdict: 要修
summary: "S9/S10 第二期 CMC/OPC 采集审查：allowlist、凭证来源、解析映射、调度名册均有代码与离线测试证据；Go 四包+demo+vet、前端 type-check/test:logic/test:components 通过；2 条范围内要修（OPC 401 未按认证失败处理、CMC 月度分母来源未标注），线上真值与端点形态未实测"
---

# 第二期后端切片 S9/S10 审查报告（第 1 轮）

- 派发：works 2026-11-17#T8-S13（父任务 2026-11-17#T8）
- 审查对象：`Newoahil/11-17-t8-s13-claude`，基线 `Newoahil/11-17-t8-quota-prediction-phase1` tip `86f7661c`
- 提交：`0d8be0d9`（S9 reader）、`0b9622b3`（S9 测试）、`1447a848`（S10 名册/调度）、`bb7c24c1`（S10 测试）、`ba318ac4`（兼容端点不可用兜底）
- 变更规模：31 个文件，+2181/−36（`git diff --shortstat 86f7661c..HEAD`）
- 依据：T8 任务说明「第二期」一节、调研 `E:/works/Homan-context/inbox/agents/调研/2026-11-17-cpa-额度预测数据调研.md` §2.5/§3.4/§3.5/§5.2、`docs/delivery/2026-11-17-quota-prediction-usability/cases.md` C-20～C-26、S11/S12 进度与交接

## 结论

- **B1 的四条要求（allowlist、凭证来源、解析映射、调度/名册）在代码与离线测试层面均有证据，主要路径通过。**
- **2 条范围内要修（均为一般，不推翻方案）**：R-01，OPC `/usage` 未纳入 `UpstreamQuota` 错误分类，401/403 不会给出 `needs_reauth`；R-02，CMC 月度窗口的分母来自厂商套餐表，但表内 plan 没有标注来源，与 C-20/调研 §5.3 C2 的要求不符。
- 4 条范围外观察（O-01～O-04）只记录，不要求本轮修改。
- **线上部分未实测**：C-22/C-23/C-24（`quota_list`、`model_advice` 的真实行数与数值）要等部署；CMC/OPC 真实上游载荷、OPC 精确 base-url 与 key 形态也仍未实测，本报告不把它们当通过。
- 本报告 `verdict: 要修` 指 R-01/R-02 需要在合入/部署前修掉并复审；S9/S10 的架构与文件归位不需要返工。

## 门禁（自己跑的，不是抄开发报告）

本机无 Go，用只读挂载 + 热卷 Docker（`golang:1.27.1`，`GOPROXY=off`、`GOFLAGS=-mod=readonly`、`TZ=UTC`、`--network none`）：

```text
go test -count=1 ./internal/quota ./internal/operations ./internal/api ./internal/repository
ok  github.com/oh-my-cpa/oh-my-cpa/internal/quota        0.027s
ok  github.com/oh-my-cpa/oh-my-cpa/internal/operations  172.856s
ok  github.com/oh-my-cpa/oh-my-cpa/internal/api         161.125s
ok  github.com/oh-my-cpa/oh-my-cpa/internal/repository  206.043s

go test -count=1 ./internal/demo
ok  github.com/oh-my-cpa/oh-my-cpa/internal/demo        38.997s

go vet ./...
VET_OK

gofmt -l internal/quota internal/operations internal/api internal/repository
internal/repository/repo_keys_test.go
```

- `repo_keys_test.go` 是基线既有红项：它不在本次 diff（`git diff --name-status 86f7661c..HEAD` 无此文件），与 S12 交接一致，不算本次问题。
- `--network none` 下四包全绿，证明新增夹具与测试没有联网依赖。
- 前端（改动只有 `rolling` kind 联合与三份 i18n 文案）：

```text
pnpm type-check      → 0
pnpm check-i18n      → 0（DICT 4266 keys）
pnpm test:logic      → 89 suites passed
pnpm test:components → exit 0（仅 jsdom 的 getComputedStyle 伪元素告警）
node scripts/check-demo.mjs → 数据集为最新（155 responses / 18 console routes）
```

- Node 版本 24.16.0 与 `package.json` 的 `22.23.2` 不一致，只触发 pnpm 的 engine WARN，不改变结果；S11 自测用的是同一版本。
- `check-docs` 仍为基线既有红：全部来自 `docs/delivery/2026-11-12-omc-three-credential-quota/impl-plan.md`（9 条 missing-path、22 条 line-reference）；本报告位于 `docs/delivery/**/acceptance/`，按冻结证据豁免，没有新增告警。
- 新增用例的定点重跑（`go test -run ... -v`）全部 PASS，包括 `TestParseCommandCodeQuota*`、`TestOpenCode*`、`TestQuotaAutoRefreshReadsTheOpenCodeEntryOnce`、`TestQuotaRosterToleratesAnUnavailableCompatibilityListing`、`TestQuotaManualRefreshAndOverviewIncludeCompatibilityCredentials`、`TestModelAdviceAndQuotaListReadTheOpenCodeEntry`、`TestQuotaPredictionRosterIncludesOpenCodeKeys`、`TestListCredentialMetadataForTypes*`。

## 一、需求还原度（B1）

### B1-allowlist —— 通过（OPC 路径未线上实测）

- 新前缀在 `internal/quota/service.go:75-76`：`https://api.commandcode.ai/alpha/billing/` 与 `https://opencode.ai/zen/go/v1/usage`；校验仍走 `IsAllowedQuotaURL`（`service.go:87-114`）。
- 边界测试 `internal/quota/allowlist_test.go:7-46` 覆盖：相邻路径（`billing-evil`）、同域其它路径、主机后缀伪造、端口、http、大小写、`..`/`.`；OPC 的 `/usage/extra`、`/usage-extra`、去掉 `/zen/go`、主机后缀、端口、大小写都被拒绝。Query 被允许是既有设计（`service.go:79-86`），不是本次放宽。
- CMC 前缀按要求是「billing 家族」，会放行该前缀下任意子路径与 query；当前只有 CMC reader 使用它，且只用 GET。按需求的写法判通过；若后续要收紧到具体两个端点，可把前缀换成两条精确 URL。
- OPC 域名来自只读证据（S11 交接：`models_list` 的 `endpoint_host=opencode.ai`）；**路径 `/zen/go/v1/usage` 来自 2026-11-12 的仓库交付记录，不是本轮线上实测**。实现从条目 base-url 拼 `/usage`（`opencode.go:71-90`），allowlist 精确匹配，配置漂移时 fail closed（`opencode_test.go:195-216` 断言 `called==0`），不会把密钥发到未确认地址。

### B1-凭证来源 —— 通过（一处与需求原文不同，已记录）

- **CMC**：需求原文说 CMC/OPC 都不在 `/credentials`、auth_index 从 config 树拿；S11 只读实测发现 3 个 CMC auth 在 `/credentials` 且 `provider=commandcode`，因此复用 `AuthFiles`（`service.go:148-181,387-394`）。这是有只读证据的偏差，代码更简单，不判缺陷；报告在此记录，供后续部署核对。
- **OPC**：`openAICompatibilityReader` 读 v0 `/openai-compatibility`；`api-key-entries[].auth-index` 优先，legacy `api-keys` 时回退 v8 `ConfigView` 的 `api-keys.openai-compatibility[].keys[]`，接受 `auth-index`/`auth_index` 两种拼写（`roster.go:66-129`）。条目必须 name 精确为 `opencode`（去空格、忽略大小写）、仅一个启用条目；auth_index 必须属于该条目（`opencode.go:45-90`）。测试：`roster_test.go`、`opencode_test.go:122-165`。
- **密钥不落库/不落日志**：合成 `AuthFile` 只带 index/name（`roster.go:46-52`）；失败文本经 `security.RedactText`（`service.go:448-452`）；测试断言上游错误体与兼容条目 key 不进入结果（`commandcode_test.go:211-231`、`model_advice_test.go:124-126`）。

### B1-CMC 解析与映射 —— 通过，但月度分母标注要修（R-02）

- 解析（`commandcode.go:106-140`）：`windowLimits.{fiveHour,weekly}.{used,cap,exceeded,resetAt}`、`credits.monthlyCredits/purchasedCredits`、`subscriptions.data.{planId,currentPeriodEnd}`。
- 映射（`commandcode.go:144-233`）：`used/cap` → `UsedPercent`（0–100 clamp）；无 cap 不算百分比并给 note；`exceeded=true` → 100% + note；`resetAt` 是 Unix ms → `ResetAtMS` + `ResetAccuracy:"exact"`；`PeriodHours` 5/168/720；monthly 用「剩余 credits + 套餐表」反推 used（公式与调研 §3.4 的插件示例 `70/42.5/27.5/39%` 对得上）。
- 缺字段/非法值不发明：全空窗口跳过；无分母不补 0；无效 reset 丢弃；测试 `commandcode_test.go:72-173`。429/失败保留旧窗口并保留 typed backoff（`commandcode_test.go:233-258`）。
- **R-02**：表内 plan（如 `individual-goat`）的 monthly 只写 `Limit/Used/UsedPercent`，`Note` 为空；只有表外 plan 才写「不在厂商额度表内」。C-20 与调研 §5.3 C2 明确要求「分母来自人工表要标注，不冒充上游精确值」。

### B1-OPC 解析与映射 —— 通过

- `usage.{rolling,weekly,monthly}.{percent,resetsAt}`（`opencode.go:129-187`）：percent 按 0–100 量纲使用，0.5 表示 0.5%（`opencode_test.go:71-79`）；越界/NaN/Inf/非法 resetsAt 整份读取失败，保留旧窗口（测试 `:96-108`）；rolling `PeriodHours=nil`，weekly/monthly 168/720；缺窗口不补 0；`resetsAt` RFC3339(Nano) → ms + `exact`。
- `WindowCycleRange` 先按 `Kind=="rolling"` 返回 `rolling_window`（`capacity.go:129-136`），OPC rolling 不会落到 `boundary_unknown`；antigravity/plugin 窗口不设置 Kind，因此该改动不影响第一期行为（`antigravity.go:248-261` 的窗口字面量没有 Kind 字段）。

### B1-调度与名册 —— 通过（clear-cooldown 见 O-03）

- 同一份 `quotaRosterFiles`（AuthFiles + OPC 合成目标，按 auth_index 去重）接入：概览、详情、控制台手动刷新、自动刷新轮、自适应调度器、MCP `quota_refresh`（`management_quota.go:47-77,158,704,848`、`quota_auto_refresh.go:104`、`quota_scheduler.go:125-131`、`agent_wiring.go:331-369`）。
- 禁用与退避没有被绕过：自动刷新先过滤 backoff、候选带 `IsDisabled/IsSupported`（`quota_auto_refresh.go:144-167`）；调度器跳过 disabled、检查 backoff 与 credential floor（`quota_scheduler.go:264-290`）；手动刷新在 `readCredentialQuota` 内加锁并复查 backoff（`quota_scheduler.go:65-95`）。
- 每凭证每轮上游次数：CMC credits+subscriptions = 2，OPC `/usage` = 1（`commandcode.go:289-308`、`opencode.go:95-116`），在 1–3 内；测试按 URL 计数（`quota_roster_test.go:137-206`）。
- 预测名册只收 name=`opencode` 的 compatibility 行；零窗口不占行（`quota_prediction.go:234-325`，测试 `quota_prediction_test.go:20-70`）。`model_advice` 用 `ProviderID=openai-compat-N` 关联路由、用 `Provider=opencode` 关联读数（`model_advice_credentials.go:55-86`、`model_advice.go:146-150`），MCP 端到端测试 `model_advice_test.go:19-126`。
- MCP 字段形状未变：`Quota`/`QuotaWindow` 结构未改，只多一个 `kind:"rolling"` 取值；`quota_list`、`model_advice` 断言通过。
- 飞书不回归：`ListFeishuCredentialMetadata` 委托新方法但类型仍固定为 `auth-file`（`feishu_report.go:191-193`），repository 测试钉住两条调用方语义不同（`feishu_report_test.go:70-118`）。

### B1-不改 CPA fork / 不含 B3 —— 通过

`git diff --name-status` 只动 OMC 的 `internal/`、`web/`、测试与 demo provenance；没有 CPA fork、`private/`、生产配置或 B3（opencode-go 插件）。

### B1-未实测如实标注 —— 通过

S11/S12 交接分别列出 CMC/OPC 真实载荷、OPC 路径与 key 形态、线上行数/数值等未实测项；代码注释也写明 OPC host/path 的证据来源（`opencode.go:23-31`）。本报告在「未覆盖/未测项」继续沿用该口径，不把离线结果当线上通过。

## 二、代码审查专项

- **正确性（除 R-01/R-02 外）**：allowlist 的拒绝路径（`..`、主机后缀、端口、scheme、大小写）都被测试钉住；OPC 的 percent 量纲、非法 percent/reset、缺窗口、429 保留旧窗口都有用例；CMC 的 exceeded、缺 cap、无效 reset、订阅失败只丢月度字段都有用例。`quotaRosterFiles` 对同 auth_index 以 AuthFiles 优先去重，避免同一凭证两行或在自动刷新里被当歧义跳过（`management_quota.go:63-75`）。`WindowCycleRange` 的 rolling 早退只影响 Kind="rolling" 的窗口，antigravity/plugin 的窗口字面量没有 Kind（`antigravity.go:248-261`），第一期行为不受影响。
- **一致性**：provider 命名在 `DetectProvider` → `CapabilitiesForProvider` → `builtInQuotaProviders` → 合成 `AuthFile` 四处一致（`service.go:148-181,196-218`、`plugin.go:19-28`、`roster.go:46-52`）；`model_advice` 的 `ProviderID=openai-compat-N` 与目录生成规则来自同一次 `OpenAICompatibility()` 遍历（`client_providers.go:482-490`），离线端到端用例验证了关联。S9 把插件 stale 用例的 provider 改成中性 `fixture-meter`（`service_test.go:937-946`），断言未改，属于行为变更的显式记录。
- **可维护性**：OPC 条目选择在 `opencode.go:57-69` 与 `roster.go:155-190` 各写了一遍（同名/停用/歧义规则相同），后续修改容易漂移；可提取为一个内部 helper，不影响本轮结论。没有发现死代码、无删测试、无 `t.Skip`；`quotaRosterFiles` 的 OPC 读失败只告警是设计，且用 `IsMissingCapability` 抑制旧网关噪音。
- **越界**：`reset_advice_test.go` 增加 `/v0/management/openai-compatibility` 的 fake 响应，是名册新增读取后的测试适配；`deploy/cloudflare/data/provenance.json` 是 demo digest 输入变化的生成物；两者都不是业务越界。
- **测试缺口（不单独判要修）**：没有 OPC 401 的用例（与 R-01 同一处，建议补）；CMC 的自动刷新只测了 reset 触发，没有测 interval 触发（同一 `AutoRefreshTrigger` 路径，风险低）；`quota_list` MCP 只用 OPC 做端到端，CMC 走同一 `buildQuotaOverview`，没有单独断言。

## 三、问题清单

### R-01（范围内要修 · 一般 · 错误处理）

- 位置：`internal/quota/failure.go:11-19`；`internal/authfailure/evidence.go:97-99`；`internal/quota/recommendation.go:9-12`。
- 问题：`quotaErrorSource` 给所有主额度端点归入 `authfailure.UpstreamQuota`，但漏了 `OpenCodeUsageURL`，它落进 `default` 的 `Supplemental`；而 `Evidence.NeedsReauth` 对 `Supplemental` 一律返回 false。结果是 OPC 的 `/usage` 返回 401/403 时，凭据被标成普通 `error`，建议是「上游刷新超时或响应异常，可尝试重新刷新」，不会提示重新配置密钥；CMC 的 credits 端点同场景会得到 `needs_reauth`。
- 复现（只读探针，`go test -overlay` 在容器内跑，不落仓库）：同一份 401 响应下：
  - OPC：`status="error"`，recommendation=`{Status:warning Action:refresh}`；
  - CMC：recommendation=`{Status:needs_reauth Action:reauth}`。
- 影响：失效的 OPC key 不会得到可操作的诊断，且可能按普通错误反复重试。
- 建议：把 `OpenCodeUsageURL` 加进 `UpstreamQuota` 的 case；在 `isRecognizedQuotaPayload` 增加 `case OpenCodeUsageURL: keys=[]string{"usage"}`；补一条 401 用例钉住 `needs_reauth`（同时保留 429 的 typed backoff 用例）。

### R-02（范围内要修 · 一般 · 需求还原度）

- 位置：`internal/quota/commandcode.go:50-69`（套餐表与前缀匹配）、`:211-220`（表内 plan 不写 note）、`:221-227`（只有表外 plan 写 note）。
- 问题：CMC 月度窗口的分母来自厂商套餐表（`individual-goat=70` 等），但表内 plan 的 `QuotaWindow.Note` 为空，`Limit/Used/UsedPercent` 与上游 API 给的精确值在形状上无法区分。C-20 要求「monthly 的分母若来自人工 plan 表，标注为估算/人工表，不冒充上游精确值」；调研 §5.3 C2 也是同一口径。当前只有表外 plan 得到 note。
- 复现（只读探针）：`individual-goat` + `monthlyCredits=42.5` 解析出的 monthly 窗口 `note=""`，同时 `limit=70`、`used=27.5`、`used_percent≈39.3`；控制台与 `quota_list` 都会把这个 70 当成与上游同等精度的上限展示。
- 建议：对 `allowance>0` 的 monthly 也写 Note（例如「分母来自厂商套餐表，非上游 API 精确值」）；`ResetAccuracy:"exact"` 只描述 reset，保持不变。若产品希望预测面板也能看到来源，需要另加 console-only/契约字段，先不扩。
- 附注：同一段前缀匹配会把 `individual-pro-annual` 这类表外 plan 归成 Pro（30），见 O-01，请与 R-02 一起定口径。

## 四、范围外观察

- **O-01 套餐表前缀匹配可能把表外 plan 当表内**：`commandCodePlanFor`（`commandcode.go:50-69`）先精确匹配，再按最长前缀匹配；只读探针显示 `individual-pro-annual` → `Pro/30`、`enterprise-x` → 无分母。若厂商 planId 确有后缀形态，需要把证据和用例写进代码；若没有，建议改精确匹配，避免给新套餐一个未经确认的分母。当前与 R-02 的「表外 plan 诚实标 note」一并复核即可。
- **O-02 `Plan.Credits` 被当作美元进入模型组余额**：CMC 把剩余月度 credits 写进 `Plan.Credits.Balance`（`commandcode.go:253-255`）；`SafeQuota` 把任意 `Plan.Credits` 转成 `Currency:"USD"`（`internal/operations/quota.go:43-49`）；模型组余额会把它求和并按美元格式显示（`internal/operations/model_groups.go:413-428`、`web/src/types/pricingDisplay.ts:29-37`）。若 CMC credits 不是美元计价，模型组页会出现「$42.50」这类误导数字。请确认 credits 的单位；若只是额度点数，建议 CMC 不写 `Plan.Credits`（月度窗口与 note 已能表达剩余），或让余额投影区分计价单位。quota 页的「Credit balance」是无币种文本，不受此影响。
- **O-03 `clear_cooldown_supported` 对 OPC 为 true，但清理接口只认 AuthFiles**：`CapabilitiesForProvider` 把 opencode 放进通用 case（`service.go:205-217`），`clearQuotaCooldown` 只查 `AuthFiles`（`quota_operations.go:14-28`）。目前 OAuth 工作台不把 quota-only 的 OPC 行建出来（`oauthWorkspaceLogic.ts:470-477`），所以界面上暂时没有可点的按钮；但 API/detail 对 OPC 宣称支持一个会 404 的动作。建议给 opencode 单独返回 `ClearCooldownSupported:false`，或让清理接口也走 `quotaRosterFiles`。
- **O-04 OPC 多 key 的显示名相同**：合成目标的 `Name` 取条目名（`roster.go:46-52`），多 key 时 `quota_list`/控制台/建议里都是同一行名 `opencode`，只能靠 auth_index 区分（只读 MCP 还会隐藏 auth_index）。不违反本期要求；若线上 OPC 确实多 key，后续可考虑带序号或掩码的显示名。

## 五、未覆盖/未测项（如实记录，不当作通过）

- **C-22/C-23/C-24**：线上 `quota_list` 是否出现 CMC 三 auth 与 OPC key 的 windows、`model_advice` 是否不再 "without valid readings"、以及与上游人眼对数值，都要第二期部署后做（部署由浩文点）；本轮没有线上第二期实例，不能判通过。
- **CMC 真实载荷**：`/alpha/billing/credits` 与 `/subscriptions` 的形状来自插件 v0.1.4 源码/交接，未用真实 key 实测；`subscriptions` 顶层 `success` 字段是解析门槛（`commandcode.go:124-133`），若真实返回没有该字段或大小写不同，会丢掉 plan/currentPeriodEnd（monthly 窗口仍会以 note 形式给出剩余 credits），部署后第一次读要核对。
- **OPC 真实载荷与 key 形态**：`/usage` 的 percent/resetsAt 形状、OPC 精确 base-url（`/zen/go/v1/usage`）、线上 key 是 `api-key-entries`（带 auth-index）还是 legacy `api-keys`，都未实测；若两者都解析不到索引，OPC 不进名册，按 S9/S12 的缺口处理，不改代码猜。
- **套餐表覆盖**：`planAllowances` 是否覆盖线上 3 个 CMC auth 的 planId，未实测；表外 plan 的行为有单测，但线上具体命中的是哪一档不知道。
- **界面真测**：本切片不改组件逻辑（仅类型联合与 i18n），没有跑浏览器 probe；已用 `type-check`、`check-i18n`、`test:logic`、`test:components` 覆盖编译与既有前端回归。OPC 行在浏览器里的真实渲染留给第二期集成验收。
- **构建/推送/部署**：本轮不改代码、不推送、不合并，也不触发第二期部署。

## 附：逐条核对索引

| 要求/用例 | 结论 | 主要证据 |
|---|---|---|
| allowlist：CMC 前缀 + OPC 精确端点、SSRF 边界 | 通过（OPC 路径未线上实测） | `service.go:59-114`；`allowlist_test.go:7-46`；`opencode.go:71-90`；`opencode_test.go:195-216` |
| 凭证来源：CMC `/credentials`、OPC v0 + v8 legacy 回退 | 通过（CMC 偏差已记录；OPC 形态未实测） | `roster.go:31-190`；`opencode.go:45-90`；`roster_test.go`；`opencode_test.go:110-165` |
| CMC 解析/映射：used/cap、exceeded、reset ms、5/168/720、monthly | 通过；分母标注 R-02 | `commandcode.go:106-233`；`commandcode_test.go:30-173` |
| OPC 解析/映射：percent 0–100、resetsAt、rolling nil | 通过 | `opencode.go:129-196`；`opencode_test.go:37-108,268-281`；`capacity.go:129-136` |
| 调度/成本：自动刷新、退避、禁用、1–3 次上游 | 通过 | `quota_auto_refresh.go:144-167`；`quota_scheduler.go:65-95,264-290`；`quota_roster_test.go:137-206` |
| 名册：quota_list / model_advice / quota_prediction | 离线通过；线上 C-22/23 未实测 | `management_quota.go:47-77,848`；`model_advice_credentials.go:55-86`；`model_advice_test.go:19-126`；`quota_prediction.go:216-326`；`quota_prediction_test.go:20-70` |
| MCP 字段形状 / 飞书回归 | 通过 | 结构未改；`feishu_report.go:191-234`；`feishu_report_test.go:70-118` |
| 不改 CPA fork / 不含 B3 | 通过 | `git diff --name-status 86f7661c..HEAD` |
| B1-4 未实测如实标注 | 通过 | S11/S12 交接；`opencode.go:23-31`；本报告第五节 |

## 复核命令（可直接重跑）

```powershell
docker run --rm --network none -v "E:/works/Newoahil__oh-my-cpa/11-17-t8-s13-claude:/src:ro" -v "omc-go-mod:/go/pkg/mod" -v "omc-gocache:/root/.cache/go-build" -w /src -e GOPROXY=off -e GOFLAGS=-mod=readonly -e TZ=UTC -e ASSISTANT_NO_SEND=1 -e OMCPA_ENV_FILE=/dev/null golang:1.27.1 go test -count=1 ./internal/quota ./internal/operations ./internal/api ./internal/repository
```
