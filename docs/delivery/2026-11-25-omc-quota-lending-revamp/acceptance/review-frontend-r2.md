# 前端增量复审 r2 · 额度拆借页改版（T6-S7）

- 审查对象：`Newoahil/11-26-t6-s4-codex` 的最终 HEAD `863b9425`（前端已合入后端 `b76faa82`；后端部分按 r4 结论不重审）
- 增量范围：`git diff 882ffbfe..863b9425 -- web scripts docs PRODUCT.md`（23 个文件，+639 / −171）
- 验收标准：`docs/delivery/2026-11-25-omc-quota-lending-revamp/prd.md` **v1.0.3**（`Newoahil/11-25-t6-s1-claude` 的 `5ac64d26`），并用 `E:/works/Homan-context/runs/briefs/2026-11-26-T6-prd-clarifications.md` 的 P-01～P-14 读法
- 契约：同目录 `contract.md`；实现说明：`impl-notes.md`；r1 报告：同目录 `review-frontend.md`
- 会话：works 2026-11-25#T6-S7，分支 `Newoahil/11-26-t6-s7-codex`（已合入目标分支，合并提交 `187a3507`）

## 结论

**通过**。r1 的 8 条全部已修，逐条有独立复验证据；新增的降级载荷改动（`c33fede6`）在名字回落、单一归一层和测试牙口上都成立；只读红线与页面 / 入口 / 详情三处一致没有回归。范围内无新增要修，另列 4 条范围外观察。

## 一、r1 的 8 条逐条复验

| # | r1 问题 | 结论 | 复验证据 |
|---|---|---|---|
| P1-1 | 主屏 Tag 12px / 10px | **已修** | 主屏 5 个 Tag 全部 13px（`只提示，不改路由`、`周限额`、`1 张要改`、`不用改`、`要改`），10px 的 `size="small"` 也已上调；`typography.json` / `typography-dark.json` 的 `tags` 数组均为 `[13,13,13,13,13]`。按 5.11，主屏主要文字（卡名、一句话、剩余、回满时间、现在、建议）≥14px，次要说明 ≥13px，且不出现 ≤12px；这些 Tag 属次要说明，13px 达标 |
| P1-2 | 回满时间 13px | **已修** | `LendingAdviceView.module.css` 的 `.card-reset` 改用 `--text-base`；两行实测 14px（`11/30 15:47 回满（3天21小时后）`、`11/29 01:23 回满（2天7小时后）`） |
| P1-3 | 正文对比度 2.87:1 / 副标题 4.38:1 | **已修** | 独立探针逐字取色、逐层合成半透明底色后重量：浅色最小 **6.33:1**（`要改` 标签文字 `rgb(137,68,15)` 在 14% warn 底色叠页面底 `rgb(247.5,237.8,230.4)` 上），深色最小 **6.74:1**（`-10` / `去设置` 的 accent 在 `rgb(18,18,20)` 上）；大字最小 7.09 / 6.74，均 ≥3:1。r1 的两个失败元素已消失：`剩余/现在/默认` 等说明改 `--fg-2` 后 8.02:1（浅）/ 7.3:1（深），页头依据 8.02:1（浅）/ 7.3:1（深） |
| P2-1 | 详情块仍含只读整句 | **已修** | `Evidence` 的 `read_only_note` 改为 `!detail` 才渲染；抽屉探针 `hasReadOnlySentence=false`、`hasDistinction=true`；组件测试断言正文不含 “there is no apply action”，同时详情页头 `data-lending-read-only` 小标签仍在 |
| P2-2 | 多张不用动只计数不点名 | **已修** | 一句话实测：`把 team-b@example.test 设成 -10；team-a@example.test、credential-03 不用动`；英文组件测试：`Set synthetic-c to -10; leave synthetic-a, synthetic-b alone`。全部要改的模板未变，P-07 两种模板都在 |
| P2-3 | 同档排名不写「并列」 | **已修** | 看依据排名列两行都是 `第 1，并列`；单元测试覆盖同档（两张第 1 并列）、跨档（第 2）与无档（差不多）三种 |
| P2-4 | 看依据的绝对说法 | **已修** | 页面句改为「排后面的卡的数字比这一组现在的最大数字低 10、20……；池外同一个模型的卡现在是几，见下面的名单」，抽屉另有 `outside_detail` 指向额度拆借页；旧句 `排到所有没设数字的卡之后` 已不存在；`ms` / `zh-Hant` 同步更新，`check-i18n` 4381 keys 通过 |
| P2-5 | 页头顺序为标题→依据→标签 | **已修** | `QuotaLendingPage` 把 Tag 放进 `PageHeader` 的 children；实测 Tag 是 `.terminal-page-heading` 的直接子元素、与标题同一行（中心线差 ≤2px、在标题右侧），依据行 `terminal-subtitle` 换到下一行且在 Tag 下方；Tag 不再位于 `[data-lending-view="page"]` 内，空状态 / 缺失状态也仍由页头承载 |

