# 前端验收报告 · 额度拆借页改版（T6-S7）

- 审查对象：`Newoahil/11-26-t6-s4-codex`（切片 S5，已合入后端 `Newoahil/11-26-t6-s3-codex`），HEAD `882ffbfe`
- 对照基线：产品小队 `Newoahil/11-25-t6-s1-claude`
- 唯一验收标准：`docs/delivery/2026-11-25-omc-quota-lending-revamp/prd.md` **v1.0.2**（`758a09ae`），并用 `E:/works/Homan-context/runs/briefs/2026-11-26-T6-prd-clarifications.md` 的 P-01～P-14 读法
- 画面依据：同目录 `review.html` 与 `before/`；与 PRD 不一致时以 PRD 为准
- 契约：同目录 `contract.md`；实现说明：同目录 `impl-notes.md`

## 结论

**范围内要修 8 条**（P1 ×3、P2 ×5），**范围外观察 3 条**。核心流程与只读边界成立：每张卡一行、现在→建议并排、三处一致、去设置不写入、保存后刷新、受控时钟不重算数字，均有一手证据。要修集中在字号与对比度（P-12）、详情块只读整句（AC-28）、页头标签顺序（5.3），以及 P-07 / P-13 / P-14 三条文案还原度。

## 一、范围与方法

- 代码审查：`git diff Newoahil/11-25-t6-s1-claude...HEAD` 共 39 个文件；重点读 `web/src/pages/quota/LendingAdviceView.tsx`、`lendingAdvice.ts`、`LendingAdviceView.module.css`、`QuotaLendingPage.tsx`、`LendingAdviceEntryLink.tsx`、`CompactQuotaView.tsx`、`CredentialQuotaBody.tsx`、`AuthFileDetailDrawer.tsx`、`OAuthManagementPage.tsx`、`types/quota.ts` 与相关测试、夹具。
- PRD 还原度：按 v1.0.2 的 AC-9、11、12、14b、15、18～29 前端部分与第 5.3～5.11 节逐条核对。
- 界面真点：`reviews/probe-t6s7.mjs`（不入 git）复用仓库探针原语 `startVite`、`createProbePage`、`installRoutes`、`createProbeChecker`，接口桩用 `scripts/acceptance/lending-fixtures.mjs` 的 `probeLending` 与 `scripts/acceptance/probes/oauthManagement.mjs` 的 `oauthManagementProbeRoutes`；headless Chromium，虚拟端点，不用真实账号、不连生产。
- 只读断言：页面 `page.on('request')` 记录所有 POST/PUT/PATCH/DELETE（请求发出即记账），PATCH 桩再显式记录请求体；渲染、展开折叠、点「去设置」全程监听。本机没有 Go，未做真实后端层，见第八节。
- 联调接缝：前端只消费后端字段（`needs_change`、`suggested_priority`、`tier`、`priority_known`、`valid_until_ms`、`outside_cards` 等），未发现前端重算建议值；字段名与 `contract.md` 对齐：`contract.md` 的 `priority_known`、`suggested_priority`、`needs_change`、`needs_change_count`、`is_balanced`、`valid_until_ms`、`outside_cards` 与 `web/src/types/quota.ts` 的 `LendingPool` / `LendingCard` 同名同义；`pools` 顺序由后端按 contract 1.1 保证，前端只 `map`。见 `lendingAdvice.ts` 的 `lendingEntryState`、`findLendingCard`、`lendingPoolSentence`。
- 夹具说明：`probeLending` 与 OAuth 安全字段夹具对 auth-02 的当前优先级起始值不同（0 与 1）；探针把两者对齐成同一来源，模拟生产里页面与设置页读同一个 CPA 优先级。这属证据夹具，不是产品行为。
- 突变探针 3 个：只改一处、跑相关测试、`git checkout --` 还原，每次后 `git status --short` 干净；命令与失败用例见第六节。

## 二、门禁与自测

