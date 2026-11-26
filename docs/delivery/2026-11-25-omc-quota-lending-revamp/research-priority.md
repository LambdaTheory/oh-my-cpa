---
type: research
slug: omc-quota-lending-revamp-priority
status: review
updated: 2026-11-25
dispatch: works 2026-11-25#T6-S2
summary: "CPA 凭证优先级（priority / weight）的真实调度规则，以及 OMC 现在能显示、修改什么；只陈述事实，不写方案。"
---


# CPA 凭证优先级调研（T6-S2）

## 1. 结论摘要

读的是 CPA `c3ce22a2`，对照 v7.3.5、v8.0.74，调度核心一致。priority 数大先选，只有最高就绪档有流量，同档才由策略分流；weight 仅 weighted-round-robin 读；同模型登记在多个 provider 时「最高档」跨 provider；亲和绑定盖过优先级。OMC 能显示、编辑、写入 priority 与 weight，无批量与回滚；额度拆借页数据里没有当前优先级。

## 2. 六个问题

### Q1 优先级怎么起作用

数大先选，只有最高就绪档有流量；weight 仅加权策略读；多 provider 同模型时跨 provider 比档；亲和绑定（TTL 1 小时，命中续期）盖过优先级；写入后下一次选号生效。（附录 A、B；未核实：线上版本与冷却值）

### Q2 存在哪、哪些凭证能设

文件凭证、插件认证文件、API key 条目都能设 priority 与 weight；兼容条目 priority 只在条目级、key 只有 weight；插件虚拟凭证不能直接改。（附录 C；未核实：线上各凭证属哪类）

### Q3 OMC 能显示、能改什么

列表行显示、抽屉可编辑，经 `PATCH /management/auth-files/fields` 写入（校验、审计、回读）；无批量、无旧值快照与回滚；额度拆借页数据无当前优先级（附录 D；未核实：写入限流）。

### Q4 数值的几种读法

①不同档：高档独占，低档闲置到高档 429；②同档靠 weight：仅加权策略生效；③只改一张：看对方现值；④留着的卡 weight 0 退出。数值相对池外凭证生效，每次重置后要再改。（附录 E；未核实：套餐大小、需求）

### Q5 线上调度配置

OMC 能读、显示、写 `routing.strategy`；`session-affinity` 只在配置页 schema。CPA 默认 round-robin、亲和关。记录里的目标配置是加权加亲和关，线上是否生效未核对（附录 F）。

### Q6 边界与已有保护

边界原文出自模型分组决定，没提写优先级；一期 q01 选「只提示」，老板未答；二期暂缓、属红线，草稿要求快照、回滚、熔断；OMC 现无旧值快照与回滚。（附录 F；未核实：草稿是否并入 dev）

### 给产品的三个要点

1. 数值是相对的：priority 在同模型所有 provider 的就绪凭证间比，未设为 0，要对池外现值定。
2. weight 仅加权策略读，策略是全局开关；否则只有 priority，硬切档。
3. CPA 只看 429 不看额度百分比，亲和盖过优先级，重置后先后翻转，需再给数。

## 3. 坑和该马上处理的事

1. 拆借页数据无当前优先级，列表把 0 当缺失。
2. 抽屉提示「同优先级内再按权重分配」对非加权策略不成立。
3. 写入无旧值快照与回滚，审计不记值。
4. 线上版本不明：`c3ce22a2`、v7.3.5、v8.0.2 三说并存。
5. 二期 D3：草稿称已定，授权记录称未答。

## 4. 待确认

- 线上 CPA 版本与 `routing.*`：缺线上只读核对。
- 池外同模型凭证、两卡 websockets 开关：缺线上凭证列表。
- 套餐大小与真实需求：缺用量数据。
- D3 是否有老板答复：缺确认。

## 5. 附录

### 附录 A 读了哪个版本、怎么读的

- 源码：本机 `E:/works/CLIProxyAPI`，只读。`git branch --show-current` 为 `codex/v8-dokploy-update`；`git log -1` 为 `c3ce22a2`（2026-09-30）；`git describe --tags --always` 为 `v8.0.4-32-gc3ce22a2`；`git status --short` 只有未跟踪目录，已跟踪文件无改动。该提交不是 `v8.0.74` 的祖先（`git merge-base --is-ancestor HEAD v8.0.74` 结果为否）。
- 项目记录写：部署分支 `codex/v8-dokploy-update` 的最后提交就是 `c3ce22a2`，v8.0.74 升级「本地完成、未上线」；同一份记录也写「线上精确版本仍未核实」（助手仓库 CPA 项目页）。本调研没有核对线上版本（红线：不读线上）。
- 对照版本：`v7.3.5`（2026-09-17，OMC 的 `docs/cpamc-parity.md` 调度语义表所写版本）与 `v8.0.74`（2026-11-16，checkout 里最新的 tag）。只用 `git show <tag>:<路径>`、`git grep <tag>` 读，没切分支、没动工作区。
- 三个版本里逐项比对、完全一致：`authPriority` 函数体；`rebuildIndexesLocked` 里 `priorityOrder` 按 `>` 降序；`pickMixedWithStrategy` 里 `priorityReady > bestPriority`；常量 `quotaBackoffBase` 1 秒、`quotaBackoffMax` 30 分钟、`minQuotaCooldownFloor` 10 秒、`transientErrorCooldown` 1 分钟；`credentialweight.Default` 1、`Max` 1,000,000。
- 三个版本之间和优先级有关的差异：
  - `sdk/cliproxy/auth/priority.go`（`ApplyAuthPriorityMetadata`）在 v7.3.5 不存在，文件凭证的 priority 读取内联在 `CPA/internal/watcher/synthesizer/file.go`，语义相同；插件认证文件分支在 v7.3.5 只读 weight，不读 priority，HEAD 与 v8.0.74 都读。
  - `session-affinity-rebind-on`（`any` / `quota` / `never`）v8.0.74 有，HEAD 没有。