补充：P2-5 的 DOM 顺序是「标题→依据→标签」，视觉顺序靠 flex `order` 变成「标题→标签→依据」。视觉满足 5.3，读屏顺序是标题、依据、标签，见第五节观察 2。

## 二、新增改动专项（`c33fede6` 降级、`794ec5c3` 演示摘要）

### ① `lendingCardName` 是否覆盖所有出现名字的地方

`lendingCardName(card) = display_name || name || auth_index`，逐个出现点核对：

| 出现点 | 取值 | 结论 |
|---|---|---|
| 一句话（`say_pair` / `say_with_keep` / `say_with_rest`） | `lendingCardName` | 覆盖 |
| 卡行大字 | `DisplayName` → `lendingCardName` | 覆盖 |
| 看依据小表「卡」列 | `lendingCardName` | 覆盖 |
| 看依据「完整名称」行 | `lendingCardName`，另用 `card.name` 作「文件」括注 | 覆盖（文件名的直接读取是设计内的第二信息） |
| 看依据池外句 | `lendingCardName` + `lendingOutsidePoolLabel` | 覆盖 |
| 池外名单 | `DisplayName` | 覆盖 |
| 详情块的两句与卡行 | `lendingCardName` / `DisplayName` | 覆盖 |
| 不动的卡行 | `DisplayName` | 覆盖 |
| OAuth 页头入口 | 只显示计数与状态，不出现名字 | 不适用 |
| 未进池名单 | `groupLendingUnpooled` 的 `member.name` | 见下 |

`grep` 直接读取只剩两处：`LendingAdviceView` 的 `card.name && card.name !== displayName`（完整名称行要同时给出文件名）与 `groupLendingUnpooled` 的 `entry.name`。后者不是漏用：`contract.md` 的 `unpooled[]` 只有 `auth_index`、`name`、`reason_codes`，没有 `display_name`；前端按契约显示即可。这带来一个契约层的不对称（池内显示账号名、未进池显示文件名），见第五节观察 3。

### ② `display_name` 改成可选的影响面

- 类型只改在 `types/quota.ts` 的 `LendingCard` / `LendingOutsideCard`；`lendingAdvice.ts` 的 `LendingNamedCard` 本来就接受可选字段，`display_name` / `name` 都可缺，最后回落 `auth_index`。
- `pnpm type-check` 通过；没有别处把这些字段当必填使用（`grep display_name` 在额度拆借之外只命中模型别名等无关结构）。
- 运行时三条路径都验过：卡片缺 `display_name`、池缺 `outside_cards`、以及两者同时缺（探针 `degraded` 状态 + 组件测试）。卡片名在行、一句话、看依据完整名称三处都显示 `credential-02.json`；池照常渲染且不出现池外折叠。

### ③ 归一是否只在一层

`api/client.ts` 的 `normalizeLendingOverview` 只在 `api.getQuotaOverview` 的返回处调用一次，把每个池的 `outside_cards` 补成 `[]`；渲染层直接读 `pool.outside_cards`（`OutsideSection`、`Evidence`、`lendingPoolSentence` 之后），`grep` 没有第二处 `?? []` 或 `Array.isArray` 兜底。降级行为只在边界发生，页面代码不为缺失字段加判断。

