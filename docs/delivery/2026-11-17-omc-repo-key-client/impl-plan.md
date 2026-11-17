---
type: impl-plan
slug: 2026-11-17-omc-repo-key-client
status: draft
updated: 2026-11-17
prd: 无独立 prd.md；需求原文见 E:/works/Homan-context/runs/msgs/2026-11-17-2026-11-17-T5-0.md，AC-1～9 见 T5 运行记录「拆分」节末
summary: "以 oh-my-cpa key 子命令族补齐使用端七步：本地 0600 密钥与信封、内存解密注入三家运行时、显式 install 与精确 purge；拆 S2a/S2b/S3/S4/S5/S7/S8，AC-1～9 逐条落到测试；Codex 用 -c+env_key 避开 argv 明文，OpenCode 用合并后的 OPENCODE_CONFIG_CONTENT"
---

# 实现计划：分 key 使用端闭环（oh-my-cpa key）

> 依据：浩文 2026-11-17 需求原话（`E:/works/Homan-context/runs/msgs/2026-11-17-2026-11-17-T5-0.md`）、T5 运行记录的「拆分」与 AC-1～9、`E:/works/Homan-context/inbox/agents/交付/2026-11-12-omc-分key-可用性评估.md`（第 3～5 节）、`E:/works/Homan-context/inbox/agents/调研/2026-11-12-omc分key可用性-S2.md`（三家运行时落点与实测）。代码基线为本地分支 `11-17-t5-omc-dev-base`（= `origin/dev` `ba9e2ee7`）；下文提到现有代码时按该基线核对。本稿只写方案，不写产品代码、不跑构建。

## 评审入口

- 先读 §2 的十个设计结论；要拍板的看 §12；要验收的看 §10。
- 最容易被实现细节绑死、需要 PM 先确认的三处：`$OMC_HOME` 默认一份还是每 agent 一份（§2.1、§12 第 1 条）、Codex `exec` 只接受 `codex` 本身（§2.6、§12 第 3 条）、OpenCode 不自动写 `models`（§2.7、§12 第 4 条）。
- 本计划没有改任何产品代码；§7.2 的文件归属是给 S2a/S2b/S3/S4 并行开工用的，不是已经存在的文件。

## 0. 一页结论

- **工具形态**：做成 `oh-my-cpa key` 子命令族，长在 OMC 自己的二进制里，复用 `internal/repokey` 的 `Decrypt` / `ParsePublicKey` / `PublicKeyFingerprint` / `NormalizeRepo` / `RepoDisplay`。`key` 在加载服务配置、数据库和 CPA 客户端之前分发，不需要启动控制台。
- **本地目录**：`$OMC_HOME`，默认 `~/.omc`（Windows `%USERPROFILE%\.omc`），0700；下面放 `keys/`、`envelopes/`、`backups/`、`state.json`、`config.json`、`.lock`。
- **命令面**：`key status` / `key gen` / `key accept` / `key exec` / `key install` / `key purge`。`key accept` 是从 stdin 读 MCP `repo_key_ciphertext` 结果并落信封的显式命令，不是隐藏行为。
- **base URL**：`--base-url` > `OMCPA_CPA_BASE_URL` > `$OMC_HOME/config.json` > 可读错误 `base_url_required`。Claude Code 用根地址，Codex / OpenCode 用根地址加 `/v1`。
- **明文边界**：`exec` 在内存里解密，只把运行时需要的变量交给子进程；`install` 是唯一会把明文写到运行时的配置文件的命令，写前备份并打警告。任何命令的 stdout/stderr、错误、状态文件都不出现明文。
- **三家 install**：Claude Code 写用户级 `~/.claude/settings.json` 的 `env`；Codex 写 `~/.codex/config.toml` 的受管 `[model_providers.oh-my-cpa]` 加顶层 `model_provider`；OpenCode 写 `~/.config/opencode/opencode.json` 的 `provider.oh-my-cpa.options`（v1 单数）。不新增 Go 依赖。
- **purge**：默认清私钥、信封、该仓库的全部 install 值和 state 记录；`--runtime` 只清一个运行时。备份默认保留，但删除包含本仓库明文的备份。purge 只清本机，不停用 CPA 里的 key，输出会明说。
- **测试**：Go 单测覆盖 status / purge / 权限 / 三家注入 / 三家 install；e2e 用 fake CPA + 真实 OMC 二进制 + MCP apply/审批/ciphertext + 掩码 helper + 用 key 打 fake CPA + purge 后失败。Unix 0600 在 Docker/Linux 断言；Windows 用 ACL 断言，不做 `Mode().Perm()==0600` 的必红断言。
- **切片**：S2 拆成 S2a（home/store/gen）与 S2b（accept/status/remote），S3/S4 从 S2a 起可并行；S4 估 110 分钟，建议拆 S4a（install）/S4b（purge）。S5 文档、S7 e2e 夹具、S8 全链路。

## 1. 目标与范围

### 1.1 目标

1. 让一个 agent 只靠 OMC 自己的二进制，从「本机没有私钥」走到「用仓库 key 跑起目标运行时」，再能干净地清掉本地材料。
2. 把「私钥放哪、信封谁写、base URL 从哪来、三家运行时怎么注入、install 写哪个文件、purge 删什么」这些今天只存在于调研里的约定，定成可执行的命令和文件契约。
3. 每个切片能独立验收；S2a 完成后 S3/S4 可以并行，S2b 与它们无文件冲突。
4. 不改变 OMC 现有的申请、审批、交付协议，不动控制台页面，不做按 agent 拆 key、自动过期、飞书审批。

### 1.2 非目标

- 不做独立的 `omc-key` 二进制，不复制一份 `internal/repokey` 的密码学实现。
- 不做 `key apply` / `key approve`；申请仍走 MCP `repo_key_apply`，审批仍走控制台或一次性链接。
- 不做服务端 key 的停用、轮换、重新签发；这些仍走控制台。`key purge` 只清本机材料。
- 不接系统钥匙串，不做密钥同步、导出、导入、备份到云端。
- 不改 `$OMC_HOME` 之外的用户配置格式（只按本计划的受管块/字段写）。
- 不在本轮给 OpenCode 自动生成模型列表，不替用户选模型。

## 2. 十个设计点的结论

### 2.1 `$OMC_HOME` 怎么定

**结论**

- 环境变量名用 `OMC_HOME`，与需求原文一致；不额外读 `OMCPA_HOME`，避免两个名字各写一半。
- 默认 `os.UserHomeDir()` 下的 `.omc`：Unix 是 `~/.omc`，Windows 是 `%USERPROFILE%\.omc`。不用 `os.UserConfigDir()`，因为它在三个平台给出三个不同的位置，和需求里的 `$OMC_HOME` 不稳定对应。
- 目录权限：Unix 0700；Windows 用只含当前用户 SID 的 DACL 并去掉继承。目录不存在时创建；创建失败或无法收紧权限时报 `home_insecure`，不继续写私钥。
- 所有命令都接受 `--home <dir>`；优先级 `--home` > `OMC_HOME` > 默认。相对路径按当前工作目录解析，并在 `key status` 里打印绝对路径。
- 目录里固定这几项：

```text
$OMC_HOME/
  keys/                 # 私钥，0600 / 当前用户 ACL
  envelopes/            # 交付信封（密文），0600 / 当前用户 ACL
  backups/              # install 前的配置文件副本，0600 / 当前用户 ACL
  state.json            # 仓库、申请、信封、install 槽位的本地状态，0600
  config.json           # base URL，0600
  .lock                 # 写操作的短锁
```

**理由**：需求已经把路径写成 `$OMC_HOME/keys` 与 `$OMC_HOME/envelopes`；`~/.omc` 和 `.claude`、`.codex`、`.config/opencode` 的放置习惯一致，跨平台可预测。一个名字、一个默认值，排错时不用猜用户设了哪个。

**备选**：用 `OMCPA_HOME` 跟服务端 `OMCPA_*` 前缀统一。代价是与需求原文不一致，且这个变量是客户端本地状态，不是部署配置；如果 PM 更看重前缀统一，必须在 S2 开工前改成一个常量，不能两个都读。

**每个 agent 各一对密钥**：默认 home 是「每个操作系统用户一份」，同一用户下的两个 agent 会共用同一对密钥。需求说「每个 agent 各一对密钥（现有设计，不动）」，因此运行手册给出约定：需要独立密钥对的 agent 设置自己的 `OMC_HOME`，例如 `~/.omc/claude`、`~/.omc/codex`。不自动按 `OMCPA_AGENT_NAME` 推导 home，原因见 §12 第 1 条。

### 2.2 base URL 从哪来

**结论**

解析顺序固定为：

1. `--base-url <url>`（`key gen`、`key exec`、`key install` 接受）
2. `OMCPA_CPA_BASE_URL` 环境变量
3. `$OMC_HOME/config.json` 的 `base_url`
4. 都没有：`base_url_required`（退出码 3），错误里给出三种设置方式和示例，不猜。

带 `--base-url` 的命令成功后，把值写进 `config.json`（0600，原子写）。环境变量和 `config.json` 里的值不会被隐式改写。`key gen` 是推荐的一次性设置入口。

**规范化**

- 只接受 `http` / `https`，必须有 host；拒绝 userinfo、query、fragment。
- 去掉末尾的 `/` 和末尾一个 `/v1` 后存「网关根」；如果末尾是 `/v1/messages`、`/v1/responses` 这种具体端点，报 `base_url_invalid`，不替用户截断。
- 非 loopback 的 `http://` 允许但打 `insecure_base_url` 警告：Docker 私有网络里的 `http://cli-proxy-api:8317` 是常见部署；真正的远端明文传输由使用者自己判断。
- 按运行时派生：
  - Claude Code：`ANTHROPIC_BASE_URL=<根>`
  - Codex：`base_url=<根>/v1`
  - OpenCode：`baseURL=<根>/v1`

**理由**：信封里没有网关地址，`repo_key_ciphertext` 只给密文和 `key_mask`。部署本身已经在用 `OMCPA_CPA_BASE_URL`，复用它比让 agent 临时猜一个地址可靠；`config.json` 让一次性设置跨 shell 生效。

**备选**：让 agent 自己在环境里设 `ANTHROPIC_BASE_URL`，`exec` 只补 token。更薄，但 `install` 仍要地址，而且三家字段不同，最终还是要一个解析器；不选。

### 2.3 信封落盘归谁、怎么保证 0600

**结论**

新增 `key accept --repo <repo> [--application-id <id>]`：

- 从 stdin 读 MCP `repo_key_ciphertext` 的结果（最多 1 MiB）；也接受裸信封 `{v,alg,epk,nonce,ciphertext}`。
- 如果外层有 `algorithm`，必须等于 `x25519-hkdf-sha256-aes256gcm`；内层必须 `v=1`、`alg` 同名。
- 用本地私钥在内存里解一次；解不开就报 `envelope_key_mismatch`，不写任何文件。这样「把别的仓库/别的 key 的信封存进来」在写盘前就被挡住。
- 校验通过后，把内层信封以紧凑 JSON（0600 / 当前用户 ACL）写到 `$OMC_HOME/envelopes/<safe-repo>.json`，并更新 state：`application_id`、`kind`、`key_mask`、`approval_status=approved`、`envelope_received_at`。
- 不把输入回显到 stdout/stderr；默认只打印一行 `accepted <repo_display>: key <mask>`，`--json` 给机器读。
- 这条命令明确写进 AC 映射和用例，不做成隐藏行为；运行手册把「用 `key accept`，不要用 shell 重定向」写成规则。

**理由**：agent 用 shell 重定向写盘时，`0600` 取决于 shell 的 umask，Windows 上根本没有 `0600` 语义；把密文回显进命令也容易进 transcript。`key accept` 是一个地方做校验、权限、状态更新，还能顺手证明信封与私钥匹配。

**备选**：让 agent 自己 `cat > $OMC_HOME/envelopes/...`。路径符合需求，但拿不到权限保证，也没有解密校验；不选。另一个备选是 `key accept --remote` 让 CLI 直接调 MCP 取密文，agent 完全不碰密文；这需要 CLI 持有管理密钥，见 §12 第 2 条，本轮不做。

### 2.4 `key status` 的「最后一次审批状态」从哪读

**结论**

- 默认**完全离线**：读 `state.json` 和文件存在性，不持管理密钥，不发网络请求。
- state 由这些命令维护：
  - `key gen`：登记仓库、私钥、公钥、指纹、`approval_status=not_applied`。
  - `key status --repo <repo> --application-id <id>`：只把申请 id 关联到仓库，状态记为 `unknown`（给「MCP 已经申请、还没批」用）。`--application-id` 需要 `--repo`，除非 state 里已经有这个 id 的记录。
  - `key accept`：记 `approval_status=approved`、`key_mask`、`kind`。
  - `key status --remote`：用 MCP 的读能力刷新已登记的 application 和仓库状态。
