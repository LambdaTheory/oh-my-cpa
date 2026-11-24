---
type: impl-plan
slug: 2026-11-23-omc-quota-lending
status: draft
updated: 2026-11-23
prd: ./prd.md
dispatch: works 2026-11-23#T7-S3
摘要: "跨凭证额度拆借的显示层与建议层实现计划：后端在额度总览/详情只读返回同池偏用建议，前端补距重置倒计时、快重置标记、按重置排序与建议面板；按 P1 后端 / P2 前端并行拆分，q01 只提示不写路由"
summary: "Read-only cross-credential quota lending: backend projects same-pool prefer/avoid advice on the existing quota read; frontend adds reset countdowns, fast-reset tags, reset sort and an advice panel; split into P1 backend and P2 frontend"
---

# 实现计划：跨凭证额度拆借（显示层与建议层）

> 依据：同目录 `prd.md`（唯一验收标准，AC-1～17）、`research.md`（T7-S1 调研）、`requirements.md`；仓库 `AGENTS.md` §2/§5/§6/§7。本稿只写方案，不写业务代码、不改配置、不跑构建测试。计划正文不引用绝对行号，只写文件与符号。

## 评审入口

- 先读 §0 的一页结论，再看 §2 接口契约；同池口径在 §3；后端工作包在 §4，前端工作包在 §5；依赖与并行度在 §6；AC-1～17 对照在 §9。
- q02（只覆盖周期 ≥ 24h 的标准窗口）、q03（重置前日均可用 = 剩余% ÷ 距重置天数）、q04（快重置 = 距重置 ≤ 周期 1/3，剩余 ≥ 50% 为高）按 PM 采纳的产品推荐写。
- q01（是否写 CPA 路由权重）按「只提示、不写路由」写；本计划不设计权重写入、不新增写入口。

## 0. 一页结论

- **建议层不是另一份单卡状态。** 后端在现有额度总览与详情里加一个只读 `lending` 对象，用同一次投影给出同池状态、每卡角色、剩余百分比、距重置天数、重置前日均可用、原因与有效期。列表页和详情页读同一份，数字、理由、有效期不会漂移（AC-9、AC-10、AC-15）。
- **同池 = 同 provider + 同 plan_type。** 只看 `scope=standard` 且周期 ≥ 24h 的长窗口；每卡取剩余百分比最低的长窗口；池内建议窗口类型必须一致。plan_type 缺失/未知、缺长窗口、缺重置时刻、读数过期、窗口口径不同、可比较卡不足两张时，整池「无法建议」并逐卡写原因，绝不退回按剩余百分比排序。
- **比较口径 = 剩余% ÷ 距重置天数。** 距重置不足 1 小时按 1 小时算，结果保留一位；最高标「偏用」、最低标「少用」，差小于 1 个百分点/天时全部「均衡使用」。可服务卡不足两张时只列受限卡与原因，不给偏用结论。
- **显示层独立于建议层。** 每个窗口的「距重置 X」、精确/推算/近似标记、快重置·余量高/低、按距自然重置排序，全部由现有窗口字段在前端推导，不新增后端字段，也不新增请求（AC-1～4、AC-16）。
- **工作包与顺序。** P1 后端负责纯计算、只读 DTO、demo 种子和单测/契约测试；P2 前端负责倒计时、标记、排序、建议面板、i18n 和组件测试。P2 的显示层可先开工；建议面板按 §2 契约用 typed mock 并行，最后接 P1。两边没有共享文件，可并行。
- **只提示，不写路由。** 页面没有任何应用、写入、切换路由或消耗重置权益的动作。写权重是另一个工作包，不在本计划内。

## 1. 依据、口径与范围

### 1.1 依据

- `prd.md` 的 AC-1～17 是唯一验收标准；本计划逐条对应，见 §9。
- `research.md` 提供现有字段、分组口径、路由事实与出处；本计划不重复调研结论，只把它落成可执行的契约与文件归属。
- 仓库 `AGENTS.md` 要求：只读优先、DTO allowlist、无 CDN、非归档文档不写绝对行号、中文技术文档按现有交付文档习惯。

### 1.2 四个待确认的处置

| 编号 | 问题 | 本期处置 | 落在哪 |
|---|---|---|---|
| q01 | 是否把建议落成 CPA 路由权重 | 红线，浩文未答；本计划一律按「只提示、不写路由」写，不设计 priority / weight 写入、回滚或审计 | §2.1、§8 |
| q02 | 建议窗口是否只覆盖长窗口 | 采纳推荐：只覆盖 `scope=standard` 且周期 ≥ 24h 的标准窗口；5 小时、滚动、模型级、分组级窗口只在单卡行展示，不参与建议 | §3.2、§4.1 |
| q03 | 同池偏用顺序按什么 | 采纳推荐：重置前日均可用 = 剩余% ÷ 距重置天数（不足 1 小时按 1 小时）；最高偏用、最低少用；差 < 1 个百分点/天全部均衡 | §3.7、§4.1 |
| q04 | 快重置如何标记 | 采纳推荐：距重置 ≤ 周期 1/3 打「快重置」；剩余 ≥ 50% 标「余量高」，< 50% 标「余量低」；周期未知不打标 | §2.6、§5.1 |

### 1.3 本期做

- 显示层：列表行与详情窗口补「距重置 X」、精度标记、快重置·余量高/低；新增「距自然重置最近」排序并记住选择。
- 建议层：同 provider + 同 plan_type 成池，给出偏用 / 少用 / 均衡 / 无法建议的只读结论、逐卡数字与原因、有效期。
- 同一份池建议在列表页顶部与凭证详情额度页出现，并写清与现有单卡重置权益建议的区别。

### 1.4 本期不做

- 不写 CPA 的 priority / weight，不做自动路由、自动切凭证、自动停用。
- 不改现有单卡重置权益建议（`reset_advice`）的语义、位置、阈值和动作。
- 不消耗 Codex 重置次数 / Claude 重置授权，不新增上游采集、通知、持久化或历史记录。
- 不覆盖 5 小时、滚动、模型级、分组级窗口的建议；不做按模型可用性筛选池、跨 provider 拆借、历史趋势曲线。
- 不新增 MCP 能力；若后续要给 Agent 读同一份建议，另立工作包。

### 1.5 与重置权益建议的分工

| 维度 | 拆借建议（本版新增） | 重置权益建议（原有，不改） |
|---|---|---|
| 回答 | 接下来偏用哪张卡、少用哪张 | 要不要在到期前用掉重置次数 / 授权 |
| 数据 | 窗口剩余百分比、自然重置时刻、额度读数鲜度 | 重置次数 / 授权余额与到期时间，2 小时内权益读数 |
| 范围 | 同 provider + 同 plan_type 的池 | 单卡；只覆盖 codex / claude |
| 输出 | 偏用 / 少用 / 均衡 / 无法建议 | 到期前用掉 / 留给恢复 / 不用管 / 证据不足 |
| 动作 | 无，纯只读 | 有消耗重置权益的既有动作，位置和语义不变 |

## 2. 接口契约

跨凭证建议不新增路由、不扩展 `reset_advice`，而是挂在现有额度只读面上：

- 总览：`GET /api/v1/management/quota` 响应增加只读顶层对象 `lending`；快照或冷却读失败、批次不完整时为 `null`。
- 详情：`GET /api/v1/management/quota/{authIndex}` 响应增加只读顶层对象 `lending`，返回该卡所属池的同一份对象；同一读失败条件下为 `null`，其余详情字段仍返回 200。
- 不新增 MCP 能力；`reset_advice` 的语义、字段、阈值与动作保持不变。若后续要给 Agent 读同一份建议，另立 `lending_advice` 能力工作包，不在本切片。
- 控制台展示位置：拆借建议的完整内容在独立二级页 `/quota-lending`；OAuth 页页头只留入口摘要，凭证详情只保留单卡池角色。三处读同一份 `lending`，不新增后端路由。路径与菜单文案集中在挂载模块 `web/src/types/quotaLendingMount.ts`；侧栏高亮由「能力」组里的额度拆借条目自己决定，路由与各处链接都引用它。

### 2.1 暴露位置与理由

| 问题 | 结论 | 理由 |
|---|---|---|
| 新增路由还是复用现有只读面 | 复用 `GET /management/quota` 与 `GET /management/quota/{authIndex}` | 列表页本来就加载全量凭证与窗口；不新增逐卡请求，满足 AC-16 与性能要求 |
| 是否扩展 `reset_advice` | 不扩展 | 两者回答不同问题：`reset_advice` 看重置权益到期，`lending` 看自然重置前的流量分配；AC-13 要求分区且语义不变 |
| 新能力是否进 MCP | 本切片不进 | MCP 能力需要独立契约、审批与文档；本期先保证控制台只读建议，Agent 能力另立工作包 |
| 计算放后端还是前端 | 后端算一次，列表与详情共用 | AC-9/AC-10/AC-15 要求两处数字、理由、有效期完全一致；前端各算一份会漂移 |
| 是否写 CPA 路由 | 不写 | 只提示；写权重另立工作包 |