### ④ 新测试 `quota-lending-degraded.component.test.tsx` 的牙口

两个突变都用 PowerShell 单点文本替换、UTF-8 无 BOM、跑完 `git checkout --` 还原，每次还原后 `git status --short` 干净：

| 突变 | 结果 | 失败证据 |
|---|---|---|
| `lendingCardName` 去掉 `|| card.name` 回退 | **红**：1 failed / 2 | `Unable to find an element with the text: codex-demo-a.json`（`reviews/mutation-r2-1.log`） |
| `normalizeLendingOverview` 去掉 `?? []` | **红**：1 failed / 2（含未处理错误） | `TypeError: Cannot read properties of undefined (reading 'map' / 'length')`（`reviews/mutation-r2-2.log`） |

两个探针都只改一处、还原后同一文件转绿；未新建突变文件，未用删除命令。

### ⑤ 归一是否影响别的 `getQuotaOverview` 页面

`getQuotaOverview` 的另外两个消费者：`OAuthManagementPage` 只用 `lendingDetailFor` 与入口计数，`CredentialQuota`（仪表盘）只读 `summary` / `quotas`，都不碰 `outside_cards`；字段存在时归一只是浅拷贝，值不变。`check:ui` 81/81（含 `oauth-management`、`quota-lending`、`dashboard-*`）通过，未见回归。

### 演示数据（`794ec5c3`）

只改 `deploy/cloudflare/data/provenance.json` 的 `inputs_digest`（`web/src/api/client.ts` 是演示数据输入清单的一员）；提交说明称按同一种子重生成后响应内容未变。`pnpm check:demo` 通过：155 responses / 20 routes，current；数据集里仍有 2 处 `outside_cards`，演示页能走到池外折叠。

## 三、只读红线与三处一致回归

- 只读：渲染、展开「看依据」「不动」「池外」、点「去设置」全程网络监听，写请求只有操作者在配置页点「保存字段修改」时的一条 `PATCH /management/auth-files/fields`，体为 `{name:'credential-02.json',auth_index:'auth-02',priority:-10}`；`writes.length === 1`。页面没有应用 / 保存 / 切换 / 消耗按钮，也没有可编辑字段。
- 三处一致：页面 / OAuth 入口 / 详情抽屉对同一张卡的名字、现在、建议、动作全等（`team-b@example.test`、`0 → -10`、`change`），入口为 `额度拆借 · 1 张要改`；不变卡 `team-a@example.test` 三处一致且详情没有「去设置」。
- 保存后回页：`data-lending-now` 变 `-10`、动作变 `keep`、池标题变「不用改」，共享 `['management-quota']` 查询失效后无需手动刷新。
- 受控时钟前进 2 小时：`now` / `suggested` 不变，倒计时文本变化（AC-23）。
- P-11：要改且不能改的卡显示「这张卡的优先级不能在这里改」；不用改且不能改的卡不显示（正反两 check 均过）。
- P-03：全读不到时池标题「不用改」、两行 `—`、不计入要改，符合 v1.0.2 / v1.0.3 的读法（见观察 1）。

## 四、门禁

| 门禁 | 结果 | 证据 |
|---|---|---|
| `pnpm type-check` | 通过 | exit 0 |
| `pnpm test:logic` | 通过 | 90 suites / 13.87s |
| `pnpm test:components` | 通过 | 51 files / 375 tests |
| `pnpm check-i18n` | 通过 | 4381 keys，exit 0 |
| `pnpm check-css-modules` | 通过 | 521 consumers，exit 0 |
| `pnpm check:style-tokens` / `check:feedback` | 通过 | exit 0 |
| `pnpm lint:antd` | 通过 | usage / performance 均 0 |
| `pnpm build` + `pnpm check:bundle` | 通过 | 构建 50.85s；加载边界与异常上限 PASS |
| `pnpm check:demo` | 通过 | 155 responses / 20 routes，current |
| `pnpm check:ui --base 882ffbfe` | 通过 | 81/81 场景，482.3s |

