---
type: review
slug: 2026-11-23-omc-capabilities-nav-review-s4-r1
status: final
verdict: 要修
subject: Newoahil/11-24-t9-s5-codex @ 6c83fbfc
baseline: 11-24-t9-dev-base (dev 90a8d8ff)
standard: prd.md（定稿，唯一基准）
updated: 2026-11-24
summary: "S4 能力分组与搬家的两路审查（代码、PRD 还原度）与界面真测：AC 逐条已实现，搬家修红有效，S6 的三条已修；要修 3 条（图标测试比别名、额度拆借交付文档只改一半、一处旧条数注释），没有严重项"
---

# 审查报告：S4 能力分组与搬家（r1）

审查对象：分支 `Newoahil/11-24-t9-s5-codex`（名字里的 s5 是派发记录编号，它就是开发切片 S4 的分支），头提交 `6c83fbfc`；S4 自己的改动是 `git diff Newoahil/11-24-t9-s3-codex...Newoahil/11-24-t9-s5-codex`。
依据：同需求目录的 `prd.md`（定稿，唯一基准）；`cases.md` 只作核对覆盖的参照。
审查方式：读代码与 diff，自己跑前端门禁，做证伪实验（改完立刻还原），在界面里真看渲染结果；不改业务代码、不推送。

## 1. 结论

**范围内要修，共 3 条，没有严重项**：N1 一般，N2 轻，N3 轻。S4 的核心承诺都兑现了：

- 分组与成员：六组顺序为运行、网关、观测、能力、插件、控制，「能力」三项依次是模型组、仓库自有密钥、额度拆借，模型组离开运行组；四种语言的分组名与条目名与 PRD 5.3 表逐字一致，没有动现有译名；20 个固定条目是 20 个不同的图形；「能力」标题的计算样式与「观测」标题逐项相同。
- 五种呈现与演示模式都在界面里真测过：展开栏、收起栏（20 个图标，顺序等于拉平后的展开栏）、手机抽屉（与展开栏同一份内容）、设置面板（按新分组列出，「n hidden」计数含新条目）、加载占位（6 组 4/3/5/3/2/3，演示 3/3/5/3/2/3，与加载后逐组相同，高度差 21px 小于一个条目）；浅色深色、四种语言都清晰。
- 高亮、面包屑、标签页标题：八个页面都按 PRD 5.5 表落点；额度拆借页的高亮在它自己，OAuth 管理不再被借用；带尾斜杠、带查询参数同；被隐藏的条目用地址打开仍写出所在分组；没有条目拥有的路由回落到仪表盘与运行组。
- 隐藏设置：已存设置继续生效，新页默认可见，锁定项仍锁，整组隐藏时展开栏、收起栏、手机抽屉都不留标题。
- 搬家修红有效：基线 `pnpm check:bundle` 红（`lendingMount.ts is eager`），S4 头通过；新模块零个 import，搬家本身没有行为变化。
- S6 的 R1～R3 三条都修了，R1、R2 的新测试在旧写法下能红（实验 E3、E4）；`resolveNavLocation` 与改前逐点等价，未匹配时的回落有真断言（实验 E5）。
- 探针里新加的条件等待补的是探针本来就有的读取点缺口：额度读取变慢时，基线与 S4 头上入口都同样晚于第一行凭证，量级相同，没有入口变慢的迹象。
- Go 改动为零；两次文档合并只带该需求目录下的文件；`pnpm check:ui --all` 81 个场景全过；打开各页发出的请求与基线逐条相同，没有写请求。

要修：

1. N1（一般）AC-6 的测试比的是图标别名，不是图形：把仓库自有密钥换成与「日志」同一图形的别名 `FileTextOutlined`，测试仍全过。
2. N2（轻）额度拆借需求的三份交付文档只改了一半：`git grep lendingMount` 还剩 4 行，且点名了已删除的常量。
3. N3（轻）`SidebarNavSettings.module.css` 头注释还写「sixteen entries」，同组件 tsx 里已改成 twenty。

范围外观察 X1～X5 见 2.2。基线红项都在基线复现，没有一条由 S4 引入，见 §7。

## 2. 问题清单

### 2.1 范围内要修

**N1（一般）AC-6 的测试比的是图标别名，不是图形**

- 位置：`web/tests/navigation.component.test.tsx` 的「gives every fixed entry its own icon」。
- 复现：在 `navigation.tsx` 里把 `/repo-keys` 的图标换成 `<FileTextOutlined />`（并把它加进导入），跑 `navigation.component.test.tsx`。
- 证据：18 个用例全过（实验 E1b）。`web/src/components/icons/index.tsx` 里 `FileTextOutlined` 与「日志」条目用的 `ProfileOutlined` 都是 `createIcon(Lucide.FileText, …)`，画出同一个图形；测试比较的是 `icon.type`，而 `createIcon` 每次返回新的组件，别名不同就算不同。图标集里这样的孪生别名现有三组：`CaretDownOutlined` 与 `DownOutlined`、`FileTextOutlined` 与 `ProfileOutlined`、`ThunderboltFilled` 与 `ThunderboltOutlined`，其中 `ProfileOutlined` 就是导航「日志」的图标。对照：同一个别名重复使用（E1a）会红，说明测试只挡得住这一种重复。
- 级别：一般。现状没有缺陷（20 个条目 20 个不同图形，界面里渲染标记也互不相同），风险在日后新增条目时挡不住，没有用户可见影响。简报要求「要有测试盯」AC-6，PRD 写的是「不使用同一个图形」，盯到别名一层不够。
- 建议：让测试比渲染结果，不比别名。例如对每个条目的 `icon` 做 `renderToStaticMarkup` 取 SVG 标记，断言 20 个两两不同（本次界面核对就是在收起栏里取 `svg.innerHTML` 比较）；改完用 E1b 的改法复验，应当变红。

