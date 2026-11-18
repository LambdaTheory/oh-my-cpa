---
type: cases
slug: 2026-11-17-omc-repo-key-client
status: draft
updated: 2026-11-17
source: 需求原文（E:/works/Homan-context/runs/2026-11-17-2026-11-17-T5.md 任务说明）· AC 编号沿用同记录「拆分」节末
impl-plan: impl-plan.md（未定稿；定稿后按文末「待方案核对」逐条对齐）
summary: "44 条用例，覆盖 AC-1～AC-9；进程级 32、单测 3、e2e 3、人工检查 6"
---

# 验收用例：分 key 使用端闭环（keygen / status / exec / install / purge）

依据：需求原话（`E:/works/Homan-context/runs/msgs/2026-11-17-2026-11-17-T5-0.md`）与 PM 拆分里的 AC-1～AC-9（`E:/works/Homan-context/runs/2026-11-17-2026-11-17-T5.md`「拆分」节末）。现状证据不重跑调研：`inbox/agents/交付/2026-11-12-omc-分key-可用性评估.md`、`inbox/agents/调研/2026-11-12-omc分key可用性-S2.md`；代码基线 `11-17-t5-omc-dev-base`（=`origin/dev` `ba9e2ee7`）。

判定口径：结论只有「通过 / 范围内要修 / 范围外观察」。**没跑过不许写通过**；一个路子打不开就如实记缺口，最多试三次。证据写清「命令 → 输出 / 文件 + 行号 / 截图」，落 `inbox/agents/验收/2026-11-17-omc-repo-key-client/`，不进 git。

## 用例索引（AC → 用例）

| AC | 验收点 | 用例 |
|---|---|---|
| AC-1 | 七步闭环可执行，五条子命令都在 | C-01、C-02、C-03 |
| AC-2 | `key gen`：X25519 密钥对，私钥 `$OMC_HOME/keys/<repo>.key` 0600，stdout 只有 `x25519:<base64>` | C-04～C-10 |
| AC-3 | `key status` 看得见私钥、绑过的仓库、最后一次审批状态 | C-11～C-15 |
| AC-4 | `key exec`：内存解密、只给子进程注入该运行时变量、不打印明文、不落盘 | C-16～C-25 |
| AC-5 | `key install`：显式写配置、写前备份并打警告 | C-26～C-30 |
| AC-6 | `key purge`：清私钥与写过的配置项；purge 后 exec 失败并给可读错误 | C-31～C-35 |
| AC-7 | 端到端可复现（keygen→apply→审批→ciphertext→exec→网关→purge→exec 失败） | C-36、C-37、C-38、C-39 |
| AC-8 | 单测：`key status`、`key purge`；私钥权限 0600 断言 | C-10、C-13、C-34（+ C-05 的权限断言） |
| AC-9 | 文档：agent 可直接照做，含禁止事项 | C-40、C-41、C-42、C-43、C-44 |

类型说明：**进程级**＝直接跑 `oh-my-cpa key` 二进制、断言退出码与输出；**单测**＝`go test`；**e2e**＝脚本串全链路、用 fake CPA 与临时 HOME；**人工检查**＝人看输出/文档/界面。

## 测试环境

### 基线与构建

- 被测分支：本需求功能分支（从 `11-17-t5-omc-dev-base` 切出）；按该分支最新提交执行，报告里写提交号。
- 工具形态：**`oh-my-cpa key` 子命令族**（PM 2026-11-17 二级决策；理由：解密实现在 `internal/repokey` 内、外部模块不能 import，独立小工具只能复制一份密码学实现；见父运行记录「决策记录」）。若方案定稿后改形态，C-01 与全部命令按方案改写，判定口径不变。
- 本机（Windows）**没有 `go`**。进程级与单测按下面 Docker 配方跑；Node 20+ 本机可用。

```bash
# 构建（容器内 / Linux）
CGO_ENABLED=0 go build -trimpath -o tmp/oh-my-cpa ./cmd/oh-my-cpa

# 单测（本机 Windows 用 Docker 配方；卷与配方沿用既有做法，见 memory omc-go-gate-docker-recipe）
docker run --rm --network none \
  -v "E:/works/<worktree>:/src:ro" \
  -v "omc-go-mod:/go/pkg/mod" -v "omc-gocache:/root/.cache/go-build" \
  -w /src -e GOPROXY=off -e GOFLAGS=-mod=readonly -e TZ=UTC -e ASSISTANT_NO_SEND=1 \
  golang:1.27.1 go test -count=1 ./cmd/oh-my-cpa ./internal/keyclient/...
```

包名 `./internal/keyclient/...` 是拆分单 S2～S4 的预判，**以方案「文件归属」一节为准**；方案定稿后本文件的包路径同步改一次。

### 隔离

每条进程级 / e2e 用例先准备一次性目录与 HOME，**不碰真实 `~/.claude`、`~/.codex`、`~/.config/opencode`、真实 `$OMC_HOME`**：

```bash
export TZ=UTC ASSISTANT_NO_SEND=1
export TMPROOT="$(mktemp -d)"
export OMC_HOME="$TMPROOT/omc-home"
export HOME="$TMPROOT/home"
mkdir -p "$TMPROOT/home" "$OMC_HOME"
```

Windows PowerShell 等价：

```powershell
$env:ASSISTANT_NO_SEND='1'; $env:TZ='UTC'
$TMPROOT = Join-Path $env:TEMP ("omc-case-" + [guid]::NewGuid().ToString('N').Substring(0,8))
New-Item -ItemType Directory -Force "$TMPROOT/omc-home","$TMPROOT/home" | Out-Null
$env:OMC_HOME="$TMPROOT/omc-home"; $env:USERPROFILE="$TMPROOT/home"
```

`HOME` 的覆盖在 Windows 上不一定被三家 CLI 认（它们多用 `USERPROFILE`/`HOMEDRIVE`+`HOMEPATH`，Go 的 `os.UserHomeDir()` 读 `USERPROFILE`）；三家分别验 C-28 时以方案写明的 home 解析规则为准，并在报告里记下实际生效的路径。

### fake CPA 与 OMC 启动

e2e 用仓库自带 `scripts/fake-cpa.mjs`（`createFakeCpaServer(managementKey)`），**不连生产、不用真实账号**。

已知现状（读代码确认）：该夹具对 `/v1/models` 用 `configDoc.access['api-keys']` 校验客户端 key（见 `scripts/fake-cpa.mjs` 里 `/v1/models` 的 `configDoc.access?.['api-keys']` 校验段），不带或带错 key 返回 401。仓库 key 由 OMC 签发时写入同一列表，所以「用这把 key 打一次网关成功」可用 `/v1/models` 的 200 判定。**它没有 `/v1/chat/completions`**（只有 `/v1/models` 一条 `v1` 路由）；若 S7 的夹具补了该路由，两者等价，报告里写明用的哪条。

