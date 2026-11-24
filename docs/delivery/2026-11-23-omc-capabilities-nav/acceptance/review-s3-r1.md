---
type: review
slug: 2026-11-23-omc-capabilities-nav-review-s3-r1
status: final
verdict: 要修
subject: Newoahil/11-24-t9-s3-codex @ 733940c2
baseline: 11-24-t9-dev-base (dev 90a8d8ff)
standard: prd.md（定稿，唯一基准）
updated: 2026-11-24
summary: "S3 仓库自有密钥独立页的两路审查（代码、PRD 还原度）：面板 F1～F12 与拆页前逐项一致，没有新增写操作；要修 3 条，均为一般或轻微"
---

# 审查报告：S3 仓库自有密钥独立页（r1）

审查对象：分支 `Newoahil/11-24-t9-s3-codex`，头提交 `733940c2`，基线 `11-24-t9-dev-base`（dev `90a8d8ff`）；共 7 个提交、21 个文件、269 行新增、16 行删除。
依据：同需求目录的 `prd.md`（定稿）；验收用例草稿 `cases.md` 只借用对照方法，判定以 PRD 为准。
审查方式：只读代码与 diff，自己跑前端门禁，在 Edge 无头里用接口打桩数据把拆页前后逐项对照；不改业务代码、不推送。

## 1. 结论

**范围内要修，共 3 条，没有严重项**：R1 一般、R2 一般、R3 轻。S3 的核心承诺都兑现了：

- 面板 F1～F12 与拆页前逐项一致。`RepoKeysPanel.tsx` 的 diff 只有「页内托管时不画自己的标题」一处行为改动；同一份桩数据下，九个静态场景与 25 步交互（含全部写操作）两端逐项相同，面板可见文字只差基线独有的那行 `h2` 标题。
- 没有新增请求与写操作：新页挂载的仓库密钥读取与基线面板的四条完全相同；密钥管理页比基线少读三条（待审批、待交付、审计推送），仍读仓库密钥列表给所属仓库小标用；19 个打开页面的场景里写请求为 0。
- 密钥管理页只留网关密钥并在页底有一行静态提示（空、失败、加载中也在）；审批链接页返回入口改指 `/repo-keys`；Go 与菜单等越界文件零改动。

AC-8、9、10、12、14、23（演示部分）、24、25、28（本切片部分）、29 逐条都是已实现，附带两处说明：AC-14 缺自动化断言（R2），AC-24 的页头刷新范围略宽（R1）。逐条证据见 §3。

要修：

1. R1（一般）页头刷新用默认的 `refetchQueries`，会连不活跃的同族缓存一起重读（实测敲搜索、开详情后刷新多读 7 条）；改 `type: 'active'` 或 `invalidateQueries`。
2. R2（一般）审批链接页返回入口没有任何自动化断言（把链接改回 `/api-keys` 全部测试仍绿）；补一条断言。
3. R3（轻）`RepoKeysPanel.module.css` 首行注释还写着面板在密钥管理页、在网关列表上方。

范围外观察 O1～O5：只有 S3 时新页的外壳表现（高亮在「仪表盘」、顶栏「管理中心」，手机上看不到页名）与手机场景登记都归 S4；标题层级 h1 到 h3；测试加固建议；新文案键的摆放。

基线红项都在基线复现或单独复跑通过：仓库自测 10 个失败（名单与基线逐条相同，环境性）、文档检查 9 与 22（相同）、`check:bundle` 的 `lendingMount.ts`（相同）、组件套件单独跑 47 个文件 345 个用例全过。没有跑的层：`verify:browser`（无 go）、`verify:toolchain`、`demo:generate --check`、Go 门禁，缺口见 §7。

给 S4 与 S9 的三点：登记 `/repo-keys` 到 `mobileConsole.mjs` 并核对规划器规则的注释（O2）；菜单条目要带出高亮、面包屑、标签页标题与手机顶栏的页名（O1）；`check:bundle` 的既有红项归 S4 修。

## 2. 问题清单

按严重程度排；「要修」每条写清复现、证据、建议改法。

### 2.1 范围内要修

**R1（一般）页头刷新连页面没显示的缓存一起重读**

- 位置：`web/src/pages/RepoKeysPage.tsx` 的 `refresh`，`queryClient.refetchQueries({ queryKey: ['management-repo-keys'] })`，`type` 默认 `all`。
- 复现：打开 `/repo-keys`，搜索框敲 `alpha` 再清空，打开一把密钥的详情切到近 30 天后关闭，点页头刷新。
- 证据：新开的页点刷新是 4 条 GET；上面的操作后点刷新是 11 条，多出的 7 条是页面当前不显示的 `?q=a`、`?q=al`、`?q=alp`、`?q=alph`、`?q=alpha`、`…/rk-1?window=7d`、`…/rk-1?window=30d`。库级对照（`@tanstack/query-core` 5.102.8）：默认 `refetchQueries` 11 条，`type: 'active'` 与 `invalidateQueries` 各 4 条。详见 §5.3。
- 判定：PRD 5.4 写页头刷新「只重读本页已有的数据」，所以算范围内。简报点名的例子（密钥管理页带来的列表）不受影响。级别一般：只有 GET，端点与参数面板本来就会发，没有新端点与写操作，缓存保留 5 分钟，新开的页上零额外读取。
- 建议：`type: 'active'`，或改用 `invalidateQueries({ queryKey: ['management-repo-keys'] })`（与面板自己写后失效的写法一致）。补一条测试：搜索框敲 `alpha` 再清空，点刷新，断言 `?q=alpha` 的读取次数没有增加。该测试在系统临时目录的副本里验证过：现有写法下失败（`expected 2 to be 1`），两种建议改法下通过，且原有两条页测试仍绿。