**N2（轻）额度拆借需求的交付文档只改了一半**

- 位置：`docs/delivery/2026-11-23-omc-quota-lending/cases.md`（C-43）与 `impl-plan.md`。
- 复现：`git grep -n lendingMount -- . ':!docs/delivery/**/acceptance/**'`。
- 证据：还剩 4 行旧文件名 `lendingMount.ts`：`cases.md` C-43 的「预期」与「证据」两行（同一用例的「前置」行已改成 `quotaLendingMount.ts`）；`impl-plan.md` 的页面行「路径与挂载由 `lendingMount.ts` 决定」与决定记录里「挂载点集中在 `lendingMount.ts`」。另外 `impl-plan.md` 的 `quotaLendingMount.ts` 那一行仍列着已删除的 `QUOTA_LENDING_NAV_KEY` 与「路径到菜单键的查表」，C-43 的预期也还点名 `QUOTA_LENDING_NAV_KEY`。同一份文档里一个模块有两个文件名，还点名了已不存在的导出；简报第 7 条要求这条搜索没有旧路径残留。
- 级别：轻，只影响另一需求的交付文档。
- 建议：补全。把这四处文件名改成 `quotaLendingMount.ts`，把 `QUOTA_LENDING_NAV_KEY` 与「查表」两处描述改成现状。不建议整体撤回：带目录的旧路径会让 `check-docs` 重新报缺失。

**N3（轻）设置面板样式模块的头注释还写十六个条目**

- 位置：`web/src/components/settings/SidebarNavSettings.module.css` 开头注释的「The rail is sixteen entries before any plugin registers a page」。
- 证据：同一组件的 `SidebarNavSettings.tsx` 头注释已被 S4 改成 `twenty`（4+3+5+3+2+3 = 20，不含插件自带页），CSS 模块里同一句漏改。
- 级别：轻。
- 建议：改成 `twenty`，或不写具体数字（PRD 第 10 节要求凡写着条数的统计同步）。

### 2.2 范围外观察

- X1　收起栏里「仪表盘」（`LayoutDashboard`）与「模型组」（`LayoutGrid`）都是四方格，外观接近，见 `04-rail-collapsed.png`。这是基线就有的配对，PRD 要求模型组沿用原图标，两个图形并不相同，不要求改；日后若收起栏被反馈认错，可从这里换。
- X2　手机扫描 `mobileConsole.mjs` 的 `ROUTES` 登记了 `/repo-keys`，没有登记 `/model-groups` 与 `/quota-lending`（简报对后者写明「找到稳定选择器才加」）。这两项在手机抽屉里的点击与落页本次在界面里验过（§6），自动化上由 `model-groups`、`quota-lending` 等场景分别覆盖。
- X3　AC-13 里「`AppLayout` 读完整模型而不是过滤后的模型」这个调用点，组件层没有断言，只有浏览器探针 `sidebar-customization` 钉住（E6，探针变红）。有主，不要求补。
- X4　写法小项：`web/tests/repo-key-apply.component.test.tsx` 与 `web/tests/shell-loading.component.test.tsx` 文件末尾缺换行；`ShellLoading.tsx` 里留着一段不挂在任何声明上的注释，开头仍写「One entry per group the rail carries」，描述的是已搬走的常量。
- X5　简报里的 `pnpm check-demo` 不是脚本名，实际是 `pnpm check:demo`（AGENTS.md 也是这个名字），本次按后者跑。

## 3. PRD 还原度：AC 逐条

本切片对应 AC-1～7、AC-12 里模型组各页地址、AC-13、AC-15～19、AC-20～23 的呈现部分、AC-25、AC-27、AC-28、AC-29；AC-8～11、14、24 归 S3，AC-26 不适用。「界面」指 §6 里 Edge 无头对开发服务器的真测，截图在 `acceptance/shots/s7/`（不进 git）。