- 路径写法：CPA 仓库里 `internal/` 开头的路径前面加 `CPA/`（相对 `E:/works/CLIProxyAPI`），和 OMC 自己的 `internal/` 区分；`sdk/` 开头的是 CPA 的，OMC 没有同名目录。
- 没有跑 CPA 测试，没有跑任何改数据的命令。下文标「（推断）」的是读代码推出来、没有运行验证的结论。

### 附录 B Q1 详述：优先级怎么起作用

读的是 `c3ce22a2`（见附录 A）。「档」指同一 priority 数值的一组凭证；「就绪」指没被禁用、冷却、额度耗尽、令牌过期、认证失败，且登记了所请求的模型（`supportedModelSetForAuth`）。下面的 `sdk/cliproxy/auth/` 路径省写前缀。

**a) 取值与方向**

| 项 | 事实 | 出处（文件加符号） |
|---|---|---|
| 类型与默认 | Go `int`，以字符串属性 `priority` 保存，读取时 `strconv.Atoi`；未设、空串、解析失败（含超出 int 范围）都当 0 | `selector.go` 的 `authPriority` |
| 负数、上限 | 负数合法；代码里没有下限、上限校验 | 同上 |
| 方向 | 数大的先选：`priorityOrder` 按 `>` 降序，`highestReadyPriorityLocked` 取第一个仍有就绪凭证的档；示例配置写 larger values take precedence | `scheduler.go` 的 `rebuildIndexesLocked`、`highestReadyPriorityLocked`；仓库根 `config.example.yaml` |
| 认证 JSON 顶层 `priority` | 只认 JSON 数字（`int(float64)`，小数被截断）或能 Atoi 的字符串；布尔、null、非整数字符串被忽略，等于没设 | `priority.go` 的 `ApplyAuthPriorityMetadata` |
| 管理接口写入 | 数字、json.Number、能 Atoi 的字符串；非法值会删掉属性（回到 0）；值为 0 也删属性 | `CPA/internal/api/handlers/management/auth_files_fields.go` 的 `syncAuthFilePriorityAttribute`、`authFileIntValue` |

**b) 档与档之间**

- 只有最高的就绪档有流量：`pickReadyLocked` 先 `highestReadyPriorityLocked` 定档，再 `pickReadyAtPriorityLocked` 在该档内选；低档只在高档没有任何满足条件的就绪凭证时才轮到。条件含：本次请求已试过的凭证排除、加权策略下权重须大于 0（`scheduledAuthPredicate`）。
- 例外一（Codex）：下游是 websocket 请求且 provider 为 `codex` 或 `xai` 时，`highestReadyPriorityLocked` 会优先找「开了 websockets 的凭证」所在的最高档，即使更高档只有未开 websockets 的凭证。两张卡 websockets 开关相同时不触发（推断：未运行验证）。
- 「不可用」的判定是 `selector.go` 的 `isAuthBlockedForModel`：禁用；有未授权失败标记（`hasUnauthorizedAuthFailure`）；访问令牌已过期；凭证级额度冷却（`credential_quota` 且恢复时刻未到）；该模型的状态为不可用或额度耗尽且重试时刻未到。
- 冷却时长（`conductor_cooldown.go` 的 `MarkResult` 一带，常量在 `conductor_refresh.go`）：

| 触发 | 冷却 |
|---|---|
| 429 | 优先用上游给的重置时间（`Retry-After`；Codex 的 `usage_limit_reached` 从 `resets_at` / `resets_in_seconds` 解析，见 `CPA/internal/runtime/executor/codex_executor_terminal.go` 的 `parseCodexRetryAfter`），下限 10 秒；没有就从 1 秒起指数退避，封顶 30 分钟（`quotaCooldownAfterFailure`、`nextQuotaCooldown`） |
| Codex 用量上限（`usage_limit_reached`） | 按整张凭证冷却（所有模型一起），到上游声明的重置时刻（`newCodexStatusErrWithCooling` 的 `credentialScoped`，调度侧 `credential_quota`） |
| 401 / 402 / 403 | 30 分钟 |
| 404、模型不被支持 | 上游给了重试时间就用它，否则 12 小时 |
| 408、500、502、503、504、520 到 526，以及其它状态码（含普通 400） | 1 分钟（`transient-error-cooldown-seconds` 为 0；−1 为不冷却）。被判为请求级错误（`shouldSkipCredentialCooldown`）的不冷却 |
| 任意 | `disable-cooling` 为真（全局或凭证级，`cooldownDisabledForAuth`）则不进冷却 |

