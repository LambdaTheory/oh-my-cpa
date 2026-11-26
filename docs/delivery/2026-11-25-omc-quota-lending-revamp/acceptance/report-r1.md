# 额度拆借改版 · 验收报告 r1

- **结论**：**通过 90 / 不通过 1 / 阻塞 0 / 已知待复测 5 / 未测 0**（共 96 条）。不通过是 N-77：旧载荷缺少本轮新增字段时整页崩溃（两条新缺陷，见第 6 节）。已知待复测的 5 个用例覆盖前端审查报告 S7 的五项未修发现（P1-1、P1-2、P1-3、P2-1、P2-5）；S7 的另三项 P2-2、P2-3、P2-4 已在 `53ed6ba7` 修好，本轮断言通过，r2 复测确认。
- **被测**：分支 `Newoahil/11-26-t6-s8-codex`，在本工作区依次合入后端、前端、PRD v1.0.3、用例四个分支后重建二进制；逐条测试在 `3787a662` 的树上执行（其后的 `2f6688a5` 只提交报告骨架）。
- **标准**：`prd.md` v1.0.3（AC 编号）；用例 `cases.md`（N-1～N-96，含本轮补回的 13.6）；契约 `contract.md`。
- **证据**：截图在 `acceptance/shots/`（只留本机、不进 git）；临时脚本与日志在 `tmp/`（git 忽略）。

## 1. 合入与被测

| 顺序 | 合并提交 | 来源分支 | 带入内容 |
| --- | --- | --- | --- |
| 1 | `83d0e3c1` | `Newoahil/11-26-t6-s3-codex` | 后端 `f45bf1cf`：分档以本档最晚的卡为基准、回满时刻不晚于当前时刻算过期、不动卡的原因并列、池外口径的测试与契约；响应形状未变 |
| 2 | `a33e0bf6` | `Newoahil/11-26-t6-s4-codex` | 前端 `53ed6ba7`（含 PRD v1.0.2；P2-2～P2-4 已在这一版修好） |
| 3 | `a3e6429d` | `Newoahil/11-25-t6-s1-claude` | PRD v1.0.3 `5ac64d26` |
| 4 | `aacdadfe` | `Newoahil/11-26-t6-s5-claude` | 用例 `2de58a05`（tip `c44a8f2e`） |

- `cases.md` 在第 4 步冲突，按指示取用例分支版本，再把本工作区的 13.6 补充加回（`3787a662`）：G1/G2 满足、G3 未做、G4 结论、G5/G6 做法、Command Code 形状、v1.0.3 池外口径（含未进池卡、共 6 张）与 N-40 的文字冲突说明。
- 构建：`pnpm build` 成功；用 `golang:1.27.1` 交叉编译 `tmp/omc-lending.exe`（65,044,480 B，SHA-256 `2974BB6D…C5D84F6`），运行前设 `TZ=Asia/Shanghai`，所有 `spawn` 用 `windowsHide: true`。
- 后端合并只动 `contract.md` 与测试，`/management/quota` 的 `lending` 形状与字段名不变；接口形状实测 200、21 个允许字段齐全（`tmp/api-lending-r1.json`）。

## 2. 门禁与工具

