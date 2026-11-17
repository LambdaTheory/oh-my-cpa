---
type: acceptance-report
slug: 2026-11-17-quota-prediction-usability-phase2-r1
status: final
updated: 2026-11-18
verdict: 通过
summary: "第二期验收：B1 的 7 条用例离线可测部分全部通过（C-20/C-21/C-25/C-26 + S16 五项修复），C-22/C-23/C-24 按未部署记未测；Go 全仓测试+vet+四包、前端 type-check/i18n/logic/components/静态门全绿；线上只读名册仍是旧版（CMC windows=[]、OPC 无行），代码要修 0 条，观察 3 条"
---

# 额度预测可用化 · 第二期验收（第 1 轮）

- 派发：works 2026-11-17#T8-S15（父任务 2026-11-17#T8），工作目录 `E:/works/Newoahil__oh-my-cpa/11-18-t8-s15-claude`，分支 `Newoahil/11-18-t8-s15-claude`。
- 验收对象：本分支 HEAD `13f82dbf`。基线 `86f7661c`（第一期分支 tip），S9/S10 的 `0d8be0d9`/`0b9622b3`/`1447a848`/`bb7c24c1`/`ba318ac4`、S13 审查报告 `c310e56a`、S16 修复 `13f82dbf`。
- 变更规模：34 个文件，+2499/−36（`git diff --shortstat 86f7661c..HEAD`）。
- 依据：需求原话第二期一节（`E:/works/Homan-context/runs/2026-11-17-2026-11-17-T8.md`）、前置调研 §3.4/§3.5/§5.2、`cases.md` B1 用例 C-20～C-26、S13 审查报告 `acceptance/review-phase2-r1.md`。
- 线上只读时钟：`quota_list`/`model_advice`/`quota_prediction` 于 2026-11-18 00:46:45–00:46:56 +08 各读一次；**未刷新、未写、未读密钥**。

## 结论

**通过。代码要修 0 条。**

- **B1 四条要求（allowlist、凭证来源、解析映射、调度/名册）在代码与离线测试层面均有证据**，5 条独立验收探针（不在仓库里，用 `go test -overlay` 注入）全部 PASS，覆盖 C-20/C-21/C-23 的离线等价、C-25 的每凭证请求次数与 429 退避、以及 S16 的 R-01/R-02/O-01～O-03 五项修复。
- **C-22/C-23/C-24 记「未实测（待部署）」**。线上仍是旧版本：`quota_list` 三个 CMC linear auth `windows: []`、OPC 完全不出现；`model_advice` 里 `commandcode/*` 与 `op-*` 仍是 `0 blocked, N without valid readings`。这是 before 对照，不是第二期失败。
- **门禁全部自跑**：Go `./...` 全绿、`go vet ./...` 无输出、四包（quota/operations/api/repository）全绿；前端 `type-check`、`check-i18n`（4266 keys）、`test:logic`（89 suites）、`test:components`、`lint:antd` 全过；`check:ui --scenario dashboard-quota-prediction` 通过；`pnpm verify:probes` 80 个场景 79 通过，唯一失败是 Playground 的 `net::ERR_NO_BUFFER_SPACE`（本机资源，非本分支面，单独重跑该场景通过）。
- 静态门有两类**基线既有红**，用 `git archive 86f7661c` 导出基线复现确认，与本期改动无关：`check-docs`（31 条，全部指向 `docs/delivery/2026-11-12-omc-three-credential-quota/`）、`test:self` 的 `fixed-waits`（3 个验收脚本）与 `native-release`（Windows 缺 `zip`、符号链接 EPERM）。
- 3 条范围外观察（O-A1～O-A3），不要求本轮修改。

## 环境与提交

| 项 | 值 |
|---|---|
| 分支 / HEAD | `Newoahil/11-18-t8-s15-claude` / `13f82dbf`（未推送） |
| 基线 | `86f7661c`（第一期 tip，已在 HEAD 历史内） |
| Go | 本机无 Go，按项目约定用 Docker `golang:1.27.1`，`GOPROXY=off`、`GOFLAGS=-mod=readonly`、`TZ=UTC`、`--network none` |
| Node / pnpm | Node v24.16.0（仓库期望 22.23.2，仅 engine WARN）/ pnpm 11.19.0 |
| 浏览器 | 项目自带 probe harness（无头 Chromium），`pnpm check:ui` / `pnpm verify:probes` |
| 线上只读 | cpa-buddy MCP `quota_list` / `model_advice` / `quota_prediction` / `providers_list` / `models_list` / `oauth_credentials_list` / `config_read` |