| AC | 结论 | 证据 |
|---|---|---|
| AC-1 分组与顺序 | 已实现 | 界面：展开栏六组依次为运行、网关、观测、能力、插件、控制（`01-rail-expanded-en-light.png`）；`navigation.component.test.tsx`「orders the six groups…」；`buildNavGroups` 把插件组插在 `control` 之前，`capabilities` 在 `observe` 与 `control` 之间 |
| AC-2 四种语言的分组名 | 已实现 | 界面逐语言切换：能力 / Capabilities / 能力 / Keupayaan，无内部键名与回退文字（`03-rail-lang-*.png`）；三个文案文件各只新增一行；`pnpm check-i18n` 过 |
| AC-3 标题样式 | 已实现 | 界面取计算样式，「能力」与「观测」标题逐项相同：字号 10px、字重 400、颜色 `rgb(152, 152, 159)`、大写、字距 1.4px、左右内边距 12px、上内边距 14px |
| AC-4 成员与顺序 | 已实现 | 界面与测试：模型组、仓库自有密钥、额度拆借三项，无其他条目；条目键 `/model-groups`、`/repo-keys`、`/quota-lending`，文案键复用 `nav.model_groups`、`repo_keys.title`（没有新增 `nav.repo_keys`）、`nav.quota_lending` |
| AC-5 名称与点击 | 已实现 | 四语言名称与 PRD 5.3 表逐字一致（文案文件与界面均核对）；手机抽屉点三项都关闭抽屉并进入对应页，桌面点菜单进入对应页并随 Back/Forward 改变高亮 |
| AC-6 图标可区分 | 现状成立，测试钉得不够 | 20 个固定条目（含插件管理与商店）底层是 20 个互不相同的 Lucide 图形；收起栏里 20 个图标的渲染标记互不相同；仓库自有密钥是 `GitBranch`，密钥管理是 `Key`，额度拆借是 `ArrowLeftRight`。测试只比别名组件，见 N1 与实验 E1a、E1b |
| AC-7 模型组离开运行 | 已实现 | 界面：运行组四项（仪表盘、智能体、操练场、模型广场），演示模式三项；测试里 operate 组的精确列表已改 |
| AC-12 模型组各页地址 | 已实现 | `App.tsx` 的路由表只改了额度拆借常量的导入路径；`/model-groups`、`/model-groups/:id`、`/model-groups/:id/add` 直接打开、刷新、Back、Forward 都落在原页面，不重定向（界面 `history` 与 `pages`） |
| AC-13 高亮与面包屑 | 已实现 | 界面：八个页面的高亮、面包屑、标签页标题与 5.5 表一致（见 6.3 表）；带查询参数同；被隐藏的模型组用地址打开，菜单里没有它、面包屑仍是「能力 › 模型组」、标签页标题仍是它的名字；未匹配的路由回落到仪表盘与运行组。测试与实验 E5、E6 |
| AC-15 已存设置继续生效 | 已实现 | 界面：已存隐藏集 `[/model-groups, /api-keys]` 刷新后两者仍隐藏；`sidebar-nav.component.test.tsx`「keeps a moved entry hidden…」 |
| AC-16 新页面默认可见 | 已实现 | 界面：只隐藏密钥管理时，仓库自有密钥与额度拆借照常显示；测试「keeps repository keys visible when key management is hidden」 |
| AC-17 锁定项仍锁定 | 已实现 | 界面：设置面板里 OAuth 管理与 OMC 设置的开关禁用并写明「always shows」；手写的隐藏集含这两项，刷新后侧栏仍显示 |
| AC-18 整组隐藏不显示 | 已实现 | 界面：设置里关掉「能力」三项后，展开栏没有「能力」标题，收起栏 20 个图标变 17 个，手机抽屉只剩 5 个有条目的分组，没有空标题（`09`、`10`、`11` 号截图）；`retainVisibleGroups` 测试 |
| AC-19 设置面板按新分组列 | 已实现 | 界面：面板里六个分组标题依次为运行、网关、观测、能力、插件、控制，「能力」下三个开关；关一项显示「1 hidden」，关三项显示「3 hidden」（`07`、`08` 号截图） |
| AC-20 收起栏 | 已实现 | 界面：20 个图标，顺序等于展开栏拉平后的顺序，没有分组标题，每个图标带 `title`（条目名）；2 倍分辨率截图 `04-rail-collapsed.png` |
| AC-21 手机抽屉 | 已实现 | 界面 390 宽：抽屉六个分组与条目的路由列表与展开栏相同；点条目后抽屉关闭、进入对应页 |
| AC-22 加载占位 | 已实现 | 界面：用 Promise 拦住首屏的偏好读取，在占位出现时量，释放后再量；占位 6 组 4、3、5、3、2、3，加载后同；演示模式 3、3、5、3、2、3，加载后同；占位到真实侧栏的高度差 21px，小于一个条目（36px）。测试钉法与实验 E2，包体积见 4.2 |
| AC-23 演示模式（呈现部分） | 已实现 | 界面：演示模式侧栏有「能力」三项，没有操练场，运行组三项；`/repo-keys` 可打开，面包屑「能力 › 仓库自有密钥」。写操作被拒的部分归 S3，未重做 |
| AC-25 其余功能不变 | 已实现 | `pnpm check:ui --all` 81 个场景全过（含 `mobile-console`、`sidebar-customization`、`model-groups`、`oauth-management`、`quota-lending`、`omc-settings`、`route-render-error`）；受影响与新增的 9 份组件测试 44 个用例全过；`/dashboard`、`/model-groups`、`/oauth-management`、`/quota-lending` 打开时的请求清单与基线逐条相同，没有写请求 |
| AC-27 额度拆借挂入 | 已实现 | 界面：「能力」第三项，高亮在它自己（OAuth 管理不高亮），面包屑「Capabilities / Quota lending」（中文「能力 / 额度拆借」），标签页标题 `Oh My CPA · Quota lending`，页内标题仍是「Quota lending advice」；带尾斜杠的 `/quota-lending/` 同；四种语言名称与 PRD 一致 |
| AC-28 文档同步 | 已实现，两处小残留 | 设计说明（`DESIGN.md`、`docs/design.md`）改为六组；`CONTEXT.md` 更新侧栏可见性条目并新增「Navigation Group」（写明不是 Capability）；`PRODUCT.md`、`docs/cpamc-parity.md`、`docs/architecture.md` 同步；全仓搜四组写法、页数、面板位置描述，只剩 N2、N3 两处 |
| AC-29 文档检查 | 已实现 | S4 改动过的 12 份文档逐份 `check-docs --file`：0 缺失路径、0 退役引用、0 行号引用；全量检查的 31 条红全部来自 `docs/delivery/2026-11-12-omc-three-credential-quota/`，与 S4 无关 |

AC-24 的整体口径（没有新增请求与写操作）：见 6.4。

## 4. 代码审查细目

### 4.1 范围与越界　通过，一处例外见 N2