基线偏差（与 r1 相同，未计入本包）：本机 Node `v24.16.0`，仓库期望 `22.23.2`；`check:docs` / `test:self` 的基线红未重跑（PM 本轮门禁清单未含）。

## 五、范围外观察

1. **P-03 全读不到的池**：池标题与一句话仍写「不用改」，指读得到的卡；两行都是 `—`。这是 PM 已定的读法（已知限制），不要求改。
2. **页头 DOM 顺序与视觉顺序不同**：视觉是标题→标签→依据（flex `order`），DOM 是标题→依据→标签，读屏会先读依据再读标签。5.3 是版式要求，视觉已满足；若后续在意朗读顺序，可把标签放到 `subtitle` 之前渲染而保留同样的 CSS。不要求本轮改。
3. **未进池名单用文件名**：`unpooled[]` 契约没有 `display_name`，池内用账号名、未进池用文件名。属后端契约口径，前端照契约显示；若要统一，需要契约加字段。
4. **归一的浅拷贝**：`normalizeLendingOverview` 每次取数都会新建 `pools` 与池对象；TanStack Query 的结构共享会在深度相等时保留旧引用，未观察到额外重渲染，仅记为噪音级观察。

## 六、环境与清理

- 全程没有删除类命令被平台拦下。需要如实记录一次：为让门禁日志从头写，执行过 `Remove-Item reviews/gates-r2.log -ErrorAction SilentlyContinue`（目标文件当时不存在，实际未删除任何内容，未被拦下）。此后未再使用删除命令。
- 本会话新建的证据都在 `reviews/`（探针、日志、`typography*.json`、突变日志、截图），按约定不入 git、留给主会话处理；产品代码与 PRD 未改。
- 两次突变探针均已 `git checkout --` 还原，最终 `git status --short` 只有本报告（`reviews/` 被忽略）。
- 端口：探针用 Vite 5195，`check:ui` 用 5181；跑前确认 5181 空闲，跑完由脚本自行停止，无残留监听进程。

## 七、证据索引

- `reviews/probe-t6s7-r2.log`：77 checks / 0 FAIL，含字号、两套主题对比度、页头顺序、P-05、降级载荷、三处一致、只读与 AC-23。
- `reviews/typography.json` / `typography-dark.json`：主屏可见文字的字号、颜色、合成后的背景与对比度（浅 / 深）。
- `reviews/mutation-r2-1.log` / `mutation-r2-2.log`：两个突变各自变红的失败输出。
- `reviews/gates-r2.log`、`test-logic-r2.log`、`test-components-r2.log`、`build-r2.log`、`bundle-r2.log`、`demo-r2.log`、`check-ui-r2.log`：门禁输出。
- 截图：`01-base-1440.png`、`02-width-{390,760,768,1024}.png`、`03-dark.png`、`04-english.png`、`05-state-*.png`、`06-three-surfaces-drawer.png`、`07-drawer-reset-advice.png`、`08-set-it-config.png`、`09-after-save.png`、`10-controlled-clock.png`、`11-keep-many-sentence.png`、`12-dark-r2.png`、`13-degraded-payload.png`。

## r3 增量

- 对象：`Newoahil/11-26-t6-s4-codex` 的 `f314c9b3`（在 r2 的 `863b9425` 之后新增 `0c91d8ee`、`f314c9b3`），已合入本分支（合并提交 `6a651659`，无冲突）
- 范围：`git diff 863b9425..f314c9b3`（10 个文件，+260 / −34），只审这两个提交的前端改动
- 标准：PRD v1.0.3 第 5.6 节表第 5 行（读数过期）与第 9 行（剩余低于 10%），以及 PM 对 R2-2 / R2-3 的裁决

**结论：通过。** 两处文案修复成立，页面与抽屉同句；另列 2 条范围外观察。