## 门禁（自己跑的，不抄开发报告）

```text
# Go 全仓
go test -count=1 ./...            → EXIT=0（无 FAIL 行）
go vet ./...                      → 无输出
go test -count=1 ./internal/quota ./internal/operations ./internal/api ./internal/repository
ok  internal/quota        0.034s
ok  internal/operations   179.022s
ok  internal/api          151.316s
ok  internal/repository   219.927s
gofmt -l internal/quota internal/operations internal/api internal/repository
internal/repository/repo_keys_test.go   ← 基线既有（69764937 引入，不在本期 diff）
```

```text
pnpm type-check          → exit 0
pnpm check-i18n          → 4266 keys
pnpm test:logic          → 89 suites passed
pnpm test:components     → exit 0（仅 jsdom getComputedStyle 伪元素告警）
pnpm lint:antd           → total 0 / deprecated 0 / a11y 0
pnpm check:ui --scenario dashboard-quota-prediction → 1 scenario passed
pnpm verify:probes       → 79/80 场景通过；1 条失败 = Playground
                           `net::ERR_NO_BUFFER_SPACE`（本机缓冲资源耗尽）
                           `pnpm check:ui --scenario playground` 单独重跑 → 通过
node scripts/check-demo.mjs → 数据集最新（155 responses / 18 console routes）
```

## 一、逐条用例（B1）

| 用例 | AC | 类型 | 结果 | 证据 |
|---|---|---|---|---|
| C-20 CMC 3 个 auth 的窗口映射 | B1-1 | e2e（离线夹具） | **通过** | 探针 `TestAcceptanceProbeCMCThreeAuths`：3 个 auth 各得 five_hour/weekly/monthly，`used_percent` = used/cap，`period_hours` 5/168/720，`reset_accuracy="exact"`，monthly 分母 70、note 含「厂商套餐表（GOAT）… 非上游 API 精确值」，credits 文本带 `credits` 单位；3 个 auth 各自独立、未合并 |
| C-21 OPC rolling/weekly/monthly 映射 | B1-1 | e2e（离线夹具） | **通过** | 探针 `TestAcceptanceProbeOPCKeys`：3 个 key 各得三窗口；`percent`→used；rolling `period_hours` 为 nil；weekly/monthly 168/720；`resetsAt`→`reset_at_ms` + `exact`；缺窗口不补 0 |
| C-22 线上 quota_list 出现 CMC 三 auth 与 OPC key | B1-1 | 接口（线上只读） | **未实测（待部署）** | 线上仍是旧版：CMC 三个 auth 全 `windows: []`；`quota_list` 无 `opencode` 行。离线名册测试 `TestQuotaManualRefreshAndOverviewIncludeCompatibilityCredentials`、`TestQuotaAutoRefreshReadsTheOpenCodeEntryOnce`、`TestQuotaRosterToleratesAnUnavailableCompatibilityListing` 全 PASS |
| C-23 model_advice 里 CMC/OPC 不再是 without valid readings | B1-2 | 接口（线上只读） | **未实测（待部署）**；离线等价通过 | 线上 `commandcode/*` 8 个模型 `0 blocked, 3 without valid readings`、`op-*` 9 个模型同；离线探针 `TestAcceptanceProbeCMCAdviceStopsBeingUnread` 证明 CMC 窗口接上后 verdict `ok`、`credentials_usable=1`、`tightest_window` 有值；OPC 由 `TestModelAdviceAndQuotaListReadTheOpenCodeEntry` 端到端覆盖 |
| C-24 真实数值与上游人眼核对 | B1-3 / B1-4 | 浏览器 + 接口 | **未实测（待部署）** | 部署红线，本期不部署；无真 key 也不读上游。CMC/OPC 真实载荷形状仍是调研与插件源码的转述，未用真 key 实测 |
| C-25 采集成本与调度兜底 | B1-1 | 接口 | **通过（离线）** | 探针计数：每个 CMC auth 每轮 credits+subscriptions = 2 次；每个 OPC key 每轮 `/usage` = 1 次，均在 1–3。调度按 `IdleInterval 2h / NormalInterval 30min`，`commandcode`/`opencode` 未单列 provider floor → 用共享 `DefaultQuotaCredentialFloor 10min`；单凭证失败在 `runQuotaAutoRefreshRound` 里只计 `Failed++`，不拖累同轮其他凭证；429 走 typed backoff，不无限重试 |
| C-26 只改 OMC，不碰 CPA fork 与生产 | 需求边界 | e2e（diff） | **通过** | `git diff --name-only 86f7661c..HEAD` 只有 `internal/`、`web/`、`docs/`、`deploy/cloudflare/data/provenance.json`；无 `private/`、生产配置、密钥、B3（`opencode-go` 只出现在测试的拒绝用例字符串里） |