OMC 启动环境（沿用既有验收做法，`inbox/agents/验收/2026-11-06-omc-repo-api-keys-e2e/e2e.mjs:153`）：

```bash
OMCPA_DATA_DIR="$TMPROOT/data" OMCPA_MASTER_KEY=<32 字节随机> \
OMCPA_CPA_MANAGEMENT_KEY=<本地随机> OMCPA_LISTEN_ADDR=127.0.0.1:<apiPort> \
OMCPA_API_TARGET=http://127.0.0.1:<apiPort> OMCPA_CPA_BASE_URL=<fake CPA base> \
OMCPA_BASE_PATH=/omc OMCPA_PUBLIC_URL=http://127.0.0.1:<uiPort> \
OMCPA_UPDATE_CHECK_ENABLED=false OMCPA_UPDATER_ENABLED=false \
OMCPA_FEISHU_FORCE_DISABLED=true OMCPA_ACCOUNT_AUTOMATION_ENABLED=false \
OMCPA_QUOTA_SCHEDULER_ENABLED=false OMCPA_OLLAMA_COLLECTOR_ENABLED=false \
./tmp/oh-my-cpa
```

MCP 走 HTTP 入口 `POST <BASE>/omc/api/mcp`，带 `Authorization: Bearer <management key>` 与 `X-Omc-Agent` / `X-Omc-Device`；工具名 `repo_key_query` / `repo_key_apply` / `repo_key_status` / `repo_key_ciphertext`，写法照 `e2e.mjs:75-91`。

### 审批这一步谁来点

- **自动化 e2e（C-36～C-39）**：测试夹具**代操作员**批。登录 `POST <BASE>/omc/api/auth/login {"password":<management key>}` 拿 cookie（`e2e.mjs:157`），再 `POST <BASE>/omc/api/v1/management/repo-keys/applications/<id>/approve {"reason":"local synthetic acceptance approval"}`（`e2e.mjs:129`）。这是本地 fake 环境里模拟人工审批，**不涉及真实账号、不批准任何真实申请**。
- **人工走查版本（C-37）**：申请人拿到 `approval_url`，浏览器里点「批准这次申请 / Approve this request」（`e2e.mjs:118-131`），截图留存。
- 夹具代批只能证明链路通，**不能冒充**「人只批该批的」这类审批策略判断——那属于既有 repo-key 需求，不在本 AC。

### 运行时落点（三家，来自调研 S2 的实测）

| 运行时 | `key exec` 注入的变量 | `key install` 写哪里 |
|---|---|---|
| claude | `ANTHROPIC_BASE_URL`、`ANTHROPIC_AUTH_TOKEN` | `~/.claude/settings.json` 的 `env` |
| codex | 按方案（调研：`model_providers.<id>` 的 `base_url`+`env_key`；`CODEX_API_KEY` 是否被读取未证实） | `~/.codex/config.toml` 的 `[model_providers.<id>]` |
| opencode | 按方案（调研：`OPENCODE_CONFIG_CONTENT` 仅本次；v1 单数 schema） | `~/.config/opencode/opencode.json` 的 `provider.<id>.options.apiKey` |

codex / opencode 的确切变量名与落点**由方案定**（拆分单 S3「三种运行时环境变量注入」是方案要定死的点）。C-19 判「与方案声明逐字一致」，不判「与调研推断一致」。

## 用例

### AC-1 七步闭环可执行、五条子命令都在

#### C-01 命令面齐全、帮助可读、未知子命令有明确退出码
- AC：AC-1 · 类型：进程级 · 优先级：P0
- 前置：二进制已构建。
- 步骤：
  1. `./tmp/oh-my-cpa key --help`
  2. 逐个 `key gen --help`、`key status --help`、`key exec --help`、`key install --help`、`key purge --help`
  3. `./tmp/oh-my-cpa key nope; echo "exit=$?"`
- 预期：
  - 五条子命令都在，各自 `--help` 退出码 0；说明里有参数、默认值与 `$OMC_HOME` 解析规则。
  - 未知子命令退出码非 0，stderr 有用法提示。
  - `key` 的帮助不出现「输出私钥」「打印明文」一类可被误读的说明。
- 证据：全部输出粘贴进报告；退出码留痕。

#### C-02 七步与五条命令的对应关系可走通（不要求 key 已批）
- AC：AC-1 · 类型：进程级 · 优先级：P0
- 前置：临时 `OMC_HOME`、临时 HOME。
- 步骤：
  1. `key status`（步骤 1 的入口）。
  2. `key gen --repo example/fixture-repo`（步骤 2）。
  3. 手工把一段**非法/伪造信封**写到 `$OMC_HOME/envelopes/example/fixture-repo.json`，再 `key status`——观察它是否区分「有信封」与「信封可用」。
  4. 按方案语法跑 `key exec --runtime claude -- printenv ANTHROPIC_AUTH_TOKEN`（信封是假的，预期失败但要可读）。
- 预期：七步每一步都有明确入口；缺前置（没信封、没审批）时给的是「缺什么、下一步做什么」的可读错误，不是 panic、不是静默成功。
- 证据：四步输出与退出码。

#### C-03 既有子命令不回归
- AC：AC-1 · 类型：进程级 · 优先级：P1
- 前置：改动前后的同一环境。
- 步骤：
  1. `./tmp/oh-my-cpa mcp`、`./tmp/oh-my-cpa mcp -h`；确认用法打印在 **stderr**（`cmd/oh-my-cpa/main.go` 的 `main` 里 `mcp` 子命令分支的既有契约：stdout 是协议通道）。
  2. 跑既有 repo-key MCP 鉴权/目录用例（或本机等价脚本），确认 `repo_key_*` 四个工具仍在目录里。
  3. `./tmp/oh-my-cpa updater`（或既有 updater 测试）行为不变。
- 预期：`mcp` / updater 的参数、退出码、stdout/stderr 分工与改动前一致；`key` 的加入没有改变默认启动路径（`main.go` 末尾的 server 动作仍是不带子命令时的行为）。
- 证据：改动前后同一命令输出对比。

### AC-2 `key gen`

#### C-04 首次生成：私钥落盘、stdout 只有公钥
- AC：AC-2 · 类型：进程级 · 优先级：P0
- 前置：空 `OMC_HOME`。
- 步骤：
  1. `./tmp/oh-my-cpa key gen --repo lambdatheory/fixture-repo > "$TMPROOT/gen.out" 2> "$TMPROOT/gen.err"; echo "exit=$?"`
  2. 查看 `gen.out`、`gen.err` 全文。
  3. 找到私钥文件（预期 `$OMC_HOME/keys/<repo>.key`，命名映射见 C-06），`wc -c` 看大小（**不要 `cat`**）。
  4. 校验公钥：`tr -d '\n' < "$TMPROOT/gen.out" | sed 's/^x25519://' | base64 -d | wc -c` → 32。