- Go：`git diff --stat Newoahil/11-24-t9-s3-codex...Newoahil/11-24-t9-s5-codex -- '*.go'` 输出为空，Go 文件改动为零。
- 两次文档合并只带来该需求目录下的文件（`git diff --name-status <合并>^1 <合并>` 逐个核对）：`8e6be013`（S2）新增 `prd.md`、`review-plain.html`；`b10cf6ce`（S5）新增 `cases.md`。
- S4 自己的改动（排除该目录）：40 个文件，532 行新增、108 行删除；`git log --first-parent` 共 14 个提交，含上面两次合并。
- 越界核对：`App.tsx`、`routePreload.ts` 各只改一行导入路径；`pages/quota` 下三个文件只改导入与一句注释；`RepoKeysPage.tsx` 只有 S6 R1 的刷新写法，`RepoKeysPanel.module.css` 只有 S6 R3 的首行注释；`/repo-keys` 页其余实现、其余页面功能均未动。
- 例外：额度拆借需求的三份交付文档被改了路径。简报第 7 条授权同步旧路径，但改得不完整，见 N2。

### 4.2 搬家与修红（check:bundle）　通过

自己跑的两端，命令都是 `pnpm build && pnpm check:bundle`（Node 24，仓库钉 22，只有引擎告警）：

| 端 | 构建 | `check:bundle` | 关键输出 |
|---|---|---|---|
| 基线 `11-24-t9-dev-base`（`90a8d8ff`，系统临时目录里的独立工作区，`pnpm install --frozen-lockfile`） | 成功 | 退出码 1 | 只红一条：`FAIL: web/src/pages/quota/lendingMount.ts is eager through assets/index-CfouW2_4.js` |
| S4 头（本工作区，构建的修订为 `c5d5092a` = S4 头加报告骨架，源码相同） | 成功 | 退出码 0 | `Loading boundaries and anomaly ceilings: PASS.` |

- 入口包体积：entry 522.36 → 522.95 KiB（gzip 176.03 → 176.19），initialJS 2016.11 → 2016.71 KiB（gzip 652.15 → 652.31）。只多出 0.6 KiB：占位侧栏的组大小在叶子模块里，没有把导航数据拖进首屏。`ShellLoading.tsx` 新引入的只有 `types/navGroupSizes.ts`（零个 import）与 `types/demoMode.ts`（只引 `types/config.ts`，后者零个 import；常驻的 `DemoNotice` 本来就在用它），没有去 import `navigation.tsx`。
- 新模块 `web/src/types/quotaLendingMount.ts` 零个 import（`grep -c '^import'` 得 0）。与旧模块逐行比对：只删掉 `QUOTA_LENDING_NAV_KEY` 与 `lendingNavKeyForPath` 两个过渡期导出并改写头部注释；`QUOTA_LENDING_PATH`、`QUOTA_LENDING_NAV_LABEL_KEY`、`QUOTA_LENDING_BACK_LABEL_KEY`、`hasPreviousHistoryEntry` 的值与函数体一字未改。
- 引用都改了：`git grep -n "quota/lendingMount" -- web scripts` 与 `git grep -n "lendingNavKeyForPath\|QUOTA_LENDING_NAV_KEY" -- web scripts` 均为空。新模块的引用者：`App.tsx`、`routePreload.ts`、`navigation.tsx`、`pages/quota` 下三个文件、两份组件测试与 `ui-impact.test.mjs` 的夹具字符串。
- 文档里的旧名残留：`git grep lendingMount`（排除冻结的 `acceptance`）还剩 4 行，见 N2。

### 4.3 借调页高亮映射的撤除与挂载测试　通过

- `AppLayout.tsx` 里 T7 加的三处（导入 `lendingNavKeyForPath`、`const navKey = lendingNavKeyForPath(...)`、按 `navKey` 过滤 `currentEntry`）全部撤除，改为 `resolveNavLocation(navGroups, location.pathname)`。映射本身已不存在，因此不会再盖住新条目。
- 带尾斜杠的 `/quota-lending/` 靠「路径加 `/` 前缀」匹配 `/quota-lending`：`navigation.component.test.tsx` 与 `quota-lending-mount.component.test.tsx` 各有一条断言，探针 `oauthManagement.mjs` 里「带斜杠地址高亮」的期望由 OAuth 条目改成额度拆借条目（`/额度拆借|quota lending/` 且不含 `OAuth|dashboard`）。界面真测见 §6。
- `quota-lending-mount.component.test.tsx` 的 diff 逐条对照旧文件：导入换名；第一条用例改名（「highlighted nav key」→「menu label key」）并删去 6 条过渡期断言（`QUOTA_LENDING_NAV_KEY` 等于 `/oauth-management`，以及 `lendingNavKeyForPath` 的 5 条），换成 `QUOTA_LENDING_NAV_LABEL_KEY` 一条；新增一条「借调页解析到能力分组下它自己的条目」；其余用例（路径、返回按钮、基路径、入口链接、整页挂载）一条没动。简报提到的「整页渲染时断言高亮落在 OAuth 管理」在旧文件里并不存在，所以没有对应改动。
- 探针 `oauthManagement.mjs` 的另一处改动（读入口前的条件等待）见 4.5。

### 4.4 resolveNavLocation 的抽取　通过

与改前（S3 分支的 `AppLayout.tsx`：`currentEntry`、`selectedKey`、`currentGroup`）逐点比对：

| 点 | 改前 | 现在 | 结论 |
|---|---|---|---|
| 最长匹配 | 过滤后按 key 长度降序取第一个 | `resolveNavLocation` 里同一写法 | 等价 |
| 路径加斜杠前缀 | `navKey === entry.key \|\| navKey.startsWith(entry.key + '/')` | 同一判断 | 等价 |
| 读完整模型 | `navEntries` 取自完整的 `navGroups` | 调用点传 `navGroups`，不是 `visibleNavGroups` | 等价；该调用点只被浏览器探针钉住，实验 E6 |
| 没有条目匹配 | 高亮回落 `/dashboard`，分组按 `selectedKey` 反查得「运行」，标题 `common.management` | 函数返回空对象；`currentGroup = matchedGroup ?? navGroups.find(…selectedKey)`，其余不变 | 等价，且有断言，实验 E5 |