- 与助手仓库 `号池与路由.md` 第 5 节的出入：它写「普通 400 标记不可用，后续跳过」；这份代码里普通 400 走默认分支，1 分钟冷却后恢复。
- 高档恢复：`pickReadyLocked` 每次选号先 `promoteExpiredLocked`，凭证的重试时刻一过，下一个请求起它就回到就绪集；没有「留在低档」的滞回逻辑（看到的）。
- 额度百分比不参与调度：`quota_signals.go` 的被动额度快照注释写明「Cooldown and scheduling fields are never read or written here」。CPA 只在上游返回 429 之后才知道一张卡耗尽。
- 同一次请求内：已试过的凭证进 `tried` 被排除，失败后立即换下一张（先同档，同档用尽才到低档）；示例配置里 `routing.retry.request-retry` 为 3 个额外轮次，作用于 403、408、429、5xx。

**c) 同一档里**

| 策略 | 怎么选 | weight |
|---|---|---|
| `round-robin`（默认；写错或为空也回落到它） | 按认证 ID 字典序轮转，每张就绪凭证一次（`pickRoundRobin`、`scheduledSuccessorIndex`） | 不读 |
| `fill-first` | 档内认证 ID 字典序第一张就绪凭证，用到它不可用才换下一张（`pickFirst`） | 不读 |
| `weighted-round-robin` | 平滑加权轮询：每轮各自累加 weight，取最大者，再减去总权重（`pickSmoothWeightedScheduled`） | 只有它读；默认 1、最大 1,000,000；0 或负数归 0 并被排除，档内全是 0 时整档被跳过、流量落到下一档 |

- 认证 ID：文件凭证的 ID 是认证目录下的相对路径（`CPA/internal/watcher/synthesizer/file.go`），所以 `fill-first` 的「第一张」是文件名字典序，不是额度多少。
- 策略名别名：`wrr`、`ff` 等（`sdk/cliproxy/service_config.go` 的 `normalizedRoutingRuntimeState`）。

**d) 跨 provider**

- 是跨 provider 的，但只发生在「同一个模型 ID 登记在 2 个及以上 provider」时：`scheduler.go` 的 `pickMixedWithStrategy` 取各 provider 分片里最高就绪档，再取其中最大值 `bestPriority`，只在这一档选。只有 1 个 provider 时走 `pickSingleWithStrategy`，只和同 provider 的凭证比。
- 档内分流：`fill-first` 按 provider 顺序取第一个有就绪凭证的；加权策略把各 provider 该档的凭证并成一个池做平滑加权；默认 `round-robin` 按各 provider「该档就绪凭证数」切段，再在 provider 内轮转。
- 后果（看到的）：某个 provider 里有一张更高 priority 的就绪凭证，同一模型的其它 provider 的所有较低档凭证都拿不到流量，直到它不可用。助手仓库 `号池与路由.md` 第 3 节有同样的结论。
- priority 是凭证级属性，不分模型；竞争范围由「哪些凭证登记了这个模型」决定。

**e) 会话亲和**

- 默认关（`session-affinity: false`）。开启后，已有绑定且绑定的凭证仍就绪（任意一档）就继续用它；绑定凭证不就绪才由回落策略在「最高就绪档」重选并重绑（`selector.go` 的 `SessionAffinitySelector.Pick`；示例配置注释写「An established binding outranks credential priority」）。
- 绑定 TTL 默认 1 小时，每次命中都刷新（`GetAndRefresh`）：一个每小时内至少有一次请求的会话，不会因为改了优先级而离开原凭证；要等它空闲超过 TTL，或绑定的凭证变得不可用。没有会话标识的请求不走绑定。
- HEAD 没有 `session-affinity-rebind-on`；v8.0.74 有，`any`（默认）下任何凭证归因失败都解绑重绑，`quota` 只在额度与认证类错误时重绑，`never` 永不迁移。
- 线上是否开启：见 Q5，本调研没有核对。

**f) 改完怎么生效**

- 凭证的 priority / weight：管理接口 `PatchAuthFileFields` 改内存里的凭证后调 `Manager.Update`，里面立即 `scheduler.upsertAuth`，随后 `persist` 写文件（返回值被丢弃，写盘失败也会回 200，看到的）。下一次选号就用新值，不用重启，也不等文件监听；插件虚拟凭证（`IsPluginVirtualAuth`）回 409，不能直接改。
- `routing.strategy` 与会话亲和配置：配置热加载（文件监听去抖 150 毫秒）或管理接口写入后，`service_config.go` 的 `applyManagerConfig` 在路由状态变化时 `SetSelector` 换新的选择器。换选择器会新建轮转与加权状态，会话亲和的绑定缓存随新对象清空（推断）。
- config.yaml 里 api-keys 条目的 priority / weight：同样靠文件监听去抖后重载（推断）。

**没核实（Q1）**：线上运行的是否正是 `c3ce22a2`；线上 `routing.cooldown`、`routing.retry` 的实际值；`SetSelector` 后会话缓存是否真被清空；`persist` 失败的实际表现。

### 附录 C Q2 详表：凭证类型 × 能不能设

运行时 CPA 把有效值放在凭证的属性里（`priority`、`weight` 两个字符串属性，权重另可回落到元数据），调度只读这两处（`authPriority`、`authWeight`）。下表是「值从哪里写进来」。路径写法见附录 A。