- `--remote` 是可选路径：
  - 需要 `OMCPA_SERVER_URL` + `OMCPA_CPA_MANAGEMENT_KEY`（只读 MCP key 也可以放在这个变量里）；缺任意一个报 `remote_not_configured`（退出码 3），不改本地状态。
  - 通过和 `oh-my-cpa mcp` 同一个 capability HTTP 传输调用 `repo_key_status`（按 application id）和 `repo_key_query`（按仓库），复用 `mcpbridge.Bridge` 的鉴权、HTTPS 限制、重定向拒绝和错误码收敛；调用前用 `Bridge.WithCaller` 带上 `OMCPA_AGENT_NAME` / `OMCPA_DEVICE_NAME`，和 `mcp` 一致；不新写一套 HTTP 客户端。
  - 每个仓库独立刷新；某个失败时标 `remote_error:<code>`，其余照常显示，命令退出码 1。
- `key status` 显示每条记录的 `status_source`（`gen` / `local` / `remote` / `accept`），让读者知道这个状态是本地记的还是刚从 OMC 读的。
- `repo_key_query` 的 `state`（`active` / `stopped` / none）单独显示为 `repo_state`，和「这次申请的状态」分开；这样「申请被拒但仓库已有别人的 active key」不会看成同一件事。
- state 丢失时 `key status` 仍按确定性文件名扫描 `keys/` 和 `envelopes/`，报告孤儿文件数量并提示用 `key gen --repo` 重建记录；孤儿不冒充已绑定仓库。

**理由**：AC-3 要「最后一次审批状态」，只靠本地文件在「申请被拒、agent 没走到 accept」时会永远显示未知。`repo_key_status` 是只读能力，调用成本低；把它做成显式 `--remote`，默认路径仍然可以离线工作。

**备选**：只显示本地文件存在性。弱，不满足 AC-3；不选。默认每次 status 都走网络。把本地工具变成网络依赖，离线/内网失败时连本地 key 都看不到；不选。

### 2.5 文件名与仓库身份

**结论**

- 仓库身份用 `repokey.NormalizeRepo`：GitHub 折小写成 `org/repo`，其他 host 保留 `host/path`，本地目录取目录名；显示名用 `repokey.RepoDisplay`，保留用户提交的拼写。
- 文件名规则：`<slug>-<hash8>.key` / `.json`。
  - `slug`：对 repo ID 的 UTF-8 字节做百分号编码，只保留 `A-Za-z0-9._-`；Windows 保留名（`CON`、`PRN`、`AUX`、`NUL`、`COM1`…）前加 `_`；去掉末尾的点和空格；超过 80 字节时按 rune 边界截断。
  - `hash8`：`sha256(repoID)` 的前 8 个十六进制字符。
- state 里存相对文件名（`keys/...`、`envelopes/...`），不靠重新计算来找旧文件；重新计算只用于发现「state 里没有的孤儿文件」。
- 同一台机器、同一个 home 下，同一 repo ID 只有一把 key；同一个仓库的不同 clone 复用同一把。SSH 和 HTTPS 的 GitHub remote 归一成同一个 ID；其他 host 按 `host/path` 区分。