- 预期：
  - stdout **只有一个** `x25519:<base64>`（允许结尾换行）；默认不包 JSON（若方案有 `--json`，默认仍须纯文本）。
  - stderr 为空或只有非敏感提示；两路都搜不到私钥的任何编码形式。
  - 私钥文件存在，内容是 32 字节或其 base64/hex 编码（按方案），不是 PEM 公钥。
  - 退出码 0。
- 证据：`gen.out`、`gen.err` 原样；`wc -c` 与 `base64 -d` 的字节数；`ls -l` 权限列。

#### C-05 私钥文件权限 0600（Windows 上怎么写断言）
- AC：AC-2、AC-8 · 类型：进程级 · 优先级：P0
- 前置：C-04 生成过一把 key。
- 步骤（Linux / macOS / 容器内，**必验**）：
  1. `stat -c '%a %U:%G' "$OMC_HOME/keys/<repo>.key"`（macOS：`stat -f '%Lp %Su:%Sg'`）
  2. 同法看 `$OMC_HOME/envelopes/<repo>.json`（若信封已写）。
  3. 容器内换用户读一次：`su nobody -s /bin/sh -c 'cat <私钥路径>'`，记录权限拒绝。
- 步骤（Windows，**单独说明，不算必红**）：
  1. `Get-Acl "$env:OMC_HOME\keys\<repo>.key"`，记录 ACL 是否只有当前用户。
  2. PS7 可看 `(Get-Item ...).UnixMode`（Go 写的文件通常显示 `-rw-rw-rw-`，因为 `os.Chmod` 只切只读位）。
- 预期：
  - Linux / macOS：位模式 **`600`**；同组与其他用户读不到。
  - Windows：`stat` 反映不出 0600 是平台限制、不是实现缺陷。判定改为两条都要成立 —— (a) 源码创建私钥时显式传 `0o600`（审查看 `os.OpenFile(..., 0o600)` 或 `os.WriteFile(..., 0o600)` 的 `file:line`）；(b) ACL 里没有 `Everyone` / `Users` 的读取项。**把「Windows 上必须出现 600」写成断言，用例本身就是错的。**
  - Docker Desktop 的 NTFS bind mount 会照抄 Windows ACL、可能显示 `777`：这种环境不判红，改用容器内原生卷（`-v tmpvol:/keys`）复验一次。
- 证据：`stat` 输出、`Get-Acl` 输出、源码 `file:line`。

#### C-06 仓库标识归一化与文件名安全（含路径穿越）
- AC：AC-2 · 类型：进程级 · 优先级：P0
- 前置：空 `OMC_HOME`。
- 步骤：
  1. 同一仓库三种写法各生成一次（每次前清空 `$OMC_HOME/keys`）：
     `--repo lambdatheory/fixture-repo`、`--repo https://github.com/LambdaTheory/Fixture-Repo.git`、`--repo git@github.com:lambdatheory/fixture-repo.git`
  2. 列出 `$OMC_HOME/keys/` 文件名，比较是否同一份；同时看公钥是否相同（归一化后应同一身份）。
  3. 恶意输入：`--repo '../../../etc/passwd'`、`--repo 'a/../../b'`、`--repo ''`、含 `<TAB>`、>256 字符。
  4. 每个输入后 `find "$TMPROOT" -newer "$TMPROOT/marker"`，确认 `$OMC_HOME` 之外没有新文件。
- 预期：
  - 三种写法落到**同一仓库身份**、同一个私钥文件；归一化规则与 `internal/repokey/repokey.go` 的 `NormalizeRepo` 一致（GitHub owner/repo 折小写，其他主机与本地目录保留大小写）。
  - `..`、绝对路径、空串、控制字符、超长：退出码非 0、可读错误，**任何情况下不在 `$OMC_HOME` 之外创建或覆盖文件**。
  - 文件名把 `/` 转义（如 `lambdatheory__fixture-repo.key`）或改子目录：方案要写明规则，且 `status` / `exec` / `purge` 用同一规则找回。
- 证据：三次文件名与公钥；恶意输入的命令 + 退出码 + `find` 结果。

#### C-07 同一仓库重复 `key gen`（按方案定：覆盖还是拒绝）
- AC：AC-2 · 类型：进程级 · 优先级：P0
- 前置：已有一把 key（C-04）。
- 步骤：
  1. 记原私钥 `sha256sum`（不打印内容）。
  2. 第二次 `key gen --repo <同一仓库>`，记录 stdout/stderr/退出码。
  3. 再取 `sha256sum`。
- 预期（两选一，**以方案定稿为准**；本用例只负责钉死判定）：
  - **方案选「拒绝」**：退出码非 0，错误说明已存在、下一步（`key purge` 或换仓库）；哈希不变。
  - **方案选「覆盖」**：退出码 0，新公钥与原公钥不同、哈希改变；并且提示「旧公钥作废，远端绑定需重新申请」——方案若规定其它提示按方案。
  - 两种都不能出现「静默追加」「留下两份私钥」。
- 证据：两次公钥、哈希对比、退出码。

#### C-08 目录不可写 / 只读文件 / `$OMC_HOME` 是普通文件
- AC：AC-2 · 类型：进程级 · 优先级：P1
- 前置：C-04 生成过 key（在独立 HOME 里另做）。
- 步骤：
  1. `chmod 500 "$OMC_HOME/keys"` 后 `key gen --repo other/fixture`。
  2. 已存在私钥 `chmod 400`，再对同一仓库 `key gen`。
  3. `OMC_HOME=$TMPROOT/notadir`（该路径建为普通文件）再 gen。
  4. 恢复权限再正常生成一次，确认可恢复。
- 预期：三次失败都是**可读错误**（哪个路径、为什么、怎么修），退出码非 0，不 panic、不出现 Go 堆栈；失败后 `$OMC_HOME/keys` 下没有 0 字节残留。
- 证据：三步输出；`ls -la` 前后对比。

#### C-09 只打印公钥：对 stdout / stderr / 落盘文件做全文扫描
- AC：AC-2 · 类型：进程级 · 优先级：P0
- 前置：C-04 的 `gen.out`/`gen.err`。
- 步骤：
  1. 用私钥算出 hex 与 base64 两种形式，`grep -F` 在 `gen.out`、`gen.err` 里找。
  2. 若方案提供 `--verbose`，重跑一次再扫。
  3. `find "$TMPROOT" -newer "$TMPROOT/marker" -type f` 后逐文件 `grep -F` 私钥 hex。