| 凭证类型 | priority | weight | 存在哪 | 出处（文件加符号） |
|---|---|---|---|---|
| OAuth / 文件凭证（codex、claude 等，每张卡一个认证 JSON） | 能：顶层 `priority` | 能：顶层 `weight` | 认证 JSON 顶层 | `CPA/internal/watcher/synthesizer/file.go`（调 `ApplyAuthPriorityMetadata`、`ApplyAuthWeightMetadata`）；`sdk/auth/filestore.go` |
| 插件认证文件（插件把账号存成认证 JSON；助手仓库记录里 commandcode 插件的日志指向认证文件） | 能（HEAD、v8.0.74；v7.3.5 不读） | 能 | 认证 JSON 顶层，插件把原始 JSON 保留到元数据（助手仓库 `号池与路由.md` 第 3 节） | 同上文件的插件分支；v8.0.74 另见 `CPA/internal/pluginhost/auth_callbacks.go` |
| 插件虚拟凭证（属性 `plugin_virtual`） | 管理接口不能直接改，回 409，要改它的源认证文件 | 同左 | 由源认证文件派生 | `CPA/internal/api/handlers/management/auth_files.go` 的 `errPluginVirtualAuth`；`sdk/cliproxy/auth/types.go` 的 `IsPluginVirtualAuth` |
| API key 型：gemini、claude、codex、vertex 条目 | 能：条目级 `priority`；v8 配置里组级可设、key 级可覆盖，包括覆盖成 0 | 能：每个 key 的 `weight` | config.yaml 的 `api-keys.<provider>` | `CPA/internal/config/config_types.go` 的 `ClaudeKey`、`CodexKey`、`GeminiKey`；`CPA/internal/config/vertex_compat.go`；`CPA/internal/config/config_v8.go` 的 `expandV8Groups`、`sharedKeyFields` |
| openai-compatibility 兼容条目（opencode、Command Code 兼容条目等） | 只能在条目（组）级，同一条目下所有 key 共用一个值 | 能：每个 key 的 `weight` | config.yaml 的 `openai-compatibility` | `OpenAICompatibility` 的 `Priority`；`OpenAICompatibilityAPIKey` 只有 `api-key`、`weight`、`proxy-url`；`expandV8Groups` 对该族单独处理 |
| 插件实例配置 `plugins.configs.<id>.priority` | 不是凭证优先级：注释写「controls plugin startup and routing order」 | 无 | config.yaml 的 `plugins` | `CPA/internal/config/config_types.go` 的 `PluginInstanceConfig` |

- 只有 weight、没有独立 priority 的：兼容条目下的每个 key（priority 在条目级，key 之间不能分档）。
- 管理接口不能直接设的：插件虚拟凭证。
- Ollama：助手仓库记录里它是一个 openai-compatibility 条目（另有一个被禁用的认证文件），按兼容条目算；线上实际形态没核实。OMC 还有「Ollama 账号」一类自己的概念，是否对应 CPA 凭证没有核对。
- 没逐个核对的 provider（gemini-cli、antigravity、kimi、qwen 等 OAuth 类）：它们走同一个文件合成路径，推断同样支持顶层 `priority`、`weight`。
- 经管理接口写 weight 时：必须是 JSON 数字，`credentialweight.ParseValue` 校验，大于 1,000,000 报错，0 或负数归 0，null 删除该字段（`auth_files_fields.go` 的 `PatchAuthFileFields`）。

**没核实（Q2）**：线上各凭证实际属于哪一类（插件认证文件还是虚拟凭证，兼容条目还是别的）；gemini-cli、antigravity、kimi、qwen 等 OAuth 类没有逐个读；OMC 的「Ollama 账号」是否对应 CPA 凭证。

### 附录 D Q3 详述：OMC 现在能显示、能改什么

读的是 dev 基线 `7b1fbb27`。

**显示**

| 位置 | 看到的 | 出处（文件加符号） |
|---|---|---|
| OAuth 管理页列表行 | 有值时显示「优先级 n」「权重 n」两个标签；值为 0 时不显示 | `web/src/pages/oauthManagement/OAuthCredentialRecord.tsx`；DTO 的 `Priority`、`Weight` 带 `omitempty`（`internal/api/management_auth_files.go`，投影在 `management_auth_file_projection.go`） |
| 值的来源 | CPA 凭证列表 `GET /credentials`（OMC 的 `management.AuthFile`，两字段无 `omitempty`）→ OMC `GET /management/auth-files` | `internal/cpa/management/client_auth_files.go`；`internal/api/handler.go` 的 `listManagementAuthFiles` |
| CPA 侧何时给值 | priority 只在显式设置且非 0 时出现（写 0 会删属性）；weight 只在显式设置时出现，显式 0 也会出现 | `CPA/internal/api/handlers/management/auth_files.go`（列表条目构造、`authWeightValue`）；`auth_files_fields.go` 的 `syncAuthFilePriorityAttribute` |
| 详情抽屉 | 优先级、权重两个输入框；初值先取 safe-fields（指针字段，保留 0），没有就用 `file.priority ?? 0`、`file.weight ?? 1` | `web/src/components/authFiles/AuthFileDetailDrawer.tsx`；`GET /management/auth-files/safe-fields` |
| 排序键 | 下拉有 `priority-desc`「优先级（高到低）」、`weight-desc`「权重（高到低）」，另有 `reset-asc`；缺 priority 当 0、缺 weight 当 1，并列按名称 | `web/src/pages/oauthManagement/OAuthManagementPage.tsx`；`oauthWorkspaceLogic.ts` 的排序函数 |
| API key 型 provider | 编辑抽屉有条目「优先级」与每个 key 的「调度权重」；表格名称下显示「优先级 n」 | `web/src/components/providers/ProviderEditorDrawer.tsx`、`ProviderTable.tsx` |
| 插件配置 | 插件抽屉可设宿主键 `priority`（整数） | `web/src/components/plugins/PluginConfigDrawer.tsx`、`pluginConfigForm.ts` |
| 仪表盘 | 只显示路由策略名，不显示 priority、weight | `web/src/components/dashboard/routing/RoutingPanel.tsx` |
| 没有的 | 卡片视图、筛选里没有 priority、weight | `OAuthManagementPage.tsx` |