| 检查 | 结果 | 证据 |
| --- | --- | --- |
| 构建 | `pnpm build` 成功，SPA 并入单二进制 | `tmp/omc-lending.exe` |
| 体验环境自检 | `node scripts/acceptance/lending-experience-env.mjs --check`：21/21 | `tmp/lending-check-r1.log` |
| 纯逻辑（Go `ProjectLendingAdvice`） | 256/256；覆盖 N-1～N-17、N-22 行 2、N-27、N-32、N-34、N-36、N-39、N-40、N-49、N-51、N-55、N-96 | `tmp/logic-result.json`、`tmp/logic-cases.mjs` |
| 前端逻辑套件 | `pnpm test:logic`：90 套全过（含 `scripts/test-quota-lending.ts`） | 终端输出 |
| 仓库探针 `quota-lending` | 118 PASS / 0 FAIL | `tmp/probe-lending.log` |
| 仓库探针 `oauth-management`（一期回归） | 63 PASS / 0 FAIL | `tmp/probe-oauth-r1.log` |
| 聚焦探针（S7 八项 + 53ed6ba7 修复） | 43 PASS / 6 FAIL，6 条全部是 S7 的未修项 | `tmp/probe-r1f.log` |
| 缺口探针（N-77 d/f、N-20 第二组、N-72 高度） | 8 PASS / 5 FAIL，5 条全在 N-77 | `tmp/probe-r1-gaps.log` |
| 真机保存链路（N-45、N-59） | 全 PASS：抽屉初值 0、保存一次 PATCH 200、回读变「不用改」、已还原 priority=0 | `tmp/e2e-save-r1.log` |
| Go 契约测试（`internal/api`） | `go test ./internal/api -run 'TestLendingCardInputs…|TestQuotaLending…'`：ok 1.154s | 本轮 docker `golang:1.27.1` |
| 接口形状与禁止键 | 200；21 个允许字段；无 token/secret/key/email/cookie/password；未登录 401 | `tmp/api-lending-r1.json`、`tmp/n82-r1.json` |
| 时区 | Asia/Shanghai 与 UTC 的拆借页/OAuth 行同一串，切回后恢复 | `tmp/lending-proof/timezone-r1.json` |
| 对比度 | 浅色 13px 2.87:1、副标题 4.38:1；深色 2.42:1、3.87:1；390 与 1440 相同 | `tmp/lending-proof/contrast-r1.json` |
| 灰度 | 「要改」「不用改」在灰度下仍是可读文字 | `tmp/lending-proof/r1-grayscale.png` |
| 密钥扫描 | `pnpm verify:secrets`：27.95 MB，no leaks | 终端输出 |
| 文案键 | `pnpm check-i18n`：DICT 4381 键，无缺口 | 终端输出 |
| 快速门禁 | `pnpm test:fast --base aacdadfe` 只报 documentation references 失败，全部来自 2026-11-12 期文档，与本轮 diff 无关（见第 7 节） | `tmp/test-fast-r1d.log` |

- 本机没有 Go 工具链（`go` 不在 PATH），Go 检查固定在 docker `golang:1.27.1` 里跑；这是环境既有红项，不是本轮缺陷。

## 3. 环境

- **hero（浩文两张卡）**：`http://127.0.0.1:18641/omc/quota-lending`，OAuth 入口 `http://127.0.0.1:18641/omc/oauth-management?page_size=48`，管理口令 `omc-e2e-management-key`；假 CPA 18642、控制口 18643；数据 `tmp/lending-experience-data`；状态 `tmp/lending-experience-env.json`（node PID 31804、app PID 45836）。
- **Command Code 场景**：`http://127.0.0.1:18651/omc/quota-lending`，OAuth 入口 `http://127.0.0.1:18651/omc/oauth-management?page_size=48`，同一口令；假 CPA 18652、控制口 18653；数据 `tmp/lending-scene-cmdcode-v2`；状态 `tmp/lending-experience-env-18651.json`（node PID 32144、app PID 21740）。
- 场景：hero 两张 `codex-demo-a/b` 都是 `pro`、现在都不设（0）、A 剩 88% 约 4 天 7 小时、B 剩 40% 约 2 天 2 小时，页面写「把 codex-demo-b 设成 -10；codex-demo-a 不用动」；Command Code 池 1 张要改、不动 2 张（余量低于 10% 1、额度类型不同 1）。
- 重置时刻在种场景时按「当前时间 + 偏移」生成，数字会随时间漂移；重新种：`node scripts/acceptance/lending-experience-env.mjs --reseed`（先停旧环境，或对状态文件里的端口另起）。关闭：`Stop-Process -Id <nodePid>`，应用随 node 退出。

## 4. 计数

| 类别 | 用例 | 通过 | 不通过 | 已知待复测 | 阻塞 | 未测 |
| --- | --- | --- | --- | --- | --- | --- |
| 5.1 建议值规则 | 17 | 17 | 0 | 0 | 0 | 0 |
| 5.2 主屏与池 | 12 | 9 | 0 | 3 | 0 | 0 |
| 5.3 不动、暂无建议与池外 | 14 | 14 | 0 | 0 | 0 | 0 |
| 5.4 看依据、时钟与界面 | 9 | 9 | 0 | 0 | 0 | 0 |
| 5.5 只读红线、三处一致与详情块 | 20 | 19 | 0 | 1 | 0 | 0 |
| 5.6 窄屏、主题、中英文与降级 | 12 | 10 | 1 | 1 | 0 | 0 |
| 5.7 一期回归 | 12 | 12 | 0 | 0 | 0 | 0 |
| 合计 | 96 | 90 | 1 | 5 | 0 | 0 |