### 2.2 总览响应 `lending`

```json
"lending": {
  "generated_at_ms": 1763000000000,
  "pools": [
    {
      "id": "codex:pro",
      "provider": "codex",
      "plan_type": "pro",
      "plan_label": "Pro 200",
      "window_kind": "weekly",
      "state": "advised",
      "valid_until_ms": 1764000000000,
      "observed_at_ms": 1762990000000,
      "fresh_until_ms": 1764000000000,
      "is_reference": false,
      "reason_codes": [],
      "cards": [
        {
          "auth_index": "auth-codex-01",
          "name": "codex-team-primary",
          "role": "prefer",
          "window_id": "weekly",
          "window_kind": "weekly",
          "remaining_percent": 88,
          "reset_at_ms": 1764000000000,
          "reset_accuracy": "exact",
          "observed_at_ms": 1762990000000,
          "fresh_until_ms": 1764000000000,
          "days_to_reset": 3.9,
          "daily_available_percent": 22.6,
          "reason_codes": [],
          "is_comparable": true
        }
      ]
    }
  ],
  "unpooled_count": 1,
  "unpooled": [
    {
      "auth_index": "auth-xai-01",
      "name": "xai-primary",
      "reason_codes": ["plan_type_unknown"]
    }
  ]
}
```

| 字段 | 类型 | 必有 | 含义 |
|---|---|---|---|
| `generated_at_ms` | int64 | 是 | 本次投影的页面时钟；前端倒计时与建议按它起算，并随页面时钟重算，不额外请求 |
| `pools` | array | 是 | 同池建议，按池内最早自然重置升序；空数组表示当前没有可拆借池 |
| `pools[].id` | string | 是 | 稳定池键，`provider + ":" + plan_type`，仅用于 React key 与详情定位 |
| `pools[].provider` | string | 是 | 归一化 provider（`codex` / `claude` / …） |
| `pools[].plan_type` | string | 是 | 归一化 plan_type；缺失或 `unknown` 的卡不进任何池 |
| `pools[].plan_label` | string | 否 | 展示用套餐名，取池内按名称排序第一张卡的 `plan_label`；缺失时前端回退 `plan_type` |
| `pools[].window_kind` | string | 否 | 池内共同建议窗口类型；窗口口径不同或无法比较时为空 |
| `pools[].state` | string | 是 | `advised` / `balanced` / `unavailable` / `limited` |
| `pools[].valid_until_ms` | int64 | 否 | 建议有效期 = 池内最早的自然重置；`unavailable` 池没有建议窗口，此字段为空 |
| `pools[].observed_at_ms` | int64 | 否 | 池内最早读数时刻，用于「依据读数」文案 |
| `pools[].fresh_until_ms` | int64 | 否 | 池内最早鲜度截止；页面时钟超过它时整池降级为「无法建议」/`reading_stale` |
| `pools[].is_reference` | bool | 是 | 池内任一卡重置精度为 `derived` / `approximate` 时为真 |
| `pools[].reason_codes` | string[] | 是 | 池级原因，见 §2.5 |
| `pools[].cards` | array | 是 | 池内每张卡一行，顺序以 `role` 优先级再按凭证名稳定排序 |
| `pools[].cards[].auth_index` | string | 是 | 凭证索引 |
| `pools[].cards[].name` | string | 是 | 凭证展示名 |
| `pools[].cards[].role` | string | 是 | `prefer` / `avoid` / `normal` / `balanced` / `limited` / `unavailable` |
| `pools[].cards[].window_id` | string | 否 | 该卡建议窗口的原始 id；不可比较时为空 |
| `pools[].cards[].window_kind` | string | 否 | 建议窗口类型；不可比较时为空 |
| `pools[].cards[].remaining_percent` | number | 否 | 建议窗口剩余百分比；缺失时为空，前端写「—」不写 0 |
| `pools[].cards[].reset_at_ms` | int64 | 否 | 建议窗口的自然重置时刻；只有文字时为空 |
| `pools[].cards[].reset_accuracy` | string | 否 | `exact` / `derived` / `approximate`；为空表示未知 |
| `pools[].cards[].observed_at_ms` | int64 | 否 | 该卡读数时刻，用于依据与鲜度 |
| `pools[].cards[].fresh_until_ms` | int64 | 否 | 该卡读数鲜度截止；前端按页面时钟判断是否过期 |
| `pools[].cards[].days_to_reset` | number | 否 | 距重置天数，保留一位；不足 1 小时按 1 小时算；为 `generated_at_ms` 快照，前端按页面时钟重算 |
| `pools[].cards[].daily_available_percent` | number | 否 | 重置前日均可用，保留一位；不可比较时为空；为 `generated_at_ms` 快照，前端按页面时钟重算 |
| `pools[].cards[].reason_codes` | string[] | 是 | 该卡不能参与偏用排名或不可比较的原因 |
| `pools[].cards[].is_comparable` | bool | 是 | 该卡是否提供了可比较的建议窗口读数 |
| `unpooled_count` | int | 是 | 未进任何池的凭证数；用于 AC-14 的底部汇总 |
| `unpooled[]` | array | 是 | 未进池凭证与原因，按凭证名稳定排序 |
| `unpooled[].auth_index` | string | 是 | 未进池凭证索引 |
| `unpooled[].name` | string | 是 | 未进池凭证名 |
| `unpooled[].reason_codes` | string[] | 是 | 未进池原因，见 §2.5 |

### 2.3 详情响应 `lending`

```json
"lending": {
  "generated_at_ms": 1763000000000,
  "pool": {
    "id": "codex:pro",
    "provider": "codex",
    "plan_type": "pro",
    "plan_label": "Pro 200",
    "window_kind": "weekly",
    "state": "advised",
    "valid_until_ms": 1764000000000,
    "observed_at_ms": 1762990000000,
    "fresh_until_ms": 1764000000000,
    "is_reference": false,
    "reason_codes": [],
    "cards": []
  },
  "unpooled_reason_codes": []
}
```

示例省略 `cards` 明细；实际返回与 §2.2 相同的完整卡片数组。`lending` 本身可为 `null`：快照或冷却读失败时整个对象不投影，详情仍返回 200，前端按字段缺失同样降级。

| 字段 | 类型 | 必有 | 含义 |
|---|---|---|---|
| `generated_at_ms` | int64 | 是 | 与总览同一时刻语义；详情按同一份投影选择该卡所在池 |
| `pool` | object \| null | 是 | 该卡所属池，字段与 §2.2 的 `pools[]` 完全一致；不在任何池时为 `null` |
| `unpooled_reason_codes` | string[] | 是 | 该卡不在池中的原因；`pool` 非空时为空数组 |

### 2.4 池状态与卡片角色

| 枚举 | 取值 | 触发条件 | 前端文案方向 |
|---|---|---|---|
| 池状态 `state` | `advised` | 可服务卡 ≥ 2，且最高与最低日均可用差 ≥ 1 个百分点/天 | 给出偏用 / 少用 / 正常 |
| 池状态 `state` | `balanced` | 可服务卡 ≥ 2，但差 < 1 个百分点/天 | 全部「均衡使用」，不分偏用 / 少用 |
| 池状态 `state` | `limited` | 池存在且有可比较读数，但可服务卡 < 2 | 不给偏用结论，列出有服务性原因的受限卡；可比较但没有服务性原因的卡保持中性 |
| 池状态 `state` | `unavailable` | 池已成立，但缺重置时刻 / 读数过期 / 缺长窗口 / 窗口口径不同 | 整池「无法建议」，逐卡写原因 |
| 卡片角色 `role` | `prefer` | `advised` 池中日均可用最高的一张 | 偏用 |
| 卡片角色 `role` | `avoid` | `advised` 池中日均可用最低的一张 | 少用 |
| 卡片角色 `role` | `normal` | `advised` 池中其余可服务卡 | 正常 |
| 卡片角色 `role` | `balanced` | `balanced` 池中的全部可服务卡 | 均衡使用 |
| 卡片角色 `role` | `limited` | `limited` 池中有服务性原因、不能承接当前流量的卡 | 受限 + 原因；仅因不足两张而无法排名的可比较卡仍是 `normal` |
| 卡片角色 `role` | `unavailable` | `unavailable` 池中数据不可比较的卡 | 原因，不硬塞角色 |