- 列表层的偏差（看到的加推断）：列表 DTO 把 0 当缺失、排序把缺 weight 当 1，所以显式 weight 0（CPA 的加权策略下会被排除）在列表里没有标签、排序里当 1 处理；抽屉的 safe-fields 读取能区分。

**编辑与写入通路**

- 抽屉：优先级为整数，范围正负 `MAX_SAFE_INTEGER`；权重为整数，最大 1,000,000，输入框没设下限（界面允许输入负数，OMC 服务端把 0 及以下改写成 0）。提示文案原文：优先级「数值越高越先被调度；同优先级内再按权重分配。」；权重「仅 weighted-round-robin 策略使用；0 或负数会被 CPA 归零并从加权调度中排除。」（`web/src/i18n/index.tsx`，`af.field_priority_hint`、`af.field_weight_hint`）。
- 保存只发与基线不同的字段；保存前没有二次确认（唯一的确认框是「放弃未保存的修改」）；`runtime_only` 凭证整体禁用；能不能编辑取决于 `canEditFile`（文件型且目标唯一，`oauthWorkspaceLogic.ts`）。
- 通路：前端 `patchManagementAuthFileFields` → OMC `PATCH /management/auth-files/fields`（`internal/api/management_auth_file_fields.go` 的 `patchManagementAuthFileFields`、`applyManagementAuthFileFields`）→ `internal/cpa/management/client_auth_files.go` 的 `PatchAuthFileFields` → CPA `PATCH /credentials/fields`（v8 路径）。priority、weight 以整数写入；null 会原样透传（CPA 侧 weight 的 null 为清除；priority 的 null 先写进元数据，随后 `syncAuthFilePriorityAttribute` 删掉属性，等于回到 0）。
- 写入校验：写串行（`providerWrites`，忙时 503）；请求体上限 256 KiB；字段白名单共 12 项；priority 须为安全整数；weight 须为整数，0 及以下改写成 0，超过 1,000,000 拒绝；带 `auth_index` 时名称对不上返回 409。
- 回读校验：PATCH 之后再读 CPA 凭证列表，比对 priority 与 weight，不一致返回 502「field update could not be verified」（`managementAuthFileFieldMismatch`）。读的是列表里的值，列表与调度器都出自 `Manager.Update` 之后的同一份凭证（推断）。
- 审计：`auth_file.fields_update`，记 `attempt`、`failure`、`success`（带 `verified: true`），详情只记字段名，不记新旧值；写前那条审计写不进去就中止。
- 权限：路由挂在 `requireConsoleOrCapability` 之下，走控制台登录会话；没有找到角色或 scope 分级（推断：登录即可写）。
- Agent / MCP 通路：能力 `oauth_set_credential_fields`（`internal/operations/oauth.go`，元数据标 write、high）→ `UpdateCredentialFields`（`internal/api/agent_wiring.go`），同样走 `providerWrites` 串行，另加 `CheckCredential` 的版本守卫；文档写明写操作要预览加人工 Allow / Deny（`docs/agent-capabilities.md`）。
- 批量：后端没有批量设置接口，只有单文件 `fields`。前端批量栏只有启用、停用、刷新、删除，循环调用单文件接口。

**额度拆借页数据里有没有「某张卡现在的优先级」**

- 没有。响应 DTO `lendingCardDTO`（`internal/api/quota_lending.go`）的字段是 auth_index、name、role、窗口信息、剩余百分比、重置时刻、观测时刻、可比性与原因码等；输入层 `quota.LendingCardInput`（`internal/quota/lending.go`）同样没有；两个文件对 priority、weight 的 grep 零命中。
- 后端哪一层拿得到（只答位置）：① `quotaRosterFiles`（`internal/api/management_quota.go`）手里的 `[]management.AuthFile` 本来就带 `Priority`、`Weight`，投影成 `QuotaItemDTO` 时没带；② 现成的 `GET /management/auth-files` 列表 DTO 与 safe-fields 读取。opencode 兼容 key 产出的条目是否带这两个字段，没核实。

**没核实（Q3）**：`byName`、`requireAuthentication` 的内部实现；是否有针对这条写入的限流（只看到登录失败限流）；供应商抽屉（API key 型条目）写 priority、weight 的范围校验没逐行读。

### 附录 E Q4 推演：两张卡的例子

**数据时刻**：下面只用 11/23 09:00 的快照做推演，不是今天的建议。卡 B 在 11/25 11:26 已经回满，今天线上两张卡的数字都不同（本调研没有读线上数据）。两张卡都按 Codex 凭证（provider 为 `codex`）算。

| | 卡 A | 卡 B |
|---|---|---|
| 周窗口剩余 | 88% | 40% |
| 回满时刻 | 11/27 16:24 | 11/25 11:26 |
| 距回满（自 11/23 09:00） | 4.31 天 | 2.10 天 |
| 刚好在回满前用完所需的日速度（剩余 ÷ 距回满） | 20.4 个百分点/天 | 19.0 个百分点/天 |