- 口径：一个用例只有一条结论；「已知待复测」指断言直接落在 S7 五项未修样式/文案上的用例，行为照实记录、不重复算新缺陷，前端修完后 r2 复测。其它用例即使渲染同一个控件，只要其断言不涉及该属性就按通过计。

## 5. 逐条结果

### 5.1 建议值规则（N-1～N-17）

| 用例 | 结论 | 证据 |
| --- | --- | --- |
| N-1 | 通过 | 纯逻辑：两张卡现在 0/0，建议 0/−10，A 不用动、B 要改 |
| N-2 | 通过 | 纯逻辑：取值表行 1～4 与服务端一致 |
| N-3 | 通过 | 纯逻辑：三张及以上 0/−10/−20 |
| N-4 | 通过 | 纯逻辑：同档同数，下一档继续 −10 |
| N-5 | 通过 | 纯逻辑：H 只取榜内参与卡，不动与池外不进 H |
| N-6 | 通过 | 纯逻辑 + 聚焦探针 N-6.c：两张都读不到时建议照给、显示「—」、不判断要改 |
| N-7 | 通过 | 纯逻辑：23h59m 同档、24h00m 与 24h01m 另起一档 |
| N-8 | 通过 | 纯逻辑：分档以本档最晚的卡为基准（3.0/2.4/1.8 天 → 前两张同档） |
| N-9 | 通过 | 纯逻辑：只按回满时刻，不看剩余百分比 |
| N-10 | 通过 | 纯逻辑：取建议窗口的长窗口，短窗口不参与 |
| N-11 | 通过 | 纯逻辑：一个池一个榜，取卡数最多的一类 |
| N-12 | 通过 | 纯逻辑：卡数并列取最早回满更早的一类 |
| N-13 | 通过 | 纯逻辑：每类只有 1 张 → 暂无建议 |
| N-14 | 通过 | 纯逻辑：停用卡进未进池；榜内不足两张 |
| N-15 | 通过 | 纯逻辑：剩余 10% 边界 |
| N-16 | 通过 | 纯逻辑 + 仓库探针 derived：照排、带 `~`、标「参考」 |
| N-17 | 通过 | 纯逻辑：回满时刻完全相同 → 同档并列 |

### 5.2 主屏与池（N-18～N-29）

| 用例 | 结论 | 证据 |
| --- | --- | --- |
| N-18 | 已知（S7 的 P2-5），r2 复测 | 两张卡、0/−10、一句话、去设置、建议大字都对；页头顺序仍是标题→依据→标签（标签 top 164，标题 88–116），不在标题旁 |
| N-19 | 通过 | 仓库探针 keep：池标题「不用改」、一句话一致、无「去设置」 |
| N-20 | 通过 | 仓库探针 balanced（70/50、差 12 小时）＋缺口探针第二组（60/58 同回满）：不用改、小字「不到 1 天」、两张都写「第 1，并列」 |
| N-21 | 通过 | 聚焦探针：X/Y/Z 顺序、0/−10/−20、「2 张要改」、一句话与卡行一致 |
| N-22 | 通过 | 纯逻辑 N-22 行 2 ＋ `scripts/test-quota-lending.ts`：一句话各形态（含 `say_with_rest` 点名多张不动卡） |
| N-23 | 通过 | 聚焦探针/仓库探针：0 显示「默认」、读不到显示「—」且不判断 |
| N-24 | 通过 | 仓库探针 base/keep/derived/mixed ＋ 英文单复数：池名、N 张要改/不用改、参考标记 |
| N-25 | 已知（S7 的 P2-5），r2 复测 | 标题、最早读数行的内容对；固定标签不在标题旁（探针测到 tagInHead=false，标签在标题下方单独一行） |
| N-26 | 通过 | 时区脚本：上海 `12/01 05:52 回满（4天6小时后）`，UTC `11/30 21:52 …`，OAuth 行同一串；切回已还原 |
| N-27 | 通过 | 纯逻辑 N-27 ＋ 仓库探针 two-pools：保留后端池顺序，无建议池排最后 |
| N-28 | 已知（S7 的 P1-1、P1-2），r2 复测 | 12px：只提示/周限额/1 张要改/不用改/要改；10px：不动折叠「已用完」「不动」；回满时间 13px。卡名 16、句子 14、建议 22 |
| N-29 | 通过 | 灰度截图：两张卡的「要改」「不用改」仍是可读文字，不只靠颜色 |