| 门禁 | 结果 | 证据 |
|---|---|---|
| `pnpm type-check` | 通过 | exit 0 |
| `pnpm check-i18n` | 通过 | 4379 keys，exit 0 |
| `pnpm check-css-modules` | 通过 | 521 consumers，exit 0 |
| `pnpm check:motion` / `check:style-tokens` / `check:feedback` | 通过 | exit 0 |
| `pnpm lint:antd` | 通过 | deprecated/a11y/usage/performance 均 0 |
| `pnpm test:logic` | 通过 | 90 suites，33.33s |
| `pnpm test:components` | 通过 | exit 0；仅 jsdom pseudo-element 警告 |
| `pnpm build` + `pnpm check:bundle` | 通过 | 本次会话构建并跑加载边界/异常上限 |
| `pnpm check:demo` | 通过 | 155 responses / 20 routes，current |
| `pnpm check:ui --base Newoahil/11-25-t6-s1-claude` | 通过 | 81/81 场景，526.5s，`reviews/check-ui.log` |
| `node scripts/check-docs.mjs` | **基线红** | 22 line-reference + 9 missing-path，全部在 `docs/delivery/2026-11-12-omc-three-credential-quota/`，本包未新增 |
| `pnpm test:self` | **基线红** | 505 tests，495 pass / 10 fail；`fixed waits in tests only ever decrease`、发布/归档环境类，以及 Go 缺失的 `spawnSync go ENOENT`；与本包无关 |
| 本机 Node | 基线偏差 | `v24.16.0`，仓库期望 `22.23.2`（engine 警告） |

## 三、AC 逐条（前端部分）

| AC | 结论 | 证据 |
|---|---|---|
| AC-9 池序 | 部分核实 | `LendingAdviceView` 的 `pools.map` 按后端数组顺序渲染，前端未重排；多池顺序未在探针里断言，见第八节 |
| AC-11 池标题行 | 通过 | `01-base-1440.png`：`codex · Pro 200`、`周限额`、`1 张要改` |
| AC-12 只读 | 通过 | 网络监听无 PATCH/POST/PUT/DELETE；点「去设置」也不写 |
| AC-14a / 5.3 页头 | **有偏差** | 标题、标签、依据行都在，但顺序是标题→依据→标签；PRD 与 mock 要求标题→标签→依据，见 P2-5 |
| AC-14b 入口三态与缺失 | 通过 | 三态 `change/keep/plain`；`lending` 缺失或 `null` 都不显示；头部高度差 ≤1px |
| AC-14c 未进池 | 通过 | `01`：`未进池 2 张` 与按原因计数 |
| AC-15 三处一致 | 通过 | 页面/入口/详情：name、now、suggested、action、sentence、entry 全等（`three surfaces` 日志） |
| AC-18 每卡一行与字号 | **有偏差** | 行结构与建议大字正确；字号见 P1-1 / P1-2 |
| AC-19 建议值 | 通过 | 0 / −10；前端不重算，直接用后端 `suggested_priority` |
| AC-20 现在的优先级 | 通过 | 读不到显示 `—`、建议照给、行尾「现在的数字读不到」，不计入 `N 张要改` |
| AC-21 要改 / 不用改 | 通过 | base、keep、balanced 三态；P-03 的全读不到读法见第七节 |
| AC-22 看依据 | **有偏差** | 小表与区别说明在；同档排名与池外绝对说法见 P2-3 / P2-4 |
| AC-23 有效期与时钟 | 通过 | 受控时钟前进 2 小时：`now`/`suggested` 不变，倒计时变化 |
| AC-24 不动的卡 | 通过 | hold 折叠计数与展开原因；无建议数字 |
| AC-25 池外提醒 | **有偏差** | 名单、数量、当前优先级通过；绝对说法见 P2-4 |
| AC-26 去设置 | 通过 | 链接 `?open=auth-02` 不带数字；打开不写；输入框是当前值；保存只发一条 PATCH 且回页数字刷新；P-11 正反都通过 |
| AC-27 卡名一致 | 通过 | 三处均为 `team-b@example.test`；长名中省略 |
| AC-28 详情块 | **有偏差** | 详情块仍含只读整句，见 P2-1 |
| AC-29 窄屏与主题 | **有偏差** | 390/760/768/1024 无横向滚动、三小行、深浅色与中英文通过；对比度见 P1-3 |

## 四、问题清单

### P1-1 主屏 Tag 文字 12px / 10px