**粗算假设**（为了算数，不是事实）：H1 两张卡周窗口一样大、「剩 x%」同一尺度，真实套餐大小不同时要折算，没有数据；H2 总请求需求 D 恒定、均匀，每天 D 个百分点，取 D=20 与 D=40 两档（两卡日速度之和约 39.5）；H3 没有会话亲和、没有 5 小时窗口限制、没有重试稀释；H4 只推演 11/23 09:00 到 11/25 11:26 这一段（2.10 天），B 回满之后先后关系会翻转，见各读法末条。

**读法 1：两张卡放在不同档**

- 请求怎么走：高档卡独占流量，低档卡没有流量；三种策略都先取最高档（附录 B）。高档卡返回 429 后，同一次请求立即转到低档卡，高档卡冷却到上游声明的重置时刻（Codex 用量上限是凭证级冷却），重置后第一个请求起自动切回高档，不用再改。
- 额度消耗（粗算，到 11/25 11:26）：

| | D=20 | D=40 |
|---|---|---|
| 卡 B 在高档 | B 用 40（11/25 09:00 用完），A 约用 2、剩约 86；B 回满，无作废 | B 在 11/24 09:00 用完，A 接手约用 44、剩约 44；B 回满，无作废 |
| 卡 A 在高档 | A 约用 42、剩约 46；B 没用，40 在回满时作废 | A 约用 84、剩约 4；B 没用，40 在回满时作废 |

- 什么情况失效：会话亲和开着时，已绑定的会话留在原卡（TTL 1 小时，每次命中续期）；下游是 websocket 且两卡 `websockets` 开关不同时，Codex 会先挑开了 websockets 的卡，可能越过档位；`priority` 写成非整数字符串会被忽略；CPA 不看额度百分比，高档卡直到出现 429 才让位，耗尽那一刻多一次失败重试；Codex 的 5 小时窗口触发 429 时同样冷却到上游声明的重置时刻（推断：代码只读 `resets_at`、`resets_in_seconds`，不区分窗口），期间流量暂落低档，之后自动切回。
- 对池外的影响：priority 是凭证级、不分模型，比较范围是「同一模型下所有 provider 的所有就绪凭证」，未设等于 0。池内先用的卡设到池外最高值之上，池外的同模型凭证全部排到它后面；池内两张都设成大于 0 而池外是 0，池外凭证被全部挡住；设成负数则排在所有未设的凭证之后。所以数值要相对池外所有凭证的现值来定，而额度拆借页现在读不到这些现值（Q3）。
- 何时再改：B 回满的 11/25 11:26 前后——B 回到 100%、约 7 天后才再回满（推断：周窗口），A 约剩 86%、2.2 天后回满，先后关系翻转；A 回满的 11/27 16:24 前后同理。不改的话，高档卡每次回满后继续独占。

**读法 2：同档，只靠 weight 分流量**

- 请求怎么走：只有 `routing.strategy` 为 weighted-round-robin 才读 weight（平滑加权轮询，份额 = 自己的权重 ÷ 同档就绪凭证权重之和）；默认的 `round-robin` 均分，`fill-first` 全给档内文件名字典序第一张，两者都忽略 weight。策略是全局的，一个开关管所有池。
- 额度消耗（粗算，D=20，到 11/25 11:26）：权重按剩余 88:40 → 份额 69%/31%：A 约用 29（剩约 59），B 约用 13（剩约 27，回满时作废）；权重按日速度 20.4:19.0 → 份额 52%/48%：A 约用 22（剩约 66），B 约用 20（剩约 20，作废）。要让 B 在回满前正好用完，B 的份额须约 95%。D=40 时份额取日速度比 52%/48%，两张约在各自回满前用完。
- 什么情况失效：策略不是加权（见上）；会话亲和下已绑定的会话不按份额走，实际分流取决于会话寿命；失败请求转到另一张卡，份额被稀释；一张卡冷却或耗尽后全部流量给另一张，不管权重；weight 0 是整张卡退出，同一次请求的故障转移也不会用它。
- 对池外的影响：份额在「最高档里所有就绪凭证」之间算，池外同档、默认权重 1 的凭证也在分母里：池内设 88:40 时，一张池外凭证只剩约 0.8% 的份额（1÷129）。weight 不改变档位，池外较低档的凭证不受影响。
- 何时再改：两张卡各自回满后，剩余与距回满都变，日速度比随之变，权重要重新算；B 回满后的日速度约 14.3 个百分点/天（100% ÷ 7 天，推断）。

**读法 3：只改其中一张，另一张保持现值（相对现值的偏移）**

- 请求怎么走：结果完全取决于对方卡与池外凭证的现值。priority 改后大于对方 → 改的这张在高档（读法 1）；小于 → 对方在高档；相等 → 同档，按策略（均分、文件名第一张或按权重）。weight 只改一张时，份额 = 新权重 ÷ （新权重 + 对方现权重），未设的权重按默认 1。
- 额度消耗：同读法 1、2 的对应行；偏移量本身没有物理意义，起作用的是改完后的相对大小。
- 什么情况失效：现值读不准——列表层把 priority 0 当未设（与 CPA 一致），weight 未设（1）与显式 0（排除）在列表层看不出区别，只有抽屉的 safe-fields 读取分得出（Q3）；写入没有比较并交换，两次读写之间对方被别处改了，OMC 发现不了（二期设计草稿 strategy-model 第 5.1 节把没有 compare-and-set 列为现状限制；Agent 路径有 revision 守卫）；审计只记字段名不记旧值，改回去要靠别处留的旧值；其余同读法 1、2。
- 对池外的影响：同读法 1、2；「相对现值加 n」不保证高于池外最高值。
- 何时再改：同读法 1、2；想恢复就需要原值（priority 写 0 等于删属性，与未设同义）。