### 5.3 不动、暂无建议与池外（N-30～N-43）

| 用例 | 结论 | 证据 |
| --- | --- | --- |
| N-30 | 通过 | 仓库探针 hold/no-advice：收起写计数、展开逐卡、不给建议数字 |
| N-31 | 通过 | 聚焦探针 a/b/c：十个原因各计 1、十行各有短标签、健康卡 0/−10 |
| N-32 | 通过 | 纯逻辑 + 聚焦探针 a/b：主原因取最高优先级，展开列全部原因 |
| N-33 | 通过 | 聚焦探针：「读数过期」在一个池里只出现一次计数 |
| N-34 | 通过 | 纯逻辑 + 仓库探针 restricted/limited：其余参与卡照常给数字 |
| N-35 | 通过 | 仓库探针 no-advice：整池收成一行「暂无建议」，点开看逐卡原因 |
| N-36 | 通过 | 纯逻辑：不会退回只按剩余百分比给先后 |
| N-37 | 通过 | 聚焦探针：没有不动的卡时不显示「不动」行 |
| N-38 | 通过 | 仓库探针 outside：提醒张数、默认收起、展开逐卡、池名与现在数字 |
| N-39 | 通过 | 纯逻辑 N-39 a–c ＋ 仓库探针：没有池外卡时整块不出现 |
| N-40 | 通过 | 纯逻辑 N-40：池外名单含未进池卡、不含本池的卡（v1.0.3），与 13.6 的夹具结论一致 |
| N-41 | 通过（`53ed6ba7` 已修） | 聚焦探针：写「低 10、20……见下面的名单」，无「排到所有」；r2 复看措辞 |
| N-42 | 通过 | 仓库探针 outside：有池的卡写池名与现在数字，无池的卡写来源与读不到 |
| N-43 | 通过 | 仓库探针 base/unpooled/outside ＋ 聚焦探针：默认收起，展开六块依次出现 |

### 5.4 看依据、时钟与界面（N-44～N-51、N-96）

| 用例 | 结论 | 证据 |
| --- | --- | --- |
| N-44 | 通过 | 聚焦探针：Enter 展开、Space 收起，数字与请求都不变 |
| N-45 | 通过 | 真机 e2e：抽屉初值是现在 0 而非建议 −10；保存走原「保存字段修改」一次 PATCH 200；已还原 |
| N-46 | 通过 | 聚焦探针：写「建议有效到」「读数时间」「到时先后会翻」 |
| N-47 | 通过 | 聚焦探针：页面时钟跳 2 小时，数字/判定/句子不变，只有倒计时变 |
| N-48 | 通过 | 聚焦探针两帧：顺序翻过来后两张都改变，池标题变「2 张要改」 |
| N-49 | 通过 | 纯逻辑：回满时刻不晚于当前时刻（含恰好到点）算过期 |
| N-50 | 通过 | 聚焦探针：页面时钟推进不产生通知请求（0） |
| N-51 | 通过 | 纯逻辑 ＋ 仓库探针 stale：回满已过而读数未刷新 → 不动，原因「读数过期」 |
| N-96 | 通过 | 纯逻辑：回满已过的卡不动，榜随之重算 |

### 5.5 只读红线、三处一致与详情块（N-52～N-71）