**理由**：repo ID 里有 `/`、`:`、`\` 和大小写差异，直接做文件名在 Windows/NTFS/macOS 上会冲突；`slug + hash` 既可读又不会因大小写不敏感或截断而撞名。state 保存实际文件名，未来改规则也不会找不到旧 key。

**备选**：把 `/` 换成 `__`。`a/b` 和 `a__b` 会撞；不选。只用 hash。安全但不可读，排错时看不出是哪个仓库；不选。

### 2.6 `key exec` 的实现

**结论**

- 签名：`key exec --runtime <claude|codex|opencode> [--repo <repo>] [--base-url <url>] -- <command> [args...]`。
- 仓库解析：`--repo` > 当前目录的 `git remote get-url origin`（没有 origin 取第一个 remote）> 当前目录名；都过 `repokey.NormalizeRepo`。找不到本地 key 时 `key_not_found`（退出码 4），错误里给出推断出的 repo display 和一行可直接复制的 `key gen` 命令。
- 检查顺序：state/repo → 私钥存在 → 信封存在 → base URL 可解析 → 解密 → 注入 → 启动子进程。任一步失败都不启动子进程，也不留下临时文件。
- `exec` 不要求 state 里有记录：私钥和信封按确定性文件名就能找到；state 只用于显示、申请状态和 install 归属。
- 解密：`repokey.Decrypt(privateKey, envelopeJSON)`，明文只保存在一个局部 `string` 和子进程环境里；不写临时文件，不 `fmt` 明文，不放进 argv（Codex 的 token 走 env，见下）。
- 子进程：`exec.Command(argv[0], argv[1:]...)`（不是 `CommandContext`）；`cmd.Env` 由 `os.Environ()` 过滤后追加；`Stdin/Stdout/Stderr` 继承；`cmd.Dir` 保持当前目录；`cmd.Run()` 的退出码原样返回。子进程起不来报 `exec_failed`（退出码 1）。
- 信号：`key exec` 不安装自己的信号处理器，`main.go` 的 `key` 分发也不套 `signal.NotifyContext`。Unix 上父子在同一进程组，Windows 上共享控制台，Ctrl-C 会同时到达两者；如果父进程被单独 SIGTERM，子进程可能继续，运行手册说明这一点。不要用 `CommandContext` 或一个会吞掉 Ctrl-C 的 handler，否则父进程收到 Ctrl-C 后 cancel 会把子进程 SIGKILL。
- 环境构造：先过滤掉运行时的冲突变量，再追加本运行时的变量。过滤不是「顺手清理」，是为了保证子进程真的用仓库 key：
  - `claude`：删除 `ANTHROPIC_API_KEY`、`ANTHROPIC_AUTH_TOKEN`、`ANTHROPIC_BASE_URL`，再设 `ANTHROPIC_BASE_URL=<根>`、`ANTHROPIC_AUTH_TOKEN=<明文>`。如果父环境里本来有 `ANTHROPIC_API_KEY`，stderr 打一句「removed ANTHROPIC_API_KEY for this child」，不打印值。
  - `codex`：要求 `argv[0]` 的 basename 是 `codex` 或 `codex.exe`（大小写不敏感）；设 `OMC_REPO_KEY=<明文>`；在 `codex` 后、用户参数前插入：

```text
-c model_provider="oh-my-cpa"
-c model_providers.oh-my-cpa.name="Oh My CPA"
-c model_providers.oh-my-cpa.base_url="<根>/v1"
-c model_providers.oh-my-cpa.env_key="OMC_REPO_KEY"
-c model_providers.oh-my-cpa.wire_api="responses"
```

  明文只在 `OMC_REPO_KEY` 环境变量里，不在命令行；`env_key` 是 Codex 已实测会读取的字段，`wire_api` 只收 `responses`（`chat` 会被拒绝）。如果命令不是 `codex`，报 `codex_command_required`（退出码 2），提示用 `key install` 或直接 `key exec --runtime codex -- codex ...`。
  - `opencode`：读 `$OPENCODE_CONFIG`（如果设置）或 `~/.config/opencode/opencode.json`；如果父环境已有 `OPENCODE_CONFIG_CONTENT`，先解析它并与文件配置合并（父内容优先），再加 `provider.oh-my-cpa`。把完整配置 marshal 成 JSON 放进子进程的 `OPENCODE_CONFIG_CONTENT`；文件不存在时只有我们这一层。合并后的内容超过 24 KiB 时报 `config_too_large`（退出码 3），提示改用 `key install`。任何一层不是纯 JSON 报 `config_not_json`。不写临时文件，不依赖 `OPENCODE_CONFIG_CONTENT` 与文件配置的合并语义（我们把文件内容完整带上）。
- 任何路径都不打印明文；错误只带 repo display、key mask、fingerprint、runtime 和子进程退出码。

**理由**：三家运行时的注入机制不同，S2 的调研已经实测了各自的落点和坑。Codex 没有「只靠环境变量选 provider」的已验证路径，`-c` 是实测可用的；把 token 放在 `env_key` 指向的环境变量里，避免 `ps` / 进程列表读到 argv。OpenCode 的 `OPENCODE_CONFIG_CONTENT` 是唯一不落盘的进程级入口，把用户配置完整合并进去可以避免「它是否与文件配置合并」的不确定性。

**备选**：三家都用 `install` 写配置。会把明文留在磁盘，违背「exec 是默认路径、install 必须显式」；不选。Codex 直接把 token 放进 `-c` 参数。argv 对其他本地用户和进程列表可见；不选。Codex 用临时 `CODEX_HOME`。会把会话历史也搬到临时目录，交互式使用会丢历史；不选。

### 2.7 `key install` 的落点与写法

**结论**

- 签名：`key install --runtime <claude|codex|opencode> [--repo <repo>] [--base-url <url>] [--config <path>] [--replace]`。
- **Claude Code**：用户级 `~/.claude/settings.json`（尊重 `$CLAUDE_CONFIG_DIR/settings.json`，`--config` 可覆盖）。结构化 JSON 编辑：顶层 `env` 对象里设 `ANTHROPIC_BASE_URL` 和 `ANTHROPIC_AUTH_TOKEN`，其余顶层键和值用 `json.RawMessage` 原样保留。如果 `env.ANTHROPIC_API_KEY` 已存在，报 `config_conflict`，不替用户删；提示先移除它，否则它可能优先于我们写的 token。绝不写项目级 `.claude/settings.json`（会提交）。
- **Codex**：`$CODEX_HOME/config.toml`，没设 `CODEX_HOME` 时 `~/.codex/config.toml`。用受管标记块，只动两块：

```toml
# >>> oh-my-cpa key (managed; `oh-my-cpa key purge --repo <repo>` removes this) >>>
[model_providers.oh-my-cpa]
name = "Oh My CPA (org/repo)"
base_url = "http://127.0.0.1:8317/v1"
wire_api = "responses"
experimental_bearer_token = "sk-cpa-..."
# <<< oh-my-cpa key <<<
```

  顶部再维护一行 `model_provider = "oh-my-cpa"`（必须在第一个表头之前）。写前先删旧的完整受管块；有开始标记没有结束标记、或存在非受管的 `[model_providers.oh-my-cpa]` 表，都报 `config_conflict` 并停止。扫描遇到多行字符串（`'''` / `"""`）或无法确定顶层边界时同样拒绝，不猜。替换 `model_provider` 时把原值记进 state，purge 时如果当前值仍是我们的，就恢复原值；如果原值不存在就删掉这一行。
- **OpenCode**：`~/.config/opencode/opencode.json`（尊重 `$OPENCODE_CONFIG`，`--config` 可覆盖）。结构化 JSON 编辑：`provider["oh-my-cpa"] = {npm:"@ai-sdk/openai-compatible", name:"Oh My CPA (org/repo)", options:{baseURL:"<根>/v1", apiKey:"<明文>"}}`，保留其他 provider 和未知键。用 v1 单数 `provider`，不猜 v2 的 `providers`。不自动写 `models` 映射（见 §11）。
- **所有 install**：写前把目标文件复制到 `$OMC_HOME/backups/<UTC 时间戳>-<路径压平>.json|toml`，0600/当前用户 ACL；备份失败就不写。写前在 stderr 打印警告：目标路径、明文会留在磁盘、备份路径、如何 purge。写用同目录临时文件 + fsync + 权限收紧 + rename，失败保留原文件。
- install 也需要私钥和信封：缺私钥报 `key_not_found`，缺信封报 `envelope_missing`，解不开报 `decrypt_failed`；任一失败都不写配置、不建备份。
- **所有权**：state 的 `runtimes.<runtime>` 记录 `repo`、`config_path`、`backup_path`、`value_fingerprint`（明文 key 的 SHA-256，带 `sha256:` 前缀）和 `backup_contains_key`（这次备份里是否有当前 key 的明文；重装同一把 key 时为 true）。目标槽位已有值且指纹不在 state 里（或属于别的仓库）时，必须显式 `--replace`；否则 `config_conflict`，不写。
- Claude Code 和 OpenCode 各只有一个受管槽位；Codex 也只有一个 `oh-my-cpa` provider。安装另一个仓库会替换槽位，旧仓库的值在备份里，state 只记录当前槽位归属。

**理由**：仓库只有 `gopkg.in/yaml.v3`，没有 TOML 库，也不能新增依赖；JSON 用结构化编辑可以保留未知键，TOML 用自包含的受管块可以精确删除而不重写别人的配置。Claude Code 的项目级配置会提交，必须禁用；Codex 的 `wire_api` 只接受 `responses`，`experimental_bearer_token` 是唯一能在重启后仍然生效的持久化形式，代价是明文在 config.toml，警告里说清楚。

**备选**：引入 TOML 库做完整解析/序列化。违反「不新增 Go 依赖」；不选。Codex 用 `env_key` + 让用户自己设环境变量。`install` 就不是「长期生效」，还得改 shell 配置；不选。OpenCode 写 v2 `providers`。当前实测 v1.19.5 只认 v1；不猜；不选。

### 2.8 `key purge` 的范围

**结论**

- 签名：`key purge --repo <repo> [--runtime <runtime>] [--dry-run]`。
- 默认（不带 `--runtime`）：按顺序清该仓库在所有运行时的 install 值、私钥、信封、state 记录。
- 带 `--runtime`：只清该运行时的值、该运行时槽位和 state 里对应的 install 记录；私钥、信封、仓库记录保留。
- 只删「指纹匹配」的值：解密出本仓库明文，算 `value_fingerprint`，与配置文件里的当前值比对；不一致说明别人改过，报 `install_not_owned`，不动它，退出码 1。文件不存在或值已经不存在算成功（幂等）。
- state 记录最后删：只有私钥、信封、所有属于本仓库的 install 槽位都清掉后才删；有没清掉的，保留记录并打印 `purge_pending`，方便重跑。
- 备份：安装时保留；purge 时扫描 `$OMC_HOME/backups/`，只删除内容里包含本仓库明文 key 的备份（逐字节精确匹配），其余保留并打印路径。备份不可读时保留并报告。
- purge 一开始就读 state；私钥还在时解密一次，把明文只留在本次调用的内存里，供安装值比对和备份扫描使用；私钥已经不在时用 state 的 `value_fingerprint` 和 `backup_contains_key`，无法扫描的备份保留并报 `backup_scan_unavailable`。因此「删私钥」排在安装值/备份清理之后，不先删私钥再找备份。
- 输出必须有一句：`本地材料已清除；CPA 里的这把 key 仍然有效，要停用它请到控制台停用/重新签发。`
- `--dry-run` 打印将删除/保留的清单，不改任何文件。

**理由**：purge 是本地清理，不是服务端吊销；不精确匹配就会误删别的仓库或用户自己后来写的值。备份是 install 覆盖前的唯一恢复点，不能一律删；但含本仓库明文的备份必须删，否则「清掉私钥」名不副实。

**备选**：purge 删除所有备份。会破坏别的配置的恢复点；不选。purge 自动恢复备份。会覆盖用户在 install 之后做的无关修改；不选。purge 直接停用 CPA 的 key。超出本机工具的范围，需要管理密钥和服务端写权限；不选。

### 2.9 测试与 e2e 方案

见 §8。结论先说：单测和进程级测试放在 internal/keyclient，命令面测试放在 `cmd/oh-my-cpa`；install 用临时目录和 `--config`，不碰真实用户配置；e2e 放 internal/keyclient/e2e，用 `//go:build e2e` 的独立入口，默认 `go test ./...` 不跑。Unix 权限在 Docker/Linux 断言；Windows 用 ACL 断言，不用 `Mode().Perm()`。

### 2.10 AC 映射

见 §10。AC-1～9 每条都有切片、测试/证据、缺口三列；AC-7 的真实 CPA 网关部分如实标为人工验证缺口，不用 fake CPA 冒充。

## 3. 命令面

### 3.1 总览

| 命令 | 作用 | 是否会写盘 | 是否会联网 | 主要参数 |
|---|---|---|---|---|
| `key status` | 看本机私钥、绑过的仓库、最后一次审批状态、install 槽位 | 否（`--remote` 会更新 state） | 仅 `--remote` | `--repo`、`--application-id`、`--remote`、`--json`、`--home` |
| `key gen` | 生成或复用 X25519 密钥对，只打印公钥 | 私钥、state、可选 config | 否 | `--repo`、`--base-url`、`--home` |
| `key accept` | 从 stdin 读 `repo_key_ciphertext` 结果，校验后落信封 | 信封、state | 否 | `--repo`、`--application-id`、`--json`、`--home` |
| `key exec` | 内存解密，只给子进程注入运行时变量 | 否 | 子进程自己决定 | `--runtime`、`--repo`、`--base-url`、`--home`、`-- <command>` |
| `key install` | 把 key 写进运行时的配置文件，写前备份 | 运行时配置、备份、state | 否 | `--runtime`、`--repo`、`--base-url`、`--config`、`--replace`、`--home` |
| `key purge` | 清私钥、信封、install 值和 state | 是（删除） | 否 | `--repo`、`--runtime`、`--dry-run`、`--home` |

`key` 自身和每个子命令都支持 `-h` / `--help`。未知子命令打印总用法到 stderr，退出码 2。

### 3.2 `--help` 文本草案

总用法：

```text
Usage: oh-my-cpa key <command> [options]

Manage the repository keys this machine holds for Oh My CPA. The key stays
local: `exec` opens the delivery envelope in memory and gives one child
process the variables its runtime needs; `install` writes it to a runtime's
own configuration file only when asked.

Commands:
  status    show local keys, the repositories they belong to, the last known
            approval state, and which runtime has one installed
  gen       generate an X25519 key pair for a repository; print only the
            public key
  accept    read a repo_key_ciphertext result on stdin and store its envelope
  exec      run a command with the repository key in its environment
  install   write the repository key into a runtime's configuration file
  purge     remove a repository's private key, envelope and installed values

Run `oh-my-cpa key <command> --help` for one command's options.

Environment:
  OMC_HOME                  where keys, envelopes and state live; default
                            ~/.omc, created with owner-only permissions
  OMCPA_CPA_BASE_URL        the CPA gateway this key is used against; also
                            accepted as --base-url and remembered in
                            $OMC_HOME/config.json
  OMCPA_SERVER_URL          OMC console URL, for `status --remote`
  OMCPA_CPA_MANAGEMENT_KEY  the management key, for `status --remote`
  OMCPA_AGENT_NAME          the agent name reported by `status --remote`
  OMCPA_DEVICE_NAME         the device name reported by `status --remote`

Exit codes:
  0  success
  1  operation failed
  2  usage error
  3  configuration missing or invalid
  4  key, envelope, application or installed value not found
  5  conflict: a different key or value already occupies the place

`exec` exits with the child's exit code once the child has started.
```

`key gen`：

```text
Usage: oh-my-cpa key gen --repo <repo> [--base-url <url>] [--home <dir>]

Generates an X25519 key pair for the repository and writes the private key
to $OMC_HOME/keys/<repo>.key with owner-only permissions. The public key is
the only thing written to stdout:
  x25519:<base64>

If a key already exists for the repository, it is reused and its public key
is printed again. Generating a replacement is `key purge` followed by
`key gen`; `gen` never rotates a key silently.

`--base-url` is remembered in $OMC_HOME/config.json for later commands.
```

`key accept`：

```text
Usage: oh-my-cpa key accept --repo <repo> [--application-id <id>] [--json] [--home <dir>]

Reads the JSON returned by the MCP tool `repo_key_ciphertext` from stdin and
stores its envelope at $OMC_HOME/envelopes/<repo>.json. The envelope is
opened once in memory with the local private key before it is stored, so an
envelope for another key is refused instead of saved.

Nothing from the input is echoed. The ciphertext, the public key and the mask
are not secrets; the private key and the plaintext gateway key are.
```

`key status`：

```text
Usage: oh-my-cpa key status [--repo <repo>] [--application-id <id>] [--remote] [--json] [--home <dir>]

Shows the local keys, the repository each belongs to, whether its envelope
is present, the last known application state, and which runtime has it
installed. Without --remote this reads only $OMC_HOME/state.json.

--application-id associates an application id with the repository. It is
useful between `repo_key_apply` and the decision; without it, status can only
report what a previous `accept` or `--remote` recorded. It needs --repo unless
the id is already recorded in state.

--remote asks OMC for the current state of recorded applications and the
repository's key state. It needs OMCPA_SERVER_URL and
OMCPA_CPA_MANAGEMENT_KEY, the same pair the `mcp` command uses; without them
it reports remote_not_configured and changes nothing.
```

`key exec`：

```text
Usage: oh-my-cpa key exec --runtime <claude|codex|opencode> [--repo <repo>] [--base-url <url>] [--home <dir>] -- <command> [args...]

Opens the repository's envelope in memory and runs <command> with only the
variables that runtime needs. The plaintext is never written to disk, never
printed and never put in this process's command line. The child inherits
stdin, stdout, stderr and the rest of the environment.

claude    sets ANTHROPIC_BASE_URL and ANTHROPIC_AUTH_TOKEN; removes
          ANTHROPIC_API_KEY for the child because it may take precedence
codex     requires <command> to be codex itself; inserts -c provider
          overrides and passes the key through OMC_REPO_KEY (Codex is not
          known to select a provider from the environment alone)
opencode  sets OPENCODE_CONFIG_CONTENT to the user's configuration merged
          with provider.oh-my-cpa; the key is in that variable only

--repo defaults to the repository of the current directory (its git origin,
or the directory name when there is no remote).
```

`key install`：

```text
Usage: oh-my-cpa key install --runtime <claude|codex|opencode> [--repo <repo>] [--base-url <url>] [--config <path>] [--replace] [--home <dir>]

Writes the repository key into the runtime's own configuration file. This is
the only command that leaves plaintext on disk, and it says so on stderr.
The previous file is copied to $OMC_HOME/backups/ first.

  claude    ~/.claude/settings.json, env.ANTHROPIC_BASE_URL and
            env.ANTHROPIC_AUTH_TOKEN (or $CLAUDE_CONFIG_DIR/settings.json)
  codex     $CODEX_HOME/config.toml or ~/.codex/config.toml,
            [model_providers.oh-my-cpa] plus model_provider = "oh-my-cpa"
  opencode  ~/.config/opencode/opencode.json, provider.oh-my-cpa.options
            (or $OPENCODE_CONFIG)

There is one installed slot per runtime per machine. Installing another
repository replaces the slot; the previous repository's values are in the
backup. --replace is required when the slot already holds a value this tool
did not write.
```

`key purge`：

```text
Usage: oh-my-cpa key purge --repo <repo> [--runtime <runtime>] [--dry-run] [--home <dir>]

Removes the repository's private key, envelope and state entry. With
--runtime, removes only that runtime's installed values and leaves the key
and envelope in place. Purge never removes a value in a runtime's config
unless its fingerprint matches this repository's key; a value someone else
changed is left alone and reported.

Backups are kept unless they contain this repository's plaintext key; those
are removed with the key. --dry-run prints what would be removed and changes
nothing.

Purge is local. The key in CPA remains active; stop or reissue it in the
console to revoke it.
```

### 3.3 stdout / stderr 约定

- `key gen` 的 stdout 只有一行公钥（`x25519:<base64>\n`）；「已复用」「已写 config」等都走 stderr。AC-2 的「stdout 只有公钥」按这条测。
- `key accept` 默认 stdout 一行摘要，`--json` 给机器读；从不回显输入。
- `key status` 默认人类可读表，`--json` 是稳定 schema；不打印私钥、明文、信封正文。
- `key exec` 自己不往 stdout 写任何东西；子进程继承 stdout。警告走 stderr。
- `key install` / `key purge` 的进度和警告走 stderr，stdout 只放摘要或 `--json`。
- 所有错误消息只带仓库显示名、key mask、公钥指纹、路径、运行时和子进程退出码；不带私钥、明文、信封正文。

### 3.4 退出码

| 码 | 含义 | 典型情况 |
|---|---|---|
| 0 | 成功 | 包括「已经存在，复用」「已经不存在，幂等」 |
| 1 | 操作失败 | I/O、解密失败、备份失败、远端拒绝、purge 有未清项 |
| 2 | 用法错误 | 未知子命令、缺 `--runtime`、`codex` 命令不匹配、未知 flag |
| 3 | 配置缺失/无效 | `base_url_required`、`base_url_invalid`、`remote_not_configured`、`config_too_large` |
| 4 | 未找到 | 没有私钥、没有信封、没有申请、没有 install |
| 5 | 冲突 | key 已存在且不是复用、配置槽位被别人占用、state 损坏 |

`key exec` 正常结束时退出码就是子进程的退出码；上面 0–5 只适用于「还没启动子进程」的阶段。被 Ctrl-C 中断时以控制台/进程组行为为准，工具不额外转换。运行手册明确这一点，避免把子进程的 3 误读成配置错误。

## 4. 目录与文件

### 4.1 `$OMC_HOME` 布局

```text
$OMC_HOME/
  keys/
    org%2Frepo-1a2b3c4d.key
  envelopes/
    org%2Frepo-1a2b3c4d.json
  backups/
    20261117T091500Z-home-user-.claude-settings.json
  state.json
  config.json
  .lock
```

目录和文件的权限：

| 路径 | Unix | Windows |
|---|---|---|
| `$OMC_HOME` | 0700 | 只含当前用户 SID 的 DACL，去掉继承 |
| `keys/`、`envelopes/`、`backups/` | 0700 | 同上 |
| 私钥、信封、`state.json`、`config.json`、备份 | 0600 | 从父目录继承的当前用户 ACL |

Unix 上如果 `$OMC_HOME` 已存在且 group/other 有写权限，报 `home_insecure`，不写私钥；只读权限不阻止（可能是共享目录里的只读挂载，写入会自然失败）。Windows 上 `SetNamedSecurityInfo` 失败（例如 FAT32/exFAT、某些网络盘不支持 ACL）时同样报 `home_insecure`，不降级成「大家可读」。

### 4.2 文件名映射

```text
repoID  = repokey.NormalizeRepo(input)
slug    = percentEncode(repoID, keep = [A-Za-z0-9._-])
          reservedWindowsName -> "_" + slug
          trim trailing "." and " "
          truncate to 80 UTF-8 bytes on a rune boundary
hash8   = lower(hex(sha256(repoID)))[:8]
key     = "keys/" + slug + "-" + hash8 + ".key"
envelope= "envelopes/" + slug + "-" + hash8 + ".json"
```

例子：

| repo ID | 文件名主干 |
|---|---|
| `org/repo` | `org%2Frepo-1a2b3c4d` |
| `gitlab.example.com/Group/Repo` | `gitlab.example.com%2FGroup%2FRepo-9e8f7a6b` |
| `C:/works/repo` 的本地目录名 `repo` | `repo-5c4d3e2f` |
| `con`（本地目录名） | `_con-...` |

`hash8` 解决大小写不敏感文件系统、截断和百分号编码后的碰撞；同一 repo ID 永远得到同一个名字。state 存实际相对路径，改名规则升级时旧文件仍能找到。

### 4.3 仓库身份推断

`key exec` / `key install` 没给 `--repo` 时：

1. `git -C <cwd> remote get-url origin`；失败则 `git -C <cwd> remote` 取第一个 remote 再取 URL。
2. 把 URL 交给 `repokey.NormalizeRepo`；`repo_display` 用 `repokey.RepoDisplay`。
3. 没有 git、没有 remote、或 git 不存在：用当前目录的 base name 作为本地目录身份（和 `NormalizeRepo` 对本地路径的处理一致）。

`git` 子进程加 5 秒超时；失败只降级到目录名，不报错，除非目录名也无效（`repo_invalid`）。运行手册建议在仓库根目录执行，或显式 `--repo`。

## 5. 接口约定

### 5.1 `state.json`

0600 / 当前用户 ACL；原子写；只在写命令持 `.lock` 时读改写。版本 1：

```json
{
  "version": 1,
  "repos": {
    "org/repo": {
      "repo": "org/repo",
      "repo_display": "Org/Repo",
      "private_key_file": "keys/org%2Frepo-1a2b3c4d.key",
      "public_key": "x25519:<base64>",
      "public_key_fingerprint": "x25519:sha256:<base64url>",
      "created_at": "2026-11-17T09:00:00Z",
      "application_id": "0f0e0d0c-...",
      "application_kind": "issue",
      "approval_status": "approved",
      "status_source": "accept",
      "status_updated_at": "2026-11-17T09:12:00Z",
      "key_mask": "sk-cpa-ab***wxyz",
      "repo_state": "active",
      "repo_state_updated_at": "2026-11-17T09:12:00Z",
      "envelope_file": "envelopes/org%2Frepo-1a2b3c4d.json",
      "envelope_received_at": "2026-11-17T09:12:00Z",
      "envelope_algorithm": "x25519-hkdf-sha256-aes256gcm"
    }
  },
  "runtimes": {
    "codex": {
      "repo": "org/repo",
      "installed_at": "2026-11-17T09:15:00Z",
      "config_path": "/home/user/.codex/config.toml",
      "backup_path": "/home/user/.omc/backups/20261117T091500Z-...",
      "value_fingerprint": "sha256:<base64url>",
      "backup_contains_key": false,
      "previous_model_provider": null
    }
  }
}
```

字段约定：

- `approval_status`：`not_applied` | `unknown` | `pending` | `approved` | `rejected` | `stopped`。
- `status_source`：`gen` | `local` | `remote` | `accept`。
- `repo_state`：`active` | `stopped` | `none` | `unknown`；只有 `--remote` 会写。
- `application_kind`：`issue` | `reuse` | `reissue`；来自 apply/ciphertext/status。
- `value_fingerprint`：`sha256:` + base64url(SHA-256(明文 key))，用于 install/purge 的所有权判断；不是 HMAC，key 本身高熵，SHA-256 足够。
- `backup_contains_key`：install 时判断目标文件里是否已经是本仓库的 key（重装）；true 时 purge 即使私钥已不在也能删掉这份备份。
- `previous_model_provider`：只给 Codex 用；install 替换顶层 `model_provider` 前的原值，没有则为 `null`。
- 不存私钥、明文、信封正文、管理密钥、approval token。

state 是索引，不是唯一真相：key/envelope 的路径是确定性的。state 丢失时，`key gen` / `key accept` 能从私钥重建记录，`key exec` 仍可按路径工作；`key install` 把无法确认归属的槽位当外部值，要求 `--replace`；`key purge` 先清能确定的私钥/信封，安装值按 `--runtime` + 配置路径 + 指纹比对处理，不能确定就保留并报 `purge_pending`。

### 5.2 `config.json`

0600 / 当前用户 ACL；版本 1：

```json
{
  "version": 1,
  "base_url": "http://127.0.0.1:8317",
  "updated_at": "2026-11-17T09:00:00Z"
}
```

只存规范化后的网关根。`key gen` / `key exec` / `key install` 带 `--base-url` 成功时写；其他命令只读。

### 5.3 私钥文件

- 内容是 32 字节 X25519 私钥的原始字节，不加前缀、不加换行。读取时长度必须正好 32，否则 `key_unreadable`。
- 用 `ecdh.X25519().NewPrivateKey(raw)` 校验；再由私钥导出公钥，`x25519:<base64>` 是 `PublicKeyText`。
- 私钥文件缺失时 `key_not_found`；存在但读不了/长度不对时 `key_unreadable`；不自动覆盖。
- `key gen` 发现私钥已存在时，重新导出公钥、修复 state 里缺失的公钥/指纹，stdout 再打印一次公钥；不生成新 key。

### 5.4 信封文件与 `key accept` 校验

`key accept` 接受两种输入：

1. MCP 结果对象：

```json
{
  "application_id": "0f0e0d0c-...",
  "kind": "issue",
  "key_mask": "sk-cpa-ab***wxyz",
  "algorithm": "x25519-hkdf-sha256-aes256gcm",
  "ciphertext": "{\"v\":1,\"alg\":\"x25519-hkdf-sha256-aes256gcm\",\"epk\":\"...\",\"nonce\":\"...\",\"ciphertext\":\"...\"}"
}
```

2. 裸信封：

```json
{"v":1,"alg":"x25519-hkdf-sha256-aes256gcm","epk":"...","nonce":"...","ciphertext":"..."}
```

也接受 capability envelope `{"status":"success","data":{...}}`，以及 MCP text content 里的 JSON 字符串（先解一层字符串）。解析时最多下钻 `data` 一层和字符串一层，不接受其他包装；认不出就报 `envelope_invalid`。`ciphertext` 是字符串时，先把它当 JSON 解析成内层信封；已经是对象时直接用。校验顺序：

1. 输入不超过 1 MiB。
2. 外层 `algorithm`（若有）等于 `repokey.Algorithm`。
3. 内层 `v == 1`、`alg == repokey.Algorithm`。
4. `repokey.Decrypt(localPrivateKey, innerJSON)` 成功；失败 `envelope_key_mismatch`。
5. 写 `envelopes/<safe-repo>.json`（内层紧凑 JSON，0600），更新 state。

`key accept` 不打印内层 JSON，不把明文写入任何日志。重复 accept 同一个仓库时，旧信封被新信封替换（同一把私钥能解开才允许）；旧 state 的 `application_id` 更新为新的。

### 5.5 Go 类型与函数

新增包 internal/keyclient，命令层只做参数解析和输出。建议的最小公开面：

```go
package keyclient

type Runtime string

const (
    RuntimeClaude   Runtime = "claude"
    RuntimeCodex    Runtime = "codex"
    RuntimeOpenCode Runtime = "opencode"
)

type Home struct{ Root string }

func ResolveHome(flagValue string) (Home, error)
func (h Home) KeyPath(repoID string) (string, error)
func (h Home) EnvelopePath(repoID string) (string, error)
func (h Home) StatePath() string
func (h Home) ConfigPath() string
func (h Home) BackupPath(configPath string, at time.Time) (string, error)

type RepoState struct {
    Repo, RepoDisplay        string
    PrivateKeyFile           string
    PublicKey, Fingerprint   string
    CreatedAt                time.Time
    ApplicationID            string
    ApplicationKind          string
    ApprovalStatus           string
    StatusSource             string
    StatusUpdatedAt          time.Time
    KeyMask                  string
    RepoState                string
    RepoStateUpdatedAt       time.Time
    EnvelopeFile             string
    EnvelopeReceivedAt       time.Time
    EnvelopeAlgorithm        string
}

type RuntimeSlot struct {
    Repo                  string
    InstalledAt           time.Time
    ConfigPath            string
    BackupPath            string
    ValueFingerprint      string
    PreviousModelProvider string
}

type State struct {
    Version  int
    Repos    map[string]RepoState
    Runtimes map[Runtime]RuntimeSlot
}

func OpenStore(home Home) (*Store, error)
func (s *Store) Save() error
func (s *Store) Repo(repoID string) (RepoState, bool)
func (s *Store) PutRepo(RepoState) error
func (s *Store) RemoveRepo(repoID string) error
func (s *Store) Runtime(runtime Runtime) (RuntimeSlot, bool)
func (s *Store) PutRuntime(runtime Runtime, slot RuntimeSlot) error
func (s *Store) RemoveRuntime(runtime Runtime) error

type KeyPair struct {
    PrivateKey []byte
    PublicKey  string
    Fingerprint string
}

func GenerateKeyPair() (KeyPair, error)
func ReadPrivateKey(home Home, repoID string) ([]byte, error)
func WritePrivateKey(home Home, repoID string, raw []byte) error
func PublicKeyText(raw []byte) string

type AcceptedEnvelope struct {
    InnerJSON     string
    ApplicationID string
    Kind          string
    KeyMask       string
    Algorithm     string
}

func ParseCiphertextResult(input []byte) (AcceptedEnvelope, error)
func AcceptEnvelope(home Home, repoID string, accepted AcceptedEnvelope, now time.Time) (RepoState, error)

func InferRepo(dir string) (repoID, repoDisplay string, err error)

type RemoteClient interface {
    RepoKeyStatus(ctx context.Context, applicationID string) (RemoteApplication, error)
    RepoKeyQuery(ctx context.Context, repo string) (RemoteRepoState, error)
}

func RefreshRemote(ctx context.Context, store *Store, client RemoteClient) ([]RepoRefreshError, error)

type Injection struct {
    Env        []string
    ArgvPrefix []string
    Warnings   []string
}

func (r Runtime) BaseURL(root string) (string, error)
func (r Runtime) Inject(key string, root string, argv []string) (Injection, error)

type ExecOptions struct {
    Runtime Runtime
    Key     string
    BaseURL string
    Argv    []string
    Env     []string
    Stdin   io.Reader
    Stdout  io.Writer
    Stderr  io.Writer
}

func RunChild(ctx context.Context, opts ExecOptions) (exitCode int, err error)

type InstallOptions struct {
    Runtime    Runtime
    Repo       RepoState
    Key        string
    BaseURL    string
    ConfigPath string
    Replace    bool
    Now        time.Time
}

type InstallResult struct {
    ConfigPath string
    BackupPath string
    Changed    []string
}

func Install(opts InstallOptions) (InstallResult, error)

type PurgeOptions struct {
    Repo       string
    Runtime    Runtime
    DryRun     bool
    Home       Home
    Now        time.Time
}

type PurgeResult struct {
    Removed []string
    Kept    []string
    Pending []string
}

func Purge(opts PurgeOptions) (PurgeResult, error)
```

命令层（`cmd/oh-my-cpa`）用一个注册表，避免 S2/S3/S4 改同一个 switch：

```go
type keyCommand struct {
    Name    string
    Summary string
    Usage   string
    Run     func(ctx context.Context, args []string, stdout, stderr io.Writer) int
}

var keyCommands = map[string]keyCommand{}

func registerKeyCommand(command keyCommand) { keyCommands[command.Name] = command }
```

`key.go` 的 `runKey` 只负责 `--help`、查找注册表、调用 `Run`；`keygen.go` 在 `init()` 注册 `gen`，S2b 的 `keyaccept.go` / `keystatus.go` 注册 `accept` / `status`，S3 的 `keyexec.go` 注册 `exec`，S4 的 `keyinstall.go` / `keypurge.go` 注册 `install` / `purge`。这样并行分支不会在 `key.go` 的 switch 上冲突；合并后 `key --help` 自动列出全部命令。

### 5.6 错误码

错误码是稳定的 snake_case，写在 stderr 的人类可读消息前；`--json` 时放 `code` 字段。消息可以带路径、repo display、key mask、fingerprint，**不得带私钥、明文、信封正文、管理密钥、approval token**。

| 码 | 退出码 | 含义 / 可读消息要点 |
|---|---|---|
| `home_unavailable` | 1 | `$OMC_HOME` 无法创建或不可写；给出路径和原因 |
| `home_insecure` | 1 | 目录/文件无法收紧到当前用户；不写私钥 |
| `repo_required` | 2 | 缺 `--repo`，且当前目录推断不出仓库 |
| `repo_invalid` | 2 | 仓库身份不合法；给 `org/repo` 示例 |
| `key_not_found` | 4 | 该仓库没有本地私钥；给 `key gen --repo ...` |
| `key_unreadable` | 1 | 私钥文件读不了或长度不是 32 字节 |
| `envelope_missing` | 4 | 没有信封；给 `key accept` 提示 |
| `envelope_invalid` | 1 | 信封 JSON/版本/算法不合法 |
| `envelope_key_mismatch` | 1 | 信封解不开；私钥不匹配，未写盘 |
| `base_url_required` | 3 | 三个来源都没有；给三种设置方式 |
| `base_url_invalid` | 3 | 不是 http/https、带 userinfo/query/fragment、或给了具体端点 |
| `insecure_base_url` | 0（警告） | 非 loopback 的 http；继续但提示明文风险 |
| `runtime_unknown` | 2 | `--runtime` 不是三家之一 |
| `command_required` | 2 | `exec` 缺少 `-- <command>` |
| `codex_command_required` | 2 | `--runtime codex` 时命令不是 `codex`；提示 install 或直接跑 codex |
| `decrypt_failed` | 1 | 信封与私钥不匹配或密文损坏 |
| `state_unreadable` / `state_corrupt` | 1 / 5 | state 打不开或 schema 不对；不猜、不覆盖 |
| `state_locked` | 1 | 另一个 key 写命令持锁超过 5 秒 |
| `config_unreadable` | 1 | 运行时配置读不了 |
| `config_not_json` | 1 | 配置不是纯 JSON（可能是 JSONC）；不重写，提示手动处理 |
| `config_too_large` | 3 | OpenCode 合并后的内容超过 24 KiB；提示 install |
| `config_conflict` | 5 | 受管块损坏、非受管同名 provider、或槽位值不是本工具写的且没 `--replace` |
| `config_owned_by_another_repo` | 5 | 槽位属于另一个仓库；先 purge 那个仓库或显式 `--replace` |
| `backup_failed` | 1 | 备份写不了；不写目标文件 |
| `install_failed` | 1 | 写配置失败；原文件保留 |
| `install_not_installed` | 4 | 该运行时没有本仓库的 install 记录 |
| `install_not_owned` | 1 | 配置里的值指纹不匹配；不删，报告 |
| `remote_not_configured` | 3 | `--remote` 缺 `OMCPA_SERVER_URL` / `OMCPA_CPA_MANAGEMENT_KEY` |
| `remote_unavailable` | 1 | 连不上 OMC；本地状态不变 |
| `remote_refused:<code>` | 1 | OMC 返回稳定错误码；只重复 code，不重复响应正文 |
| `purge_pending` | 1 | 有属于本仓库但没清掉的项；列出，可重跑 |
| `exec_failed` | 1 | 子进程没起来（命令不存在、权限、工作目录） |

### 5.7 `key status --json` 输出

这是给 agent/脚本读的稳定形状（Tier 2 契约）；字段名改动要按 AGENTS.md §2 处理。不包含私钥、明文、信封正文、管理密钥、approval token。

```json
{
  "home": "/home/user/.omc",
  "base_url": "http://127.0.0.1:8317",
  "base_url_source": "config",
  "repos": [
    {
      "repo": "org/repo",
      "repo_display": "Org/Repo",
      "public_key": "x25519:<base64>",
      "public_key_fingerprint": "x25519:sha256:<base64url>",
      "private_key_file": "keys/org%2Frepo-1a2b3c4d.key",
      "private_key_present": true,
      "envelope_file": "envelopes/org%2Frepo-1a2b3c4d.json",
      "envelope_present": true,
      "envelope_received_at": "2026-11-17T09:12:00Z",
      "application_id": "0f0e0d0c-...",
      "application_kind": "issue",
      "approval_status": "approved",
      "status_source": "accept",
      "status_updated_at": "2026-11-17T09:12:00Z",
      "key_mask": "sk-cpa-ab***wxyz",
      "repo_state": "active",
      "repo_state_updated_at": "2026-11-17T09:12:00Z",
      "installed": {"claude": true}
    }
  ],
  "runtimes": {
    "claude": {
      "repo": "org/repo",
      "installed_at": "2026-11-17T09:15:00Z",
      "config_path": "/home/user/.claude/settings.json",
      "backup_path": "/home/user/.omc/backups/20261117T091500Z-..."
    }
  }
}
```

`base_url_source` 是 `flag` | `env` | `config` | `absent`；没有 base URL 时 `base_url` 为空、`base_url_source=absent`，`status` 仍然成功（看本地 key 不需要网关）。`installed` 只列 `true` 的运行时；`runtimes` 是槽位归属，和 `repos[].installed` 一致。

## 6. 三种运行时

### 6.1 `exec` 注入

| 运行时 | 进程内变量 | 额外 argv | 过滤掉 | 说明 |
|---|---|---|---|---|
| Claude Code | `ANTHROPIC_BASE_URL=<根>`、`ANTHROPIC_AUTH_TOKEN=<明文>` | 无 | `ANTHROPIC_API_KEY`、旧的 `ANTHROPIC_AUTH_TOKEN` / `ANTHROPIC_BASE_URL` | 官方文档允许 `ANTHROPIC_AUTH_TOKEN`；项目级 settings 会提交，禁用 |
| Codex | `OMC_REPO_KEY=<明文>` | `-c model_provider="oh-my-cpa"`、`-c model_providers.oh-my-cpa.{name,base_url,env_key,wire_api}=...` | 不设 token 到 argv | `env_key` 指向 `OMC_REPO_KEY`；`wire_api` 只收 `responses`；命令必须是 `codex` |
| OpenCode | `OPENCODE_CONFIG_CONTENT=<用户配置 + provider.oh-my-cpa>` | 无 | 旧 `OPENCODE_CONFIG_CONTENT` | v1 单数 `provider`；不写临时文件 |

补充：

- Claude Code 如果用户在 OAuth 登录态，环境变量是否优先未实测；S3 的探针要在本机 Claude Code 上确认。如果 OAuth 优先，运行手册要求用户先退出登录，或改用 `--settings` 的 `env`（本计划的 `install` 写的就是用户级 settings，属于同一优先级层）。这个风险在 §11 记录。
- Codex 如果用户的 `~/.codex/config.toml` 里已经有 `model_provider = "..."`，`-c model_provider="oh-my-cpa"` 是本次命令的最高层覆盖（session 级），不会改用户文件；这是 `exec` 不落盘的关键。
- Codex 的 `-c` 必须在子命令之前；`key exec` 只识别 `codex` 本身，不识别 `codex exec` 之外的包装脚本。运行手册给正例和反例。
- OpenCode 的 `OPENCODE_CONFIG_CONTENT` 在 1.19.5 实测生效；它是否与文件配置合并没有定论，所以我们发送「文件内容 + 我们这一层」的完整 JSON。Windows 环境块有大小限制，超过 24 KiB 就拒绝并建议 install。

### 6.2 `install` 落点与写法

| 运行时 | 默认路径 | 写入的字段 | 编辑方式 | 备注 |
|---|---|---|---|---|
| Claude Code | `~/.claude/settings.json`（或 `$CLAUDE_CONFIG_DIR/settings.json`） | `env.ANTHROPIC_BASE_URL`、`env.ANTHROPIC_AUTH_TOKEN` | `map[string]json.RawMessage` 顶层 + `env` 对象；保留未知键 | 若 `env.ANTHROPIC_API_KEY` 存在，拒绝 |
| Codex | `$CODEX_HOME/config.toml` 或 `~/.codex/config.toml` | 受管 `[model_providers.oh-my-cpa]` + 顶层 `model_provider` | 行级受管块；只替换/删除自己的块和一行顶层键 | `experimental_bearer_token` 明文；`wire_api="responses"` |
| OpenCode | `~/.config/opencode/opencode.json`（或 `$OPENCODE_CONFIG`） | `provider.oh-my-cpa.{npm,name,options.baseURL,options.apiKey}` | `map[string]json.RawMessage` 递归保留其他 provider | v1 单数；不写 `models` |

写前检查：

1. 目标文件存在则读入；JSON 解析失败报 `config_not_json`，不重写（可能是 JSONC）。
2. 目标槽位已有值：如果 state 里 `runtimes.<runtime>.repo` 是本仓库且指纹匹配，视为重装；否则需要 `--replace`。
3. Codex：受管块开始/结束标记必须成对；非受管同名 provider 表存在则拒绝。
4. 备份成功后才写。
5. 写完重新读一遍，确认字段是本仓库的值、其他键还在；失败则用备份恢复并报 `install_failed`。

### 6.3 备份、覆盖与所有权

- 备份路径：`$OMC_HOME/backups/<UTC 时间戳>-<压平后的配置路径>`，例如 `20261117T091500Z-home-user-.claude-settings.json`；同名冲突加 `-2`、`-3`。
- 备份文件 0600/当前用户 ACL；备份失败不写目标。
- 覆盖别人写的值必须 `--replace`；`--replace` 只表示「允许覆盖这个槽位」，不删除文件里的其他键，也不改变「明文会留下」的警告。
- state 只记录当前槽位归属；安装另一个仓库后，旧仓库的 `key status` 显示「未安装」，但备份里保留旧值。
- `key status` 列出每个运行时的 `repo`、`installed_at`、`config_path`、`backup_path`，不打印值。

### 6.4 purge

顺序：

1. 读 state，确定该仓库的私钥、信封和各运行时槽位。
2. 私钥还在时读并解密一次，明文只留在本次调用的内存；私钥已不在时用 state 的 `value_fingerprint` / `backup_contains_key`，并标记 `backup_scan_unavailable`。
3. 对每个要清的运行时：读配置，找当前值；用 state 指纹或本次解密的值比对；匹配才删；不匹配报 `install_not_owned` 并保留。
4. Codex：删受管块；顶层 `model_provider` 仍等于 `oh-my-cpa` 时恢复 `previous_model_provider`（有）或删行（无）；否则保留并报告。
5. 扫描 `backups/`：`backup_contains_key=true` 或内容中包含本次明文 key 的备份删除；其余保留并列出；无法扫描时保留并报告。
6. 删私钥、信封。
7. 所有属于本仓库的项都清掉后删 state 记录；否则保留并报 `purge_pending`。
8. 打印「本地已清；CPA 里的 key 仍有效」的提示。

`--dry-run` 只做 1、2、3、5、6 的检查并打印，不写不删。

## 7. 切片、文件归属与依赖

### 7.1 切片表

在 PM 原拆分的基础上，把 S2 拆成 S2a/S2b，让 S3/S4 不必等 status/accept 完成；S4 建议按 install/purge 再拆成 S4a/S4b（见 §13）。S6/S9/S10/S11/S12 沿用 PM 原表，不在本计划重复展开。

| 切片 | 做什么 | 依赖 | 改哪里（计划） | 估时（分钟） | 验收点 |
|---|---|---|---|---|---|
| S2a | 命令骨架 + `key` 分发 + 注册表 + home/store/锁 + 私钥格式 + `key gen` + 仓库推断 + Unix/Windows 权限 | S1 | cmd/oh-my-cpa/main.go、cmd/oh-my-cpa/key.go、cmd/oh-my-cpa/keygen.go、internal/keyclient/{types,errors,home,home_unix,home_windows,lock,state,keystore,repo}.go 及测试 | 80 | AC-1、AC-2、AC-8 |
| S2b | `key accept` + `key status`（本地 + `--remote`） | S2a | cmd/oh-my-cpa/keyaccept.go、cmd/oh-my-cpa/keystatus.go、internal/keyclient/{accept,status,remote}.go 及测试 | 70 | AC-1、AC-3、AC-8 |
| S3 | `key exec`：内存解密、三家注入、子进程、退出码、错误脱敏 | S2a | cmd/oh-my-cpa/keyexec.go、internal/keyclient/{runtime,exec}.go 及测试 | 90 | AC-4、AC-6 |
| S4 | `key install` + `key purge`：JSON/TOML 受管编辑、备份、所有权、精确清理 | S2a | cmd/oh-my-cpa/keyinstall.go、cmd/oh-my-cpa/keypurge.go、internal/keyclient/{install,purge,jsondoc,tomlblock}.go 及测试 | 110（可拆 S4a 70 / S4b 40） | AC-5、AC-6、AC-8 |
| S5 | 文档：agent 运行手册 + capability/architecture/operations/install/testing 同步 | S1；完整同步在 S2a–S4 后 | docs/（见 §9） | 60 | AC-9 |
| S7 | e2e 夹具：fake CPA + 真实 OMC 二进制 + MCP apply/审批/ciphertext 助手 + 掩码 helper；先跑通不经过 key 命令的链路 | S1 | internal/keyclient/e2e/、package.json（test:key-e2e） | 100 | AC-7 |
| S8 | 全链路：CLI 七步串起来，出可复跑命令与证据（含 purge 后失败） | S2a、S2b、S3、S4、S7 | internal/keyclient/e2e/ | 60 | AC-7 |

### 7.2 文件归属

并行分支的文件互不重叠。`cmd/oh-my-cpa/main.go` 只由 S2a 改一次（加 `key` 分发）；`key.go` 只由 S2a 写（注册表 + 总 help）；S2b/S3/S4 只新增自己的命令文件和 internal/keyclient 文件，通过 `init()` 注册，不改 `key.go` 的 switch。

```text
S2a
  cmd/oh-my-cpa/main.go                  (edit: dispatch `key` before config load; no signal.NotifyContext)
  cmd/oh-my-cpa/key.go                   (new: runKey, registry, top-level help)
  cmd/oh-my-cpa/keygen.go                (new: gen command)
  internal/keyclient/types.go            (new: Runtime, RepoState, RuntimeSlot, State)
  internal/keyclient/errors.go           (new: codes + typed errors)
  internal/keyclient/home.go             (new: resolve/create/layout)
  internal/keyclient/home_unix.go        (new: chmod 0700/0600)
  internal/keyclient/home_windows.go     (new: DACL)
  internal/keyclient/lock.go             (new: per-home write lock)
  internal/keyclient/state.go            (new: load/save/atomic)
  internal/keyclient/keystore.go         (new: keypair/private key/filename)
  internal/keyclient/repo.go             (new: InferRepo via git)
  internal/keyclient/*_test.go
  cmd/oh-my-cpa/key_test.go

S2b
  cmd/oh-my-cpa/keyaccept.go             (new: accept command)
  cmd/oh-my-cpa/keystatus.go             (new: status command)
  internal/keyclient/accept.go           (new: parse/validate/decrypt/store)
  internal/keyclient/status.go           (new: local report)
  internal/keyclient/remote.go           (new: mcpbridge.Bridge read calls)
  internal/keyclient/*_test.go

S3
  cmd/oh-my-cpa/keyexec.go               (new: exec command)
  internal/keyclient/runtime.go          (new: per-runtime env/argv)
  internal/keyclient/exec.go             (new: child process/exit code)
  internal/keyclient/*_test.go

S4
  cmd/oh-my-cpa/keyinstall.go            (new: install command)
  cmd/oh-my-cpa/keypurge.go              (new: purge command)
  internal/keyclient/install.go          (new: install orchestration/backups)
  internal/keyclient/purge.go            (new: purge orchestration)
  internal/keyclient/jsondoc.go          (new: JSON structured edit)
  internal/keyclient/tomlblock.go        (new: TOML managed block)
  internal/keyclient/*_test.go

S5
  docs/repo-key-client.md                (new: agent runbook)
  docs/agent-capabilities.md             (edit: pointer + client boundary)
  docs/architecture.md                   (edit: key entrypoint + package row)
  docs/operations.md                     (edit: OMC_HOME)
  docs/install.md                        (edit: pointer)
  docs/install-for-agents.md             (edit: pointer)
  docs/testing.md                        (edit: claim map)

S7/S8
  internal/keyclient/e2e/e2e_test.go     (new: build/start/flow)
  internal/keyclient/e2e/helper_test.go  (new: mask/HTTP helper process)
  internal/keyclient/e2e/fakecpa_test.go (new: httptest CPA)
  package.json                           (edit: test:key-e2e entry)
```

### 7.3 并行与关键路径

```text
t0        S2a ─────────────┐
          S5 (usage doc)   │
          S7 (harness)     │
                           ├─ S2b ─┐
                           ├─ S3  ─┼─ S8 ─ S11 ─ S12
                           └─ S4  ─┘
 S5 (sync docs) ─────────────────────┘
 S9 ─ S10 ─────────────────────────────┘
```

- S2a 是唯一阻塞 S2b/S3/S4 的切片；它先定 `types.go` / `state.go` / `keystore.go` 的签名。
- S2b、S3、S4 只新增文件，可同时开工；三方都不改 `key.go` 和 `main.go`。
- S7 的 fake CPA + 真实 OMC + apply/审批/ciphertext 不依赖 key 命令，可与 S2a 同时起；S8 等 CLI 四块齐。
- S5 先写运行手册（依赖需求与 S1），等 S2a–S4 合并后再补 architecture/operations/install/testing 的准确路径和测试名。
- S9 等 S2a/S2b/S3/S4/S5；S10 跟 S9；S11 等 S8/S10；S12 收口。

### 7.4 与 PM 原拆分的差异

| 原切片 | 本计划 | 原因 |
|---|---|---|
| S2（90 分钟） | S2a（80）+ S2b（70） | 原 S2 同时含 home/store/gen/status/权限/accept；拆开后 S3/S4 只等 S2a，关键路径少约 60 分钟 |
| S4（75 分钟） | S4（110），建议 S4a（install 70）+ S4b（purge 40） | 两家 JSON + 一家 TOML + 备份 + 三套 purge + 测试，75 分钟不够；拆开后 install 与 purge 可并行（共享 `jsondoc`/`tomlblock` 的签名由 S4a 先定） |
| S7（80 分钟） | S7（100） | 真实 OMC 二进制的启动、capability HTTP、审批链接、fake CPA 的 v8 门禁比原估多 |

这些只是估时调整，不改 AC、不改范围；PM 的 `follow.py --plan` 若把超过 90 分钟的切片标粗，按上表拆行即可。

## 8. 测试与 e2e 方案

### 8.1 Go 单测（internal/keyclient）

| 文件（计划） | 覆盖的断言 | 对应 AC |
|---|---|---|
| home_test.go | `--home` > `OMC_HOME` > 默认；相对路径解析；目录 0700；group/other 可写时 `home_insecure`；目录/文件布局 | AC-2、AC-8 |
| home_unix_test.go（`//go:build !windows`） | 私钥/信封/state/config/备份 0600；home 0700 | AC-2、AC-8 |
| home_windows_test.go（`//go:build windows`） | DACL 只有当前用户；无 Everyone/Users/Authenticated Users；文件继承 | AC-2、AC-8（Windows 缺口见 §8.5） |
| keystore_test.go | 生成 X25519；公钥 `x25519:<base64>`；复用不轮换；私钥长度/损坏；文件名百分号编码、保留名、长名、大小写碰撞 | AC-2、AC-8 |
| state_test.go | 版本、往返、损坏 JSON、未知版本、原子写、锁超时 | AC-3、AC-8 |
| accept_test.go | 外层对象/裸信封；`alg`/`v` 错误；私钥不匹配；>1 MiB；输入不回显；state 更新 | AC-1、AC-6、AC-8 |
| status_test.go | 本地视图；`--application-id` 关联；`--remote` 成功/部分失败/缺配置；只读 key | AC-3、AC-8 |
| runtime_test.go | Claude env + 冲突变量移除；Codex argv + `OMC_REPO_KEY`；OpenCode 合并配置 + 24 KiB 上限；argv 无明文 | AC-4 |
| exec_test.go | helper 进程看到变量；退出码透传；命令不存在；无临时文件；stdout/stderr 无明文 | AC-4、AC-6 |
| jsondoc_test.go | 保留未知顶层键；`env`/`provider` 类型错误拒绝；非 JSON 拒绝；写后重读 | AC-5 |
| tomlblock_test.go | 受管块增/换/删；保留其他表与注释；标记不配对拒绝；非受管同名表拒绝；`model_provider` 替换/恢复 | AC-5 |
| install_test.go | 三家写入；备份；警告；`--replace`；所有权；失败回滚；重装幂等 | AC-5、AC-8 |
| purge_test.go | 全清；`--runtime` 只清一个；`--dry-run`；指纹不匹配；缺文件幂等；state 最后删；备份只删含本 key 的 | AC-6、AC-8 |

命令层 cmd/oh-my-cpa/key_test.go：

- 合并后 `key --help` 列出六个命令；每个命令的 `--help` 退出 0。
- `key gen` 的 stdout 只有公钥一行；警告在 stderr。
- 未知命令/缺参数退出 2；`key_not_found` 退出 4；`base_url_required` 退出 3；`config_conflict` 退出 5。
- `key` 分发不启动服务、不读 `.env`、不需要数据库。

### 8.2 进程级测试（`key exec`）

用 `TestHelperProcess` 模式（`internal/mcpbridge/bridge_test.go` 已有同类用法）：父测试把 `os.Args[0]` 当子进程，设置 `GO_WANT_KEY_HELPER=1`，helper 在测试二进制里运行：

- Claude：断言 `ANTHROPIC_BASE_URL` 是根、`ANTHROPIC_AUTH_TOKEN` 已设置、`ANTHROPIC_API_KEY` 已移除；打印掩码后的变量名，不打印值。
- Codex：断言 `OMC_REPO_KEY` 已设置；遍历 `os.Args`，断言明文 key 不在任何参数里；断言 `-c model_provider=...`、`-c model_providers.oh-my-cpa.env_key=...`、`-c ...wire_api=responses` 都在。
- OpenCode：解析 `OPENCODE_CONFIG_CONTENT`，断言 `provider.oh-my-cpa.options.apiKey` 存在、`baseURL` 以 `/v1` 结尾，用户原有 `provider.other` 仍在。
- 退出码：helper 用 `os.Exit(7)`，父测试断言 `RunChild` 返回 7。
- 不落盘：在 `t.TempDir()` 的 home 里执行，前后快照文件列表，断言 exec 没有新增文件。
- 不打印：捕获 stdout/stderr，断言明文 key 字符串不出现；错误路径（信封缺失、私钥不匹配）同样不出现。

### 8.3 install 的临时配置测试

- 每个运行时用 `--config <t.TempDir()>/...`，不碰 `~/.claude` / `~/.codex` / `~/.config/opencode`。
- 预置「别人的配置」：Claude 的 `env.OTHER=1`、Codex 的 `[model_providers.other]` 和注释、OpenCode 的 `provider.other`；install 后断言这些原样保留。
- 断言备份文件存在、0600/ACL、内容等于写前文件；警告文本含目标路径和「plaintext」/「明文」，不含 key。
- 断言 install 后 `--replace` 语义：槽位是别的仓库时无 `--replace` 失败、有则成功；备份里是旧值。
- 断言 purge 后：本仓库字段/块删除，其他 provider/env 保留；Codex 的 `model_provider` 恢复原值或删除；state 的 `runtimes` 槽位清掉。
- JSONC：给 `opencode.json` 加 `//` 注释，install 报 `config_not_json`，文件字节不变。
- TOML 异常：开始标记无结束标记、非受管 `[model_providers.oh-my-cpa]`，都报 `config_conflict`，文件字节不变。

### 8.4 e2e（`//go:build e2e`）

目标文件：internal/keyclient/e2e/{e2e_test.go,helper_test.go,fakecpa_test.go}。

**启动**

1. `go build -trimpath -o <tmp>/oh-my-cpa ./cmd/oh-my-cpa`。`internal/web/embed.go` 的 `//go:embed all:dist` 在只有 `.gitkeep` 时也能编译，所以 e2e 不需要先 `pnpm build`；服务只走 API，不开浏览器。
2. fake CPA 用 `httptest.NewServer`：
   - `GET /v8/management/config/config-version` → `8`
   - `GET /v8/management/config` → `{"config-version":8,"access":{"api-keys":[]}}`
   - `PATCH /v8/management/config` → 合并 `access.api-keys`（apply/approve 时 `UpdateClientAPIKeys` 会用到）
   - `POST /v1/messages` → 校验 `Authorization: Bearer <当前 api-keys 里的某个 key>`，返回 200 的最小 Anthropic JSON；不是 401
3. 真实 OMC 二进制：
   - `OMCPA_DATA_DIR=<tmp>/data`
   - `OMCPA_MASTER_KEY=<64 hex>`
   - `OMCPA_CPA_BASE_URL=<fake CPA URL>`
   - `OMCPA_CPA_MANAGEMENT_KEY=<e2e management key>`
   - `OMCPA_LISTEN_ADDR=127.0.0.1:<free port>`
   - `OMCPA_BASE_PATH=/omc`
   - `OMCPA_ENV_FILE=<tmp>/empty.env`
   - 关掉后台采集/额度轮询（`OMCPA_USAGE_INGEST_MODE=off`、`OMCPA_QUOTA_SCHEDULER_ENABLED=false`），只留仓库 key 需要的管理调用。
   - 轮询健康检查直到 200；失败时收集 stderr 并 fail。

**链路**

1. `key gen --home <tmp>/home --repo org/repo --base-url <fake CPA URL>`；stdout 取公钥。
2. 直接调 OMC capability HTTP：`POST /omc/api/v1/capabilities/invoke`，Bearer 管理密钥，`X-Omc-Agent: e2e-agent`、`X-Omc-Device: e2e-device`，`{"name":"repo_key_apply","arguments":{"repo":"org/repo","purpose":"e2e","public_key":"<公钥>"}}`；取 `data.application_id` 和 `data.approval_token`。
3. 审批：`POST /omc/api/v1/management/repo-keys/apply-link/<approval_token>/approve`，Bearer 管理密钥，空 body（`decodeOptionalManagementJSON` 允许）。
4. 取密文：`POST /omc/api/v1/capabilities/invoke`，`repo_key_ciphertext`，`{"application_id":"<id>"}`；把整个 `data` JSON 喂给 `key accept --home <tmp>/home --repo org/repo` 的 stdin。
5. `key exec --home <tmp>/home --runtime claude --repo org/repo -- <helper>`；helper 打印掩码变量，并用 `ANTHROPIC_AUTH_TOKEN` 调 fake CPA `/v1/messages`，打印 HTTP 状态。断言 200、掩码、无明文。
6. `key status --home <tmp>/home --json`：断言 repo、公钥、信封、`approved`、`key_mask`。
7. `key purge --home <tmp>/home --repo org/repo`：断言私钥/信封/state 记录不存在。
8. 再跑同一个 `key exec`：断言退出码 4、stderr 有 `key_not_found`、helper 没有运行（helper 写一个 marker 文件，断言不存在）。
9. `key status --remote`：在步骤 2 之后、3 之前跑一次，断言 `pending`；3 之后跑一次，断言 `approved`。这一步复用真实 OMC 的 `repo_key_status` / `repo_key_query`。

**补充**

- `key install` 的三家写在同一个 e2e 里用临时 `--config` 再做一遍，断言备份、警告、purge 后配置恢复；不需要真实 Claude/Codex/OpenCode 可执行文件。
- 真实运行时探针（本机 Claude Code / Codex / OpenCode 是否按注入生效）不在自动 e2e 里；S8 验收在本机手动跑，记录版本和结果，见 §11。
- e2e 只证明「key 被一个会校验 bearer 的网关接受」，不证明真实 CPA 的路由/模型配置；真实 CPA 的一次成功请求是人工验收项。

### 8.5 Windows / Linux 可行性

| 检查 | 本机 Windows | Docker/Linux | 说明 |
|---|---|---|---|
| Go 单测 | 本机没有 Go 二进制，不能直接跑 | 可以 | 用 `omc-go-gate-docker-recipe` 的配方；`golang:1.27.1` + 模块缓存卷 |
| Unix 0600/0700 | 不适用 | 可以，且是主要证据 | `home_unix_test.go` |
| Windows ACL | 需要 Windows Go 工具链；本机没有 | 只能 `GOOS=windows go build` 编译检查 | ACL 运行时断言是缺口；见 §11 |
| JSON/TOML install | 可在 Windows Go 下跑；当前用 Docker | 可以 | 临时 `--config`，不碰真实用户配置 |
| e2e | 可在 Windows Go 下跑；当前用 Docker | 可以 | fake CPA + 真实 OMC + helper；不需要浏览器 |
| 真实 Claude/Codex/OpenCode | 三家本机都有，可手动探针 | 不建议 | 不进 CI；S8 记录版本与结果 |
| `pnpm check-docs` | 可以（Node 24） | 可以 | 文档门禁 |
| `pnpm verify` | 可以；有既有 Windows 红项 | 可以 | 不把基线红项算成本次引入 |

### 8.6 0600 / ACL 断言

- Unix：`os.OpenFile(path, O_CREATE|O_EXCL, 0600)` + `os.Chmod(path, 0600)`；测试断言 `info.Mode().Perm() == 0600`，home 为 `0700`。
- Windows：**不写** `info.Mode().Perm() == 0600`。`os.Chmod` 在 Windows 只切只读位，这个断言会必红且没有意义。改为：
  - `home_windows.go` 用 `golang.org/x/sys/windows`（已是直接依赖，`internal/repository/disk_free_windows.go` 在用）构造只含当前用户 SID 的 DACL，`SetNamedSecurityInfo` 加 `PROTECTED_DACL_SECURITY_INFORMATION` 去掉继承；目录 ACE 带 `OBJECT_INHERIT_ACE|CONTAINER_INHERIT_ACE`。
  - `home_windows_test.go` 读回 DACL，断言没有 `Everyone` / `BUILTIN\Users` / `Authenticated Users` 的 allow ACE，当前用户 SID 有 full control。
  - `SetNamedSecurityInfo` 失败（FAT32/exFAT、部分网络盘）时返回 `home_insecure`，宁可拒绝写私钥，也不写一个大家可读的文件。
- 测试文件按 `//go:build !windows` / `//go:build windows` 分开，不用 `t.Skip` 把 Linux 上的 Windows 断言绕过去。

### 8.7 命令与门禁

```bash
# 文档（本计划提交时）
node scripts/check-docs.mjs
node --test scripts/check-docs.test.mjs

# Go 单测（Docker/Linux，按既有配方）
docker run --rm --network none \
  -v "<worktree>:/src:ro" \
  -v omc-go-mod:/go/pkg/mod \
  -v omc-gocache:/root/.cache/go-build \
  -w /src \
  -e GOPROXY=off -e GOFLAGS=-mod=readonly -e TZ=UTC \
  -e ASSISTANT_NO_SEND=1 -e OMCPA_ENV_FILE=/dev/null \
  golang:1.27.1 go test -count=1 ./internal/keyclient ./cmd/oh-my-cpa

# vet（同上）
docker run ... golang:1.27.1 go vet ./internal/keyclient ./cmd/oh-my-cpa

# e2e（同上，需要可写的 /tmp；源码仍只读）
docker run ... golang:1.27.1 go test -tags e2e -count=1 ./internal/keyclient/e2e

# Windows 编译检查（不运行 ACL）
docker run ... -e GOOS=windows golang:1.27.1 go build ./internal/keyclient ./cmd/oh-my-cpa

# 本机文档/静态门禁
pnpm test:fast
pnpm verify
```

所有测试命令带 `ASSISTANT_NO_SEND=1`。e2e 加 `package.json` 的 `test:key-e2e` 只是入口，不会进 `pnpm verify` 的默认门禁；是否进 CI 由 PM 决定（见 §12）。

## 9. 文档清单

| 文档 | 改什么 | 切片 |
|---|---|---|
| docs/repo-key-client.md（新增） | agent 可直接照做的七步运行手册：命令、输出、三家注入表、禁止事项、排错、退出码、purge 只清本地的说明 | S5 |
| `docs/agent-capabilities.md` | 在「Repository-owned API keys」节补客户端指针：`oh-my-cpa key` 的七个命令、`key accept` 的角色、私钥/明文边界、信封不是明文 | S5 |
| `docs/architecture.md` | 在 `oh-my-cpa mcp` 的入口说明旁补 `oh-my-cpa key`（第三个本地入口，config 之前分发、不打开数据目录）；包表加 internal/keyclient 一行 | S5 |
| `docs/operations.md` | 环境变量表加 `OMC_HOME`；说明 `key status --remote` 复用 `OMCPA_SERVER_URL` / `OMCPA_CPA_MANAGEMENT_KEY`；`OMCPA_CPA_BASE_URL` 的复用 | S5 |
| `docs/install.md` | 「Native executable」/「From source」后补一句：二进制自带 `key`，用法见运行手册；安装不生成 key | S5 |
| `docs/install-for-agents.md` | 同上，给 agent 的 runbook 加一段「装完后 agent 怎么用 key」 | S5 |
| `docs/testing.md` | 在 claim map 加：internal/keyclient 单测、进程级注入测试、install 临时配置、e2e 的归属；Unix/Windows 权限断言的分层 | S5（等 S2a–S4 的测试名确定后补） |
| `README.md` / `README.zh-CN.md` | 建议不改：这是 agent 使用端能力，不是新用户安装后第一眼要看的入口；若 PM 认为要提一句，加一条能力列表即可 | 待定 |

运行手册大纲：

1. 这是什么、七步图、为什么 key 不经过 OMC。
2. 开始前：`OMC_HOME`、`OMCPA_CPA_BASE_URL`、`OMCPA_AGENT_NAME` / `OMCPA_DEVICE_NAME`（MCP apply 的审批页要显示正确的 agent/device）。
3. 七步逐条命令（含 `key accept` 的 heredoc 示例、`key status --remote` 的 application id）。
4. 三家运行时表：exec 变量、install 路径、exec 的限制（Codex 必须是 `codex` 本身）。
5. 禁止事项：不进日志、不进对话、不进 git、不提交 `.claude/settings.json` / `config.toml` / `opencode.json`；不把 `$OMC_HOME` 放进仓库；不把私钥/明文贴进 issue/PR/截图。
6. 安全模型：明文只在子进程环境；同用户进程可读 `/proc/<pid>/environ` 或内存；install 会把明文留在磁盘；备份也有明文风险。
7. 排错：错误码表、`key status`、备份位置、如何重装/清理。
8. 「purge 只清本地」：停用/轮换/重新签发去控制台。

## 10. AC-1～9 映射

| AC | 要求 | 切片 | 测试/证据 | 缺口 |
|---|---|---|---|---|
| AC-1 | 七步闭环可执行：`status` / `gen` / `exec` / `install` / `purge` 都在，行为与需求第 1～7 步一致 | S2a、S2b、S3、S4、S5、S8 | `key --help` 六命令测试；各命令单测；e2e 七步；运行手册 | 无（真实运行时探针见 AC-7 缺口） |
| AC-2 | `key gen --repo` 生成 X25519，私钥落 `$OMC_HOME/keys/` 下（文件名规则见 §4.2）且 0600，stdout 只有 `x25519:<base64>` | S2a | `keystore_test.go`；`home_unix_test.go` 0600；`key_test.go` stdout 纯度；e2e | Windows 0600 用 ACL 等价断言，运行时 ACL 测试见 §11 |
| AC-3 | `key status` 看得见本机私钥、绑过的仓库、最后一次审批状态 | S2b | `status_test.go` 本地/`--remote`；e2e 的 pending/approved；`key status --json` schema | 拒绝/停用状态的远端刷新只测 mock + e2e 的 pending/approved；真实拒绝需手工 |
| AC-4 | `key exec --runtime ... -- <命令>` 内存解密，只给子进程注入该运行时变量，不打印明文、不落盘 | S3 | `runtime_test.go`；`exec_test.go` helper 进程；e2e 掩码 helper；stdout/stderr 不含明文；exec 前后文件快照 | 真实 Claude/Codex/OpenCode 是否按注入生效是手工探针，见 §11 |
| AC-5 | `key install --runtime` 显式写对应配置，写前备份并打警告 | S4 | `install_test.go` 三家；`jsondoc_test.go` / `tomlblock_test.go`；备份/警告/`--replace`/回滚；e2e 临时配置 | Windows ACL 对备份文件的运行时断言同 AC-2 |
| AC-6 | `key purge --repo` 清私钥和写过的配置项；purge 后同一 `exec` 失败并给可读错误 | S3、S4、S8 | `purge_test.go` 全清/单运行时/dry-run/指纹不匹配/幂等；e2e 步骤 7～8 | 真实 CPA 里 key 仍在是预期；输出会明说 |
| AC-7 | 端到端：keygen → apply → 审批 → ciphertext → exec 掩码脚本 → 用该 key 打一次 CPA 网关成功 → purge 后 exec 失败 | S7、S8 | `go test -tags e2e ./internal/keyclient/e2e`；fake CPA + 真实 OMC 二进制 + capability HTTP + 审批链接 + helper | 「真实 CPA 网关」只做到 fake CPA 的 bearer 校验；真实 CPA 的一次请求是人工验收，见 §11 |
| AC-8 | 单测：`key status`、`key purge`；私钥权限 0600 断言 | S2a、S2b、S4 | `status_test.go`、`purge_test.go`、`home_unix_test.go`、`home_windows_test.go` | Windows ACL 运行时断言依赖 Windows Go runner；见 §11 |
| AC-9 | 文档：agent 可直接照做的使用说明，含禁止事项（不进日志、不进对话、不进 git、不提交配置） | S5 | docs/repo-key-client.md 逐条 review；`node scripts/check-docs.mjs`；S9/S11 按清单核对 | 文档可读性是人工 review，不是自动化断言 |

## 11. 未验证缺口与风险

1. **真实 CPA 网关**：自动 e2e 用 fake CPA 校验 bearer；真实 CPA 的路由、模型、`/v1/messages` 行为没有自动证据。需要一次人工验收：真实仓库、真实 CPA、`key exec --runtime claude -- claude -p ...`（或等价的只读请求），记录响应。不得用 fake CPA 冒充 AC-7 的「真实网关」。
2. **真实运行时是否按注入生效**：
   - Claude Code 的 OAuth 登录态是否优先于 `ANTHROPIC_AUTH_TOKEN` 未实测；S3 先跑探针，若 OAuth 优先，运行手册要求退出登录或使用 `--settings`。
   - Codex 的 `-c` 位置（必须在子命令前）和 `env_key` 读取已在 0.167.1 实测；其他版本可能不同。`exec` 只接受 `codex` 本身，包装脚本不支持。
   - OpenCode 1.19.5 的 `OPENCODE_CONFIG_CONTENT` 已实测生效；与文件配置的合并语义未定，所以我们发送完整合并 JSON。v2 schema 出现后本工具会写 v1，可能被忽略；运行手册要求核对 `opencode --version`。
3. **Windows ACL**：代码用 `x/sys/windows` 构造 DACL，但本机没有 Windows Go 工具链，运行时断言没有本地证据；只能做 `GOOS=windows` 编译检查。需要在有 Windows Go runner 的环境跑 `home_windows_test.go`，或由 PM 接受「编译 + 代码审查」作为本轮证据。
4. **Windows 文件系统**：FAT32/exFAT/部分网络盘不支持 ACL，`home_insecure` 会拒绝写私钥；这是有意为之，但会让 `$OMC_HOME` 放在这类盘上的用户用不了。运行手册说明把 home 放在 NTFS 用户目录。
5. **OpenCode `models` 与 `npm`**：`provider.oh-my-cpa` 没有 `models` 映射时，某些 OpenCode 版本可能不显示模型。本计划不自动生成模型列表（需要网关 `/v1/models` 和选择策略，超出范围）。如果 S8 证明必须写，PM 决定是否加 `--model`（范围变更）。`npm: "@ai-sdk/openai-compatible"` 是按自定义 provider 的常见写法；S4 用 `opencode debug config` 验证，若该版本不需要或包名不同，只调整 install 的常量，不改用户配置。
6. **同一用户多 agent 共用 home**：默认 `~/.omc` 是一用户一份；两个 agent 不设各自的 `OMC_HOME` 时会共用一对密钥。需求说「每个 agent 各一对」，运行手册给出 per-agent home 约定，但工具不强制。见 §12 第 1 条。
7. **`key status --remote` 持管理密钥**：这是 key 客户端唯一的网络路径，只读；如果把只读 MCP key 放进 `OMCPA_CPA_MANAGEMENT_KEY`，也能工作。若 PM 认为客户端不应接触任何管理密钥，砍掉 `--remote`，AC-3 退化为「本地最后一次已知状态」。
8. **信封进入 transcript**：`repo_key_ciphertext` 的密文会出现在 agent 的对话里，`key accept` 又要求把它通过 stdin 传入；密文本身不是明文，但没有私钥也能被转发。运行手册说明「信封不是明文，但也不要提交/分享」。
9. **purge 不吊销服务端 key**：用户可能以为 purge 等于停用；命令输出和运行手册都明说，但这是产品语义风险，需要在验收时确认文案够清楚。
10. **备份里的明文**：install 的备份可能含用户原有的其他密钥；purge 只删含本仓库明文的备份，其他保留。备份目录本身需要 0700/ACL，否则等于把用户的其他密钥暴露给同机其他用户。
11. **`internal/web/dist` 只有 `.gitkeep`**：e2e 的 `go build` 能编译，但二进制只提供 placeholder 页面；e2e 不测浏览器。如果 S7 需要真实 SPA，必须先 `pnpm build`，但那会把 e2e 变成构建依赖，当前不需要。
12. **Windows 基线红项**：`internal/app` 的 retention 时区测试、`test:self` 的符号链接/zip、并行探针的 socket 缓冲等是既有问题，不计入本次；验收报告要单列。
13. **项目级/配置层覆盖**：Claude Code 的项目本地/项目 settings 优先于用户级，Codex 的项目 `.codex/config.toml` 在受信任目录和 profile 层的 `model_provider` 也可能优先于用户级。`install` 只写用户级，运行手册要求检查项目级和 profile 同名配置；`exec` 的 `-c` 是 session 级，优先于这些层，所以 `exec` 不受这条影响。

## 12. 待定（PM 拍板）

1. **per-agent home**：默认 `~/.omc` + 运行手册约定 `~/.omc/<agent>`，还是按 `OMCPA_AGENT_NAME` 自动推导？推荐前者（可预测、`key status` 在任何 shell 都能看到同一个 home）；自动推导会让没设环境变量的 shell 看到空状态。若选自动，必须在 S2a 定死。
2. **`key accept --remote`**：本轮只做 stdin，还是也给 CLI 管理密钥直接取密文？推荐本轮 stdin（保持客户端默认离线；密文不是明文）；`--remote` 作为后续增强。若 PM 认为 stdin 对 agent 太别扭，可在 S2b 加，但要把管理密钥边界写进 AC。
3. **Codex exec 的限制**：只接受 `codex` 本身（推荐，避免 argv 明文和临时 home），还是支持包装脚本？支持包装脚本需要 `CODEX_HOME` 临时目录或 argv token，两者都有明显代价；推荐保持限制，运行手册写明。
4. **OpenCode `models`**：不写（推荐，本轮不猜模型），还是加 `--model`/自动拉 `/v1/models`？后者是范围变更，需要新的 AC。
5. **备份保留策略**：保留到用户手动删（推荐，除 purge 命中本 key 的备份），还是限制最近 N 份？限制会删掉恢复点；推荐不自动限。
6. **`key status --remote` 的凭据**：允许 `OMCPA_CPA_MANAGEMENT_KEY`（推荐，和 `mcp` 一致）还是只允许 `OMCPA_MCP_READONLY_KEY`？只允许只读 key 更安全，但用户可能没配。
7. **Windows ACL 证据**：接受 `GOOS=windows` 编译 + 代码审查（本机没有 Windows Go），还是要求 Mac/CI 的 Windows runner 跑 `home_windows_test.go`？推荐后者作为 AC-8 的完整证据，前者作为本轮可交付的最小证据。
8. **e2e 是否进 CI**：`test:key-e2e` 只作为本地入口（推荐，先不改变 CI 门禁），还是进 `verify:full`？进 CI 会增加构建/启动时间，且 fake CPA 需要维护。

## 13. 工作量与关键路径

| 切片 | 原估 | 本计划 | 说明 |
|---|---|---|---|
| S1 | 75 | 75 | 本稿 |
| S2a | —（原 S2 90） | 80 | home/ACL/store/gen/registry/main.go；ACL 和锁是新增量 |
| S2b | —（含在原 S2） | 70 | accept/status/remote；可与 S3/S4 并行 |
| S3 | 90 | 90 | 三家注入 + 进程级测试 |
| S4 | 75 | 110 | 两家 JSON + 一家 TOML + 备份 + 三套 purge + 测试；建议拆 S4a/S4b |
| S5 | 40 | 60 | 运行手册 + 六份现有文档同步 |
| S7 | 80 | 100 | fake CPA + 真实 OMC + 审批链接 + helper |
| S8 | 60 | 60 | 七步串联 + 证据 |
| S9 | 45 | 45 | 审查 |
| S10 | 60 | 60 | 返工 |
| S11 | 60 | 60 | 集成验收 |
| S12 | 20 | 20 | 推分支/开 PR |

关键路径（按拆分后）：

```text
S1 75
 → S2a 80
 → S4 110（S8 还等 S2b 70 / S3 90 / S7 100；S4 是最长的一支）
 → S8 60
 → S11 60
 → S12 20
```

关键路径是 S1→S2a→S4→S8→S11→S12，约 75 + 80 + 110 + 60 + 60 + 20 = 405 分钟；S9/S10 在 S2a–S5 之后并行插入，约 +105 分钟，总计约 510 分钟。峰值并发：S2a + S5 + S7（3 条），S2a 完成后 S2b + S3 + S4（3 条），加上 S5 同步/ S9 准备最多 4 条。比原估算的关键路径（S1→S2→S3→S9→S10→S11→S12 ≈ 440）长，是因为把 install/purge 和 e2e 的真实成本算进来了；并行度更高，S3/S4 不必等 status。

## 14. 方案自检（对应派发说明的 10 个问题）

| 派发问题 | 在哪一节 | 结论一句话 |
|---|---|---|
| 1. `$OMC_HOME` 怎么定 | §2.1、§4.1 | `OMC_HOME`，默认 `~/.omc`，0700/ACL；`--home` 可覆盖；per-agent 用不同 home |
| 2. base URL 从哪来 | §2.2 | `--base-url` > `OMCPA_CPA_BASE_URL` > `config.json` > `base_url_required`；Claude 用根，Codex/OpenCode 加 `/v1` |
| 3. 信封落盘归谁、0600 | §2.3、§5.4 | `key accept` 从 stdin 读、内存解密校验、0600 原子写；不做隐藏行为 |
| 4. status 的审批状态从哪读 | §2.4、§5.1 | 默认离线读 state；`--remote` 用 MCP 读能力刷新，缺配置明说 |
| 5. 文件名与仓库身份 | §2.5、§4.2 | `NormalizeRepo` + `slug-hash8`，Windows 安全、大小写/截断不撞 |
| 6. `key exec` 实现 | §2.6、§6.1 | 内存解密、继承 env、三家注入、Codex `-c`+`env_key`、退出码透传、不打印明文 |
| 7. 三家 install 落点与写法 | §2.7、§6.2 | Claude settings.json `env`；Codex config.toml 受管块；OpenCode opencode.json v1；不新增依赖 |
| 8. purge 范围 | §2.8、§6.4 | 私钥+信封+install 值+state；`--runtime` 只清一个；备份只删含本 key 的；明说不停用 CPA key |
| 9. 测试与 e2e | §8 | 单测/进程级/install 临时配置/e2e；Unix 0600 在 Docker，Windows ACL 单独断言；真实 CPA/运行时是人工缺口 |
| 10. AC 映射 | §10 | AC-1～9 逐条有切片、测试、缺口；AC-7 的真实网关部分如实标缺口 |

## 15. 交付前检查清单

- [x] `node scripts/check-docs.mjs --file docs/delivery/2026-11-17-omc-repo-key-client/impl-plan.md` 通过（missing-path=0、retired-reference=0、line-reference=0）。
- [x] `node --test scripts/check-docs.test.mjs` 通过（13 pass / 0 fail）。
- [x] `pnpm test:fast --base ba9e2ee7` 已跑：本文件通过；全量 docs 的失败项全部在基线已有的 T18 交付文档（9 missing-path + 25 line-reference），不是本文件引入，也没有改那些文件。
- [ ] 本文件里的命令面与 `--help` 草案一致；S2a/S2b/S3/S4 的文件归属无重叠。
- [ ] AC-1～9 每条都能在 §10 找到切片、测试/证据和缺口。
- [ ] §12 的待定项已由 PM 给出选择，或明确保留为「按推荐执行」。