回落的测试不是「只断言不崩」：`app-layout-navigation.component.test.tsx` 在 `/plugin-pages/retired/0`（停用插件的页面）上断言标签页标题是 `Oh My CPA · Management center`、面包屑含 `Operate` 与 `Management center`、菜单选中项含 `Dashboard`。实验 E5 去掉回落那句，该用例按预期变红（`expected 'Management center' to contain 'Operate'`）。

### 4.5 探针里新加的条件等待　判断：补探针本来就有的同步缺口，不是掩盖入口变慢

- 改动：`scripts/acceptance/probes/oauthManagement.mjs` 的 `verifyLendingStates` 里，`setState('mixed'); await gotoOAuth();` 之后、读入口之前加了 `await page.locator('[data-lending-entry]').waitFor({ timeout: 20000 })`。它等的是紧接着要读的那个元素，不是固定时长，没有把任何等待拉长。
- 缺口本来就在：`gotoOAuth` 只等页头和第一行凭证记录（等行那步还带 `.catch(() => {})`），而入口取决于另一路读取（`/management/quota` 的 `lending` 投影）。为了分清「入口一直晚于凭证行」还是「S4 之后才变晚」，用同一套桩、同一个 `mixed` 状态，在页面里用 MutationObserver 记第一行凭证与入口第一次出现的时刻，基线与 S4 头各测：

| 条件 | 基线 `90a8d8ff` | S4 头 |
|---|---|---|
| 额度应答即时，各 5 次 | 入口与第一行同一时刻出现，间隔 0 ms，0/5 次更晚 | 同，0/5 次更晚 |
| 额度应答被拖慢 400 ms，各 3 次 | 入口晚于第一行 263、285、259 ms | 305、331、282 ms |

- 结论：两个修订上，第一行由凭证列表画出，入口随额度应答到达，晚多少只取决于额度应答多快；S4 头与基线同一量级（每组只有 3 次，两组之间约 40 ms 的差别在各组自身的抖动范围内），没有入口变慢的迹象。所以这处条件等待补的是探针本来就有的读取点缺口，不是掩盖退化，无需追查原因。
- 基线上把 `quota-lending` 场景本身单独跑 3 次（探针里没有这条等待）：3 次全过，112.2、109.5、110.8 秒；S4 头在 `check:ui --all` 里该场景也通过。这个缺口在即时应答的桩下确实不容易暴露，所以没有失败过不能证明它不存在，上面的延迟测量才是证据。
- 范围外观察：同一个函数里更早的几处读入口（`advised`、`balanced`、`single`、`empty`）读取点与这一处同样在凭证行之后、入口之前，结构上同样可能早于入口，只是没有失败过；这一处等待没有统一收进 `gotoOAuth`，属写法，不记要修。

### 4.6 S6 对 S3 提的 R1～R3　三条都已修，测试都能红

依据：S6 分支上的 `review-s3-r1.md` 第 2.1 节。

| 条 | 要求 | S4 的改动 | 核对 |
|---|---|---|---|
| R1 页头刷新只重读活跃查询 | `type: 'active'` 或改 `invalidateQueries`，并补能分出两种写法的测试 | `RepoKeysPage.tsx` 的 `refetchQueries` 加 `type: 'active'`，头注释同步；`repo-keys-page.component.test.tsx` 新增「refreshes only the reads the page is showing」：先敲搜索词再清空、打开一把密钥的详情再关闭，让搜索与详情各留一个不活跃缓存，点刷新后断言列表读取加 1，搜索与详情读取次数不变 | 已修。实验 E3 把写法改回旧的 `refetchQueries({ queryKey })`，该用例变红（`expected 2 to be 1`，与 S6 副本里的失败数字相同），还原后通过 |
| R2 审批链接页返回入口无断言 | 断言链接文字与 `href` 为 `/repo-keys` | `repo-key-apply.component.test.tsx` 新增「returns to the repository keys page from the approval link」：取名为 `Repository keys` 的链接，断言 `href` 是 `/repo-keys` | 已修。实验 E4 把 `RepoKeyApplyPage.tsx` 的链接目标临时改回 `/api-keys`，该用例变红（`expected '/api-keys' to be '/repo-keys'`），还原后通过 |
| R3 `RepoKeysPanel.module.css` 首行注释过时 | 只描述样式语言，不写页面位置 | 首行改为「the repository-owned keys panel. Same terminal-flat language as the gateway key list…」 | 已修，与 S6 的建议一致 |

S6 的 O2（手机场景登记）也一并兑现：`mobileConsole.mjs` 的 `ROUTES` 在 `/api-keys` 之后登记了 `['/repo-keys', '.repo-keys-page']`，现共 16 条，`docs/testing.md` 里「16-route sweep」与实际相符。

## 5. 证伪实验

每个实验：只改一个文件，跑相关测试，立刻 `git checkout -- <文件>` 还原；每次还原后 `git diff --stat` 为空。日志在 `acceptance/shots/s7/logs/`（不进 git）。vitest 命令统一为 `node web/node_modules/vitest/vitest.mjs run --root <绝对路径>/web --config <绝对路径>/web/vitest.config.ts <测试文件>`。