### 2.5 原因码（`reason_codes`）

| 原因码 | 作用域 | 含义 |
|---|---|---|
| `plan_type_unknown` | 卡 / 未参与 | 套餐缺失、空或 `unknown`，不进任何池 |
| `pool_too_small` | 卡 / 未参与 | 同 provider + 同 plan_type 的可比较卡不足两张，不成池 |
| `no_long_window` | 卡 / 池 | 没有 `scope=standard` 且周期 ≥ 24h 的窗口 |
| `window_type_mismatch` | 卡 / 池 | 池内各卡的建议窗口类型不同，不跨类型比较 |
| `reset_time_unknown` | 卡 / 池 | 建议窗口只有上游文字，没有具体重置时刻 |
| `reading_stale` | 卡 / 池 | 读数年龄超过 `quota.PredictionFreshWindow`，或窗口标记 `stale` |
| `reading_failed` | 卡 / 池 | 最近读取失败或凭证状态为读取错误 |
| `remaining_unknown` | 卡 / 池 | 建议窗口没有剩余百分比 |
| `remaining_below_ten` | 卡 | 剩余 < 10%，不参与偏用排名 |
| `window_exhausted` | 卡 | 已耗尽，或任一标准窗口耗尽且未重置 |
| `cooldown_active` | 卡 | CPA 冷却中，当前不能承接流量 |
| `needs_reauth` | 卡 | 需要重新授权（具体判据待实现时确认） |
| `credential_disabled` | 未参与 | 凭证停用，不计入池 |
| `not_serviceable` | 卡 | 兜底：不满足可服务条件但未命中更具体原因 |

### 2.6 前端显示层需要的字段

显示层不新增后端字段，全部从现有 `QuotaWindow` 推导：

| 显示 | 数据来源 | 规则 |
|---|---|---|
| 「距重置 X」 | `reset_at_ms`、`now` | 有具体时刻才显示倒计时；按分钟/小时/天格式化 |
| 精确 / 推算 / 近似 | `reset_accuracy` | `exact` 不加标记；`derived` / `approximate` 前加 `~` 并标「参考」 |
| 只有文字 | `reset_label`、无 `reset_at_ms` | 保留上游原文并标「时刻未知」，不换算、不排序 |
| 快重置 | `period_hours`、`reset_at_ms`、`now` | `距重置 ≤ period_hours/3` 时打标；周期未知不打标 |
| 余量高 / 低 | `remaining_percent` | `≥ 50` 为高，`< 50` 为低；缺失时只打「快重置」或「时刻未知」 |
| 按距自然重置排序 | `windows[]` | 取所有标准长窗口中最早的 `reset_at_ms` 升序；无长窗口或无时刻排最后 |
| 建议面板 | `lending` | 后端已算好的池、角色、数字、原因与有效期 |
| 建议随时间重算 | `lending` + `now` | 前端用 `deriveLendingView` 重算天数/日均/角色/状态；静态原因与窗口选择沿用后端；超过 `fresh_until_ms` 降级为无法建议 |

## 3. 同池口径落地方案

调研确认没有现成的凭证分组表，本版用「provider + plan_type」这个唯一现成的可比键成池。下面是可直接实现的判定顺序；标「待实现时确认」的，是不影响契约、但实现时要定死的边界。

### 3.1 池键

- `provider` 取 `NormalizedQuota.Provider`，归一化为小写、去首尾空格。
- `plan_type` 取 `NormalizedQuota.Plan.PlanType`，同样归一化；`Plan` 为空、`PlanType` 为空或为 `unknown` 时，该卡不进任何池。
- 不再按凭证 `type`（OAuth / API key）细分：同一套餐的 OAuth 与 API key 凭证可以同池，避免把同一套餐的卡拆散。
- 池键 `id = provider + ":" + plan_type`，只用于 React key 与详情定位，不作为持久化标识。

### 3.2 长窗口与建议窗口

- 长窗口 = `window.Scope == "standard"` 且 `window.PeriodHours != nil` 且 `*window.PeriodHours >= 24`。
- `scope` 为 `model` / `group` 的窗口、`kind` 为 `rolling` / `credit_usage` 的窗口、周期缺失或 ≤ 0 的窗口，都不是长窗口；一张卡没有任何长窗口时记 `no_long_window`。
- 建议窗口 = 该卡所有长窗口中 `RemainingPercent` 最低的那个；它是最先卡住这张卡的窗口。
- 并列最低时，本期按「重置时刻更早者优先，再按窗口 `kind` 字典序」取一个；PRD 未规定并列规则，**待实现时确认**。这条只影响并列时的展示窗口，不改变角色与日均可用。

### 3.3 同池窗口类型

- 池内所有可比较卡的建议窗口 `Kind` 必须一致；不一致时整池 `state = unavailable`，池级与逐卡都写 `window_type_mismatch`，不跨类型比较。
- 类型一致才继续算日均可用；这一步在可服务门槛之前，避免拿不同类型窗口算出来的数字互相比较。

### 3.4 可服务门槛（AC-8）

以下情况不参与偏用排名，但仍出现在池的 `cards[]` 里，`role = limited`，原因逐条列出：

| 情况 | 判据（实现时按现有符号接） | 原因码 |
|---|---|---|
| 凭证停用 | `NormalizedQuota.Disabled` | 不进池，记入 `unpooled` 的 `credential_disabled` |
| CPA 冷却中 | `NormalizedQuota.ActiveCooldown.IsActive` | `cooldown_active` |
| 需要重新授权 | 现有 reauth / credential status 判据，**待实现时确认** | `needs_reauth` |
| 读取失败 | 凭证状态为读取错误，或 `last_refresh.outcome != success` | `reading_failed` |
| 已耗尽 / 某标准窗口耗尽且未重置 | 凭证状态 `exhausted`，或任一 `scope=standard` 窗口 `remaining_percent == 0` 且 `reset_at_ms > now` | `window_exhausted` |
| 剩余低于 10% | 建议窗口 `remaining_percent < 10` | `remaining_below_ten` |
| 其他不可服务 | 未命中以上具体条件 | `not_serviceable` |

停用凭证按 AC-8 直接不计入池，因此不会出现在 `cards[]`，只计入 `unpooled`。其余受限卡保留在池里，让运营人员看到「同池还有谁、为什么现在不能用」。

### 3.5 可比性与鲜度（AC-10）

- 建议窗口没有 `reset_at_ms`（只有 `reset_label` 文字）→ `reset_time_unknown`；该卡不可比较，整池 `unavailable`。
- 建议窗口没有 `remaining_percent` → `remaining_unknown`；同样不可比较。
- 读数年龄取卡片 `ObservedAtMS` 与窗口 `ObservedAtMS`（若有）中较新的一个；超过 `quota.PredictionFreshWindow(window)` → `reading_stale`，整池 `unavailable`。
- `window.Stale == true` 同样记 `reading_stale`。
- 鲜度用额度预测的 `PredictionFreshWindow`（至少 2 小时、长窗口按周期 1/4、最多 24 小时），不是 `reset_advice` 的 2 小时权益鲜度；两者回答的问题不同，不能混用。
- 后端把每卡 `observed_at_ms` 与 `fresh_until_ms` 一并返回，前端才能随页面时钟判断过期并降级；这不新增请求，符合 AC-16。
- 任何一张卡不可比较时，整池 `unavailable`，逐卡写原因；未知的卡可能是最宽裕的那张，不能假装它不存在。

### 3.6 池成立与不足两张

- 同池至少两张「未停用且有可比较长窗口读数」的卡才成池；不足两张时：
  - 总览不显示该池，相关卡进入 `unpooled`，原因 `pool_too_small`；
  - 详情 `pool = null`，`unpooled_reason_codes` 含 `pool_too_small`。
- 池成立但可服务卡不足两张时，池保留，`state = limited`；不给偏用 / 少用结论，列出有服务性原因的受限卡与原因；可比较但没有服务性原因的卡保持 `normal`，不冒充受限。
- 池成立且可比较卡 ≥ 2 时，只有可服务卡 ≥ 2 才进入 `advised` / `balanced`；否则退到 `limited`。

### 3.7 排名、均衡与有效期

- 只对可服务卡计算：
  - `days = (reset_at_ms - now) / 86_400_000`；
  - `days = max(days, 1/24)`（不足 1 小时按 1 小时）；
  - `daily = remaining_percent / days`，四舍五入到一位小数。