**R2（一般）审批链接页返回入口没有任何自动化断言**

- 位置：`web/src/pages/repo-key/RepoKeyApplyPage.tsx` 页尾的 `Link`（AC-14 的改动本体）。
- 复现：在副本里把 `to="/repo-keys"` 改回 `to="/api-keys"`，跑 `repo-key-apply`、`repo-key-demo`、`repo-key-display` 与两条新页测试：5 个文件 9 个用例全部仍通过（突变 M1，§5.9）。
- 证据：在 `web/tests`、`scripts/acceptance` 与 `scripts` 下搜 `/repo-keys`，命中只有三个测试文件里的路由或页底提示目标（`repo-keys-page`、`api-keys-page`、`repo-key-display` 的挂载路由）、`check-demo.mjs` 的路由登记、`repoKeyApproval.mjs` 里读取桩的接口路径；没有任何一处断言审批链接页的返回目标。`repoKeyApply.mjs` 探针只断言「页面在侧栏之外」。简报第 8 项要求有这条测试。
- 级别：一般。行为本身我在浏览器里核对是对的（§6），所以不算严重；但这是 PRD 明写的用户可见行为，没有断言就会无声回退。
- 建议：在 `web/tests/repo-key-apply.component.test.tsx` 里渲染审批链接页（文件里已有读取桩），断言页尾链接文字为 `Repository keys` 且 `href` 为 `/repo-keys`；或在 `repoKeyApply.mjs` 探针里加同一条断言。

**R3（轻）CSS 模块首行注释过时**

- 位置：`web/src/components/keys/RepoKeysPanel.module.css` 开头的注释块，写着「repository-owned keys on the key management page」与「the gateway key list above it」。
- 证据：同一次改动已把组件文件里的「beside」「above」改掉，同目录的样式模块漏改；面板如今只在 `/repo-keys` 上。AGENTS.md §6 要求逻辑变了就更新注释。
- 级别：轻。
- 建议：改为只描述样式语言，例如「repository-owned keys panel; the same terminal-flat language as the gateway key list」，不写页面位置。

### 2.2 范围外观察

- O1（归 S4）　只有 S3 时，`/repo-keys` 在外壳里按「未登记路由」兜底：菜单高亮落在「仪表盘」，面包屑「运行 / 管理中心」，标签页标题「Oh My CPA · 管理中心」；手机宽度下外壳把页内 `h1` 收起、顶栏显示的也是「管理中心」，所以手机上看不到页名「仓库自有密钥」。简报已把 AC-11 的标签页标题与 AC-13 的高亮、面包屑划给 S4 的菜单条目，不记 S3 要修；提示：S3 若脱离 S4 单独合入 dev，用户看到的就是这个兜底表现。证据：`net/s6/head-pop-zh-desktop.json` 的 `selectedNav`、`breadcrumb`、`title`，截图 `head-pop-zh-phone.png`。
- O2（归 S4）　手机场景登记：`mobileConsole.mjs` 经抽屉里的 `data-route-path` 菜单项点击进入每个页面，没有菜单条目就进不去，所以 S3 最后一个提交撤掉了 `['/repo-keys', '.repo-keys-page']`，理由成立。后果有两点：S3 阶段没有任何浏览器场景打开 `/repo-keys`（在 `scripts/acceptance` 里搜不到导航到该地址的脚本，`check:ui --all` 全过也说明不了该页的渲染，该页由本次的并排对照覆盖）；`check-ui-plan.mjs` 新加规则与 `ui-impact.test.mjs` 的自测把 `mobile-console` 称作「打开它的场景」，在 S4 登记之前并不成立。S4 待办：登记该条目，并核对这条规则的注释。
- O3　标题层级：新页上是 `h1` 直接到 `h3`（面板的 `h2` 按简报隐藏，区块标题仍是 `h3`）；基线是 `h1`、`h2`、`h3`。PRD 没有要求，不影响 F1～F12。可选做法：页内托管时把区块标题降一级，或由页头统一给 `h2`。证据：`net/s6/head-pop-zh-desktop.json` 的 `headings`。
- O4　测试加固建议：(a) 页面级没有断言密钥管理页仍读取仓库密钥列表并标出所属仓库，把 `repoByKey` 清空（突变 M7）所有测试仍绿；(b) 页底提示的单元测试只覆盖列表为空，读取失败与加载中我在浏览器里核对过，且提示不在任何条件分支里，风险低；(c) 新页刷新测试只在新开的页上断言，分不出刷新范围（突变 M4 仍绿，见 R1）。
- O5　新文案键 `keys.repo_keys_moved`、`keys.repo_keys_moved_action` 插在 `repo_keys.title` 与 `repo_keys.subtitle` 之间，简报说放在 `keys.*` 文案附近。不影响功能与 `check-i18n`，只是整洁。