### 一、5.6 第 5 行与第 9 行

| 检查点 | 结论 | 证据 |
|---|---|---|
| 回满判断用 overview 的 `generated_at_ms`，不用页面时钟 | 成立 | `lendingHoldRowNote` 用 `card.reset_at_ms <= context.generatedAtMS`；页面传 `lending.generated_at_ms`，抽屉传 `detail.generated_at_ms`（同一个值）。受控时钟越过回满时刻 2 分钟后，句子仍是「25 小时前」，没有翻成「回满时刻已到」 |
| 边界「不晚于当前时刻（含恰好到点）」 | 成立 | 比较用 `<=`；`reviews/r3-boundary.log` 直接调用核对：`reset_at_ms = generated_at_ms` 返回过期句，`generated_at_ms + 60s` 返回「25 小时前」 |
| 年龄用页面时钟，不足 1 小时写分钟 | 成立 | `ageMS = max(0, nowMS - observed_at_ms)`；`< 1h` 写 `{n} 分钟`（最少 1），否则写 `round(hours)`；60 秒读数实测「1 分钟前」，25 小时读数实测「25 小时前」 |
| 不再用 `Math.max(1, 小时)` 抬短读数 | 成立 | `grep` 无旧的 `Math.max(1, Math.round(... / 3_600_000))`；分钟分支的 `Math.max(1, ...)` 只用于 1 分钟下限 |
| 第 9 行写明额度类型与剩余 | 成立 | 有 `window_kind` 与剩余时写「周限额剩 5%，留作应急，不参与排名」（`remaining_below_ten_window`）；任一缺失退回「留作应急，不参与排名」 |
| 中英与 ms / zh-Hant 文案齐 | 成立 | `web/src/i18n/index.tsx` 新增 `reading_stale_expired`、`reading_stale_minutes`、`remaining_below_ten_window`，`locales/ms.ts` 与 `locales/zh-Hant.ts` 同步；`check-i18n` 4384 keys 通过 |
| 未知原因 / 缺主原因 | 成立 | 返回 `undefined`，行内不写补充句（单测覆盖 `future_code` 与 `undefined`） |

### 二、页面与抽屉同句（AC-15）

- 页面展开行与抽屉不动块都调用 `lendingHoldRowNote(card, t, { generatedAtMS, nowMS, windowLabel })`，`windowLabel` 都由 `quotaWindowLabel(card.window_kind, undefined, t)` 解析。
- 探针逐字比较：`auth-02`（回满已到）页面与抽屉同为「回满时刻已到（不晚于当前时刻），读数已不准；先刷新额度」；`auth-03`（1 分钟）同为「最后读数在 1 分钟前，先刷新额度」。
- `grep` 旧函数 `lendingHoldNote` 无残留；旧的小时抬升写法也只存在于探针夹具的无关位置。

### 三、新测试的牙口

两个突变都用 PowerShell 单点替换、UTF-8 无 BOM，跑完 `git checkout --` 还原，`git status --short` 干净：

| 突变 | 纯逻辑层 | 组件层 |
|---|---|---|
| 去掉「回满已到」判断（条件改 `false`） | **红**：`actual 'reading_stale(n=26)'` vs `expected 'reading_stale_expired'` | **红**：2 failed / 23 |
| 去掉「不足 1 小时写分钟」分支（条件改 `false`） | **红**：`actual 'reading_stale(n=0)'` vs `expected 'reading_stale_minutes(n=1)'` | **红**：3 failed / 23 |

### 四、红线与三处一致回归

- 探针 84 checks / 0 FAIL（在 r2 的 77 条上新增 7 条 r3 断言），只读检查仍通过：渲染、展开「看依据」「不动」与「去设置」全程唯一写请求仍是操作者保存时的一条 `PATCH /management/auth-files/fields`。
- 三处一致（页面 / OAuth 入口 / 抽屉）未回归；受控时钟只改变倒计时，不改 now / suggested。
- 仓库自带 `quota-lending` 场景（含页面与抽屉的 stale / limited 断言）通过。