- 按 `daily` 从高到低：最高 `role = prefer`，最低 `role = avoid`，中间 `role = normal`。
- `max - min < 1.0` 个百分点/天 → 全部 `role = balanced`，池 `state = balanced`；恰好等于 1.0 时按「分先后」处理（`>= 1.0`）。
- `valid_until_ms` = 池内所有可比较卡（含受限卡）`reset_at_ms` 的最小值；它是建议有效期，不承诺到点后角色翻盘。`unavailable` 池没有建议窗口，不返回该字段。
- 池排序：按 `valid_until_ms` 升序，最紧迫的池在前；没有有效期的池排在最后。

### 3.8 判不准时的行为

| 判不准的情况 | 显示 | 下一步 |
|---|---|---|
| plan_type 缺失 / `unknown` | 该卡不进池，底部「未参与」计数 + 原因 | 核对凭证套餐字段；不按 provider 硬凑 |
| 没有长窗口 | 该卡「没有可比较的长窗口」；池已成立时整池无法建议，否则计入「未参与」 | 核对凭证是否发布日 / 周 / 月窗口 |
| 缺重置时刻（只有文字） | 该卡「重置时刻未知」；池已成立时整池无法建议 | 等上游给出具体时刻，或核对额度读取 |
| 读数过期 | 该卡「读数已过期」；池已成立时整池无法建议 | 刷新额度，等新读数 |
| 建议窗口类型不同 | 整池「窗口口径不同」，列出每张卡的窗口类型 | 分别看单卡；等出现同类型窗口再比较 |
| 可服务卡不足两张 | 池 `limited`，列出受限卡和原因 | 处理受限卡（刷新、等冷却、重新授权） |
| 同套餐只有一张 | 不显示池，计入「未参与」 | 正常情况，不需要处理 |

所有「判不准」都停在「无法建议 / 受限」；不降级成按剩余百分比排序，也不把未知卡当成不存在。

### 3.9 同一账号与模型可用性（本期留白）

- 同一上游账号重复登记时，两张卡读数可能完全相同，日均可用会落进「均衡」，不会给出错误偏用。当前没有可靠的账号 id 可用于排除；本期按不同卡参与比较，并在 §8 记录风险。
- 同套餐不等于同模型可用性。建议只表达流量偏好，不替代 CPA 的可用性判断；本期不把模型可用性纳入池口径。

## 4. 后端工作包 P1（跨凭证建议计算 + 只读 DTO）

### 4.1 做什么

1. **纯投影函数**：新增 `quota.ProjectLendingAdvice`，输入是已经构建好的卡片列表与 `nowMS`，输出池、角色、原因、有效期。它不读仓库、不调上游、不写任何状态，只做纯计算，便于表驱动测试。
2. **分组与窗口选择**：按 §3 的池键、长窗口、建议窗口、窗口类型一致性规则分组；同一张卡只取一个建议窗口。
3. **可服务与降级**：按 §3.4、§3.5 标出受限卡与不可比较原因；整池不可比较时 `state = unavailable`，可服务卡不足两张时 `state = limited`；不降级为按剩余百分比排序。
4. **排名**：对可服务卡算重置前日均可用，标 `prefer` / `avoid` / `normal`；差 < 1 个百分点/天时全部 `balanced`。
5. **总览与详情同源**：把每卡归一化构建抽成一个共享 helper，总览和详情都拿同一批卡片喂给 `ProjectLendingAdvice`；详情从结果里选出该卡所在池，保证 AC-15 的数字、理由、有效期一致。
6. **只读 DTO**：在 `internal/api` 定义手写 allowlist DTO，只输出 §2.2 / §2.3 列出的字段；`internal/quota` 的领域结构不直接当 wire DTO 使用。
7. **demo 种子**：给 `internal/demo/quota.go` 的快照写入 `PlanJSON` / `PlanType` / `PlanTier`，让 demo 至少出现一个同套餐池；否则 demo 页面永远没有建议可看。
8. **测试**：纯逻辑表驱动测试 + handler 契约测试；不改采集、预测、状态、通知。

### 4.2 改哪里（文件 + 符号）

| 文件 | 符号 / 改动 | 说明 |
|---|---|---|
| `internal/quota/lending.go`（新增） | `LendingCardInput`、`LendingResult`、`LendingPool`、`LendingCard`、`LendingUnpooled`、`LendingDetail` | 领域结构与纯计算；不含 JSON 行号、不读仓库 |
| 同上 | `ProjectLendingAdvice(cards []LendingCardInput, nowMS int64) LendingResult` | 分组、长窗口、建议窗口、排名、降级 |
| 同上 | `LendingResult.PoolFor(authIndex string) (*LendingPool, []string)` | 详情选择池与未进池原因 |
| 同上 | `LongStandardWindow(window QuotaWindow) bool` | `scope=standard` 且 `PeriodHours >= 24`；前后端规则同源 |
| 同上 | `DailyAvailablePercent(remaining float64, resetAtMS, nowMS int64) (days float64, daily float64)` | 不足 1 小时按 1 小时，保留一位 |
| 同上 | `LendingReason*` 常量 | §2.5 的原因码，避免字符串散落 |
| `internal/quota/lending_test.go`（新增） | `TestProjectLendingAdvice…` | 表驱动覆盖 §4.5 的场景 |
| `internal/api/quota_lending.go`（新增） | `lendingOverviewDTO`、`lendingDetailDTO`、`lendingPoolDTO`、`lendingCardDTO`、`lendingUnpooledDTO` | 手写 JSON allowlist；字段名与 §2 一致 |
| 同上 | `projectLendingOverview`、`projectLendingDetail` | 领域结构到 DTO 的唯一投影 |
| `internal/api/management_quota.go` | `quotaOverviewData.Lending *lendingOverviewDTO` | 总览顶层字段 |
| 同上 | `buildQuotaOverview` 末尾调用 `quota.ProjectLendingAdvice` 并赋值 | 计算落在总览构建里，详情复用 |
| 同上 | 抽取共享的每卡构建 helper（建议名 `buildQuotaItems`） | 现在总览与详情各拼一份 `NormalizedQuota`；抽出来避免两份逻辑漂移 |
| 同上 | `getCredentialQuotaDetail` 用共享 helper 的卡片列表算同一份 lending，再选池 | 详情只额外补 history / model_quotas |
| 同上 | `getQuotaOverview` 保留 `consoleResetAdvice` 与逐卡 `ResetAdvice` 赋值 | 不动 `reset_advice` 的语义与位置 |
| `internal/api/handler.go` | 不新增路由 | 复用 `GET /management/quota` 与 `GET /management/quota/{authIndex}` |
| `internal/api/management_quota_test.go` / 新增 `internal/api/quota_lending_test.go` | 总览与详情契约测试 | 断言字段、null、原因、两处一致 |
| `internal/demo/quota.go` | `saveDemoQuotaSnapshot` 增加 plan 参数；`seedQuotaObservations` 传入解析出的 `PlanType` / `PlanTier` / `PlanJSON` | 当前只写 `WindowsJSON`，demo 池会为空 |
| `internal/demo/fixture.go` | `credentialCatalog` 与 `quotaPayloads` 的 plan 对齐 | `auth-codex-02` catalog 标 plus、payload 返回 pro，**待实现时确认**二选一：对齐 catalog，或给它单独 payload |
| `deploy/cloudflare/data/responses.json` | 用 `pnpm demo:generate` 重新生成 | 不手改；提交 diff；`pnpm check:demo` 必须通过 |
| `internal/api/demo_export_test.go` | 确认额度响应形状变化仍被导出覆盖 | 若导出断言写死旧字段，按新形状更新 |

### 4.3 依赖

- 依赖 PRD 的 AC-1～17 与 §3 口径；不依赖前端。
- 复用现有领域类型与函数：`quota.QuotaWindow`、`quota.QuotaPlan`、`quota.NormalizedQuota`、`quota.PredictionFreshWindow`、`quota.ApplyCredentialStatus`、`quota.EvaluateStatusAndRecommendation`。
- 复用 `repository.QuotaSnapshotRecord` 的 `PlanJSON` / `PlanType` / `PlanTier` / `ObservedAtMS`；无数据库迁移。
- 不改 `internal/operations/reset_advice.go`、`internal/operations/quota_prediction.go`、`internal/quota/reset_advice.go`、`internal/quota/prediction.go` 的行为。

### 4.4 对应 AC