- 复现：打开 `/quota-lending`（base 夹具），量主屏所有可见文字的计算字号。`reviews/typography.json` 与 `reviews/probe-t6s7.log` 的 `FAIL type: the main screen has no text at 12px or below` 列出 6 条 12px：`只提示，不改路由`、`周限额`、`1 张要改`、`不用改`、`要改`、`不用改`。
- 证据：`web/src/index.css` 的 `.tag` 用 `--text-sm`（12px），`.tag.is-small` 用 `--text-2xs`（10px）；`--text-sm:12px`、`--text-base:14px`。`Tag size="small"` 在「不动」折叠计数/行里是 10px。
- 违反：AC-18 与 5.11「主屏不出现 12 号（12px）及以下的字」。
- 建议：主屏 Tag 至少用 `--text-md`（13px）或与正文 `--text-base`（14px）对齐；`Tag size="small"` 的 10px 同样上调，不再用 `--text-2xs`。
- 证据截图：`reviews/shots/01-base-1440.png`（可见标签明显小于正文）。

### P1-2 回满时间 13px

- 复现：同一 base 页，量 `[data-lending-card="auth-02"]` 的回满时间行。`probe-t6s7.log` 的 `FAIL type: main text is at least the console body size — console=14px` 列出 `_card-reset_*` 为 13px。
- 证据：`web/src/pages/quota/LendingAdviceView.module.css` 的 `.card-reset { font-size: var(--text-md); }`（`--text-md:13px`）。
- 违反：5.11 把「回满时间」列为主屏主要文字，要求不小于控制台正文 14px；AC-18 同。
- 建议：`.card-reset` 改用 `--text-base`（14px），与剩余、现在、建议的正文层一致。

### P1-3 正文对比度不足

- 复现：同一 base 页，逐元素取计算颜色与有效背景，按 WCAG 算对比度。`probe-t6s7.log` 的 `FAIL contrast: body text reaches 4.5:1 (v1.0.2 P-12)` 给出：`rgb(152, 152, 159)` 的 13px 文字（剩余/现在/建议设成标签、回满时间、默认）对白底为 **2.87:1**；副标题 `rgb(120, 120, 128)` 的 13px 为 **4.38:1**。
- 证据：`reviews/typography.json` 的 `ratio` 字段；`web/src/pages/quota/LendingAdviceView.module.css` 的 `.card-caption`、`.card-reset`、`.card-default` 与 `terminal-subtitle` 的颜色令牌。
- 违反：P-12 与 5.11 / 第 7 节「正文不低于 4.5:1，大字不低于 3:1」；13px 不属大字，按正文 4.5:1。
- 建议：这些 13px 文字换用更深的文字令牌（`rgb(80, 80, 85)` 对白底约 7.4:1，或调深 secondary 令牌到 ≥4.5:1）；副标题的 4.38:1 也要一并调到达标。若只改页面而不动令牌，需确认设计说明里的下限本身是否写错。

### P2-1 详情块仍含只读整句

- 复现：在 OAuth 页打开 `auth-02` 详情，展开拆借块的「看依据」，正文含「不改 CPA 里任何设置」的整句。`probe-t6s7.log` 的 `FAIL detail: the block omits the long read-only sentence (AC-28)`，截图 `reviews/shots/07-drawer-reset-advice.png`。
- 证据：`LendingAdviceView.tsx` 的 `DetailBlock` 渲染 `<Evidence pool={pool} now={now} detail />`；`Evidence` 无条件渲染 `<p className={styles.note}>{t('quota.lending.read_only_note')}</p>`（`read_only_note` =「只提示，不改路由：页面上没有应用按钮，不改 CPA 里任何设置。」）。
- 违反：AC-28「详情块……不再画整个池块，不含只读整句；页头小标签保留」；5.10 同。
- 建议：`Evidence` 在 `detail` 模式下不渲染 `read_only_note`，保留 `distinction`；详情页头已有「只提示，不改路由」小标签。

### P2-2 多张不用动时未点名

- 复现：构造 1 张要改 + 2 张不用动（探针 `oneChangeTwoKeeps`），一句话为「把 team-b@example.test 设成 -10；其余 2 张不用动」。`probe-t6s7.log` 的 `FAIL sentence/keep-many...`，截图 `reviews/shots/11-keep-many-sentence.png`。
- 证据：`lendingAdvice.ts` 的 `lendingPoolSentence` 在 `kept.length > 1` 时用 `quota.lending.say_with_rest`；`web/src/i18n/index.tsx` 该键为「{set}；其余 {n} 张不用动」。
- 违反：v1.0.2 5.3「不用动的卡有多张时写『把 X 设成 a；Z、W 不用动』」——要点名，不是计数。
- 建议：`say_with_rest` 改为拼接 `kept` 的 `display_name`（用列表分隔符），英文按同一结构处理单复数。

### P2-3 同档排名未写「并列」