- 预期：三处都无匹配；`--verbose` 只增加路径、耗时一类元信息。
- 证据：`grep` 命令与退出码 1（无匹配）；`find` 文件清单。

#### C-10 单测：私钥创建 0600 断言
- AC：AC-8、AC-2 · 类型：单测 · 优先级：P0
- 前置：方案给出的单测文件。
- 步骤：
  1. `go test -count=1 -v -run 'Test.*(Perm|0600|PrivateKey)' ./internal/keyclient/...`
  2. 读测试源码：断言的是**创建时传入的 mode**（`os.OpenFile` 的 `0o600`）与 `os.Stat` 的 `Perm()`，而不是只断言文件存在。
- 预期：测试存在、可跑、通过；容器内 `Perm()&0o077 == 0`。若该断言在 Windows 主机上不可避免为红，测试应带构建约束（`//go:build !windows`）并在报告里写明——**不允许**把平台差异写成永久红，也不允许直接删断言。
- 证据：`go test -v` 输出；测试源码行号。

### AC-3 `key status`

#### C-11 三种状态都能读懂：无 key / 有私钥无信封 / 有私钥有信封有审批
- AC：AC-3 · 类型：进程级 · 优先级：P0
- 前置：三个独立环境（或依次清理），配合 fake CPA 与 OMC。
- 步骤：
  1. **A（无 key）**：空 `OMC_HOME` 跑 `key status`。
  2. **B（有私钥、无信封）**：`key gen --repo example/fixture-repo` 后再跑。
  3. **C（有私钥、有信封、已审批）**：MCP `repo_key_apply` → 审批 → `repo_key_ciphertext`，把 ciphertext 存成 `$OMC_HOME/envelopes/<repo>.json`，再跑。
  4. 每个状态再跑一次 `key status --json`（若方案提供）。
- 预期：
  - A：明确「本机没有私钥」并给下一步（`key gen --repo <repo>`）；退出码 0（「没有 key」不是错误）。
  - B：列出仓库、私钥存在、信封「未取到」，提示去取 ciphertext 并存盘；不给「已就绪」。
  - C：仓库、私钥、信封、最后一次审批状态 `approved` + 时间；**不显示密钥明文、不显示完整密文、不显示私钥内容**，只给掩码（如 `sk-cpa-…abcd`）或「已就绪」。
  - 三态可区分，不靠退出码猜状态。
- 证据：三种输出全文；C 态另附审批状态来源（MCP `repo_key_status` 响应或本地记录文件路径）。

#### C-12 `key status` 的机器可读输出
- AC：AC-3、AC-1 · 类型：进程级 · 优先级：P1
- 前置：C-11 的三个状态。
- 步骤：
  1. 每个状态 `key status --json | python -m json.tool`。
  2. 记录字段集合与三态取值。
  3. 同状态再跑一次，比较除时间戳外的字段。
- 预期：JSON 合法、字段稳定、不含明文与完整密文；时间字段格式与方案一致。若方案没有 `--json`，本条记「不适用」并写进报告，不算范围外问题。
- 证据：三次 JSON（敏感字段掩码后）。

#### C-13 单测：`key status` 的状态判定
- AC：AC-8、AC-3 · 类型：单测 · 优先级：P0
- 前置：方案给出的单测文件。
- 步骤：
  1. `go test -count=1 -v -run 'Test.*Status' ./internal/keyclient/...`
  2. 读测试，核查覆盖：无 key、只有私钥、私钥+信封、审批 none/pending/approved/rejected；损坏信封；仓库不存在。
- 预期：覆盖上述分支且通过；断言用户可见状态文案/字段，不是内部函数名。
- 证据：`-v` 输出；测试源码行号。

#### C-14 「最后一次审批状态」的来源与离线可用性
- AC：AC-3 · 类型：进程级 · 优先级：P1
- 前置：C-11 状态 C 的环境；另起一份**不设 `OMCPA_SERVER_URL` / `OMCPA_CPA_MANAGEMENT_KEY`** 的环境。
- 步骤：
  1. 离线环境跑 `key status`，用 `time` 记耗时。
  2. 联网环境跑，比较审批状态字段。
- 预期（按方案定的来源判定）：
  - 读本地记录：离线也显示同一审批状态与时间；从未记录过就明说「无本地审批记录」，不猜。
  - 走 MCP：离线时明确报「连不上 `<server>`，审批状态未知」，不写成 `none`/`approved`；不卡超过方案给的上限（方案要写，例如 5 秒）；退出码区分「未知」与「无 key」。
- 证据：两次输出、耗时、`env | grep OMCPA` 证明没设。

#### C-15 `key status` 遇到坏文件
- AC：AC-3 · 类型：进程级 · 优先级：P1
- 前置：信封文件改成非法 JSON；另建一个 0 字节私钥文件。
- 步骤：跑 `key status`。
- 预期：能标出「信封损坏 / 私钥损坏」，不 panic；文案区分「坏文件」与「没有文件」。
- 证据：输出、退出码。

### AC-4 `key exec`

> 仓库如何选定：需求原文的 `key exec` 没有 `--repo`。若方案决定「按当前目录的 git remote 推断」，用例在对应仓库目录内跑；若要求 `--repo`，显式带上。二者都按方案写死，报告里写明。

#### C-16 只给子进程注入变量，父进程不受影响
- AC：AC-4 · 类型：进程级 · 优先级：P0
- 前置：C-11 状态 C 的环境。
- 步骤：
  1. `env | grep -E '^(ANTHROPIC_|CODEX_|OPENCODE_)'`，记录（应为空或既有无关值）。
  2. `key exec --runtime claude -- sh -c 'printenv ANTHROPIC_BASE_URL; printenv ANTHROPIC_AUTH_TOKEN' > "$TMPROOT/exec.out" 2>&1`（**别直接打到终端对话框**；看的时候只留长度与首尾掩码）。
  3. 同一 shell 再 `env | grep -E '^(ANTHROPIC_|CODEX_|OPENCODE_)'`。
  4. 子进程里 `sh -c 'env | sort'`，与父进程 `env | sort` 做 diff。
- 预期：
  - 第 1、3 步一致：父 shell 里**没有**这些变量（`key exec` 不导出到当前 shell，也不落盘）。
  - 第 2 步两值非空，base URL 与方案声明一致；token 解出的值与信封明文逐字节相同（测试里用私钥单独解密一次比对，只记「相同/不同」）。
  - 第 4 步 diff 只多出该运行时需要的变量，不多不少。
- 证据：四步输出（token 只留长度与掩码）；`env` diff。