### 五、门禁

| 门禁 | 结果 | 证据 |
|---|---|---|
| `pnpm type-check` | 通过 | exit 0 |
| `pnpm test:logic` | 通过 | 90 suites / 14.51s |
| `pnpm test:components` | 通过 | 51 files / 378 tests |
| `pnpm check-i18n` | 通过 | 4384 keys |
| `pnpm check-css-modules` | 通过 | 521 consumers |
| `pnpm check:style-tokens` / `check:feedback` / `lint:antd` | 通过 | exit 0；antd 各项 0 |
| `pnpm build` + `pnpm check:bundle` | 通过 | 构建 49.03s；加载边界 PASS |
| `pnpm check:ui --base 863b9425` | 通过 | 14/14 场景，164.7s（5181 空闲，未杀进程） |

### 六、范围外观察

1. **`reading_stale` 且没有 `observed_at_ms` 时会露出 `{n}`**：`lendingHoldRowNote` 在既没有过期回满、也没有读数时间时返回 `t('quota.lending.hold_note.reading_stale')`，该文案含 `{n}`，而 `makeT` 只替换传入的变量，页面会显示「最后读数在 {n} 小时前，先刷新额度」。后端在 `observedAt <= 0` 时也会给 `reading_stale`（`analyzeLendingCard`），所以这条路径可达。该行为在这两个提交之前就存在（旧 `lendingHoldNote` 同样如此），不是本次引入；建议补一句「读数时间未知，先刷新额度」的文案，或在该分支省略年龄从句。
2. **PRD 5.6 第 5 行仍写「N 小时前」**：不足 1 小时写分钟是 PM 这次裁决的新读法，代码已按此执行，但 PRD 正文还没有这句话。建议下一版把分钟写进 5.6，避免后续复审按字面判偏差。

### 七、r3 证据索引

- `reviews/probe-t6s7-r3.log`：84 checks / 0 FAIL，含 r3 的 7 条（两处补充句、页面与抽屉逐字同句、10% 行、受控时钟不翻转）。
- `reviews/mutation-r3-1.log` / `mutation-r3-1-ui.log`、`mutation-r3-2.log` / `mutation-r3-2-ui.log`：两个突变在纯逻辑层与组件层的失败输出。
- `reviews/test-logic-r3.log`、`test-components-r3.log`、`build-r3.log`、`bundle-r3.log`、`check-ui-r3.log`：门禁输出。
- 截图：`reviews/shots/14-stale-hold-notes.png`、`15-limited-hold-note.png`、`16-stale-hold-clock.png`。

## r4 增量

- 对象：`Newoahil/11-26-t6-s4-codex` 的 `4853671c`（r3 的 `f314c9b3` 之后只多这一个提交），已合入本分支（合并提交 `1f160083`，无冲突）
- 范围：`git diff f314c9b3..4853671c`（7 个文件，+36 / −1：`lendingAdvice.ts` 一行、三个语言包、两个测试、`impl-notes.md`）
- 标准：PRD v1.0.4（`Newoahil/11-25-t6-s1-claude` 的 `eeeb3720`，`git show` 只读核对）5.6 第 5 行与第 9 行

**结论：通过。** r3 的两条范围外观察都已关闭：读数时间缺失有了不带变量的句子，分钟读法也写进了 PRD 正文。另列 1 条既有、非本次引入的文案观察。

### 一、与 PRD v1.0.4 5.6 第 5 行一致