| 编号 | 改动 | 跑什么 | 结果 | 说明 |
|---|---|---|---|---|
| E1a | `navigation.tsx`：仓库自有密钥的图标换成 `KeyOutlined`（与密钥管理同一个别名） | `navigation.component.test.tsx` | 红：「gives every fixed entry its own icon」`expected 19 to be 20` | 同一别名重复能抓到 |
| E1b | `navigation.tsx`：仓库自有密钥的图标换成 `FileTextOutlined`（图标集里与「日志」的 `ProfileOutlined` 同为 `createIcon(Lucide.FileText)`，渲染出同一个图形） | 同上 | 绿：18 个用例全过 | 两个别名指向同一图形，测试抓不到，见 N1 |
| E2 | `navGroupSizes.ts`：常规的观测组 5 改 4，演示的运行组 3 改 4 | `shell-loading` 与 `navigation` 两份测试 | 红 3 条：占位侧栏的常规与演示两个渲染用例、导航测试里的「pins the placeholder group sizes」 | 占位的条数被钉在真实模型上，两个层都有 |
| E3 | `RepoKeysPage.tsx`：刷新改回旧写法（去掉 `type: 'active'`） | `repo-keys-page.component.test.tsx` | 红：「refreshes only the reads the page is showing」`expected 2 to be 1` | S6 R1 的测试能红 |
| E4 | `RepoKeyApplyPage.tsx`：返回链接目标改回 `/api-keys` | `repo-key-apply.component.test.tsx` | 红：`expected '/api-keys' to be '/repo-keys'` | S6 R2 的测试能红 |
| E5 | `AppLayout.tsx`：去掉未匹配时回落到仪表盘所在分组的那句 | `app-layout-navigation.component.test.tsx` | 红：`expected 'Management center' to contain 'Operate'` | 回落的测试不是只断言不崩 |
| E6 | `AppLayout.tsx`：`resolveNavLocation` 改读过滤后的模型 `visibleNavGroups`（被隐藏条目的页面失去名字与分组） | 四份组件测试（`app-layout-navigation`、`navigation`、`quota-lending-mount`、`sidebar-nav`）；再跑浏览器场景 `sidebar-customization`（5187 端口） | 组件测试绿，33 个用例全过；浏览器场景红：`timed out after 10000ms waiting for the document title of a hidden page` | 「读完整模型」这个调用点由浏览器探针钉住，组件层没有 |

全部实验还原后，S4 头的已跟踪文件无改动。

## 6. 界面证据

### 6.1 路径与环境

- 路径：Edge 无头（`C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe`）加仓库自带的 `playwright-core` 1.62.1，没有用内置浏览器。开发服务器与接口桩复用仓库的探针机制（`scripts/acceptance/probe.mjs` 的 `startVite`、`installRoutes`；夹具取自 `modelGroups.mjs`、`oauthManagement.mjs`、`lending-fixtures.mjs`）。脚本放在系统临时目录，不进仓库，分九段：展开栏、四种语言、收起栏、手机抽屉、设置面板与隐藏、页面的高亮与面包屑、前进后退与刷新、加载占位、演示模式。
- 没有起构建出的二进制：本机没有 go，所以服务端对地址的兜底没有在二进制上验证；Go 零改动，S3 报告 5.1 已读过兜底代码。构建产物上的演示模式验收 `pnpm verify:demo` 单独跑了，见 §7。
- 端口：`pnpm check:ui --all` 用 5181，起之前它被 S8 工作区的同类运行占着，等对方结束后才起，没有动那个进程；5187 是 S4 头的开发服务器（界面脚本与场景实验），5188 是基线的开发服务器与基线场景，18471 给 `pnpm verify:demo`。没有碰 18421～18433、18441、18442、18461～18469。收尾时 5181、5187、5188、18471 均核对为空闲，自己起的进程全部结束。
- 加载占位的截法：用一个 Promise 拦住首屏的偏好读取，占位出现时测量并截图，再放行，加载完成后再测一次，全程没有固定等待。

### 6.2 结果

- 最终整套 9 段共 80 项全过：展开栏 11、语言 15、收起栏 4、手机抽屉 5、设置面板与隐藏 13、页面 16、前进后退与刷新 6、加载占位 6、演示模式 4。
- 如实记一次偶发：第一轮整套是 58/59，`pages` 段在该浏览器上下文的首次导航（`/model-groups`）上等 `.model-groups-page` 超时一次，没有留下页面报错。加了诊断输出后，同一段连续跑了三次（其中一次是上面的最终整套）全部通过，未复现，原因未查明；`check:ui --all` 里的 `model-groups` 场景同样通过。按开发服务器的偶发处理，不记为 S4 的问题，但不隐瞒。另有一项早先的失败是我的选择器写错（页底提示的类名是 CSS 模块哈希），已改用链接地址。

### 6.3 页面的高亮、面包屑与标签页标题（英文界面）

| 地址 | 菜单高亮 | 面包屑 | 标签页标题 | 页内标题 |
|---|---|---|---|---|
| `/model-groups` | 模型组 | Capabilities › Model Groups | Oh My CPA · Model Groups | Model Groups |
| `/model-groups/preset-labor` | 模型组 | Capabilities › Model Groups | Oh My CPA · Model Groups | 该组名 |
| `/model-groups/preset-labor/add` | 模型组 | Capabilities › Model Groups | Oh My CPA · Model Groups | Add models to … |
| `/repo-keys` | 仓库自有密钥 | Capabilities › Repository keys | Oh My CPA · Repository keys | Repository keys |
| `/quota-lending` 与 `/quota-lending/` | 额度拆借（OAuth 管理不高亮） | Capabilities › Quota lending | Oh My CPA · Quota lending | Quota lending advice |
| `/api-keys` | 密钥管理 | Gateway › Key management | Oh My CPA · Key management | Key management，页底有指向「Capabilities」下仓库自有密钥页的提示 |
| `/oauth-management` | OAuth 管理 | Gateway › OAuth management | Oh My CPA · OAuth management | OAuth management |
| `/repo-keys?x=1&y=2`、`/model-groups/preset-labor?tab=members` | 同上两行 | 同上两行 | 同上两行 | 查询参数不影响 |
| `/plugin-pages/retired/0`（停用插件的页面，没有条目拥有它） | 仪表盘 | Operate › Management center | Oh My CPA · Management center | — |
| `/model-groups`，且模型组被设置隐藏 | 无（侧栏里没有它） | Capabilities › Model Groups | Oh My CPA · Model Groups | Model Groups |