## 3. PRD 还原度：AC 逐条

本切片对应 AC-8、9、10、12、14、23（演示部分）、24、25、29，以及 AC-28 中涉及仓库自有密钥所在位置的三份文档；其余 AC 归 S4。

| AC | 结论 | 证据 |
|---|---|---|
| AC-8 密钥管理页只剩网关密钥 | 已实现 | `/api-keys` 的标题只有 `h1` 密钥管理与 `h2` API 密钥列表；八种状态（桌面、深色、手机、English、列表为空、读取失败、加载中、演示）下面板的任何部分都不在。列表里仍标出所属仓库（`example/alpha-old`）与「未绑定仓库」。截图 `head-km-zh-desktop.png`、`head-km-zh-dark.png`、`head-km-zh-phone.png` |
| AC-9 页底提示 | 已实现 | 静态一行，位置在网关密钥面板之后（原面板所在处），四种语言文案齐全；列表为空、读取失败、加载中时都在（`head-km-empty.png`、`head-km-failure.png`、`head-km-loading.png`）；点击进入 `/omc/repo-keys`，浏览器后退回 `/api-keys`；390 宽无横向滚动 |
| AC-10 逐项一致 | 已实现 | 面板 diff 只有「页内托管时不画自己的标题」（§4.1）；同一份数据九个静态场景与 25 步交互两端逐项相同（§4.2、§4.3），F1～F12 无多无少 |
| AC-12 现有地址不变 | 已实现 | 路由表只新增 `repo-keys`；`/api-keys`、`/repo-key-apply/:token` 直接输入、刷新、后退、前进都落在原页（§6.4）；服务端对 `/*` 是通用兜底；`model-groups`、`oauth-management` 未被改动，各自的 `check:ui` 场景通过 |
| AC-14 审批链接页返回入口 | 已实现；测试缺口见 R2 | 文案仍是「仓库自有密钥」（English 为 Repository keys），目标由 `/omc/api-keys` 变为 `/omc/repo-keys`，点击后落在新页；链接格式、有效期、批准与驳回行为不变，两端读请求相同。截图 `head-apply-zh.png`、`head-apply-zh-landing.png` |
| AC-23 演示（仓库自有密钥页） | 已实现 | 演示标志下新页可打开、显示数据，所有写入口禁用，禁用状态与悬停提示与基线逐个相同（`head-demo-zh.png`、`head-demo-empty-zh.png`）；`pnpm verify:demo` 对演示站点渲染 23 条路由全部通过，含 `/repo-keys`；`pnpm check:demo` 通过（155 条响应，覆盖 20 条控制台路由，基线 19 条）。构建出的 Go 二进制的演示没有跑（本机无 go；Go 无改动，演示策略里仓库密钥写接口的拒绝规则未变） |
| AC-24 没有新增请求与写操作 | 已实现；刷新范围见 R1 | `/repo-keys` 挂载的仓库密钥读取与基线面板的四条完全相同（集合与顺序）；`/api-keys` 比基线少三条（待审批、待交付、审计推送状态），仍读仓库密钥列表，头里没有基线没有的请求；19 个打开页面的场景里，基线 257 条请求、头 203 条，非 GET 均为 0，无外部主机；25 步交互两端写请求逐条相同 |
| AC-25 其余功能不变 | 已实现 | `pnpm check:ui --all` 81 个场景全过；组件测试 47 个文件 345 个用例全过（单独跑）；纯逻辑 90 个套件、类型检查、i18n、CSS 模块、令牌、动效、反馈、antd 规则全过；既有测试只改了 `repo-key-display` 的挂载路径 2 行，没有删除或放宽断言。`verify:browser`（构建出的二进制加假 CPA）没有跑，需要 go |
| AC-28 文档（本切片部分） | 已实现 | `docs/agent-capabilities.md`、`docs/repo-key-client.md`、`docs/testing.md` 三处改动准确；全仓库没有其他文档把面板写在密钥管理页上（按「面板」「页」两组词搜）。设计说明、术语表、产品说明、分组名归 S4 |
| AC-29 文档检查 | 已实现 | 三份改动文档 `check-docs --file` 均为 0 缺失路径、0 退役引用、0 行号引用；全量 9 条缺失路径、22 条行号引用，与基线一致（全在 2026-11-12 的历史交付文档里）；本报告同样 0 |

不属于 S3、没有计入：AC-11 的标签页标题与 AC-13 的高亮、面包屑（页头标题「仓库自有密钥」已在，其余由 S4 的菜单条目带出，见 O1）。

## 4. AC-10：面板 diff 与 F1～F12 逐项对照

### 4.1 面板源码 diff（核心证据）