**读法 4（补充）：留着的那张退出，只让先用的那张接流量**

- 请求怎么走：加权策略下 weight 设 0，整张卡从候选里排除（既不接流量，也不当同档兜底，也不当同一次请求的故障转移对象）；`round-robin`、`fill-first` 下 weight 无效，只有禁用（disabled）能让它退出，OMC 里是 `PatchAuthFileStatus` 那条写入。
- 额度消耗：先用的卡独占全部需求，留着的卡额度不动，到它回满时作废。
- 什么情况失效：策略不是加权时 weight 0 没有效果；先用的卡耗尽时没有后备，请求直接失败（429 或 auth_unavailable），除非另有同模型的凭证。
- 对池外的影响：只影响这张卡，池外凭证不受影响。
- 何时再改：留着的卡要重新参与时（回满前后）必须改回，否则一直闲置。

**没核实（Q4）**：两张卡的真实套餐大小与真实请求需求速度（粗算全靠上面的假设）；两张卡的 `websockets` 开关；池外是否有同模型凭证及其现值；卡 B 回满后的下一个重置时刻（按周窗口推断）；Codex 5 小时窗口的数值（与 OMC 页面的窗口口径没有对照）。

### 附录 F Q5、Q6 出处

**Q5：`routing.strategy` 与 `session-affinity`**

| 项 | OMC 里读 | OMC 里显示 | OMC 里写 | 出处（文件加符号） |
|---|---|---|---|---|
| `routing.strategy` | 能：`ConfigScalars` 读存储配置里的 `routing.strategy`，没写时补默认 `round-robin`（注释说明该默认值在 CPA v8.0.2 上测过） | 能：仪表盘路由面板显示策略名；配置页有 `routingStrategy` 三选一 | 能：`UpdateConfigScalar`，校验接受三种策略并归一别名（rr、wrr、ff 等）；Agent / MCP 的配置键里也有 `routing_strategy` | `internal/cpa/management/client_config.go`；`internal/operations/config_validation.go`、`config.go`；`web/src/components/dashboard/routing/RoutingPanel.tsx`；`web/src/types/configSchema.ts` |
| `session-affinity` | 不在 `ConfigScalars` 的已知标量里，也不在 Agent 的配置键里 | 只在配置页的可视编辑 schema 里：`routing.session-affinity` 开关、`-ttl`、`-subagents`、`-rebind-on`；配置里没写时，页面显示的是 OMC schema 的默认值，不是 CPA 运行时的值（推断） | 配置页保存配置时可写 | `web/src/types/configSchema.ts`；`docs/cpa-v8-compat.md` |

- CPA 默认值：`routing.strategy` 为 `round-robin`（写错或为空也回落到它）；`session-affinity` 为 false；`session-affinity-ttl` 为 1 小时；`session-affinity-subagents` 为 true（`sdk/cliproxy/service_config.go` 的 `normalizedRoutingRuntimeState`；`config.example.yaml`）。
- 策略是全局的：CPA 只有一个选择器管所有凭证池（`applyManagerConfig` 里的 `SetSelector`），切换会影响所有模型、所有凭证。
- 记录里的目标配置：助手仓库 `projects/cliproxyapi/notes/号池与路由.md`（版本 3，核实日期 2026-11-10）第 3 节「改法」写的是：

```yaml
routing:
  strategy: weighted-round-robin   # 相等权重时等价于 round-robin；想按套餐分配就用权重
  session-affinity: false          # 共享池不要粘会话；开了会 1h 内固定同一个号
```

- 线上是否已生效：该节是「改法」，不是核实结果，文中没有写已生效；文档开头写明生产改动要老板点头。**线上是否已生效，本调研没有核对。** 同一文件第 8、9 节（2026-11-10 复核）记录兼容条目里权重 0 的两个 key 被排除、权重 10 的两个在出流量，权重 0 被排除只会发生在加权策略下，所以那天线上像是加权策略（推断，不是核实）。

**Q6：边界与已有保护**

下面引文里的人名一律换成了角色（老板）。