#### C-17 stdout / stderr / 落盘都找不到明文（含 `--verbose`）
- AC：AC-4 · 类型：进程级 · 优先级：P0
- 前置：C-16；测试进程内存里有已知明文 `SECRET`（本地签发时记录，仅用于比对，不写日志）。
- 步骤：
  1. `key exec --runtime claude -- sh -c 'echo ok' > "$TMPROOT/out" 2> "$TMPROOT/err"`；两文件 `grep -F "$SECRET"`。
  2. 若方案提供 `--verbose`，重跑再扫。
  3. `find "$TMPROOT" -newer "$TMPROOT/marker" -type f` 后逐个 `grep -F`。
  4. Linux：`ps -ef | grep 'oh-my-cpa key exec'` 与 `/proc/<pid>/cmdline`，确认自身命令行参数里没有明文。
- 预期：四处都搜不到；`--verbose` 只增加「用了哪个信封、注入了哪些变量名」，**不打印值**。同用户可读进程环境属已知风险，不算违规，但文档必须写（C-42）。
- 证据：各 `grep` 与退出码；`find` 清单；`--verbose` 全文。

#### C-18 子进程退出码透传，且与 `key exec` 自身失败可区分
- AC：AC-4 · 类型：进程级 · 优先级：P0
- 前置：C-11 状态 C。
- 步骤：
  1. `key exec --runtime claude -- sh -c 'exit 42'; echo "code=$?"`
  2. `... 'exit 0'; echo "code=$?"`
  3. `... 'kill -TERM $$'; echo "code=$?"`
  4. 构造解密失败（C-21 之一）再跑，记录自身失败退出码。
- 预期：42→42、0→0；信号终止按方案约定（Go 常见 128+15=143）。自身失败用**不与子进程冲突**的退出码（方案要定，典型 1/2），不能把「exec 失败」混成「子进程失败」。
- 证据：四次 `code=`。

#### C-19 三运行时各自注入正确的变量名
- AC：AC-4 · 类型：进程级 · 优先级：P0
- 前置：同一信封；方案写明三家变量名。
- 步骤：
  1. `key exec --runtime claude -- sh -c 'env | grep -E "ANTHROPIC_"'`
  2. `key exec --runtime codex -- sh -c 'env | grep -E "CODEX_|OPENAI_"'`
  3. `key exec --runtime opencode -- sh -c 'env | grep -E "OPENCODE_"'`
  4. `key exec --runtime nope -- true; echo "exit=$?"`
- 预期：每家只出现方案声明的那组变量（值打码）；不串味（claude 跑时没有 `OPENCODE_*`，反之亦然）；未知运行时退出码非 0、可读错误、列出可选值。
- 证据：三段过滤输出；变量名与方案表格逐字对照。

#### C-20 `key exec` 不落盘：运行前后文件系统对比
- AC：AC-4 · 类型：进程级 · 优先级：P0
- 前置：C-11 状态 C；`$TMPROOT` 与临时 HOME 的快照。
- 步骤：
  1. `find "$TMPROOT" -type f -printf '%p %s\n' | sort > "$TMPROOT/before"`；临时 HOME 下的 `~/.claude`、`~/.codex`、`~/.config/opencode` 一并列入。
  2. 跑 `key exec --runtime claude -- true`。
  3. 同法生成 `after`，`diff before after`。
- 预期：只有方案允许的可解释变化（如 exec 记一次时间戳，且不含明文）；**没有新出现含明文或解出的文件**。
- 证据：`diff` 输出。

#### C-21 错误路径：无信封、错仓库、私钥与信封不匹配、信封损坏、私钥损坏
- AC：AC-4 · 类型：进程级 · 优先级：P0
- 前置：分别构造环境。
- 步骤：
  1. 只有私钥没有信封：`key exec --runtime claude -- true`。
  2. 有信封 A、私钥 A，但 `--repo B`（若方案支持显式 repo）。
  3. 用仓库 B 的私钥、仓库 A 的信封（手工挪文件）。
  4. 信封 JSON 非法 / `alg` 改值 / `v` 改 99。
  5. 私钥文件替换成 31 字节。
- 预期：每种都是可读错误——说清哪里缺、下一步做什么（例：先 `repo_key_ciphertext` 取信封，或 `key purge` 后重新申请）；退出码非 0 且能区分（至少「没信封」≠「解不开」）；错误信息不带明文、不带私钥/密文全文（可带掩码与文件名）。
- 证据：五种输出与退出码。

#### C-22 `--` 分隔符语义与子进程参数透传
- AC：AC-4 · 类型：进程级 · 优先级：P1
- 前置：C-11 状态 C。
- 步骤：
  1. `key exec --runtime claude -- printf '%s\n' 'a b' '--flag'`
  2. `key exec --runtime claude echo`（缺 `--`）
- 预期：`--` 之后原样交给子进程，参数里的空格与 `--flag` 不被 `key exec` 吃掉；缺 `--` 时按方案要么报错、要么执行并提示，不能静默改参数。
- 证据：输出逐行对照。

#### C-23 信封 0600 与「谁写盘」的责任边界
- AC：AC-4、AC-2 · 类型：进程级 · 优先级：P1
- 前置：C-11 状态 C；方案写明信封由 agent/脚本写、还是由 key 命令代取。
- 步骤：
  1. 按文档给的方法写信封（若工具提供 `key envelope --file`，改用它）；看文档是否给 0600 的写法。
  2. `stat` 私钥与信封。
  3. 信封权限改成 644 再跑 `key exec`，记录是否拒绝或警告。
- 预期：方案说清责任；两者 0600；**读到权限过宽的私钥/信封至少警告一次**（方案规定拒绝则按拒绝判）。
- 证据：`stat`、警告文案（或「方案未要求，记范围外观察」）。

#### C-24 并发与重复执行
- AC：AC-4 · 类型：进程级 · 优先级：P2
- 前置：C-11 状态 C。
- 步骤：`for i in 1 2 3 4 5; do key exec --runtime claude -- true & done; wait; echo "exit=$?"`
- 预期：五次都成功（或按方案给「忙」的可读错误）；没有读到半个信封、没有把临时文件写坏；退出码一致。
- 证据：五次退出码与 stderr。

#### C-25 明文不外泄：日志面统一扫描
- AC：AC-4 · 类型：进程级 · 优先级：P0
- 前置：跑完 C-16～C-21 之后。
- 步骤：只在本次生成的范围内扫：`grep -RF "$SECRET" "$TMPROOT"`；若 OMC 服务日志与 key 命令共用目录，一并扫。
- 预期：无匹配。**别扫真实用户目录并把它当证据**。
- 证据：扫描范围与结果。

### AC-5 `key install`

#### C-26 首次 install：写前备份 + 警告
- AC：AC-5 · 类型：进程级 · 优先级：P0
- 前置：临时 HOME 下预置「既有配置」（`~/.claude/settings.json` 里已有别的 `env` 项）；C-11 状态 C。
- 步骤：
  1. 记录目标文件原内容与哈希。
  2. `key install --runtime claude`，捕获 stdout/stderr。
  3. 找备份文件（命名以方案为准，例：`settings.json.bak.<时间戳>`）。
  4. 对比目标文件。