```
git diff --numstat 11-24-t9-dev-base...Newoahil/11-24-t9-s3-codex -- web/src/components/keys/RepoKeysPanel.tsx
9    4    web/src/components/keys/RepoKeysPanel.tsx      （零上下文共 5 处）
```

逐处解释，没有别的改动：

| 改动 | 性质 |
|---|---|
| 新增导出接口 `RepoKeysPanelProps`，唯一属性 `isPageHosted?: boolean` | 可选属性，不传即拆页前行为 |
| 组件注释两行改写，去掉「beside the gateway's own key list」「The gateway list above」 | 纯注释；面板不再与网关列表同页 |
| 签名 `RepoKeysPanel()` 改为 `RepoKeysPanel({ isPageHosted = false })` | 默认值保持原行为 |
| 标题 `h2` 前加 `!isPageHosted &&` | 页内托管时不画自己的标题；整份 diff 里唯一的行为改动 |

说明句 `p.subtitle`（F1）原样保留。基线与头逐项计数相同：`isDemo` 12 处、`queryKey:` 8 处、`refetchInterval` 2 处、`useMutation(` 8 处、`t('repo_keys.` 引用 171 处。因此 F1～F12 对应的数据读取、写操作、提示、空态、演示禁用的代码一行没少、没改。

### 4.2 同一份数据的并排对照

方法：同一份桩数据（5 把密钥含 3 种停用类别、4 条待审批含重新启用与代提交、2 条待交付、审计推送已开启待推 3 隔离 1）喂给基线的 `/api-keys` 下半块与头的 `/repo-keys`；两端用同一脚本、同一语言与视口，比较面板可见文字、控件（含禁用状态与悬停提示）、请求、结果状态。

- 九个静态场景（简体中文桌面、深色、390 宽手机、English、列表为空、加载中、读取失败、演示、演示且为空）：面板可见文字只差一行，就是基线独有的 `h2` 标题「仓库自有密钥」（English 为「Repository keys」）；控件、表头列宽（桌面 1140 宽、手机 366 宽）、手机行数（11 对 11）、空态个数（3 对 3）、仓库密钥请求及其顺序全部相同。
- 25 步交互（搜索与筛选 7 步；详情与三个统计窗 4 步；泄露类重新启用的禁用检查 1 步；审阅与驳回 3 步，含原因为空时确认按钮的禁用检查；批准、授权链接、取走密文、停用、申请重新启用各 1 步；查看明文 2 步，含原因为空的禁用检查；代 agent 提交 3 步）：每步的写请求（方法、路径、体）、仓库密钥读请求、新出现的提示、弹窗全文、一次性链接与明文与密文的取值、各区块标题计数，两端逐步相同，没有差异。

### 4.3 F1～F12 逐项

| 项 | 对照结果（基线与头相同） |
|---|---|
| F1 说明句 | 两种语言文字一致；位于页头标题之下、搜索框左侧 |
| F2 搜索与筛选 | `?q=alpha`、`?q=agent-a`、`?q=zzzz`、`?status=active`、`?status=stopped` 的请求与行数相同；区块标题计数 1、0 |
| F3 代 agent 提交 | 新仓库显示「这个仓库还没有密钥，批准后会签发一把新的」，已有仓库显示复用与掩码；两种公钥来源；提交体 `repo`、`purpose`、`agent`、`public_key`；回执带一次性链接 |
| F4 待审批 | 审阅详情含「公钥指纹和上次领取时不同」提示；批准体 `{"reason":""}`；驳回在原因为空时确认按钮禁用，填 `nav-check` 后体 `{"reason":"nav-check"}`；重新启用申请在同一列表，方式标签为「重新启用」 |
| F5 待交付 | 取走密文发 `POST …/ciphertext`，弹窗显示密文，取走后该条离开队列 |
| F6 密钥列表 | 表头、掩码、状态、最近领取人、近 7 天费用 `$12.3400`；详情、查看明文、停用、重新启用；泄露类的重新启用禁用，悬停提示「泄露的密钥不能重新启用，请为仓库重新申请一把」 |
| F7 密钥详情 | 三个统计窗读 `?window=7d`、`24h`、`30d`；泄露类详情有「停用后仍有 7 次调用被拒绝」一句 |
| F8 停用、重新启用、查看明文 | 停用体 `{"category":"misuse","reason":"nav-check"}`；重新启用只要原因；查看明文原因为空时确认禁用，查看后明文只在弹窗里出现 |
| F9 一次性审批链接 | `POST …/approval-link`，弹窗显示新链接 |
| F10 审计推送 | 已开启：待推送 3、隔离 1、最近成功时间；未配置：0 与「—」 |
| F11 加载、失败、空、手机 | 加载中、读取失败、三块空态文案、手机宽度按行呈现，四种状态逐项一致 |
| F12 演示只读 | 演示标志下所有写入口禁用，禁用状态与悬停提示与基线逐个相同 |

## 5. 代码审查细目

### 5.1 路由、懒加载、预加载　通过