### 用例本身的缺口（不改用例，照记）

- **C-25「按 idle 2h / normal 30min 分层调度兜底」**：计划间隔确为 30min/2h，但 `commandcode`/`opencode` 落进共享 10 分钟 floor（`internal/config/config.go:246-262`），比计划间隔更紧，实际不会更频繁；记为通过，但「每凭证最低间隔 10 分钟」是共享兜底而非为这两家单独定的（见 O-A2）。

## 二、S16 修复复核（对 S13 审查的 R-01/R-02/O-01～O-03）

| 项 | 结论 | 证据 |
|---|---|---|
| R-01 OPC 401/403 归入认证失败 | **已修** | `internal/quota/failure.go:14` 把 `OpenCodeUsageURL` 加进 `UpstreamQuota`；`:67-68` 识别键 `usage`。探针与 CMC 同场景逐条对比：`invalid_api_key` 401/403 → 两边都是 `needs_reauth/reauth`；裸 `message` 401 同为 `needs_reauth`，403 同为 `warning/refresh`（与 CMC 完全一致，未过宽）。429 仍保留 typed backoff 与旧窗口 |
| R-02 CMC 月度分母来源标注 | **已修** | `internal/quota/commandcode.go:209-216`：表内 plan 写「分母来自厂商套餐表（GOAT），非上游 API 精确值」；`ResetAccuracy` 仍只描述 reset |
| O-01 plan 精确匹配 | **已修** | `commandcode.go:47-61` 去掉最长前缀匹配；`individual-pro-annual` 不再借 `individual-pro` 的 30，改为无分母 + 「不在厂商额度表内」note（`TestCommandCodePlanTableMatchesExactIDs`） |
| O-02 credits 不当美元 | **已修** | `commandcode.go:249-254` balance 带 `credits` 单位；`internal/operations/quota.go:45-49` 的 `parseGroupBalance` 是非数字即拒绝，故模型组 USD 投影跳过（`TestCommandCodeCreditsAreNotProjectedAsDollars`、`model_groups_test.go` 的 `credits_are_not_currency`）；quota 页 `Credit balance` 仍显示原文（`QuotaProgressBar`/`CompactQuotaView` 读 `plan.credits.balance` 文本） |
| O-03 OPC 不宣称可清 cooldown | **已修** | `internal/quota/service.go:210-218`：`opencode` 单独分支 `ClearCooldownSupported:false`；`commandcode` 保持 true（探针 `TestAcceptanceProbeCapabilityAndUnitBits` + `TestOpenCodeCapabilitiesDoNotAdvertiseCooldownClearing`） |
| O-04 多 key 同名（范围外） | 未改，符合约定 | 合成目标 `Name` 取条目名（`roster.go:46-52`），多 key 只靠 auth_index 区分 |

## 三、线上只读名册现状（before，2026-11-18 00:46 +08）

**版本依据**：线上入口与 `deploy/prebuilt` 的 `a0f29eb9`（源 `dev ee40f0be`）一致，而第二期分支没有合入 dev、没有 prebuilt、没有部署。以下读数与这个结论一致。

| provider | 凭证 | 状态 | windows | 关键结论 |
|---|---|---|---:|---|
| commandcode | `commandcode-key-06c9…` / `4f58…` / `631c…` | idle | **0** | `windows: []`，recommendation `action: refresh`「暂无实时配额数据」 |
| opencode（OPC） | — | — | **无行** | `quota_list` 没有任何 `openai-compatibility` 行 |