- 复现：balanced 夹具（两张同档），展开「看依据」，排名列为「差不多 | 差不多」。`probe-t6s7.log` 的 `FAIL ties: cards in one tier share a rank and say 并列 (v1.0.2 P-14)`。
- 证据：`lendingAdvice.ts` 的 `lendingRankLabel`：`pool.is_balanced || card.tier == null` 返回 `quota.lending.rank_same`（「差不多」），否则 `quota.lending.rank_n`（「第 {n}」），没有「并列」分支。
- 违反：v1.0.2 5.4 / AC-22「同档的卡在『排第几』里写同一个名次并标『并列』，例：『第 1，并列』」。
- 建议：同档（含 `is_balanced`）输出「第 {n}，并列」；不同档仍「第 {n}」。开发已在修 P-14。

### P2-4 看依据仍有绝对说法

- 复现：outside 夹具，展开「看依据」，池外提醒句仍含「排到所有没设数字的卡之后」。`probe-t6s7.log` 的 `FAIL outside/evidence: no absolute claim ... (v1.0.2 P-13)`。
- 证据：`web/src/i18n/index.tsx` 的 `quota.lending.evidence.outside` 仍为「建议值只往下压，所以排后面的卡会排到所有没设数字的卡之后。」
- 违反：P-13「去掉这类绝对说法（H 不小于 10 时不成立）」，改为「比这一组现在的最大数字低 10、20……；池外同一个模型的卡现在是几，见下面的名单」。
- 建议：按 P-13 的新句替换中英文；开发已在修 P-13。

### P2-5 页头标签位置与 5.3 / mock 不符

- 复现：打开 `/quota-lending`，页头顺序为「额度拆借建议」→「依据 … 的读数 · 数字越大，越先用」→「只提示，不改路由」。截图 `reviews/shots/01-base-1440.png`。
- 证据：`QuotaLendingPage.tsx` 先渲染 `<PageHeader title subtitle />`，再由 `LendingAdviceView` 的 `.page-head` 渲染只读标签；`review.html` 的 `.pg-h` 把标题与 `pill` 放在同一行，依据在下一行；PRD 5.3 第 1 行「标题；紧挨着固定显示标签；下面一行依据」。
- 违反：5.3 页头顺序（AC-14a）。标签不在标题旁，依据行也不在标签下方。
- 建议：把标签并入 `PageHeader` 的 `title`（`ReactNode` 可包含标签）或加一个标题旁的 badge 槽，使顺序为标题→标签→依据；若团队决定保留现状，需要 PM 明确接受这个版式偏差。

## 五、三处一致与只读

- 三处一致（`probe-t6s7.log` 的 `three surfaces: the changed card agrees...`）：页面 `team-b@example.test / 0 → -10 / change`，详情同；入口 `额度拆借 · 1 张要改`。不变卡 `auth-01` 三处一致、详情无「去设置」。
- 只读：渲染、展开「看依据」「不动」、点「去设置」的过程中没有 PATCH/POST/PUT/DELETE；全程只有操作者在配置页点「保存字段修改」时的一条 `PATCH /management/auth-files/fields`，体为 `{name:'credential-02.json',auth_index:'auth-02',priority:-10}`。页面无「应用/保存/切换/消耗」按钮、无可编辑字段。
- 去设置：链接 `?open=auth-02`，不带数字、不带 `priority`；打开抽屉停在「配置」，优先级输入为当前值 `0` 而非建议 `-10`；打开不写。保存后回拆借页，`data-lending-now="-10"`、动作变 `keep`、池标题 `不用改`。
- AC-23：`clock.install` 后前进 2 小时，`data-lending-now` 与 `data-lending-suggested` 不变，倒计时文本变化（`reviews/shots/10-controlled-clock.png`）。
- P-11：要改且不能改的卡显示「这张卡的优先级不能在这里改」；不用改且不能改的卡不显示（探针正反两 check 均通过）。
- P-03：全读不到时池标题仍「不用改」、行显示 `—`、不计 `N`，符合 v1.0.2 的读法（见第七节）。

## 六、突变探针