- `web/src/App.tsx`：`api-keys` 旁加平铺路由 `repo-keys`，页组件从 `routePages` 导入；`api-keys` 与 `repo-key-apply/:token` 两条路由未动（diff 里没有）。
- `web/src/routePages.ts`：照 `loadApiKeysPage` 的写法加 `loadRepoKeysPage` 与 `React.lazy` 页；`web/src/routePreload.ts` 的 `ROUTE_LOADERS` 加 `/repo-keys`。
- 直接输入、刷新、前进后退：服务端兜底是通用的，`internal/api/handler.go` 对 `/*` 的 GET 与 HEAD 一律回 `index.html`，没有页面清单，所以 Go 不用改。开发服务器上，每个场景都是直接输入 `/omc/repo-keys` 打开；导航历史见 §6.4。没有在构建出的二进制上跑（本机没有 go，Go 无改动）。

### 5.2 页内托管写法　通过

`isPageHosted` 命名符合布尔前缀规矩，默认 `false` 保持原行为；说明句保留，只去标题；页组件传 `isPageHosted`。没有用 CSS 隐藏标题（那样读屏仍会读到重复标题），做法合适。一处观察见 O3（标题层级）。

### 5.3 页头刷新　要修 R1

`web/src/pages/RepoKeysPage.tsx` 的刷新写成 `queryClient.refetchQueries({ queryKey: ['management-repo-keys'] })`。`refetchQueries` 的 `type` 默认是 `all`，会连缓存里不活跃的同族查询一起重读。

- 简报点名的例子「从密钥管理页带来的列表」不受影响：它的键 `['management-repo-keys','keys','','all']` 与面板默认列表是同一个缓存条目，在新页上是活跃的，只读一次。从 `/api-keys` 经页底提示进入新页再点刷新，实测为四条读取（列表、待审批、待交付、审计推送）。
- 受影响的是不活跃的同族条目：每敲一个搜索字符、每打开过的详情与统计窗、每次审阅、代提交表单里每次仓库查询，都会在缓存里留一个条目（保留 5 分钟），之后点页头刷新会被一并重读。实测：搜索框敲 `alpha`、清空、打开一把密钥的详情并切到近 30 天、关闭，再点刷新，共 11 条 GET，其中 7 条是页面当前并未显示的（`?q=a`、`?q=al`、`?q=alp`、`?q=alph`、`?q=alpha`、`…/rk-1?window=7d`、`…/rk-1?window=30d`）；新开的页点刷新则是 4 条，与挂载时相同。
- 库级证据（仓库安装的 `@tanstack/query-core` 5.102.8，脚本放在系统临时目录）：同样的缓存状态下，默认 `refetchQueries` 重读 11 条，`refetchQueries({ type: 'active' })` 与面板自己写后用的 `invalidateQueries` 都只重读 4 条，无关的 `management-client-keys` 一条都不碰。
- 判定：PRD 5.4 要求页头刷新「只重读本页已有的数据」，这里多读了页面没显示的数据，所以算范围内；但都是 GET、端点与参数面板本来就会发、没有新端点和写操作、保留期 5 分钟，所以只算一般。`web/src` 里 `refetchQueries` 仅此一处，面板自己整族刷新用的是 `invalidateQueries`。
- 建议：改为 `queryClient.refetchQueries({ queryKey: ['management-repo-keys'], type: 'active' })`，或直接 `invalidateQueries({ queryKey: ['management-repo-keys'] })`，与面板自己的 `invalidate` 一致；测试里先造一个不活跃的同族条目（例如先搜索再清空），断言刷新后它没有被重读。现有测试只在新开的页上断言列表读取加 1，分不出两种写法。

### 5.4 密钥管理页　通过

只删了 `RepoKeysPanel` 的引用与导入，`repoKeysQuery` 与 `repoByKey` 保留；原位置换成静态的 `p.repo-keys-moved`（文字加 `Link to="/repo-keys"`），不在任何条件分支里，所以列表为空、读取失败、加载中都在（三种状态与演示、手机、深色各截图）。样式用设计令牌（`--muted`、`--text-sm`、`--accent`）。四种语言的新文案齐全：`index.tsx` 的 `[zh, en]`、`zh-Hant.ts`、`ms.ts`，与 PRD 5.3 的名称一致（倉庫自有金鑰、Kunci repositori、Keupayaan）；`check-i18n` 通过。

### 5.5 一次性审批链接页　通过，测试缺口见 R2

`RepoKeyApplyPage.tsx` 只改了 `Link` 的目标 `/api-keys` 为 `/repo-keys`，文案键仍是 `repo_keys.title`，链接格式、有效期、批准与驳回未动。浏览器里读页尾链接文字「仓库自有密钥」「Repository keys」，目标 `/omc/repo-keys`，点击后落在新页（基线为 `/omc/api-keys`）；页面在侧栏之外；两端读请求相同。

### 5.6 回归　通过

查询键、轮询、各对话框、演示禁用都没动（§4.1 的计数相同）。两页共用缓存键 `['management-repo-keys','keys','','all']`，端点、参数、数据形状相同，没有串味风险；基线里两者本来就同页并存。面板写操作后的 `invalidate` 仍是前缀失效，只重读活跃条目。