| 用例 | 结论 | 证据 |
| --- | --- | --- |
| N-52 | 通过 | 仓库探针 read-only：渲染、展开、点「去设置」全程无 PATCH/POST/PUT/DELETE 指向凭证或配置 |
| N-53 | 通过 | 仓库探针 read-only：OAuth 入口与凭证详情都只读，无写控件 |
| N-54 | 通过 | 仓库探针 set-it：链接只带凭证（`?open=auth-02`），不带数字；打开抽屉停在当前值；参数被消费 |
| N-55 | 通过 | 聚焦探针 N-55.b ＋ 仓库探针 cannot-edit：只有要改且能改的卡有「去设置」；不能改时写「不能在这里改」；不用改的卡不写拒绝语 |
| N-56 | 通过 | 仓库探针 read-only：页面没有应用/保存/切换/消耗按钮，也没有可编辑字段 |
| N-57 | 通过 | 合并 diff 未新增路由；新数据只作为既有 GET 响应的字段；探针全程只观察到 GET |
| N-58 | 通过 | 仓库探针 detail：详情里的「去设置」打开这张卡已有的优先级输入，不写 |
| N-59 | 通过 | 真机 e2e：保存后回读变 keep、池标题与句子翻转；已还原 priority=0 |
| N-60 | 通过 | 仓库探针 hero/long-name：主名称与 OAuth 页同一叫法（有账号名用账号名，没有用文件名） |
| N-61 | 通过 | 仓库探针 long-name：长名中间省略，完整名称在「看依据」里可读 |
| N-62 | 通过 | 仓库探针 base ＋ 聚焦探针：页面、入口、详情同一份数据的数字/要改/名称/池/有效期一致 |
| N-63 | 通过 | 仓库探针：入口四态 change/keep/plain/缺失（缺失与 null 都不显示） |
| N-64 | 通过 | 聚焦探针：入口只数要改的卡，读不到的卡不计入 |
| N-65 | 通过 | 仓库探针：入口是页头内的一行链接，页头高度前后相同（130.71875） |
| N-66 | 通过 | 仓库探针 detail：详情只画这张卡，能看出它在榜里 |
| N-67 | 通过 | 聚焦探针 C/D：低余量与读数过期的卡写「这张卡不动」、给出现在数字、没有「去设置」 |
| N-68 | 通过 | 聚焦探针 auth-03/04/12：不在任何池里 → 一句话加原因，建议不动，无建议数字 |
| N-69 | 通过 | 聚焦探针 a/b：字段缺失与 `null` 都不画拆借块、不画占位；重置权益照常 |
| N-70 | 已知（S7 的 P2-1），r2 复测 | 拆借块不再画整个池、与重置权益块分离；但仍含整句「不改 CPA 里任何设置」（探针测到 data-lending-detail-state=keep 且该句在） |
| N-71 | 通过 | 仓库探针：整组链接进入 `/omc/quota-lending`，不带写参数，返回回原页 |

### 5.6 窄屏、主题、中英文与降级（N-72～N-83）

| 用例 | 结论 | 证据 |
| --- | --- | --- |
| N-72 | 通过 | 缺口探针 390×844：池块 309px ≤ 一屏；`scrollWidth=390`；卡名/回满/数字三行 top 199/229/255；建议 22px > 卡名 16px |
| N-73 | 通过 | 聚焦探针 a–d：长名中间省略、看依据全名折行、全部展开与英文下 390 都无横向滚动 |
| N-74 | 通过 | 聚焦探针：760 三小行、761 宽屏一行，两种宽度都无横向滚动 |
| N-75 | 已知（S7 的 P1-3），r2 复测 | 浅色 13px 2.87:1、副标题 4.38:1；深色 13px 2.42:1、副标题 3.87:1；两种主题都低于 4.5:1（S7 只报了浅色，深色同样不达标） |
| N-76 | 通过 | 仓库探针英文 ＋ `pnpm check-i18n`：无缺键、英文无残留中文、单复数正确（1 to change / 2 to change） |
| N-77 | 不通过 | d：缺 `display_name` 时整页崩溃（TypeError，`splitDisplayName` 读 undefined.length），未退回文件名；f：缺 `outside_cards` 时同样崩溃（undefined.map/length），其余池也不显示。a/b/c/e 通过。详见第 6 节 D1/D2 |
| N-78 | 通过 | 仓库探针：只读既有批量额度端点，展开/收起不额外请求；页面时钟推进不多发请求（41→41） |
| N-79 | 通过 | `tmp/api-lending-r1.json` 200、21 个允许字段齐全；Go `TestQuotaLendingDTOAllowlist` 通过 |
| N-80 | 通过 | Go `TestQuotaLendingOverviewAndDetailAgree`（本轮 docker 运行）＋ 仓库/聚焦探针的三处数字一致 |
| N-81 | 通过 | 纯逻辑原因顺序 ＋ 聚焦探针：`primary_reason` 主原因在前，`reason_codes` 列全部 |
| N-82 | 通过 | hero 环境 `lending.priority` 与 `safe-fields.priority` 都是 0/默认、相等；Go `TestLendingCardInputsReadsRosterPriorityAndEditable`：15/3/2/9 来自凭证记录、compat 无优先级；s3 的 partial-priority 测试通过 |
| N-83 | 通过 | 未登录 401（无数据）；已登录响应无 token/secret/key/email/cookie/password；账号名只在管理接口 |