| 检查点 | 结论 | 证据 |
|---|---|---|
| 四种情形的判定顺序：回满已到 → 不足 1 小时分钟 → 满 1 小时小时 → 读数时间缺失 | 成立 | `lendingHoldRowNote` 依次判 `reset_at_ms <= generatedAtMS`、`observed_at_ms != null && ageMS < 1h`、`observed_at_ms != null`，最后才返回 `reading_stale_unknown`；`reviews/r4-boundary.log` 直接调用核对：回满已到 + 无读数时间与回满已到 + 5 分钟读数都返回过期句，回满未到 + 无读数时间返回新句 |
| 回满判断用 `lending.generated_at_ms`，年龄用页面时钟 | 成立 | 页面传 `lending.generated_at_ms`，抽屉传 `detail.generated_at_ms`；`ageMS = max(0, nowMS - observed_at_ms)`，`nowMS` 是页面时钟。受控时钟断言（r3 新增）本轮仍通过 |
| 不足 1 小时写分钟，N 至少 1 | 成立 | `< 1h` 走 `reading_stale_minutes`，`Math.max(1, Math.round(ageMS / 60_000))`；边界核对 5 分钟读数返回 `reading_stale_minutes(n=5)` |
| 满 1 小时写小时，无 `Math.max(1, 小时)` 抬升 | 成立 | `round(ageMS / 3_600_000)`；2 小时 10 分返回 `reading_stale(n=2)` |
| 新句中文与 PRD 逐字相同 | 成立 | 两边都是「读数时间不明，先刷新额度」（PRD 5.6 第 5 行第 ④ 种，逐字比对通过） |
| 英文、ms、zh-Hant 四个语言包都有 | 成立 | `index.tsx`（中 / 英）、`locales/ms.ts`、`locales/zh-Hant.ts` 都新增 `quota.lending.hold_note.reading_stale_unknown`；`check-i18n` 4385 keys 通过 |

### 二、占位符独立排查（不采信 impl-notes 的结论）

从 `web/src/i18n/index.tsx` 的 `DICT` 里按 key 边界切块，找出全部含 `{…}` 的 `quota.lending.*` 键共 33 个，逐个回到调用点核对。分三类：

| 类别 | 键 | 数据缺失时会怎样 |
|---|---|---|
| 值由调用点兜底，永不缺 | `value.percent`、`say_pair`、`detail_suggested`、`detail_suggested_keep`、`outside_priority_default`（数字走 `lendingPriorityText`，缺值给 `—`）；`say_pair` / `detail_*` / `evidence.name_pair` 的名字走 `lendingCardName`（`display_name → name → auth_index`，恒非空） | 不露 `{` |
| 参数缺失时改走不带变量的句子 | `hold_note.reading_stale` 与 `hold_note.reading_stale_minutes`（`observed_at_ms` 缺失时改走 `reading_stale_unknown`，本轮新增）；`hold_note.remaining_below_ten_window`（缺 `remaining_percent` 或 `window_kind` 或 `windowLabel` 时退回 `remaining_below_ten`）；`card.reset_at` / `card.reset_with_countdown`（`reset_at_ms` 缺失时走 `value.missing`）；`evidence.validity`（`valid_until_ms` / `observed_at_ms` 缺失时用 `value.missing` 填空） | 不露 `{` |
| 只在数据存在时渲染 | `evidence.names`（`names.length > 0`）、`evidence.outside` / `evidence.outside_detail`（`outside_cards.length > 0`）、`detail_outside_reason`（`unpooled_reason_codes.length > 0`）、`outside_toggle`（`OutsideSection` 在 0 张时返回 `null`） | 整句不渲染 |
| 计数与拼接 | `basis`、`entry_change_other`、`unpooled_title`、`pool_label`、`pool_change`、`pool_all_hold`、`say_set`、`say_with_keep`、`say_with_rest`、`say_balanced_other`、`say_hold_suffix`、`rank_n`、`rank_tied`、`hold_toggle`、`outside_now`、`detail_hold` | 都是数字、已拼好的子串或非空名字；`rank_*` 在无档位时先返回 `rank_same`，`detail_hold` 的原因缺失时给 `hold.unknown` |

结论：33 个占位键里没有一个会在数据缺失时把 `{` 留在界面上；「读数时间缺失」是唯一一条真的走得到、且以前会把占位符露出来的路径，本轮已按 PRD 改成不带变量的句子。

### 三、缺字段不动卡的页面与抽屉探针