### 5.7 一致性　通过

页根容器 `terminal-page terminal-page-stack repo-keys-page` 与 `keys-page`、`providers-page` 的写法相同（标记类只给测试与探针选择，没有样式）；`PageHeader` 的 `actions` 与 `mobileActions`（`ActionMenu` 包 `RefreshButton`）与 `ApiKeysPage` 一致；CSS 类名短横线、代码注释英文且讲原因。唯一的写法差异是 R1 的 `refetchQueries`。

### 5.8 越界　通过

- Go：`git diff --stat 11-24-t9-dev-base...Newoahil/11-24-t9-s3-codex -- '*.go'` 输出为空。
- 21 个改动文件里没有 `navigation.tsx`、`sidebarNav.ts`、`SidebarNavSettings.tsx`、`ShellLoading.tsx`、`AppLayout.tsx`、`DESIGN.md`、`docs/design.md`、`CONTEXT.md`、`PRODUCT.md`、额度拆借相关页面与挂载模块、`docs/delivery/` 下任何文件（按文件名正则在改动清单里搜，结果为空）。
- 没有新增跳过开关或更长等待；已有断言没有被删、没有放宽，唯一改动的既有测试 `repo-key-display.component.test.tsx` 只把挂载路径 `/api-keys` 改为 `/repo-keys`（2 行）。

### 5.9 测试能否真失败　部分有缺口，见 R2、O4

方法：在系统临时目录里 `git archive` 导出 S3 头，对副本做突变，每个突变改一个文件、跑相关测试、还原；被审工作区没有动，副本还原后无改动。结果在 `net/s6/mutation-results.json`。

| 突变 | 结果 | 含义 |
|---|---|---|
| M1 审批链接页返回入口改回 `/api-keys` | 5 个文件 9 个用例全绿 | AC-14 没有断言（R2） |
| M2 面板总是画自己的 `h2` | `repo-keys-page` 的「draws the panel under the page header instead of repeating its title」失败 | 真能失败 |
| M3 页头刷新什么都不做 | `repo-keys-page` 的「refreshes only the panel's own reads」失败 | 真能失败 |
| M4 刷新改成不带键过滤的 `refetchQueries()` | 全绿 | 分不出刷新范围（R1、O4） |
| M5 密钥管理页重新画面板 | `api-keys-page` 失败 | 真能失败 |
| M6 去掉页底提示 | `api-keys-page` 失败 | 真能失败 |
| M7 `repoByKey` 清空（所属仓库小标消失） | 全绿 | 页面级没有断言（O4） |
| M8 提示链接改指 `/api-keys` | `api-keys-page` 失败 | 真能失败 |

另外，改写 `repo-key-display` 的两行挂载路径没有改变任何断言；该文件的三个用例在头上全过。

### 5.10 文档　通过

`docs/agent-capabilities.md` 把「控制台一侧」改指仓库自有密钥页 `/repo-keys`；`docs/repo-key-client.md` 在 `key purge` 一条里补上去哪个页面停用或重签；`docs/testing.md` 新增的一段所述测试归属与实际相符（页壳的标题、说明句、页头刷新，以及页底提示与面板在 `/api-keys` 上缺席）。用「面板」「页」「密钥管理」等词在全仓库文档里搜，没有残留把面板写在密钥管理页上的描述。三份文档 `check-docs --file` 均为 0 缺失路径、0 退役引用、0 行号引用。遗留一处属于注释而非文档：R3。

## 6. 界面证据

### 6.1 路径与环境

- 没有用内置浏览器。用仓库自带的探针机制（`scripts/acceptance/probe.mjs` 的 `startVite`、`installRoutes`）起 Vite 开发服务器并给接口打桩，`playwright-core` 1.62.1 驱动 Edge 无头（`C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe`）。脚本在系统临时目录，不进仓库：`C:/Users/lhw/AppData/Local/Temp/omc-t9-s6/scripts/`（桩数据 `rk-fixture.mjs`、场景 `rk-run.mjs`、对比 `rk-diff.mjs`、刷新范围 `refetch-scope.mjs`、突变 `mutate.mjs`）。
- 基线：`git archive 11-24-t9-dev-base` 导出到系统临时目录再 `pnpm install`，没有给共享的 `.git` 加工作树。
- 端口：`pnpm check:ui` 固定 5181，一次连续跑完 81 个场景（约 9.5 分钟，16:10 到 16:20）后释放；头的开发服务器 5185；基线的开发服务器 5186，演示验收也用 5186（先后使用）。18461～18469 没有用（没有起假 CPA 与二进制，本机无 go）。收尾检查：这些端口都已释放，没有遗留进程。
- 数据：全部假数据，仓库名 `example/...`，密钥与令牌是桩值。不连生产、不用真实账号、不碰任何写接口的真实实现。

### 6.2 截图（`shots/s6/`，不进 git；`head-` 是 S3 头，`base-` 是基线）