### 5.7 一期回归（N-84～N-95）

| 用例 | 结论 | 证据 |
| --- | --- | --- |
| N-84 | 通过 | `oauth-management` 探针：剩余百分比与「距重置」逐行照旧，推算值带 `~` |
| N-85 | 通过 | `oauth-management` 探针：`Fast reset · plenty left` 标记在，快重置判定不变 |
| N-86 | 通过 | `oauth-management` 探针：按距自然重置排序的选择与顺序保留 |
| N-87 | 通过 | `oauth-management` 探针：排序选择刷新后保留，筛选控件可用 |
| N-88 | 通过 | 仓库探针 two-pools/unpooled：同池判定、未进池汇总（未进池 2 张）照旧 |
| N-89 | 通过 | 聚焦探针：重置权益块的判定、次数、标签都在，且不提建议优先级/要改/去设置 |
| N-90 | 通过 | 仓库探针 empty：无同池时是一期空状态，不白屏 |
| N-91 | 通过 | 仓库探针 history：路径、标题、返回与直接打开都照旧；直接打开无返回控件 |
| N-92 | 通过 | 仓库探针 unpooled：未进池默认折叠，点开按原因分组 |
| N-93 | 通过 | 时区脚本 ＋ 仓库探针 clock：页面时钟与生效时区不变 |
| N-94 | 通过 | 接口只读 GET、无新历史；探针无推送/通知请求；采集与预测代码未改 |
| N-95 | 通过 | 聚焦探针：OAuth 列表行上的「偏用/少用」小标签已消失 |

## 6. 范围内要修

本轮新增 **2 条**，都落在用例 N-77（P0）；另有 S7 的 5 条未修样式/文案仍由前端按既定计划修，r2 复测。

### D1（P0）卡条目缺 `display_name` 时整页崩溃，未退回文件名

- 复现：给 `/management/quota` 的 `lending.pools[].cards[]` 去掉 `display_name`（模拟旧版载荷）；打开 `/omc/quota-lending`。
- 实际：整页变成「This page couldn't load」，控制台 4 条 `TypeError: Cannot read properties of undefined (reading 'length')`；栈顶为 `splitDisplayName`（`web/src/pages/quota/LendingAdviceView.tsx:51`，`DisplayName` 的 `name.length`）。
- 预期：卡名退回文件名（PRD 第 7 节「账号名拿不到时退回文件名」，N-77 ④）。
- 建议：在 `DisplayName` 或映射处取 `card.display_name || card.name || card.auth_index`；一句话与看依据里的名称走同一个取值。证据：`tmp/probe-r1-gaps.log` 的 N-77 d 三条、`tmp/lending-proof/r1-gap-debug.png`。

### D2（P0）池缺 `outside_cards` 时整页崩溃，未按「没有池外卡」降级

- 复现：给同一响应的 `lending.pools[]` 去掉 `outside_cards`；打开 `/omc/quota-lending`。
- 实际：整页「This page couldn't load」，`TypeError: Cannot read properties of undefined (reading 'map')` / `(reading 'length')`；`pool.outside_cards.length`（`LendingAdviceView.tsx` 的看依据块）与 `OutsideSection` 的 `cards.length` 都直接读。
- 预期：不显示池外提醒，池、卡、数字照常（N-77 ⑥）。
- 建议：把 `outside_cards` 缺失归一成空数组再往下传（只在这一层归一，其它字段仍按契约），或在 `OutsideSection`/看依据处用 `?? []`。证据：`tmp/probe-r1-gaps.log` 的 N-77 f 两条。