| AC | P1 负责的部分 |
|---|---|
| AC-5 | 同 provider + 同 plan_type 成池；缺失/未知不进池；未参与数量与原因 |
| AC-6 | 只取标准且周期 ≥ 24h 的窗口；每卡取剩余最低的长窗口；窗口类型不同不给建议 |
| AC-7 | 日均可用计算、偏用/少用/均衡、1 个百分点/天门槛 |
| AC-8 | 停用排除；冷却、重新授权、读取失败、耗尽、<10% 的受限原因与不参与排名 |
| AC-9 | 池有效期、每卡窗口/剩余/天数/日均、原因码（前端负责文案） |
| AC-10 | 缺重置时刻、读数过期、缺长窗口、窗口类型不同 → 整池 unavailable + 逐卡原因 |
| AC-11 | `is_reference` 与精度字段；差值门槛内的均衡 |
| AC-12 | 后端不新增写接口、不写 CPA |
| AC-14 | `unpooled_count` / `unpooled` 汇总 |
| AC-15 | 总览与详情共用同一份投影 |
| AC-17 | 不改采集、预测、状态、通知；建议不落库 |

### 4.5 自测要点

**纯逻辑表驱动（`internal/quota/lending_test.go`）**

- 两张同套餐 Codex 卡：A 剩 88%、3.9 天 → 22.6%/天；B 剩 40%、2.3 天 → 17.6%/天；A `prefer`、B `avoid`。
- 快重置但余量高：A 30%/4 天、B 95%/1 天；B `prefer`，A `avoid`。
- 快重置但余量低：A 5%/30 天、B 80%/2 天；A `remaining_below_ten` 受限，只剩 B，池 `limited`，不给偏用。
- 缺重置时刻：建议窗口只有 `reset_label`；整池 `unavailable`，该卡 `reset_time_unknown`。
- 读数过期：年龄超过 `PredictionFreshWindow`；整池 `unavailable`，该卡 `reading_stale`。
- 窗口类型不同：一卡 weekly、一卡 daily；整池 `unavailable`，`window_type_mismatch`。
- 同套餐只有一张：不进池，`unpooled` 记 `pool_too_small`。
- plan_type 缺失 / `unknown`：不进任何池，`unpooled` 记 `plan_type_unknown`。
- 停用凭证：不计入池，`unpooled` 记 `credential_disabled`。
- 冷却 / 重新授权 / 读取失败 / 耗尽 / 剩余 <10%：各自原因码，`role = limited`，其他卡照常排名。
- 不足 1 小时：距离 20 分钟的卡按 1 小时算，日均可用不爆炸。
- 差值边界：差恰好 1.0 → 分先后；差 0.9 → 全部 `balanced`。
- 精度：`derived` / `approximate` → 池 `is_reference = true`；`exact` → false。
- 总览与详情：同一批输入下，详情选出的池与总览里对应池逐字段一致。

**Handler 契约（`internal/api`）**

- `GET /management/quota` 返回 `lending` 顶层对象；无池时 `pools: []`、`unpooled_count` 正确。
- `GET /management/quota/{authIndex}` 返回同一池；不在池时 `pool: null` 且 `unpooled_reason_codes` 非空。
- 两个接口的池 `cards[]` 顺序、角色、数字、原因、`valid_until_ms` 一致。
- `reset_advice` 字段与改动前逐字段一致（快照对比）。
- 响应里没有新写入口、没有 target URL、没有原始 CPA 载荷。

**不做新请求 / 不写状态**

- 手动核对 handler 路径：`lending` 只消费已经读取的 auth files、快照、冷却与被动观测，不触发 `RefreshQuota`。
- 建议不落库、不写审计、不发通知；可在测试里断言仓库写入调用数不变（若现有测试夹具支持）。

**demo**

- `pnpm demo:generate` 后 `deploy/cloudflare/data/responses.json` 含 `lending`，且至少一个 pool 的 `cards` 有两张卡。
- `pnpm check:demo` 通过；demo 快照的 `plan_type` 与 `credentialCatalog` 一致。

## 5. 前端工作包 P2（显示层 + 额度拆借二级页与入口）

### 5.1 做什么

**显示层（可先于 P1 开工）**

1. 每个有读数的窗口显示带标签的「距重置 X」；有 `reset_at_ms` 才显示倒计时并附日期；`derived` / `approximate` 前加 `~` 并标「参考」；只有 `reset_label` 时保留上游原文并标「时刻未知」；没有读数时沿用现有 `quotaEmptyStateKey` 说明缺的是哪种读数。
2. 距重置 ≤ `period_hours/3` 的窗口打「快重置」标记；剩余 ≥ 50% 为「余量高」，< 50% 为「余量低」；周期未知不打标；标记用文字 + 颜色，不只靠颜色。
3. 排序新增「距自然重置最近」：取每张卡所有标准长窗口中最早的 `reset_at_ms` 升序；无长窗口或无时刻排最后；短窗口不参与；同刻按剩余低者在前，再按名称。
4. 排序选择沿用现有偏好持久化；点「重置筛选」回到名称排序。

**建议层（按 §2 契约用 typed mock 先做，最后接 P1）**

5. 新增二级页 `QuotaLendingPage`（`/quota-lending`，路径由 `web/src/types/quotaLendingMount.ts` 决定，高亮由「能力」组条目自己决定）：页头标题、返回上一页的链接（直接打开时不显示）、一行「依据 X 时刻的读数 · 随页面时钟重算」；每个池一块（沿用现有池块：池标题、结论与参考标签、有效期、理由、逐卡角色 / 剩余 / 距重置 / 日均）；「未进池」区；页底只读声明。页面是一列区块，为二期的多窗口、订阅截止、将断档、自动化确认留位置，但一期不画空占位。
6. OAuth 页页头统计行末尾新增轻入口：不再渲染 `LendingAdviceView`；入口按 `lending` 的池状态显示「额度拆借 · N 条建议」「额度拆借 · 均衡」或「额度拆借」；`lending` 字段缺失时不显示。整项是链接，不是按钮，不占独立区块，不增加页面纵向高度。
7. 凭证详情额度页保留单卡池角色小块（角色、一句理由、有效期）；下面加「在额度拆借页查看整池」链接，不复制整池表格。
8. 空状态与降级：旧后端没有 `lending` 或读取失败返回 `null` 时二级页显示降级空态；OAuth 页入口与凭证详情建议块同样不显示。有字段但没有池时显示空状态并带「未进池」汇总，不无声跳过。
9. 「未进池」默认折叠：收起时只显示总数和按原因分组的计数；展开后按原因分组，每组再展开才列凭证名；N=0 时整区不显示。折叠控件键盘可达，外层用 `aria-expanded`。
10. 与 `ResetAdviceView` 分区展示，文案不混用；拆借建议固定写「只提示，不改路由」，没有任何应用 / 写入 / 切换 / 消耗权益按钮。
11. 中英 i18n 全量补齐；`ms` 与 `zh-Hant` 目录同步；窄屏单列，不横向滚动。
12. 建议随页面时钟重算：`deriveLendingView(pool, nowMS)` 只重算距重置天数、日均可用、角色与状态，以及入口的三态；不重新分组、不改建议窗口；超过 `fresh_until_ms` 时整池降级为「无法建议」`reading_stale`。

### 5.2 改哪里（文件 + 符号）