| 状态 | 头 | 基线对照 |
|---|---|---|
| 新页，中文桌面，有数据 | `head-pop-zh-desktop.png` | `base-pop-zh-desktop.png`（`/api-keys` 全页，面板在下半块） |
| 新页，深色 | `head-pop-zh-dark.png` | `base-pop-zh-dark.png` |
| 新页，390 宽手机 | `head-pop-zh-phone.png` | `base-pop-zh-phone.png` |
| 新页，English | `head-pop-en-desktop.png` | `base-pop-en-desktop.png` |
| 列表为空 | `head-state-empty.png` | `base-state-empty.png` |
| 加载中、读取失败 | `head-state-loading.png`、`head-state-failure.png` | `base-state-loading.png`、`base-state-failure.png` |
| 演示（有数据、为空） | `head-demo-zh.png`、`head-demo-empty-zh.png` | `base-demo-zh.png`、`base-demo-empty-zh.png` |
| 密钥管理页：桌面、深色、手机、English | `head-km-zh-desktop.png`、`head-km-zh-dark.png`、`head-km-zh-phone.png`、`head-km-en-desktop.png` | 同名 `base-km-…` |
| 密钥管理页：为空、读取失败、加载中、演示 | `head-km-empty.png`、`head-km-failure.png`、`head-km-loading.png`、`head-km-demo.png` | 同名 `base-km-…` |
| 审批链接页与点击后的落点 | `head-apply-zh.png`、`head-apply-zh-landing.png`、`head-apply-en.png`、`head-apply-en-landing.png` | 同名 `base-apply-…` |

### 6.3 看到的结果

- 新页：页头标题「仓库自有密钥」加右侧「刷新」；面板从说明句开始，右侧是搜索框、状态筛选与「代 agent 提交申请」；其下是待审批、待交付、密钥、审计平台推送四块。与基线面板相比只少一行重复的标题。
- 窄屏 390 宽：三个列表按行呈现（手机行 11 对 11），页面无横向滚动。
- 深色：文字与控件清晰可读。
- 空态：三块空态文案与「未配置」「0」；`demo-readiness.mjs` 为新页登记的选择器 `.repo-keys-page .empty-state` 在头上命中 3 个元素。
- 演示：写入口全部禁用，悬停提示与基线逐个相同；「详情」仍可点。
- 密钥管理页：只剩网关密钥列表加页底一行提示，列表为空、读取失败、加载中时提示仍在；强调色的链接文字，视觉与面板里的「在审计页查看这个仓库的记录」一致。
- 审批链接页：页尾「仓库自有密钥」点击后落在新页。

### 6.4 地址、刷新、前进后退（开发服务器）

| 动作 | 落点与页内标题 |
|---|---|
| 直接输入 `/repo-keys` | `/omc/repo-keys`，仓库自有密钥 |
| 依次打开 `/dashboard`、`/repo-keys`、`/audit`，后退、后退、前进 | `/repo-keys`、`/dashboard`、`/repo-keys` |
| 在 `/repo-keys` 刷新；再前进 | `/repo-keys`，然后 `/audit` |
| 直接输入与刷新 `/api-keys` | `/omc/api-keys`，密钥管理 |
| 直接输入与刷新 `/repo-key-apply/stubtoken` | `/omc/repo-key-apply/stubtoken`，审批仓库密钥申请 |
| 从 `/api-keys` 点页底提示，后退，前进 | `/repo-keys`、`/api-keys`、`/repo-keys` |

没有重定向，没有页面错误。

### 6.5 建议重点看的三处

1. 新页顶部：标题进了页头，面板头只剩说明句与操作；桌面上待审批表的第一列「仓库」很窄（`example/alpha` 折成三行），基线完全相同（列宽逐列一致），是既有现象。
2. 密钥管理页底的提示行：淡色文字加强调色链接，390 宽下是否一眼可见。
3. 只有 S3 时新页的外壳表现（高亮在「仪表盘」、顶栏「管理中心」，手机上看不到页名），S4 挂菜单条目后应当消失（O1）。

没有留着运行的环境：简报要求收尾时关掉自己起的进程，且本机无 go，起不了真二进制环境。要现场点：看上面的截图；需要真交互时用 S9 的体验环境（`cases.md` 0.3，假 CPA 加构建出的二进制，端口 18461～18469）；或把 `rk-run.mjs` 里的 `headless: true` 改成 `false`，在可见的 Edge 里重放同一批场景。

## 7. 门禁与基线红项

本机 Windows 11，Node 24.16.0（仓库钉的是 22.23.2），没有 go 与 zip。基线用 `git archive` 导出到系统临时目录复跑。