在已有探针脚本里加第 15 节（`reviews/probe-t6s7.mjs`），喂三张缺字段的不动卡，页面展开行与凭证详情抽屉各读一遍渲染文字：

| 卡 | 缺什么 | 页面展开行 | 抽屉不动块 |
|---|---|---|---|
| `auth-01` | 读数过期卡，无 `observed_at_ms`，回满未到 | 「读数时间不明，先刷新额度」 | 同句 |
| `auth-02` | 余量 5%，`window_kind` 为空 | 「留作应急，不参与排名」 | 同句 |
| `auth-03` | 余量低于 10%，无 `remaining_percent` | 「留作应急，不参与排名」 | 同句 |

三行与三个抽屉的文字都不含 `{`，整页文字也不含 `{`。整套探针 91 checks / 0 FAIL（r3 的 84 条全保留，新增 7 条 r4 断言）。

### 四、新测试的牙口

突变：把新分支的返回改回旧的 `quota.lending.hold_note.reading_stale`（PowerShell 单点替换，UTF-8 无 BOM，跑完 `git checkout --` 还原，`git status --short` 干净）。

| 层 | 结果 | 证据 |
|---|---|---|
| 纯逻辑（`pnpm test:logic`） | **红** | `actual 'quota.lending.hold_note.reading_stale' vs expected '..._unknown'`，1 of 90 suite 失败 |
| 组件（`pnpm test:components`） | **红** | `names an unknown reading time on the page and in the drawer without a placeholder` 失败，1 failed / 378 passed |

### 五、红线与三处一致（只确认没被这个提交碰到）

- 提交只改了一句返回、三个语言包的文案、两个测试与实现说明，没有碰请求、路由、数字或入口代码；`git diff f314c9b3..4853671c -- web/src` 里没有任何写入或 fetch 路径。
- 顺带重跑整套探针确认状态没变：只读检查仍通过，唯一写请求仍是操作者保存时的一条 `PATCH /management/auth-files/fields`；三处一致（页面 / OAuth 入口 / 抽屉）名字、数字与判定一致。

### 六、门禁

| 门禁 | 结果 | 证据 |
|---|---|---|
| `pnpm type-check` | 通过 | exit 0 |
| `pnpm test:logic` | 通过 | 90 suites / 13.69s |
| `pnpm test:components` | 通过 | 51 files / 379 tests |
| `pnpm check-i18n` | 通过 | 4385 keys |
| `pnpm lint:antd` | 通过 | exit 0 |
| `pnpm check:ui --scenario quota-lending` | 通过 | 1/1，77.3s |
| `pnpm check:ui --scenario oauth-management` | 通过 | 1/1，32.8s |

两个 UI 场景分开跑（`--scenario` 传两次只跑后一个），5181 跑前空闲，未杀任何进程。

### 七、范围外观察

1. **英文小时句在 1 小时时不改单复数**：读数 1 小时到 1.5 小时之间，`reading_stale` 会渲染成「The last reading was 1 hours ago; refresh the quota」。中文、ms、zh-Hant 没有这个问题，PRD 也不要求单复数。这条在本轮提交之前就存在，不影响本轮结论；要修的话给小时句加一个单数变体即可。

### 八、r4 证据索引

- `reviews/probe-t6s7-r4.log`：91 checks / 0 FAIL，含 r4 的 7 条（三张缺字段卡在页面与抽屉的文字、整页无占位符）。
- `reviews/r4-boundary.log`：四种情形的判定顺序与回满边界（含恰好到点、回满已到优先于分钟读数）。
- `reviews/mutation-r4-1.log` / `mutation-r4-1-ui.log`：突变在纯逻辑层与组件层的失败输出。
- `reviews/gates-r4-typecheck.log`、`test-logic-r4.log`、`test-components-r4.log`、`gates-r4-i18n.log`、`gates-r4-antd.log`、`check-ui-r4.log`、`check-ui-r4-lending.log`：门禁输出。
- 截图：`reviews/shots/17-missing-fields-page.png`、`18-missing-fields-drawer.png`。