| 文件 | 符号 / 改动 | 说明 |
|---|---|---|
| `web/src/pages/quota/quotaFormat.ts` | 新增 `quotaResetCountdownLabel(window, nowMS, t)` | 返回带标签的倒计时、精度与「时刻未知」；保留 `quotaResetCountdown` 供旧调用 |
| 同上 | 复用 `resetAccuracyMarker`、`formatShortDateTime` | `~` 与日期格式沿用现有实现 |
| `web/src/pages/quota/quotaWindowSelection.ts` | 新增 `isLongStandardWindow(window)` | `scope=standard` 且 `period_hours >= 24`；与后端 `LongStandardWindow` 同规则 |
| 同上 | 新增 `earliestLongWindow(windows)` | 排序键；返回最早长窗口的 `resetAtMS` 与 `remainingPercent`，无则 `null` |
| 同上 | 新增 `quotaFastResetState(window, nowMS)` | 返回 `high` / `low` / `null`；周期未知返回 `null` |
| `web/src/pages/quota/CompactQuotaView.tsx` | `window-foot` 渲染 `quotaResetCountdownLabel` 与快重置 `Tag` | 列表行主入口 |
| `web/src/pages/quota/QuotaProgressBar.tsx` | 详情窗口行渲染同一 label 与标记 | 详情与列表同规则 |
| `web/src/pages/quota/CredentialQuotaBody.tsx` | 保留 `LendingAdviceView` 详情小块，并在其下加「在额度拆借页查看整池」链接 | 两个建议分区，不合并 |
| `web/src/pages/quota/lendingAdvice.ts`（新增） | `deriveLendingView`、`lendingEntryState`、`groupUnpooled`、`lendingBasisMS` | 纯函数：重算时间相关字段与入口三态；分组计数不丢不重 |
| `web/src/pages/quota/LendingAdviceView.tsx`（新增） | `mode="page"` 的池块、未进池折叠、只读声明；`mode="detail"` 的单卡小块 | 二级页与详情共用同一派生，数字不漂移 |
| `web/src/pages/quota/LendingAdviceView.module.css`（新增） | 页区块、池、角色色、折叠区、窄屏单列 | 遵守 `docs/design.md` 与 `check:style-tokens` |
| `web/src/types/quota.ts` | 新增 `LendingOverview`、`LendingDetail`、`LendingPool`、`LendingCard`、`LendingUnpooled`；扩展 `QuotaOverviewResponse` / `CredentialQuotaDetailResponse` | 字段与 §2 一一对应；P2 独占该文件 |
| `web/src/components/authFiles/authFileLogic.ts` | `AuthFileSortKey` 增加 `'reset-asc'`；`sortAuthFiles` 增加分支 | 兼容仍使用 `sortAuthFiles` 的脚本 |
| `web/src/pages/oauthManagement/oauthWorkspaceLogic.ts` | `AUTH_FILE_SORTS` 增加 `'reset-asc'`；`sortOAuthWorkspaceRecords` 增加排序分支 | 使用记录的 `windows[]` 调 `earliestLongWindow`；无值排最后 |
| `web/src/pages/oauthManagement/OAuthManagementPage.tsx` | 排序 `Select` 增加「距自然重置最近」；页头统计行末尾渲染 `LendingAdviceEntryLink`；移除 `LendingAdviceView`；把 `lending` 传给 `CompactQuotaView` / `CredentialQuotaBody` | 页面只做装配，不重算建议 |
| `web/src/pages/quota/QuotaLendingPage.tsx`（新增） | 二级页：同一个 `['management-quota']` react-query 缓存、`useVisibleNow`、「返回上一页」、降级空态 | 路径由 `quotaLendingMount.ts` 决定；不新增请求 |
| `web/src/types/quotaLendingMount.ts`（新增） | `QUOTA_LENDING_PATH`、菜单文案键 `nav.quota_lending`、返回按钮文案键 `common.back`、历史守卫 `hasPreviousHistoryEntry`；路由、入口、详情链接、返回链接都引用它 | 导航调整时只改这一处 |
| `web/src/pages/quota/LendingAdviceEntryLink.tsx`（新增） | `LendingAdviceEntryLink`：按 `lending` 三态渲染页头入口链接；缺字段返回 `null` | 入口只跳转，不含任何写入口 |
| `web/src/App.tsx` / `routePages.ts` / `routePreload.ts` | 新增平铺路由、懒加载与预加载登记 | 风格同 `model-groups/:id/add` |
| `web/src/components/common/AppLayout.tsx` | 无改动：借调页在「能力」组里，高亮由该条目自己决定 | 不再需要路径→菜单键查表，与其它页面共用同一套导航解析 |
| `web/src/pages/oauthManagement/useOAuthManagementViewState.ts` | 验证 `sort` 已在偏好里；不需要为新键加特判 | 若现有持久化不接受新键，只补白名单 |
| `web/src/hooks/useVisibleNow.ts` | 复用现有页面时钟 | 倒计时与建议随页面时钟重算，不新增请求 |
| `web/src/i18n/index.tsx` | 新增 `quota.lending.*`、`quota.reset_countdown_label`、`quota.fast_reset.*`、`quota.sort.reset_asc` 等 `[zh, en]` | 文案不硬编码 |
| `web/src/i18n/locales/ms.ts` / `zh-Hant.ts` | 同步上述键 | `pnpm check-i18n` 兜底 |
| `web/tests/lending-advice.component.test.tsx` | 二级页五态、入口三态 + 缺字段、未进池折叠、详情链接、只读断言 | 新增/扩展组件测试 |
| `web/tests/credential-quota.component.test.tsx` | 补倒计时、精度、快重置、详情池区块与二级页链接 | 现有组件测试扩展 |
| `web/tests/reset-advice.component.test.tsx` | 断言重置权益建议语义/位置/文案未变 | AC-13 回归 |
| `scripts/test-oauth-sort.ts` | 补 `reset-asc` 排序与稳定性 | 纯逻辑 |
| `scripts/test-auth-file-logic.ts` | 补 `AuthFileSortKey` 新值 | 若 `sortAuthFiles` 仍被脚本覆盖 |

### 5.3 依赖

- 显示层只依赖现有 `QuotaWindow` 字段与 `useVisibleNow`，不依赖 P1，可先开工。
- 建议面板依赖 §2 契约；P1 未合入时用 typed mock / fixture，字段名按 §2 固定，最后切真实响应。
- 与 P1 无共享文件：P1 只改 Go 与 demo；P2 独占 `web/src/types/quota.ts`、`web/src/pages/quota/*`、`web/src/pages/oauthManagement/*`、i18n。
- 同一文件内的顺序：先做排序与倒计时（显示层），再做建议面板；`OAuthManagementPage.tsx` 两处改动由同一执行者串行完成，避免撞车。
- 依赖现有 UI 原语 `Tag`、`Panel` 与 `docs/design.md` 的状态色；不新增设计 token。

### 5.4 对应 AC

| AC | P2 负责的部分 |
|---|---|
| AC-1 | 每窗口「距重置 X」、精度 `~`、只有文字时「时刻未知」 |
| AC-2 | 快重置标记、余量高/低、周期未知不打标、文字 + 颜色 |
| AC-3 | 新增「距自然重置最近」排序与稳定 tie-break |
| AC-4 | 排序偏好持久化；重置筛选回名称排序 |
| AC-9 | 面板显示有效期、每卡三列数字与一句理由 |
| AC-10 | 整池「无法建议」与逐卡原因；不退回剩余百分比 |
| AC-11 | `~` + 「参考」；均衡文案 |
| AC-12 | 无应用/写入/切换/消耗按钮；固定只读说明 |
| AC-13 | 与 `ResetAdviceView` 分区、文案不混用、原建议不变 |
| AC-14 | 空状态 + 未进池数量与原因汇总 |
| AC-14a | 二级页路径（挂载模块）、标题、返回上一页（直接打开不显示）、侧栏高亮（能力组条目）、直接打开/刷新/前进后退、旧后端降级 |
| AC-14b | 入口三态 + 缺字段不显示；无面板；不增高；点击进二级页 |
| AC-14c | 未进池默认折叠、按原因计数、每组展开列凭证名、计数与后端一致 |
| AC-15 | 二级页、入口摘要与详情同一份池建议 |
| AC-16 | 控制台时区、页面时钟重算、不新增请求 |
| AC-17 | 无新的持久化 / 推送 / 历史 |

### 5.5 自测要点

**纯逻辑（`scripts/test-oauth-sort.ts`、`scripts/test-quota-*.ts`）**

- 倒计时：`exact` 显示倒计时 + 日期；`derived` / `approximate` 带 `~`；只有 `reset_label` 保留原文 + 「时刻未知」。
- 快重置边界：距重置恰好 `period/3` 打标，超过不打；剩余恰好 50% 为高，49.9% 为低；`period_hours` 缺失不打标。
- 排序：最早长窗口重置在前；无长窗口/无时刻排最后；5 小时窗口不参与；同刻按剩余低者在前，再按名称。
- 排序偏好：刷新后保持；点「重置筛选」回 `name-asc`。
- 日均可用展示：不足 1 小时按 1 小时；数字与后端 DTO 一致，缺失写「—」不写 0。
- 建议随时间重算：同一池在 `now` 推进后，天数/日均/角色按阈值翻转；超过 `fresh_until_ms` 降级为 `unavailable` + `reading_stale`；静态原因与窗口选择不变。

**组件（`web/tests`）**

- 二级页池块：`advised`（偏用/少用/正常）、`balanced`、`limited`、`unavailable`、无池空状态五种形态；入口三态与缺字段不显示；未进池默认折叠、展开按原因分组；详情小块下的「查看整池」链接。
- 详情页：同一池的角色、数字、理由、有效期与列表一致；不在池时显示原因。
- 未进池汇总：`unpooled_count` 与按原因分组计数始终可见；展开前不列凭证名，展开后每组再展开才列；N=0 整区不显示。
- 只读：面板内没有 `button` / 写入 / 路由切换 / 消耗权益入口；固定文案「只提示，不改路由」。
- 既有 `ResetAdviceView`：语义、位置、阈值、动作不变（快照或显式断言）。
- 窄屏 ≤ 760px：面板与凭证行单列，无横向滚动。

**静态门禁**