- `model_advice`：`commandcode/*` 8 个模型 `0 blocked, 3 without valid readings`（`commandcode/z-ai/glm-5.3-flash` 为 1）；`op-*` 9 个模型 `0 blocked, 3 without valid readings`。`recommended = gemini-3.1-flash-image`，gemini / claude 排序不受影响（但 claude 7 个模型本次读到 `1 blocked, 1 without valid readings`、`credentials_usable=0`——这是 claude 自身的冷却状态变化，与本期无关，见 before 对照）。
- `providers_list`：`openai-compat-3` name=`opencode`、`endpoint_host=opencode.ai`；`models_list` 同一条目 9 个模型、路由 `openai-compat-3`。与实现假设一致。

### 凭证来源核对（C-22 前置）

- **CMC 三个 auth 确实在 `/credentials`**（`oauth_credentials_list` 与 `quota_list` 都列 `provider=commandcode`，均为 `active`、各自独立 auth_index），因此实现复用 `AuthFiles`（`service.go:148-181`）与需求原文「CMC/OPC 都不在 /credentials」有偏差，但**与线上事实一致**，判通过并在 S13 报告里已记录。
- **OPC 不在 `/credentials`**：`oauth_credentials_list` 13 条里没有任何 openai-compatibility。实现靠 `OpenAICompatibility()`（v0 `/openai-compatibility`，带 `api-key-entries[].auth-index`）取索引，legacy `api-keys` 再回退 v8 `ConfigView`（`roster.go:66-129`）。**线上 OPC 是哪种形态仍未实测**（MCP 的 `providers_list` 不暴露 key 条目），见部署后复核清单。
- **未打印密钥、账号原文或 auth_index 之外的凭据材料**；MCP 只读层不返回 key。`config_read` 只返回 routing 三项，不含 `api-keys` 树。

## 四、真实值核对结果

**未实测。** 原因：第二期未部署，线上读不到 CMC/OPC 的新窗口；本期也没有用真实 key 直接调上游（生产凭据、生产数据是红线）。C-20/C-21 的夹具结果**不能当作** C-24 的线上实测。部署后必须人眼对一次，见下一节。

## 五、部署后需复核清单

1. **C-22**：`quota_list` 里 `commandcode` 三个 auth 各自有 1–3 个窗口、`opencode` 至少 1 行。窗口数与上游返回一致，不用 0 填充。
2. **C-23**：`commandcode/*`、`op-*` 不再是 `N without valid readings`；`credentials_usable > 0`、`tightest_window` 有值；gemini/claude 推荐不回归。
3. **C-24 真实值**：同一分钟内对比 OMC `quota_list` 与上游页面/接口，used ≤ 1pp、reset ≤ 2min。
4. **OPC 条目形态**：线上 `openai-compatibility` 是 `api-key-entries`（带 auth-index）还是 legacy `api-keys`。若两者都解析不到索引，OPC 不进名册；**这是最可能的首读失败点**。
5. **CMC subscription 形状**：`commandcode.go:116-125` 以顶层 `success` 为门槛；真实返回若无该字段或大小写不同，只丢 plan/currentPeriodEnd，rate-limit 两窗口仍在，monthly 会退化为「剩余 credits」note。
6. **OPC 精确 base-url**：`/zen/go/v1/usage` 来自 2026-11-12 的仓库记录，不是本轮线上实测；allowlist 精确匹配，配置漂移会 fail closed（`called==0` 已由测试钉住）。部署后确认配置里的 base-url 能拼出被放行的 URL。
7. **plan 表覆盖**：线上 3 个 CMC auth 的 planId 是否在表内（`individual-goat`/`individual-pro`/`individual-pro-v1`/`individual-provider`/`individual-max`/`individual-ultra`/`teams-pro`）。表外 plan 只给剩余 credits、无百分比——这是预期行为，不是缺陷。

## 六、范围外观察（只记录，不要求本轮修改）