### S7 未修项（不是本轮新缺陷，前端修完后 r2 复测）

- P1-1 主屏 Tag 字号 12px、折叠行 Tag 10px；P1-2 回满时间 13px。
- P1-3 对比度：浅色 13px 2.87:1、副标题 4.38:1（深色 2.42:1、3.87:1，S7 只报了浅色，修时两套主题一起看）。
- P2-1 详情块仍含只读整句；P2-5 页头标签顺序（标题→依据→标签，要求标题→标签→依据）。
- P2-2、P2-3、P2-4 已在 `53ed6ba7` 修好：多张不用动点名、同档写并列、看依据改成「低 10、20……见下面的名单」；本轮断言通过，r2 只做确认。

## 7. 范围外观察

- **文档门禁既有红**：`pnpm test:fast --base aacdadfe` 只有 documentation references 失败，9 条 missing-path 与 22 条 line-reference 全部来自 `docs/delivery/2026-11-12-omc-three-credential-quota/{cases.md,impl-plan.md}`；`git diff aacdadfe..HEAD` 没有触及这两个文件，与本轮验收无关。
- **Go 工具链**：本机 PATH 里没有 `go`，全部 Go 检查在 docker `golang:1.27.1` 里执行；这是环境既有状态，不当作产品缺陷。
- **深色主题对比度**：除 S7 已报的浅色外，深色 13px 2.42:1、副标题 3.87:1 也不达标；归入 P1-3 一起修，不另计一条。
- **探针与实现口径不一致**：仓库探针里「只读标签位于标题块下方」的断言是照实现写的，与 PRD 5.3 / S7 P2-5 的「标题旁」要求相反；本轮以 PRD 与用例为准，不把该探针断言当作 N-25 的通过证据。

## 8. AC 覆盖

改写与新增逐条：

| AC | 用例 | 结论 |
| --- | --- | --- |
| AC-6、AC-7、AC-8（改写） | N-1～N-17、N-31、N-34、N-36、N-40、N-51、N-96 | 通过 |
| AC-9、AC-10、AC-11（改写） | N-18～N-29、N-43、N-76 | 通过（N-18/N-25/N-28 的已知待复测项除外） |
| AC-12（改写） | N-52～N-57 | 通过（全程只读） |
| AC-13（沿用） | N-89 | 通过（重置权益块不变） |
| AC-14b（改写） | N-63～N-65 | 通过 |
| AC-15（改写） | N-62、N-66～N-71、N-80 | 通过；N-70 的详情只读整句仍待修（P2-1） |
| AC-18（新增） | N-18、N-21、N-28、N-72 | 通过；字号见 N-28 的已知项 |
| AC-19（新增） | N-3、N-21、N-34 | 通过 |
| AC-20（新增） | N-23、N-79、N-82 | 通过 |
| AC-21（新增） | N-20、N-22 | 通过 |
| AC-22（新增） | N-20、N-43 | 通过 |
| AC-23（新增） | N-47、N-50、N-78、N-94 | 通过（不随时钟变、不推送、不新增请求） |
| AC-24（新增） | N-30～N-35 | 通过 |
| AC-25（新增） | N-38～N-43 | 通过（池外口径按 v1.0.3） |
| AC-26（新增） | N-54～N-56、N-58、N-59 | 通过（去设置只跳转、不写） |
| AC-27（新增） | N-60、N-61、N-73 | 通过 |
| AC-28（新增） | N-66～N-71 | 通过；N-70 的整句待修（P2-1） |
| AC-29（新增） | N-72～N-75 | 通过；N-75 对比度待修（P1-3） |
| 降级（PRD 第 7 节兼容，N-77） | N-77 a～f | **不通过**：a/b/c/e 通过，d/f 崩溃（D1/D2） |

沿用一期 AC-1～AC-5、AC-14、AC-14a、AC-14c、AC-16、AC-17 的冒烟由 N-84～N-95 覆盖，全部通过。

## 9. 环境与清理