- `pnpm check-i18n`：中英及 `ms` / `zh-Hant` 键齐全。
- `pnpm check-css-modules`：`styles['kebab-case']` 全部有定义。
- `pnpm check:motion` / `pnpm check:style-tokens` / `pnpm check:feedback`：动画、字号/圆角、反馈面与共享原语合规。
- `pnpm test:fast` 跑受影响检查；涉及页面交互与布局时加 `pnpm check:ui`。
- 不新增固定等待、skip 开关或重试来凑绿。

## 6. 工作包依赖与并行度

### 6.1 并行结论

- **P1 与 P2 可以全程并行。** 两者写集不重叠：P1 只改 Go、demo 种子与 demo 数据集；P2 只改 `web/src`、i18n、前端测试与 `scripts` 的纯逻辑测试。
- **P2 的显示层可以先于 P1 落地。** 倒计时、快重置标记、按重置排序只消费现有 `QuotaWindow` 字段，不等后端。
- **P2 的建议面板按 §2 契约用 typed mock 先做。** 字段名与枚举在 §2 冻结；P1 合入后只把 mock 换成真实响应，不改组件结构。
- **P1 内部顺序**：先写 `quota.ProjectLendingAdvice` 与纯逻辑测试，再接 API DTO 与 handler 契约测试，最后补 demo 种子与数据集。
- **P2 内部顺序**：先做 `quotaFormat` / `quotaWindowSelection` 的纯函数与排序，再做 `CompactQuotaView` / `QuotaProgressBar` 展示，最后做 `LendingAdviceView` 与页面装配。

### 6.2 写集与撞车规避

| 工作包 | 独占写集 | 说明 |
|---|---|---|
| P1 | `internal/quota/lending.go`、`internal/quota/lending_test.go`、`internal/api/quota_lending.go`、`internal/api/management_quota.go`、`internal/api/*quota*test.go`、`internal/demo/quota.go`、`internal/demo/fixture.go`、`deploy/cloudflare/data/responses.json` | 不改 `web/src` |
| P2 | `web/src/types/quota.ts`、`web/src/pages/quota/*`、`web/src/pages/oauthManagement/*`、`web/src/components/authFiles/authFileLogic.ts`、`web/src/i18n/**`、`web/tests/**`、`scripts/test-oauth-sort.ts`、`scripts/test-auth-file-logic.ts` | 不改 Go |
| 共享契约 | 本文件 §2 | 只有契约是共享的；字段或枚举要变，先改本计划并通知另一方 |

- `web/src/types/quota.ts` 归 P2 独占；P1 只按 §2 输出 JSON，不在 TS 里加类型。
- `OAuthManagementPage.tsx` 会被 P2 的「排序」与「建议面板」各改一次；同一执行者串行完成，避免同文件冲突。
- `CompactQuotaView.tsx` 会被「窗口倒计时/标记」与「可选的角色 chip」各改一次；同样由 P2 串行完成。
- P1 若在实现中发现契约需要增删字段，必须在合入前更新 §2 并通知 P2；不要静默改 JSON 名。

### 6.3 兼容与回退

- `lending` 是**新增只读字段**，旧前端会忽略；旧后端没有该字段时，P2 必须把它当空状态处理，不报错、不隐藏凭证。
- 不设功能开关：字段存在就展示，字段缺失就显示「暂无可拆借建议」；不回退到按剩余百分比排序。
- P1 与 P2 任何一方单独合入都不破坏现有行为：P1 只多一个 JSON 字段，P2 显示层不依赖 P1。
- 建议面板接真实响应前，用 `lending` typed fixture 覆盖五种状态；接真实响应后保留同一组组件测试。

### 6.4 建议交付顺序

1. P2 显示层（AC-1～4）先落地，可独立验收。
2. P1 纯逻辑 + DTO + 契约测试（AC-5～8、10、14）。
3. P2 建议面板接 mock → 接真实响应（AC-9、11～15）。
4. 联调：总览/详情一致性、demo 数据集、i18n、窄屏。
5. 文档同步与静态门禁。

## 7. 文档同步清单

本改动会触发以下文档更新；括号里写清改什么。

| 文档 | 改什么 | 触发条件 |
|---|---|---|
| `CONTEXT.md` | 在 `## Terms` 增领域词：可拆借同池、自然重置、重置前日均可用、拆借建议；写清与「重置权益重置」的区别 | P1 完成口径定义后 |
| `docs/architecture.md` | 在 `## 8. Quota flow` 与 `### OAuth management is one credential-centred workspace` 补：总览/详情共用一次只读 `lending` 投影，不新增上游请求、不落库 | P1/P2 落地时 |
| `docs/agent-capabilities.md` | 本切片不新增能力；若后续加 `lending_advice` MCP 能力，必须同步能力注册表、JSON Schema 与审批语义 | 仅当另立 Agent 能力工作包 |
| `PRODUCT.md` | 在能力矩阵补「只读跨凭证额度拆借建议」及其边界（不改路由） | 用户可见能力完成时 |
| `README.md` / `README.zh-CN.md` | 只在能力完整可见时补一句；配套截图按需重生 | 发布前 |
| `docs/testing.md` | 记录新 claim 的归属：`internal/quota/lending_test.go`、`internal/api` 契约测试、`web/tests/lending-advice.component.test.tsx`（含入口三态、二级页五态、未进池折叠）、`scripts/test-quota-lending.ts`、`lending` 浏览器场景 | P1/P2 测试落地时 |
| `docs/architecture.md` | 把「列表与详情共用 `lending`」改成「二级页承载完整建议，OAuth 页页头只读入口摘要，详情只读单卡池角色」 | P2 落地时 |
| `deploy/cloudflare/data/responses.json` | 用 `pnpm demo:generate` 重新生成并提交 diff；`pnpm check:demo` 必须通过 | 响应形状变化后 |
| `docs/images/readme/oauth-management.en.webp` / `.zh.webp` | 若截图页面的列表行或详情抽屉外观变化，用 `pnpm build && pnpm readme:screenshots` 重生 | 页面外观变化时 |
| `docs/delivery/2026-11-23-omc-quota-lending/prd.md` | 不回改 AC；若实现中发现口径冲突，在本计划 §11 记录并由 PM 决定 | 仅当冲突出现 |

不触发：`docs/ops/sqlite-operations.md`（无迁移）、`docs/releasing.md`（无发布流程变化）、`docs/install*.md`（无安装变化）。

## 8. 风险与范围外

| 项 | 本期处理 | 留白 / 范围外 |
|---|---|---|
| q01 CPA 路由权重 | 只提示；页面固定写「只提示，不改路由」，无应用按钮 | 若开启写权重需另立工作包 |
| Codex 重置时刻是推算值 | 显示 `~` + 「参考」；池级 `is_reference` 为真；差值 < 1 个百分点/天按均衡 | 不把推算值当精确值，不承诺回满后一定翻盘 |
| 插件只给重置文字 | 保留上游原文 + 「时刻未知」；不参与倒计时与排序 | 不尝试从文字反推时刻 |
| 2 小时权益鲜度 vs 额度鲜度 | 拆借建议用 `quota.PredictionFreshWindow`（至少 2h、周期 1/4、最多 24h），不用 `reset_advice` 的 2h 权益鲜度 | 过期即整池「无法建议」，不降级为按剩余百分比 |
| 同一账号多张凭证 | 当前无法可靠识别账号；按不同卡参与比较；读数完全相同会落进「均衡」 | 若未来能拿到账号 id，再在池口径里显式排除 |
| 同套餐但模型可用性不同 | 建议只表达流量偏好；不把模型可用性纳入池 | 本期不做按模型可用性筛选；CPA 仍按可用性路由 |
| 短窗口耗尽但长窗口宽裕 | 按 AC-8 排除出偏用排名，原因写 `window_exhausted` | 不把它当可服务卡 |
| demo 无 plan_type | P1 修 `internal/demo/quota.go` 的种子，写入 `PlanJSON`/`PlanType`/`PlanTier` | `auth-codex-02` 的 catalog plan 与 quota payload 不一致，待实现时确认对齐方式 |
| 建议被误读成已改路由 | 面板固定只读说明；无写入、切换、消耗权益按钮 | 不新增任何写入口 |
| 建议持久化 / 通知 / 历史 | 不落库、不推送、不产生历史；每次打开按最新读数重算 | 后续是否沉淀历史另立需求 |

## 9. AC 对照表（AC-1～17）