| # | 突变 | 命令 | 结果 | 还原 |
|---|---|---|---|---|
| 1 | `LendingAdviceView.tsx` 的 `cardAction` 把 `true/false` 判断写反 | 目标 vitest `web/tests/lending-advice.component.test.tsx` | 5 failed / 18，例：`expected 'change' to be 'keep'` | `git checkout -- web/src/pages/quota/LendingAdviceView.tsx`；`git status --short` 干净 |
| 2 | `lendingAdvice.ts` 的 `lendingEntryState` 去掉 `if (!lending) return null`，改成返回 plain | `node --experimental-strip-types --import ./scripts/ts-resolve.mjs scripts/test-quota-lending.ts` | 1 failed / 15：`expected null, got {kind:'plain'}` | `git checkout -- web/src/pages/quota/lendingAdvice.ts`；干净 |
| 3 | `LendingAdviceView.tsx` 的 `DetailBlock` 改成读 `pool?.cards[0]`（读错卡） | 目标 vitest 同上 | 2 failed / 18，例：`expected 'hold' to be 'keep'` | `git checkout -- ...`；干净 |

- 三个探针都只改一处、相关测试变红、还原后绿；按 PM 2026-11-25 的规矩用 PowerShell 文本替换（UTF-8 无 BOM），未用删除命令，未新建突变文件。
- 一期被取代的断言去向：`web/tests/credential-quota.component.test.tsx` 已去掉角色小标签断言；新断言在 `web/tests/lending-advice.component.test.tsx` 与 `scripts/test-quota-lending.ts`，覆盖建议、入口、详情与只读。

## 七、范围外观察

1. **一期「偏用/少用」小标签移除干净（已接受）**：无 `data-lending-role-chip`、无 `lendingRoleTone` / `lendingRoleLabel`、无旧文案键/样式/测试断言残留；`CompactQuotaView` 的 diff 只删 chip 与 prop，倒计时、快重置标记、排序、额度内容未变；PRD v1.0.1 已明确随一期用语退役。判为范围外观察，不要求改。
2. **P-03 全读不到**：池标题「不用改」、一句话「现在的设置已经合适，不用改」；v1.0.2 明确池标题指「读得到的卡」，全读不到属已知限制（读不到多出现在不进池的兼容条目）。不要求改，但建议后续在文案上区分「没有可判断的卡」。
3. **P-05 / P-10**：前端按后端 `pools` 顺序渲染（`LendingAdviceView` 的 `pools.map`），未重排；停用原因「凭证已停用，不计入池」含规定词。均不要求改；池序的最终正确性属后端/联调。

## 八、未核实与缺口

- 本机没有 Go：未构建二进制、未跑真实后端探针；「合入后端后的联调接缝」按 `contract.md` 与夹具字段核对，不是真实后端输出。
- 多池顺序（AC-9 / P-05）未在界面探针里断言；`twoPools` 夹具存在但本次未纳入真点。
- P-08「回满时刻已过当作过期」的后端 `reading_stale` 路径未在前端探针覆盖，只按字段透传检查。
- 对比度只量了主屏可见文本；详情抽屉与深色主题未逐元素量（深色截图 `03-dark.png` 仅作视觉检查）。
- `pnpm build && check:bundle` 与 `check:ui` 在本次会话较早步骤完成；之后只改了 `reviews/` 与报告，产品源码未变。
- `check-docs` 与 `test:self` 的基线红、Node 版本偏差见第二节，未计入本包。

## 九、环境与清理

- 未执行任何删除类命令，没有被平台拦下的删除对象。
- 本次新建的临时证据在 `reviews/`（`probe-t6s7.mjs`、`probe-t6s7.log`、`typography.json`、`check-ui.log`、`test-self.log`、`shots/`），按约定不入 git、保留给主会话处理。
- 突变探针全部还原；最后 `git status --short` 只应有本报告（`reviews/` 被忽略）。
- 端口：探针用 Vite 5195，`check:ui` 用 5181；跑完已停，无残留监听进程。

## 十、证据索引

- `reviews/probe-t6s7.log`：62 checks / 7 FAIL，含每条的文字度量、三处一致、只读、AC-23、P-07/P-13/P-14。
- `reviews/typography.json`：主屏 32 条可见文字的字号、颜色、背景、对比度。
- `reviews/check-ui.log`：81/81 场景，526.5s。
- `reviews/test-self.log`：505 tests / 10 baseline fails。
- 截图：`reviews/shots/01-base-1440.png`、`02-width-{390,760,768,1024}.png`、`03-dark.png`、`04-english.png`、`05-state-*.png`、`06-three-surfaces-drawer.png`、`07-drawer-reset-advice.png`、`08-set-it-config.png`、`09-after-save.png`、`10-controlled-clock.png`、`11-keep-many-sentence.png`。