| 命令 | 头 | 基线 | 判定 |
|---|---|---|---|
| `pnpm test:fast --base 11-24-t9-dev-base`：类型检查、纯逻辑（90 个套件）、i18n、CSS 模块、令牌、动效、反馈、antd 规则 | 全部通过 | 不适用 | 通过 |
| 同上的文档检查 | 9 条缺失路径、22 条行号引用，全在 2026-11-12 的历史交付文档 | 相同的 9 与 22 | 基线既有 |
| 同上的仓库自测 | 505 个用例，10 个失败 | 504 个用例，10 个失败，失败名单逐条相同 | 基线既有、环境性（无 go：`spawnSync go ENOENT`，无 zip，Windows 符号链接 EPERM，「fixed waits」棘轮列的是额度拆借环境脚本 5 个文件）；头多出的 1 个用例是 S3 新增的规划器自测，通过 |
| 同上的组件测试 | 与其余子项并行时 1 个用例失败：`account-automation` 里找不到「Select all」按钮 | 不适用 | 负载下的抖动，与 S3 无关：该文件单独跑 3 次全过，组件套件单独完整跑一遍 47 个文件 345 个用例全过 |
| 新增与受影响的 6 个测试文件（`repo-keys-page`、`api-keys-page`、`repo-key-display`、`repo-key-apply`、`repo-key-demo`、`repo-keys-cost`） | 18 个用例全过 | 不适用 | 通过 |
| `pnpm check-i18n`、`node scripts/check-docs.mjs` | 包含在 `test:fast`，另按文件跑过三份文档 | | 通过 |
| `pnpm check:ui --all` | 81 个场景全过，565 秒，端口 5181 | 不适用 | 通过 |
| `pnpm check:demo` | 通过：155 条响应，覆盖 20 条控制台路由 | 19 条 | 通过 |
| `pnpm build`，`pnpm verify:demo`（演示 Worker 加构建出的控制台） | 构建通过；演示 23 条路由全过，含 `/repo-keys` | 不适用 | 通过 |
| `pnpm check:bundle` | 失败 1 项：`web/src/pages/quota/lendingMount.ts` 被入口块带进来 | 相同的 1 项 | 基线既有（额度拆借一期，S4 会修），S3 不引入；新页是独立懒加载块，约 28.5 KB，入口块里只有动态导入语句 |
| `pnpm verify:secrets`（worktree 扫描，gitleaks 8.24.3） | 无泄漏 | 不适用 | 通过。脚本自己的解压步骤在 Git Bash 下因 GNU `tar` 失败，改用 System32 的 `tar.exe` 解压已核对哈希的钉定版本再用 `GITLEAKS_BIN` 跑原脚本 |

没有跑：`pnpm verify:toolchain`（Node 版本与钉定不符，只算兼容性）、`pnpm verify:browser`（需要 go 构建二进制，所以构建出的二进制加假 CPA 的浏览器验收缺口，S9 的集成环境补）、`pnpm demo:generate --check`（Windows 上必红 12 个叶子路径）、Go 的测试与 vet（Go 无改动）。因此 `pnpm verify` 没有整体按原样跑，上表是它的前端与仓库类子项逐项跑的结果。

基线红项复现证据：`net/s6/` 下 `self-tests-failing-head.txt` 与 `self-tests-failing-base.txt` 逐条相同，`check-docs-head.txt` 与 `check-docs-base.txt` 的计数相同，`bundle-head.log` 与 `bundle-base.log` 是同一条失败。

## 8. 证据索引与复现命令

下列路径都在 S6 工作区，截图与日志不进 git：

```
docs/delivery/2026-11-23-omc-capabilities-nav/acceptance/shots/s6/    42 张截图（head-、base- 各 21）
docs/delivery/2026-11-23-omc-capabilities-nav/acceptance/net/s6/      场景 JSON 与各命令日志
```

`net/s6/` 里的主要文件：

| 文件 | 内容 |
|---|---|
| `head-*.json`、`base-*.json` | 每个场景的面板文字、控件、请求、页信息；`flows` 是 25 步交互，`refresh` 与 `km-to-repo-refresh` 是刷新范围，`nav-history` 是地址与前进后退 |
| `refetch-scope.txt` | R1 的库级对照（`refetchQueries` 默认 11 条，`type: 'active'` 与 `invalidateQueries` 各 4 条） |
| `mutation-results.json` | §5.9 的八个突变 |
| `test-fast-head.log`、`components-head-alone.log`、`check-ui-all-head.log`、`verify-demo-head.log` | 头上的门禁输出 |
| `bundle-head.log`、`bundle-base.log`、`self-base.log`、`self-tests-failing-*.txt`、`check-docs-*.txt` | 基线红项与复现 |

复现（脚本在系统临时目录 `C:/Users/lhw/AppData/Local/Temp/omc-t9-s6/scripts/`，头在 S6 工作区，基线是 `git archive 11-24-t9-dev-base` 的导出）：

```
node rk-run.mjs head <输出目录>            # 开发服务器 5185，全部场景
node rk-run.mjs base <输出目录>            # 开发服务器 5186
node rk-diff.mjs <输出目录>                # 头与基线逐场景对比，只列差异
node refetch-scope.mjs                     # R1 的库级对照
node mutate.mjs                            # §5.9 的突变，作用在 git archive 导出的副本上
pnpm test:fast --base 11-24-t9-dev-base
pnpm check:ui --all                        # 固定占用 5181
pnpm build && OMCPA_DEMO_PORT=5186 pnpm verify:demo
```

报告副本：`E:/works/Homan-context/inbox/agents/验收/2026-11-24-omc-capabilities-nav/review-s3-r1.md`（助手仓库，不进业务仓库）。