| AC | 主责 | 落地位置 | 验证 |
|---|---|---|---|
| AC-1 | P2 | `quotaFormat.quotaResetCountdownLabel`；`CompactQuotaView` / `QuotaProgressBar` | 纯逻辑 + 组件测试：精确/推算/近似/只有文字 |
| AC-2 | P2 | `quotaWindowSelection.quotaFastResetState`；窗口 foot / 详情行 | 边界测试：`period/3`、50% |
| AC-3 | P2 | `quotaWindowSelection.earliestLongWindow`；`sortOAuthWorkspaceRecords` | `scripts/test-oauth-sort.ts` |
| AC-4 | P2 | `AUTH_FILE_SORTS`、`useOAuthManagementViewState` 现有偏好 | 刷新保持 + 重置筛选回名称 |
| AC-5 | P1 + P2 | `quota.ProjectLendingAdvice` 分组；`LendingAdviceView` 池标题/未进池 | 纯逻辑 + 组件 |
| AC-6 | P1 | `LongStandardWindow`、建议窗口选择、窗口类型一致性 | 纯逻辑：长窗口/类型不同 |
| AC-7 | P1 | `DailyAvailablePercent`、角色与 1pp/天门槛 | 纯逻辑：22.6 vs 17.6、边界 |
| AC-8 | P1 | 可服务门槛与原因码 | 纯逻辑：冷却/重新授权/读失败/耗尽/<10% |
| AC-9 | P1 数据 + P2 渲染 | DTO 字段；`LendingAdviceView` 三列 + 理由 + 有效期 | 契约 + 组件 |
| AC-10 | P1 + P2 | 整池 `unavailable` 与逐卡原因；面板文案 | 纯逻辑 + 组件 |
| AC-11 | P1 数据 + P2 渲染 | `is_reference`、`reset_accuracy`；`~` + 「参考」 | 纯逻辑 + 组件 |
| AC-12 | P1 + P2 | 后端无写接口；二级页、入口与详情无应用/写入/切换/消耗按钮 | 契约 + 组件只读断言 |
| AC-13 | P1 + P2 | `reset_advice` 不改；`ResetAdviceView` 分区与回归 | 快照/显式断言 |
| AC-14 | P1 + P2 | `unpooled_count` / `unpooled`；空状态汇总 | 契约 + 组件 |
| AC-15 | P1 + P2 | 总览/详情共用 `ProjectLendingAdvice`；两处共用 `LendingAdviceView` | 契约一致性 + 组件 |
| AC-16 | P2 | `useVisibleNow` + `deriveLendingView`；控制台时区格式化 | 组件 + 纯逻辑：时钟推进、过期降级；无新请求 |
| AC-17 | P1 + P2 | 不落库、不推送、不新增历史；不改采集/预测/状态/通知 | 代码审查 + 契约断言 |

## 10. 交付前检查清单

**本稿自检（已完成）**

- [x] frontmatter 带 `type`、`slug`、`status`、`updated`、`prd`、`摘要`；与同目录 PRD 呼应。
- [x] P1 / P2 各有「做什么、改哪里（文件 + 符号）、依赖、对应 AC、自测要点」五节。
- [x] AC-1～17 在 §9 全部有主责、落地位置与验证方式。
- [x] 接口契约给出字段表与 JSON 形状，前后端可据此并行。
- [x] 非归档文档不写绝对行号，只写文件 + 符号。
- [x] q01 只保留「只提示、不写路由」；未设计权重写入、回滚或审计。
- [x] 文档同步清单覆盖 `CONTEXT.md`、`docs/architecture.md`、`docs/agent-capabilities.md`、`PRODUCT.md`、README、`docs/testing.md`、demo 数据集与 README 截图。
- [x] 风险与范围外覆盖路由权重、Codex 推算精度、插件文字重置、额度鲜度、同账号多卡、模型可用性、短窗口耗尽、demo plan_type。
- [x] 只改本计划文件；未写业务代码、未改配置、未跑构建测试。

**实现阶段逐项确认**

- [ ] P1：`quota.ProjectLendingAdvice` 纯函数与表驱动测试先合，再接 API DTO。
- [ ] P1：总览与详情共用同一批卡片与同一投影，契约测试断言两处逐字段一致。
- [ ] P1：`reset_advice` 输出与改动前逐字段一致。
- [ ] P1：demo 种子写入 `plan_type`；`pnpm demo:generate` 后 `pnpm check:demo` 通过。
- [ ] P2：倒计时、快重置、排序的纯逻辑测试先合，再做组件。
- [ ] P2：二级页五种状态（advised / balanced / limited / unavailable / 无池）与入口三态都有组件测试。
- [ ] P2：面板无任何写入、应用、切换路由、消耗权益入口；固定「只提示，不改路由」。
- [ ] P2：`pnpm check-i18n`、`check-css-modules`、`check:motion`、`check:style-tokens`、`check:feedback` 通过。
- [ ] 联调：二级页、入口摘要与详情的条数、角色、数字、理由、有效期一致；窄屏单列。
- [ ] 文档：按 §7 更新；不引用已删除文件或旧字段。
- [ ] 验收：按 §4.5、§5.5 的六种数据形状加边界值逐条跑。

**待实现时确认（不影响契约）**

- [ ] 建议窗口并列最低时的 tie-break：本期写「重置更早优先，再按 `kind` 字典序」，实现时按测试固定。
- [ ] `needs_reauth` 的现有判据：接 `ApplyCredentialStatus` / recommendation / raw signals 中的哪一个，按现有符号确认。
- [ ] demo 的 `auth-codex-02`：catalog 标 plus、payload 返回 pro，二选一对齐，不能留下不一致。
- [ ] 详情 handler 抽取共享每卡构建 helper 的命名与签名，按现有 `management_quota.go` 风格定。

## 11. 决策记录

| 日期 | 问题 | 结论 | 理由 / 备选 |
|---|---|---|---|
| 2026-11-23 | 跨凭证建议挂哪个只读面 | 复用 `GET /management/quota` 与 `GET /management/quota/{authIndex}`，新增顶层 `lending` | 列表页已加载全量凭证与窗口，不新增请求；备选「新增 `/management/quota/lending`」会多一次读并需要新路由分类 |
| 2026-11-23 | 是否扩展 `reset_advice` | 不扩展 | 两者回答不同问题；AC-13 要求分区展示、语义/阈值/位置/动作不变；备选「合并两套建议」会让相反动作混在一起 |
| 2026-11-23 | q01 路由权重 | 只提示、不写路由；写权重另立工作包 | 项目边界「不做自动路由，不进 CPA 请求路径」；浩文未答；备选「人工确认后写 weight」需要回滚、策略兼容与会话亲和设计 |
| 2026-11-23 | q02 建议窗口范围 | 只覆盖 `scope=standard` 且周期 ≥ 24h 的长窗口 | PM 采纳产品推荐；5 小时窗口自愈太快，排序会来回跳 |
| 2026-11-23 | q03 比较口径 | 重置前日均可用 = 剩余% ÷ 距重置天数（不足 1h 按 1h） | PM 采纳产品推荐；同时看余量与重置时间，避免平均主义与「明天重置、95% 余量被少用」 |
| 2026-11-23 | q04 快重置标记 | 距重置 ≤ 周期 1/3；剩余 ≥ 50% 为高、< 50% 为低；周期未知不打标 | PM 采纳产品推荐；跨窗口类型可复用 |
| 2026-11-23 | 是否新增 MCP 能力 | 本切片不新增 | 避免把范围扩到能力注册表、审批与 Agent 文档；后续可另立 `lending_advice` |
| 2026-11-23 | P1 / P2 并行 | 并行；显示层可先落地，建议面板按契约用 mock | 写集不重叠；契约 §2 冻结字段与枚举 |
| 2026-11-23 | 计算放后端还是前端 | 后端算一次，列表与详情共用 | AC-9/10/15 要求两处一致；前端各算一份会漂移 |
| 2026-11-23 | 鲜度用哪个界 | `quota.PredictionFreshWindow` | 与额度预测一致；`reset_advice` 的 2h 是权益鲜度，回答不同问题 |
| 2026-11-23 | 建议放在哪 | 完整建议搬到 `/quota-lending` 二级页，OAuth 页页头只留入口；挂载点集中在 `quotaLendingMount.ts` | 浩文看体验环境后要求独立二级页；二期内容也落在这一页；入口不占地、不增加页面纵向高度；备选「继续放 OAuth 页顶部」会让凭证列表、问题提示与建议挤在一起 |

### 11.1 与 PRD 的冲突

无。q01 按红线「只提示」写；q02–q04 按 PM 采纳的产品推荐写；AC-1～17 均有落点。若实现阶段发现 PRD 未规定的边界（如并列建议窗口、`needs_reauth` 判据），按 §10 的「待实现时确认」在实现中定，并在 PR 描述里记录，不改 AC。

### 11.2 仍需 PM / 浩文决定的事

- 无新增待决事项。q01 若后续改选「写权重」，按 §8 另立工作包，本计划的显示层与建议层主体不变。