- 预期：
  - 写前有**备份**，内容与原文件逐字节相同；位置与命名方案写明。
  - 有**警告**：明文落盘、文件可能被提交/同步、泄漏后果、`key purge` 用法。
  - 目标文件只增加本工具负责的项（claude：`env.ANTHROPIC_BASE_URL`、`env.ANTHROPIC_AUTH_TOKEN`）；原有键不动（JSON 若重排，语义等价且方案允许）。
  - 退出码 0。
- 证据：前后文件（token 打码）、备份 diff、警告原文。

#### C-27 重复 install 幂等
- AC：AC-5 · 类型：进程级 · 优先级：P0
- 前置：C-26 之后。
- 步骤：
  1. 第二次 `key install --runtime claude`。
  2. 比较两次后的文件（除备份文件名时间戳外应无差异）。
  3. 数备份文件个数。
- 预期：不重复追加、不出现同名双键；文件语义不变；备份不无限堆（方案若允许堆，要写上限）。
- 证据：两次文件 diff；备份目录 `ls -la`。

#### C-28 只改自己要写的那一项，不动别人配置（三家分别验）
- AC：AC-5 · 类型：进程级 · 优先级：P0
- 前置：临时 HOME 下三家各有一份有真实内容的配置：claude `settings.json` 带 `permissions`/`model`/其他 env；codex `config.toml` 带一个 `[model_providers.other]` 与别的段；opencode `opencode.json` 带 `provider.cpa*` 与 `model`。
- 步骤：逐家 `key install --runtime <r>`，diff 原文件。
- 预期：
  - codex：只加/改方案声明的那一个 provider 段，别的段语义等价（TOML 库会丢注释；若方案允许，报告里注明）。
  - opencode：只加/改 `provider.<id>.options.apiKey`（与 `baseURL`，若方案要）；`provider.cpa*` 不动；用 v1 单数 schema（调研实测 v2 键被忽略）。
  - claude：`env` 里只动本工具两个键；**项目级 `.claude/settings.json`（会提交）不被写**。
  - 三家都先备份。
- 证据：三份 diff（值打码）。

#### C-29 目标文件缺失 / 损坏 / 目录不存在
- AC：AC-5 · 类型：进程级 · 优先级：P1
- 前置：`~/.claude/settings.json` 不存在；`~/.codex/config.toml` 是非法 TOML；`~/.config/opencode` 目录不存在，另一处 `opencode.json` 非法 JSON。
- 步骤：逐家 install。
- 预期：文件不存在 → 按方案创建（权限方案要定）或明确报错让用户先建；文件损坏 → **不覆盖**、可读错误、原文件逐字节不动；目录不存在 → 创建或报错；方案没写的记范围外观察。
- 证据：每种退出码、文件哈希前后对比。

#### C-30 install 后的配置能被该运行时读到（只读探测）
- AC：AC-5 · 类型：进程级 · 优先级：P1
- 前置：临时 HOME；本机装了 claude / codex / opencode（调研时版本 claude 2.1.322、codex-cli 0.167.1、opencode 1.19.5，实际以 `--version` 为准）。
- 步骤（**只读探测，不登录、不打真实上游**）：
  1. claude：按官方优先级核对 `env` 两个键会被读取（可用 `claude --help` 支持的 settings 相关开关，不发起请求）。
  2. codex：`codex debug models` 一类只读命令，确认 provider 段被加载。
  3. opencode：`opencode debug config`，确认 `provider.<id>.options` 出现在输出里。
- 预期：三家配置被真实运行时识别（输出里出现新 provider/env）；**不做真实登录、不发真实请求**。探测命令不存在或需网络时如实记「本机探测不可行」，不算通过也不算红。
- 证据：命令与输出（值打码）。

### AC-6 `key purge`

#### C-31 purge 清掉私钥、信封、状态记录
- AC：AC-6 · 类型：进程级 · 优先级：P0
- 前置：C-11 状态 C 之外再放一个「别的仓库」的 key（验隔离）。
- 步骤：
  1. `find "$OMC_HOME" -type f | sort` 记录。
  2. `key purge --repo example/fixture-repo`，记录输出。
  3. 再 `find`。
  4. `key status`。
- 预期：本仓库的私钥、信封、（若方案有）本地状态记录都被清掉；`key status` 回到「无 key」；输出说明清了哪些路径；退出码 0。**别的仓库文件逐字节保留**。
- 证据：前后 `find` 清单、status 输出。

#### C-32 purge 清掉 install 写过的配置项
- AC：AC-6 · 类型：进程级 · 优先级：P0
- 前置：C-26～C-28 已对三家 install。
- 步骤：逐家 `key purge --repo <repo>`；diff 配置；看备份是被用于恢复还是留在原处。
- 预期：install 写进的那一项被移除（claude 的 `env` 两键、codex 的 provider 段、opencode 的 `options.apiKey`）；其它配置与 install 前语义一致；方案要说明「恢复原备份」还是「精确删掉自己加的项」，二者都算通过但必须与方案一致。
- 证据：三份最终 diff（与 install 前基线对比）。

#### C-33 purge 后同一 `exec` 失败，错误可读
- AC：AC-6 · 类型：进程级 · 优先级：P0
- 前置：C-31 之后。
- 步骤：
  1. `key exec --runtime claude -- true; echo "exit=$?"`
  2. 读错误文案。
  3. 再 `key purge --repo <同一仓库>`（重复 purge）。
- 预期：exit 非 0；错误明确说「该仓库没有私钥/信封，先 `key gen` 并按文档申请」；重复 purge 幂等（0 或明确「本来就没有」），不报成失败。**远端网关 key 在远端仍可用**——purge 是本地清理不是撤销（需求原文只要求本地清理 + exec 失败）；若方案规定 purge 也调停用，按方案判，报告写明。
- 证据：两次输出与退出码；远端可用性可用 C-36 的已解明文另打一次网关（不打印值）佐证。

#### C-34 单测：`key purge` 的影响面
- AC：AC-8、AC-6 · 类型：单测 · 优先级：P0
- 前置：方案给出的单测。
- 步骤：
  1. `go test -count=1 -v -run 'Test.*Purge' ./internal/keyclient/...`
  2. 读测试：覆盖「只删本仓库」「保留他人文件」「配置项移除」「重复 purge」「中途失败不留半截」。
- 预期：覆盖上述分支且通过；单测不依赖真实 HOME。
- 证据：`-v` 输出；源码行号。