- 项目页原文（助手仓库 `projects/oh-my-cpa/AGENTS.md`「定义」一节）：「边界：不做自动路由，不进 CPA 请求路径（2026-10-30 定）。」来源决定是同项目 `decisions/2026-10-30-模型分组缩范围.md`，原文：「澄清后范围从「组内额度降级路由」缩为「只做分组 + 按组推荐」，不做自动路由、不进请求路径；降级路由要 CPA 插件 + `model.route`，不在本期。」这条决定讲的是模型分组的范围，没有提额度拆借或写优先级。一期调研 `research.md` 的 A45 引用它时写成「边界」节，实际在「定义」节下。
- 二期的项目页表述（同一文件）：二期（让系统真去调流量，N1123-09）托管期间暂缓（2026-11-25），二期会动真实网关，属红线，开工前必须老板本人点头（人名已换成角色）。托管授权决定把「额度拆借二期（让系统真去调流量）、真机演练、总开关」列在需要老板本人的红线动作里。
- 一期 q01 原文（助手仓库 `inbox/agents/产品/2026-11-23-omc-quota-lending/decide-q01.md`）：问题「q01 是否把建议落成 CPA 路由权重（priority/weight）是红线」。三个选项：「只提示，不改路由（推荐）」——「对线上请求零影响；代价是照建议做仍要人工调流量」；「本期再提供「人工确认后写入 priority/weight」」——「改的是线上所有请求，要额外设计回滚、策略兼容、会话亲和影响…不推荐本期做」；「按建议自动、持续写权重」——「等于自动路由…不做」。理由：「weight 在 fill-first/round-robin 下还可能不生效、会话亲和会绕过。所以默认只做提示；要不要更进一步由老板定。」卡里没有老板的答复；一期按推荐「只提示」实现（一期 `impl-plan.md`：「红线，老板未答；本计划一律按「只提示、不写路由」写」）。
- 自动化档位（同目录 `decide-automation-tier.md`）：结论句「数据够做只读建议，不够做「按比例全自动改权重」」；D3 四档为「L2 + L3 窄（推荐）」「只 L2」「只 L1（现状）」「L3 广口径（不推荐）」；一期是 L1。二期预留项里：D4「不允许；非 weighted-round-robin 时只提供 priority 档」，D7 把边界改写为「不进请求路径、不改全局调度策略、每次写配置可预览/快照/回滚/审计/熔断」，D9 给出「驻留 30 分钟、死区 1 个百分点/天或相对 10%、每凭证每日 ≤4 次」。两处说法冲突：二期实现计划草稿写「D3=L2+L3 窄…均已定」，托管授权决定写二期「问了一天半没答复」；没有找到老板回答 D3 的记录（「已定」大概是产品按推荐落计划，推断）。
- 二期安全设计要点（只列要点；两份都是草稿，在 T7 的两个 worktree 里，不在本基线的 `docs/delivery/2026-11-23-omc-quota-lending/` 下，是否已并入 dev 没核实）：
  - T7 的 `11-23-t7-s10-codex` worktree、一期交付目录下的 `strategy-model.md`：第 5.1 节写出现状与三个限制（无比较并交换；回滚要自己留快照；自动化与字段写入没有共享锁）；第 5.3 节「人工确认后写入」的六条要求：预览、快照加比较并交换、回滚（建议 6 小时自动过期）、生效范围（影响该 provider 所有调用方）、值级审计、熔断；第 5.4 节：非加权策略下 weight 是空操作，priority 是硬切档，会话亲和使收敛取决于会话寿命，重试会稀释分流；第 5.5 节抖动五道闸：死区、驻留、迟滞、观察窗、自动过期；第 6.1 节缺口：字段审计无旧值。
  - T7 的 `11-23-t7-s14-codex` worktree、一期交付目录下的 `impl-plan-v2.md`：第 0 节「没有快照和比对就不写」「审计写不进就不许写」、写锁与账户自动化共用、熔断为连续 3 次失败或回读不一致需人工复位；第 2.3、2.5、2.7 节一次性预览（默认 5 分钟过期）、回滚遇比较不一致即拒绝、写入顺序「取预览、重读凭证比对、写 attempt、调 apply、写 success/failure」；第 3 节状态存 SQLite、不进 CPA 配置；第 4.1 节执行前四道前置（预览有效、总开关与池层级、池未熔断、`routing_strategy` 与预览时一致）；第 4.5 节策略为 weighted-round-robin 才写 weight，否则只写 priority、不改全局策略；第 8 节边界 ADR 草稿。
- OMC 现在改优先级已有的保护（逐条出处在附录 D）：写串行与忙时 503；请求体上限与字段白名单、类型与范围校验；名称与 `auth_index` 对不上返回 409；审计 attempt、failure、success，审计写不进就中止；写后回读 priority 与 weight，不一致返回 502；抽屉只发有变化的字段、`runtime_only` 凭证只读；Agent 路径另有 `CheckCredential` 的版本守卫与预览加人工 Allow / Deny。
- 没有的（在这条写入路径上没找到）：改前值快照与回滚入口；审计里的新旧值；比较并交换（控制台路径）；批量与熔断；写入前的二次确认（控制台路径）；角色或 scope 分级；针对这条写入的限流。配置写入的备份（ADR 0051）列举的写入种类里是 `credential_status`，没有凭证字段写入。
- 相关 ADR（`docs/adr/`）：0038（上游凭证按 v8 组写入，key 可覆盖组的 priority，包括覆盖成 0）、0051（每次配置写入都留可恢复备份）、0108 与 0114（账户自动化的保护模式）、0106（模型建议只读）；没有专门讲 priority / weight 字段写入的 ADR。一期验收用例 C-26、C-27 是红线用例（无 priority / weight 写入）。

**没核实（Q5、Q6）**：线上 `routing.strategy` 与 `session-affinity` 的实际值；`号池与路由.md` 目标配置有没有生效；二期草稿是否已并入 dev；ADR 0051 的备份是否覆盖字段 PATCH（代码层未核）。

## 变更范围

- 要改的文件：无，只读（本报告是唯一新增文件）
- 要不要建 Issue：否，只陈述现状；是否为额度拆借改版建 Issue 由产品方案决定
- 预计几轮：一轮；若产品要补查线上配置，另起一轮只读核对
- 走哪条路：自建小队（本报告无代码要改；后续实现由跟进者按产品方案再定）