- **O-A1 `kind: rolling` 在凭据额度页没有本地化标签。** 预测面板有 `dash.prediction.window.rolling`，但 `quotaWindowLabel`（`web/src/pages/quota/quotaFormat.ts:138-145`）没有 `rolling` 分支，回落显示 reader 给的原文「滚动用量 (Rolling)」。属实现质量小瑕疵，不影响 B1 任何验收点；第二期的 web 改动本来就只加类型联合与三份 i18n。
- **O-A2 `commandcode`/`opencode` 没有自己的 credential floor。** 两者落到共享 10 分钟 floor 而不是 idle 2h / normal 30min 计划间隔（`internal/config/config.go:246-262`）。实际读取频率仍受计划间隔约束，成本不超预期（每轮 CMC 3 auth × 2 + OPC N key × 1）；若后续想让它们更省上游，可在 `DefaultQuotaCredentialFloorByProvider` 单列。
- **O-A3 月度分母在「剩余 > 套餐额度」时取自剩余值。** 探针显示 remaining=90、GOAT 额 70 时 `limit=90`、`used=0`，note 仍写「分母来自厂商套餐表（GOAT）」。公式 `pool=max(allowance,remaining)+purchased` 抄自厂商 CLI，行为一致、`used=0` 也不伪造消耗；只是那种情况下 note 的措辞与实际分母略有出入，属文案精度问题。

## 七、未实测 / 未覆盖

- **C-22/C-23/C-24 的线上部分**：第二期未部署，全部记未实测，见上两节。
- **CMC/OPC 真实上游载荷**：`/alpha/billing/credits`、`/subscriptions`、`/usage` 的形状来自插件源码与交接，未用真 key 实测。
- **OPC 线上 key 形态、数量与 base-url**：MCP 只读层不暴露，部署后按清单第 4、6 条核对。
- **界面的 CMC/OPC 真渲染**：`check:ui --scenario dashboard-quota-prediction` 用的是 mock API 夹具，不含 CMC/OPC 行；它们的真实渲染要等部署后在额度页看（属 C-22/C-24 的部署后部分）。
- **本期不改代码、不推送、不合并、不部署**；临时验收探针放在工作区外部的 `.tmp-accept-probe/`，用 `go test -overlay` 注入，跑完已删除，不进 git。
- `check-docs` / `test:self` 的基线既有红已用 `git archive 86f7661c` 导出基线复现（同断言、同失败项），不记为本期回归。

## 八、独立验收探针（未提交，可重放）

4 个探针文件，通过 `go test -overlay` 注入 `internal/quota` 与 `internal/operations`，不落仓库：

```text
TestAcceptanceProbeCMCThreeAuths              PASS  C-20
TestAcceptanceProbeOPCKeys                    PASS  C-21 / C-25(请求次数)
TestAcceptanceProbeOPCErrors                  PASS  R-01（与 CMC 逐条对比）
TestAcceptanceProbeCapabilityAndUnitBits      PASS  O-02 / O-03
TestAcceptanceProbeMonthlyDenominatorMatchesTheNote PASS  R-02 的边界输入
TestAcceptanceProbeCMCAdviceStopsBeingUnread  PASS  C-23 的离线等价
```

## 附：逐条核对索引

| 要求 / 用例 | 结论 | 主要证据 |
|---|---|---|
| allowlist：CMC 前缀 + OPC 精确端点 | 通过 | `service.go:59-114`；`allowlist_test.go`；`opencode_test.go:195-218` |
| 凭证来源：CMC `/credentials`、OPC v0/v8 回退 | 通过（CMC 偏差已记录） | 线上 `oauth_credentials_list`/`quota_list`；`roster.go:31-190`；`roster_test.go` |
| CMC 解析/映射 + 月度分母标注 | 通过 | `commandcode.go:98-256`；`commandcode_test.go`；探针 C-20 |
| OPC 解析/映射（percent 0–100、rolling nil） | 通过 | `opencode.go:118-196`；`opencode_test.go`；探针 C-21 |
| 调度/成本：自动刷新、退避、禁用、1–3 次上游 | 通过 | `quota_auto_refresh.go:104-230`；`quota_scheduler.go:110-300`；`quota_roster_test.go`；探针 C-25 |
| 名册：quota_list / model_advice / quota_prediction | 离线通过；线上未实测 | `management_quota.go:38-77,848`；`model_advice_credentials.go`；`quota_prediction.go:216-330`；`model_advice_test.go`；探针 C-23 |
| S16 R-01/R-02/O-01～O-03 | 通过 | `failure.go:14,64-68`；`commandcode.go:47-61,209-254`；`service.go:210-218`；`quota.go:45-49` |
| 不改 CPA fork / 不含 B3 | 通过 | `git diff --name-only 86f7661c..HEAD` |
| 未实测如实标注 | 通过 | 本报告第三、四、七节 |