#### C-35 purge 的路径安全与失败语义
- AC：AC-6 · 类型：进程级 · 优先级：P1
- 前置：`$OMC_HOME` 里有「别人的」私钥与同名但内容不同的文件。
- 步骤：
  1. `key purge --repo '../../../etc/passwd'`、`--repo ''`。
  2. 让目标私钥不可写（`chmod 400`）再 purge。
  3. `find "$TMPROOT" -newer "$TMPROOT/marker"`，确认 `$OMC_HOME` 之外无删除。
- 预期：非法 repo 被拒绝且不删任何文件；不可写时给可读错误，已删部分要说明（方案若要求事务性，按方案判）；绝不删 `$OMC_HOME` 之外的东西。
- 证据：命令、退出码、`find` 前后对比。

### AC-7 端到端

#### C-36 e2e 全链路：keygen → apply → 审批 → ciphertext → exec 掩码脚本 → 打一次网关成功 → purge 后 exec 失败
- AC：AC-7 · 类型：e2e · 优先级：P0
- 前置：
  - fake CPA + OMC 二进制；临时 `OMC_HOME` 与 HOME；`ASSISTANT_NO_SEND=1`。
  - 夹具仓库名，如 `lambdatheory/fixture-repo-e2e`。
  - 审批由夹具代操作员批（见「审批这一步谁来点」）。
- 步骤：
  1. `key gen --repo <repo>` → 取公钥。
  2. MCP `repo_key_apply {repo, purpose, public_key}`，`X-Omc-Agent` / `X-Omc-Device` 用夹具身份；记 `application_id` 与状态。
  3. 夹具代批：`POST /omc/api/v1/management/repo-keys/applications/<id>/approve`；MCP `repo_key_status` 应为 `approved`、`ciphertext_ready=true`。
  4. MCP `repo_key_ciphertext {application_id}` 取信封，原样写 `$OMC_HOME/envelopes/<repo>.json`（0600）。
  5. `key status` → 三样都在。
  6. `key exec --runtime claude -- sh -c 'echo "base_url_set=${ANTHROPIC_BASE_URL:+yes} token_len=${#ANTHROPIC_AUTH_TOKEN}"'` → 只打印掩码/长度。
  7. 用同一明文（测试进程内存里单独解密得到，仅用于发请求）打网关：`curl -s -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $VALUE" <fake CPA>/v1/models` → 200；不带 key → 401。
  8. `key purge --repo <repo>`。
  9. 同一第 6 步命令再跑 → 非 0 且可读错误。
- 预期：九步一条不缺；第 7 步 200；第 9 步断言的是「exec 失败」而不是「网关失败」——**别写成 401**（purge 不撤销远端绑定）。
- 证据：脚本（建议落 `internal/keyclient/e2e/`，S7/S8 提供）与 `runs/<时间戳>/results.json`；报告贴关键片段。自动化脚本必须一条命令可重跑，入口写进文档。

#### C-37 同一链路的人工走查版本（审批由人在浏览器点）
- AC：AC-7 · 类型：人工检查 · 优先级：P1
- 前置：C-36 的环境，但审批不代批；拿 `approval_url`。
- 步骤：按 C-36 第 1～4 步，第 3 步改为浏览器打开 `approval_url` 点「批准这次申请」，截图待批与批准后；继续第 5～9 步。
- 预期：页面可打开、按钮可用、批准后状态与自动化一致；截图留 `inbox/agents/验收/2026-11-17-omc-repo-key-client/screenshots/`。**不能拿自动化代批冒充这条。**
- 证据：两张截图 + 终端记录。

#### C-38 免审复用路径：已批 caller 再申请不等人，exec 仍能解
- AC：AC-7、AC-4 · 类型：e2e · 优先级：P1
- 前置：C-36 已批过同一 agent/device 与公钥（先别 purge）。
- 步骤：
  1. 同一 caller 再 `repo_key_apply` → 期望 `status=approved`、`kind=reuse`（既有行为，见 `internal/operations/repo_keys.go` 的 auto-approve 判定）。
  2. 重新取 `repo_key_ciphertext` 覆盖信封。
  3. `key exec --runtime claude -- true`。
- 预期：不经人工审批；解出的明文与第一次相同（一仓一把 active key）；信封可重复取。
- 证据：两次 apply 状态；两次明文比对结论（只记相同/不同）。

#### C-39 e2e 清场
- AC：AC-7 · 类型：e2e · 优先级：P1
- 步骤：脚本结束时核对：临时 HOME、`OMC_HOME`、fake CPA 端口、OMC 进程都清掉；`git status` 干净（除本需求的 `docs/delivery/...` 与 e2e 夹具）。
- 预期：不留后台进程；不写仓库内临时文件（`tmp/`、`tmp-*` 已被 `.gitignore` 覆盖）。

### AC-9 明文不外泄与文档

#### C-40 明文不外泄：日志、错误、`--verbose`、shell history 逐条检查
- AC：AC-9、AC-4 · 类型：人工检查 · 优先级：P0
- 前置：C-16～C-21、C-36 的产物。
- 步骤与判定：
  1. **日志**：`grep -RF "$SECRET" "$TMPROOT"`（只扫临时目录与本次日志）；期望无匹配。
  2. **错误信息**：C-21 五种错误逐条看全文，确认不含明文、不含完整私钥/密文（可含掩码与路径）。
  3. **`--verbose`**：C-17 第 2 步全文，确认只写变量名不写值。
  4. **shell history**：在 `history` 或 `~/.bash_history` 副本里搜 `SECRET` 与 `ANTHROPIC_AUTH_TOKEN=`。用例要求命令从不把明文放参数或 `echo` 里；发现文档示例写了 `echo $ANTHROPIC_AUTH_TOKEN` 判「范围内要修」（文档）。
  5. **对话与 git**：`git status`、`git log -p` 抽样搜夹具路径，确认 e2e 产物不进 git；截图不含明文。
- 预期：五条都给出「查了哪里、怎么查、结果」；有一处不符合就写清复现步骤。
- 证据：每条命令与输出。

#### C-41 文档：agent 从零照做能走通
- AC：AC-9 · 类型：人工检查 · 优先级：P0
- 前置：干净环境（新的临时 HOME 与 `OMC_HOME`），**只按文档操作**，不看代码、不看本用例。
- 步骤：
  1. 照文档第 1～7 步逐步执行，只照抄命令（文档没写的参数/路径记成「文档缺口」，先不猜，记下再继续）。
  2. 第 6 步（exec）用文档给的掩码脚本，确认输出没有明文。
  3. 第 7 步（install/purge）按文档做一遍并还原。
  4. 统计：哪些命令可直接粘贴运行；哪些必须补 `$OMC_HOME`、server URL、管理 key 之类环境变量（这些应写在文档里）。