- 两个体验环境仍在运行：hero 与 Command Code（入口、口令、端口、PID 见第 3 节）。关闭：`Stop-Process -Id 31804`、`Stop-Process -Id 32144`（应用随 node 退出）。保留原因：r2 还要用同一场景复测；数据目录里的重置时刻是种场景时定的，r2 前可用 `--reseed` 重新锚定。
- 占用的本机端口：18641/18642/18643、18651/18652/18653；探针临时用过 18647/18648/18649/18650，已随脚本退出释放。
- 本轮没有执行删除类命令，也没有被平台拦下的命令；`pnpm check:ui` 未运行（未占用 5181），只用自己的探针与体验环境。
- 自建产物（全部保留，未删、未入库）：
  - `tmp/omc-lending.exe` 65,044,480 B；`tmp/lending-experience-data` 3,098,056 B；`tmp/lending-scene-cmdcode-v2` 3,192,816 B；早期场景目录 `lending-check-data`、`lending-scene-cmdcode`、`-cmdcode-fail`、`-herokeep`、`-outside` 各约 3 MB。
  - `tmp/lending-proof/` 2,983,356 B（对比度、时区、灰度、N-77 崩溃截图）。
  - 日志：`tmp/probe-lending.log`、`tmp/probe-oauth-r1.log`、`tmp/probe-r1f.log`、`tmp/probe-r1-gaps.log`、`tmp/lending-check-r1.log`、`tmp/e2e-save-r1.log`、`tmp/test-fast-r1d.log`、`tmp/lending-experience-app.log`。
  - 脚本：`tmp/logic-cases.mjs`、`tmp/run-lending-probe.mjs`、`tmp/run-one-probe.mjs`、`tmp/probe-r1-known.mjs`、`tmp/probe-r1-gaps.mjs`、`tmp/e2e-save-r1.mjs`、`tmp/shots-r1.mjs`、`tmp/contrast-r1.mjs`、`tmp/timezone-r1.mjs`、`tmp/grayscale-r1.mjs`、`tmp/api-lending-r1.mjs`、`tmp/n82-r1.mjs`。

截图（`docs/delivery/2026-11-25-omc-quota-lending-revamp/acceptance/shots/`，由 `.gitignore` 排除）：

| 文件 | 内容 |
| --- | --- |
| `05-r1-hero-oauth-entry.png` | OAuth 页头入口「额度拆借 · 1 张要改」 |
| `05-r1-hero-lending.png` | 浩文两张卡的拆借页与一句话 |
| `06-r1-cmdcode-lending.png` | Command Code 池（1 张要改 + 不动 2 张） |
| `06-r1-cmdcode-hold.png` | 不动卡展开 |
| `06-r1-cmdcode-oauth-entry.png` | Command Code 的 OAuth 入口 |
| `01`～`04` | 上一轮留下的环境与入口截图 |

## 10. 证据索引

| 证据 | 覆盖 |
| --- | --- |
| `tmp/logic-result.json`、`tmp/logic-cases.mjs` | 真实 Go `ProjectLendingAdvice` 的 256 条取值断言 |
| `tmp/probe-lending.log` | 仓库 `quota-lending` 探针 118 PASS |
| `tmp/probe-oauth-r1.log` | 仓库 `oauth-management` 探针 63 PASS（一期回归） |
| `tmp/probe-r1f.log` | 聚焦探针 43 PASS / 6 FAIL（6 条全是 S7 未修项） |
| `tmp/probe-r1-gaps.log` | 缺口探针：N-20 第二组、N-72、N-77（含 5 条失败） |
| `tmp/e2e-save-r1.log` | 真机保存链路 N-45/N-59 |
| `tmp/api-lending-r1.json`、`tmp/n82-r1.json` | 接口形状、允许字段、优先级同源 |
| `tmp/lending-check-r1.log` | 体验环境自检 21/21 |
| `tmp/lending-proof/contrast-r1.json` | 浅色/深色/390 的对比度 |
| `tmp/lending-proof/timezone-r1.json` | Asia/Shanghai 与 UTC 的同一串 |
| `tmp/lending-proof/r1-grayscale.png` | 灰度下的「要改/不用改」 |
| `tmp/lending-proof/r1-gap-debug.png`、`r1-gaps-390.png` | N-77 崩溃页与 390 下的池布局 |
| `tmp/test-fast-r1d.log` | `pnpm test:fast --base aacdadfe` 的既有文档红项 |