中文界面下，三个新条目的面包屑是「能力 › 模型组」「能力 › 仓库自有密钥」「能力 › 额度拆借」，标签页标题分别是 `Oh My CPA · 模型组`、`· 仓库自有密钥`、`· 额度拆借`，额度拆借页内标题仍是「额度拆借建议」。点菜单进入、Back、Forward、刷新，每一步高亮与面包屑都跟着落在对应条目（`history` 段）。

### 6.4 请求与写操作

- 同一套桩、同一个脚本，在基线与 S4 头上各打开几页，逐条对比 GET 清单：`/dashboard`（21 条）、`/model-groups`（8 条）、`/oauth-management`（11 条）、`/quota-lending`（7 条）两端逐条相同，次数也相同，且没有任何写请求。`/api-keys` 在 S4 头上少三条（待审批、待交付、审计推送状态），没有多出的，与 S3 的 AC-24 口径一致。`/repo-keys` 在 S4 头上读 `management/repo-keys`、`applications` 两条、`audit-push` 共四条面板读取，另加外壳的读取。
- 写请求按段断言：展开栏一段只有 GET；设置面板一段只有我在面板里关开关触发的 `PUT /preferences/omc_nav_hidden`；页面一段只有为「被隐藏的条目用地址打开」预先写入的同一个 PUT。展开栏、页面、演示三段没有页面报错，展开栏一段另断言没有请求离开本机来源。
- 因此 S4 没有新增请求与写操作；各页面自身功能的回归由 `check:ui --all` 的 81 个场景承担。

### 6.5 建议重点看的三处

1. `shots/s7/04-rail-collapsed.png`（2 倍分辨率）：收起栏 20 个图标的顺序与可区分度；仪表盘与模型组的四方格外观接近（X1）。
2. `shots/s7/21-placeholder-loading.png` 对 `shots/s7/23-after-load-rail.png`：占位侧栏 6 组 4/3/5/3/2/3，与加载后的侧栏组头位置几乎重合，总高度差 21px。
3. `shots/s7/08-settings-panel-capabilities-hidden.png` 与 `09-rail-capabilities-hidden.png`：设置面板里「能力」下的三个开关、「3 hidden」的计数，以及关光后展开栏里整组消失。

没有留着运行中的服务（收尾要求全部关掉）。要自己看：在工作区根目录 `pnpm install` 后 `pnpm check:ui --scenario sidebar-customization` 等场景只出结论；有数据的页面请看上面的截图，或按 `docs/testing.md` 起 fake CPA 与构建出的二进制（本机缺 go）。

截图清单（37 张，`acceptance/shots/s7/`，不进 git）：`01`～`02b` 展开栏与整页（浅色、深色）；`03-rail-lang-{zh,en,zh-Hant,ms}` 四种语言；`04` 收起栏；`05`、`06` 手机抽屉与点击后；`07`、`08` 设置面板；`09`～`11` 展开、收起、手机下的整组隐藏；`12`～`18` 各页面；`19` 未匹配路由；`20` 被隐藏条目用地址打开；`21`、`21b`、`22`、`22b`、`23` 加载占位与加载后（常规与演示）；`24`、`25` 演示模式。

## 7. 门禁与基线红项

本机 Windows 11，Node 24.16.0（仓库钉的是 22.23.2，所有命令都有引擎告警），没有 go。基线是 `11-24-t9-dev-base`（`90a8d8ff`）在系统临时目录的独立工作区；判新红的规矩是先在基线复跑同一条。