- 预期：能走通；走不通的每一处都是「范围内要修」（文档属 AC-9 交付物）。文档必须写明命令名、参数、`$OMC_HOME` 默认值与覆盖方式、三家运行时、审批在哪一步、失败怎么办。
- 证据：逐步记录（命令 + 输出 + 卡点），截图或终端粘贴。

#### C-42 文档：禁止事项齐备且具体
- AC：AC-9 · 类型：人工检查 · 优先级：P0
- 前置：C-41 的文档。
- 步骤：逐条核对是否写了、写清后果与替代做法：
  1. 不进日志（含 agent 的 transcript 与会话记录）。
  2. 不进对话（不 `cat`、不 `echo`、不贴进聊天/工单/PR）。
  3. 不进 git（不提交 `$OMC_HOME`、不提交任何含明文的配置；项目级 `.claude/settings.json` 会被提交，只能写用户级）。
  4. 不提交配置（`install` 必须显式、结果不要提交；给出 `purge` 与方法）。
  5. 已知风险如实写：Linux 同用户可读 `/proc/<pid>/environ`；Windows 内存转储含环境块；私钥丢失=信封作废、需重新登记公钥并人工确认；泄露=停用整仓。
- 预期：五条都在，不能只写一句「注意保密」。缺任一条判「范围内要修」。
- 证据：逐条引用文档原文行号。

#### C-43 文档：能力索引与架构/安装文档按契约同步
- AC：AC-9 · 类型：人工检查 · 优先级：P1
- 前置：拆分 S5 的产物。
- 步骤：
  1. 读 `docs/agent-capabilities.md`「Repository-owned API keys」一节（基线在 `11-17-t5-omc-dev-base:docs/agent-capabilities.md` 的同一标题下），确认新增了指向使用说明的指针，而不是复制算法细节。
  2. 读 `docs/architecture.md`、安装/测试文档（按 `AGENTS.md` §2）中被改动的段落。
  3. `pnpm check-docs`、`pnpm test:docs`。
- 预期：指针可点开、命令可复制；文档检查通过；没有把功能说成默认开启（exec 默认、install 显式）。
- 证据：三处文档引用 + 两条命令输出。

#### C-44 文档命令面与 `--help` 逐字一致
- AC：AC-9 · 类型：人工检查 · 优先级：P2
- 前置：C-01 与 C-41。
- 步骤：把文档里出现的每条命令拷进 `--help` 对照，逐字比对子命令名与参数名。
- 预期：零差异；差异即「范围内要修」（文档或帮助文案二选一改，按方案）。
- 证据：对照表。

## 待方案核对（impl-plan 定稿后逐条对齐）

拆分单里「待方案定的设计点」；用例留了分支，**方案定稿后必须先核这些再开测**：

| 事项 | 用例 | 方案必须给出 |
|---|---|---|
| 工具形态与命令名、参数（`key` 子命令族 vs 独立工具；`--repo`、`--json`、`--verbose`、`--force` 是否存在） | C-01、C-07、C-12、C-22 | 命令面表 |
| `$OMC_HOME` 默认值与覆盖（环境变量名、Windows 路径） | 全部 | 解析规则 |
| 仓库身份 → 文件名映射（含 `/` 与 `..` 防护） | C-06、C-35 | 映射规则 + 校验 |
| 信封由谁写盘、0600 怎么保证、是否提供取信封命令 | C-23、C-36 | 责任边界 |
| base URL 来源（信封里没有）：从哪读、缺了怎么办 | C-16、C-36 | 来源与优先级 |
| `key status` 的审批状态读本地还是走 MCP、离线行为与超时 | C-14 | 来源 + 超时 |
| codex / opencode 的确切变量名与 install 落点、备份命名与保留策略 | C-19、C-26、C-27、C-28、C-30 | 三家落点表 |
| `key exec` 自身失败时的退出码约定（与子进程区分） | C-18 | 退出码表 |
| 重复 `key gen` 是覆盖还是拒绝 | C-07 | 明确选一 |
| purge 是否同时撤销远端绑定 | C-33 | 明确（需求只要求本地） |
| 单测包路径（本例按 `internal/keyclient/...` 写） | C-10、C-13、C-34 | 文件归属 |

核完后只改本文件对应条目（只改判定，不扩范围），再交开发与跟进 Agent。

## 不可自动化的项

| 用例 | 为什么不能自动化 |
|---|---|
| C-37（人工审批走查） | 审批是人的动作；夹具代批只能证明链路通，不能冒充人工审批的走查 |
| C-41（文档照做） | 要人第一次照文档走，卡的正是「文档没写清楚」这类判断 |
| C-42、C-43、C-44（文档内容） | 「写得够不够明白」「指针对不对」只能人读 |
| C-40 第 4 条（shell history） | 依赖交互式 shell 的历史文件，脚本化只能做半自动扫描 |
| C-30（真实运行时识别） | 需要本机装三家 CLI；只读探测可脚本化，但「是否被识别」的结论要人看 |

## 本机跑不了的项与原因

| 项 | 原因 | 替代做法 |
|---|---|---|
| Go 单测（C-10、C-13、C-34）与二进制构建 | 本机（Windows）**没有 `go`** | Docker `golang:1.27.1` + 现成模块缓存卷（配方见「测试环境」与 memory `omc-go-gate-docker-recipe`） |
| 私钥 0600 的真实断言（C-05） | Windows 的 `os.Chmod` 只切只读位，`stat` 反映不出 600；Docker Desktop bind mount 会照抄 NTFS ACL | 容器里用原生卷（`-v tmpvol:/keys`）断言 `stat -c %a`=600；Windows 侧查源码 mode 与 ACL |
| 真实运行时全量联调（C-30 打真实网关部分） | 需要真实账号与生产端点 | 只做只读配置探测；真实请求用 fake CPA 与夹具 key 覆盖（C-36） |
| 审批页面真实浏览器截图（C-37） | 要起 OMC + 前端 Vite（`pnpm dev`），耗时且浏览器工具可能被沙箱拦 | 本机可跑则跑并截图；打不开如实记缺口（最多试三次），**不写成通过** |
| Mac 构建 / arm64 相关 | 本需求不涉及；`memory/long/2026-11-16-windows-cannot-run-arm64` 只影响构建 | 不验 |

## 不测的

- 申请 / 审批 / 交付协议本身（`repo_key_apply` / `repo_key_status` / `repo_key_ciphertext` 的语义与审批页面）：既有 repo-key 需求范围，本需求明确不改协议、不动控制台页面；只用 C-03、C-36 反证没改坏。
- 按 agent 拆 key、自动过期、飞书审批：需求明确列为后续，不在 AC。
- 系统钥匙串接入：默认原则明确不做。
- 生产部署、Dokploy、真实 prebuilt：红线，不在验收范围。
- 像素级视觉走查：本需求没有新界面（审批页面复用既有）。