| 命令 | S4 头 | 基线 | 判定 |
|---|---|---|---|
| `pnpm build && pnpm check:bundle` | 构建成功，`check:bundle` 退出码 0，`Loading boundaries and anomaly ceilings: PASS` | 构建成功，`check:bundle` 退出码 1：`web/src/pages/quota/lendingMount.ts is eager through assets/index-….js` | S4 修了基线红 |
| `pnpm check:ui --all`（端口 5181） | 81 个场景全过，619.2 秒，退出码 0 | 未跑全量；`quota-lending` 单独跑 3 次全过（112.2、109.5、110.8 秒） | 无新红 |
| 新增与受影响的 9 份组件测试 | 9 个文件 44 个用例全过 | — | 通过 |
| `pnpm type-check` | 退出码 0 | — | 通过 |
| `pnpm check-i18n` | 退出码 0（4339 个键） | — | 通过 |
| `pnpm check-css-modules`、`pnpm check:motion`、`pnpm check:style-tokens`、`pnpm check:feedback` | 全部退出码 0 | — | 通过 |
| `pnpm check:demo`（简报写的 `pnpm check-demo` 不是脚本名） | 退出码 0：155 条响应，覆盖 20 条控制台路由 | — | 通过 |
| `pnpm verify:demo`（构建出的控制台加进程内演示 Worker，端口 18471） | 退出码 0：23 条路由都渲染，含 `/model-groups` 系列、`/api-keys`、`/repo-keys`；操练场仍关闭且没有导航入口 | — | 通过 |
| `node scripts/check-docs.mjs` | 退出码 1：9 条缺失路径、22 条行号引用 | 逐行相同的 9 与 22 | 基线既有，31 条全在 `docs/delivery/2026-11-12-omc-three-credential-quota/` |
| `pnpm test:fast --base Newoahil/11-24-t9-s3-codex`（14 路） | 10 路通过：样式令牌、CSS 模块、动效、翻译键、工作流语法、钉定工具链、antd 规则、反馈面、纯逻辑测试（50.3 秒）、类型检查；4 路红，见下 | 见下 | 无新红 |
| ↳ Go 测试 | 红：`spawn go ENOENT`（本机没有 go） | 同 | 环境性 |
| ↳ 文档引用 | 红：同上 31 条 | 同 | 基线既有 |
| ↳ 仓库自测 | 红：10 个用例失败，另有演示数据新鲜度通过 | `node scripts/test-self.mjs` 在基线上同样 10 个，失败名单逐条相同 | 基线既有，环境性（无 go、无 zip、Windows 符号链接权限，以及下一行） |
| ↳ `fixed waits in tests only ever decrease` | 红：`experience-env.mjs` 2、`lending-experience-env.mjs` 1、`plugin-quota.mjs` 1、`probes/oauthManagement.mjs` 1、`quota-auto-refresh-round.mjs` 3 处固定等待，允许 0 | 逐行相同，同样 5 个文件 8 处 | 基线既有；`probes/oauthManagement.mjs` 那 1 处在基线就有，S4 新加的 `waitFor({ timeout })` 不是固定等待，没有被计入 |
| ↳ 组件与接口集成测试 | 365 个用例 364 个通过，1 个失败：`account-automation` 的「single and batch unadopt…」找不到「Select all」按钮 | 不适用 | 并行负载下的偶发：该文件单独连跑 3 次全过（15 个用例）；S6 在 S3 分支上记过同一条；S4 没动这个文件及其源码 |

没有跑：`pnpm verify:toolchain`（Node 版本与钉定不符，只算兼容性）、`pnpm demo:generate --check`（Windows 上必红 12 个叶子路径）、Go 的测试与 vet（Go 零改动，本机无 go）、`pnpm verify:browser`、`pnpm verify:e2e`、`pnpm verify:full`（都需要 go 构建出的二进制）、`pnpm verify:secrets`（不在任务清单里，没有跑扫描；S4 的 diff 我通读过，没有看到凭据类内容）。因此 `pnpm verify` 没有整体按原样跑。

基线红项复现证据：`logs/` 下 `base-check-bundle.log`（基线 `check:bundle` 红）、`base-check-docs.log` 与 `head-check-docs.log`（31 条逐行相同）、`fixed-waits-base.log` 与 `fixed-waits-head.log`（逐行相同）、`base-test-self.log`（基线仓库自测）；`logs/head-test-fast.log` 是 S4 头的完整输出。

## 8. 证据索引与复现命令

绝对路径（本机，证据都不进 git）：

- 本报告：`E:/works/Newoahil__oh-my-cpa/11-24-t9-s7-claude/docs/delivery/2026-11-23-omc-capabilities-nav/acceptance/review-s4-r1.md`，副本在 `E:/works/Homan-context/inbox/agents/验收/2026-11-24-omc-capabilities-nav/review-s4-r1.md`。
- 截图与结构化结果：`E:/works/Newoahil__oh-my-cpa/11-24-t9-s7-claude/docs/delivery/2026-11-23-omc-capabilities-nav/acceptance/shots/s7/`（37 张 PNG；`s7-ui-final.json` 是最终整套 80 项的逐项结果与数据，`s7-ui-head-requests.json`、`s7-ui-base-requests.json` 是两端的请求清单）。
- 日志：同目录下 `logs/`（`check:ui --all`、`check:bundle` 两端、七个证伪实验、到达时机测量、基线场景三次、`check-docs` 两端、`fixed-waits` 两端、`verify:demo`、`type-check`、各项静态门禁）。
- 脚本：同目录下 `tools/`（`s7-ui.mjs` 界面九段，`s7-arrival.mjs` 到达时机测量，`run-scenario.mjs` 单场景重复跑），原件在系统临时目录 `C:/Users/lhw/AppData/Local/Temp/s7-tools/`。
- 基线工作区（`git worktree add --detach` 在系统临时目录，用完已 `git worktree remove`）：`11-24-t9-dev-base` 即 `90a8d8ff`。

复现命令（在 S4 头的工作区根目录，新工作区先 `pnpm install --frozen-lockfile`）：

```
pnpm build && pnpm check:bundle                       # 基线红、S4 头过
pnpm check:ui --all                                   # 81 个场景，约 10 分钟，端口 5181
node <脚本目录>/s7-ui.mjs <工作区绝对路径> 5187 <输出目录> final   # 界面九段
node <脚本目录>/s7-arrival.mjs <工作区绝对路径> 5187 3 mixed 400   # 到达时机，第 5 个参数是额度应答延迟毫秒
node <脚本目录>/run-scenario.mjs <工作区绝对路径> 5188 quota-lending 3
node web/node_modules/vitest/vitest.mjs run --root <工作区绝对路径>/web --config <工作区绝对路径>/web/vitest.config.ts tests/navigation.component.test.tsx
OMCPA_DEMO_PORT=18471 pnpm verify:demo
git grep -n lendingMount -- . ':!docs/delivery/**/acceptance/**'   # N2
git grep -n -i sixteen -- web/src                                   # N3
```
