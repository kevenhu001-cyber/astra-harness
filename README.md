# Astra Harness

Uncertainty-Driven Software Engineering Runtime (v0.1).

[![CI](https://github.com/kevenhu001-cyber/astra-harness/actions/workflows/ci.yml/badge.svg)](https://github.com/kevenhu001-cyber/astra-harness/actions/workflows/ci.yml)

Astra 不是又一个 “Chat + Edit + Run” 的 Coding Agent 外壳。它把 **Knowledge State、Claim、Evidence、Unknown、Goal、Action、Execution** 作为一等公民，运行闭环是：

> Understand → Build Knowledge State → Identify Unknowns → Estimate Importance → Choose Best Action → Execute → Collect Evidence → Update Knowledge State → Verify Goal → Repeat

Agent 是可替换、可销毁的计算资源；持久化的系统智能存在于 State Core 与 Evidence System。

## 功能

- **TUI**：对标 Claude Code / Codex CLI / OpenCode 的交互体验
  - 三个区域：标题栏（品牌、provider/model、模式、branch、goal progress）、聊天主区、可切换的侧边栏（Ctrl+B）
    - 侧边栏四种模式（j/k 导航，tab/m 切换）：Sessions · Files · Knowledge · Activity
    - 点击/回车在侧边栏直接打开 claim/unknown/file overlay
  - 流式 Markdown 渲染：glamour + chroma 语法高亮（go/python/rs/ts/json/yaml/toml/md/diff 等）
  - 工具调用卡片：edit_file/write_file 自动识别为 unified diff，红绿着色，文件夹上下文清晰
  - 权限弹层：`y/a/n/N/esc` 会话级「always 允许/拒绝」
  - Agent 提问弹层：纯文本回答模式（plain composer）
  - 斜杠命令补全：分类展示（Session / Knowledge / Model / Safety / Build / Files / Help），含快捷键提示
  - `/`、`@`、`!` 三种快捷输入：
    - `/` 进入命令面板
    - `@` 进入文件补全（基于知识索引）
    - `!` 进入 shell 模式（本地 30s 超时，保留环境变量）
  - ⌘K 命令面板：模糊匹配，键盘导航，分组着色
  - 状态栏实时显示：Provider/Model、权限模式、branch、Goal 进度、Claim/Unknown/Evidence 计数、Token 用量与累计 USD 成本
  - 键盘：`enter` 发送，`alt+enter` 换行，`ctrl+c` 停止/退出，`ctrl+u/d` 滚动，`x`/`ctrl+o` 折叠工具输出，`?`/`F1` 帮助，`ctrl+b` 侧边栏，`ctrl+k` 调色板，`ctrl+l` 清屏，`ctrl+t` 新会话，`ctrl+↑/↓` 历史，`/undo` 撤回一轮

- **Slash 命令全集**（按类别）

  | 类别 | 命令 |
  | --- | --- |
  | Session | `/help /status /goal /claims /unknowns /evidence /actions /events /sessions /resume /rename /export /clear /new /quit` |
  | Knowledge | `/tree` 项目文件树 |
  | Build | `/init /index /verify /commit /branch /diff /diff-base [base]` |
  | Model | `/model /provider /cost /stats /reasoning [low|medium|high|xhigh]` |
  | Safety | `/permissions /plan /undo` |
  | Files | `/add-file` 直接预览文件 |
  | Help | `/theme [name] /paste /mcp /agents /tasks /debug`

## CodeX 对齐

Astra 的 TUI 与 CodeX / OpenCode 共享同样的输入模型，并针对 Astra 的不确定性内核做了扩展：

- **可切换主题**：`/theme astra-dark | astra-light | mono` 实时重绘所有 chip / box / diff 样式，`CurrentTheme()` 在 `/debug` 中可见
- **Ctrl+R 反向增量搜索**：bash/zsh-style `fzf`-like 搜索 composer 历史，Ctrl+R 上一条、Ctrl+S 下一条、Enter 提交、Esc 取消
- **Bash 流式输出**：`!` 模式通过 `cmd.StdoutPipe()` + `bufio.Scanner` 行级推送，命令未结束前就能看到部分结果
- **@-file live 预览**：在 @-completion 弹层里选中候选时，下方显示该文件的前 8 行（行号 + 截断），无需切换 sidebar
- **Tabbed overlays**：`/claims /unknowns /evidence /actions /events /sessions /help /models` 都支持 `←/→/数字键` 切换分组（如 Claims = By status / By confidence、Models = Recent / Available / Configured），j/k 只在当前分组内循环
- **Recent models**：`/model` 后切换过的 provider|model 自动写入 `.astra/config.json`，下次启动可一键回到
- **Reasoning effort chip**：状态栏实时显示 `reason=high`（o-series），`/reasoning low|medium|high|xhigh` 调整并持久化
- **Cache / reasoning token chip**：在底部状态栏显示 `cache N` `reason N`，由 Provider 自行上报 `llm.Usage.CacheReadTokens / ReasoningTokens`
- **Stats overlay（`/stats`）**：当前 session 的 turns / 工具成功率 / token / cost / 知识存量 / 磁盘上 sessions 总数
- **Diff vs base**：`/diff` 仍然是 unstaged，`/diff-base [main]` 显示 HEAD vs base 分支
- **会话改名**：`/rename <new-id>`（路径分隔符和 `..` 会被拒绝）
- **AGENTS.md 指令支持**：从项目根到当前目录逐层收集 `AGENTS.md`（`AGENTS.override.md` 优先），拼接注入系统提示，默认 32 KiB 上限（`max_project_doc_bytes` 可调）
- **自动上下文压缩**：`max_context_tokens` 预算的 80% 阈值触发，按 token 估算自动 `/compact`，长会话不炸上下文
- **瞬态错误重试**：模型调用遇 429/5xx/网络错误自动退避重试（1s/2s/4s，最多 3 次），部分输出已发出则不重试
- **MCP 客户端**：双 transport——stdio（JSON-RPC 2.0，兼容 newline 与 Content-Length 帧格式）与 streamable-HTTP（会话头 + JSON/SSE 响应）；`mcp_servers` 配置，工具以 `mcp__<server>__<tool>` 暴露，调用走 EXECUTE 权限闸门，支持 per-tool `disabled` 配置（对齐 Codex `[mcp_servers.<name>.tools.<tool>]`）
- **State Core**：`.astra/` 下的 Event Sourcing（`events.jsonl`）+ 物化状态（`state.json`），可回放、可恢复
- **Knowledge Engine**：文件/符号/测试索引（Go、Rust、Python、TS/JS、Java、Kotlin、C/C++、C#、PHP、Ruby），ripgrep 检索 + 符号匹配排序
- **Uncertainty Engine**：`priority = impact × uncertainty × dependency_weight ÷ resolution_cost`
- **Decision Engine**：`utility = goal_progress × goal_weight + info_gain × uncertainty_weight − cost × cost_weight − risk × risk_weight`，并给出 Next Best Action- **Agent Runtime**：OpenAI 兼容协议（OpenAI / DeepSeek / Qwen / OpenRouter / Ollama / 本地模型）+ Anthropic Messages API，统一流式接口与工具调用；token 用量解析兼容双命名约定（Anthropic `input_tokens/output_tokens` 与 OpenAI 兼容 `prompt_tokens/completion_tokens`），OpenAI / DeepSeek / Qwen 后端不再报 0 用量
- **Exec 流式执行**：`run_command` 的 stdout/stderr 按行分批推送（`EvToolStream` 事件），TUI 与无头模式实时显示部分输出；进程绑定运行上下文（ctrl+c 即杀）与超时，超时强制终止
- **apply_patch 工具**：Codex 兼容的补丁格式（`*** Begin Patch` / `*** Update File` / `@@` 上下文锚点 / `-`+` ` 变更行 / `*** Add File` / `*** Delete File` / `*** Move to` / `*** End of File`），一次调用多文件增删改与重命名；hunk 匹配带三档宽松（exact → 去尾空白 → 全 trim），上下文锚点精确定位，失败时文件保持不变
- **Lifecycle Hooks**（对齐 Codex hook_runtime.rs）：`PreToolUse` / `PostToolUse` / `PreCompact` / `PostCompact` 四类命令式钩子，`hooks` 配置注册，stdin 传 JSON 载荷（工具名/参数/结果），Pre 钩子非零退出即拦截（fail-closed），Post 钩子输出进系统事件，可按工具名过滤
- **跨会话记忆**：State Core 的持久化结论在每次 Run 开始时按**目标相关性**激活——Claim 按 Goal 词项重叠 + 置信度 + 新鲜度排序注入编译状态（上限 8 条防上下文膨胀，`…N more` 截断提示），并输出记忆摘要系统事件
- **Permission 模型**：READ / WRITE / EXECUTE / NETWORK / CREDENTIAL / DEPLOY / DELETE；`ask | allow | deny` 三档 + 会话级 always 决策 + Plan 模式；edit_file/write_file 走 WRITE 检查，run_command/MCP 走 EXECUTE 检查，deny 模式等价只读
- **Verification**：自动识别 `go test`、`cargo test`、`npm test`、`pytest`、`gradle test`、`make test` 与对应 build 命令，测试/构建结果自动沉淀为 Evidence 与 Claim
- **State Compiler**：把 Goal、Verified Claims、Top Unknowns、Recent Actions、Next Best Action 编译为决策上下文，而不是堆聊天历史

## 快速开始

```bash
# 本地构建（CI 也会在 GitHub Actions 上远程编译）
go build -o astra ./cmd/astra

# 或直接使用 CI 产物 / release binaries
./astra              # 进入 TUI
./astra init         # 初始化 .astra 状态与索引
./astra "实现用户登录的 JWT 刷新"   # 带初始任务进入 TUI
./astra run "重构数据库层" --yes    # 无头运行，自动批准权限
./astra status       # 查看编译后的知识状态
./astra unknowns     # 查看按优先级排序的 Unknowns  ./astra verify       # 跑测试/构建并记录证据
  ./astra login        # 通过官网设备码登录 Astra 账号
  ./astra whoami       # 查看当前登录账号

# 账号服务（官网 + 认证 API）
./astra-auth --addr :8080 --base-url http://localhost:8080
```

## 账号与登录

- **官网**（`internal/authsrv/site`，go:embed 进 `astra-auth` 二进制）：首页 / 登录注册 / 设备授权 / 账号管理四页，视觉对齐 Socrates site 的暗色中性体系（纯黑底、细边框、小圆角、Geist 字体、内联 SVG 图标，无渐变、无蓝紫色、无 emoji）
- **邮箱注册**（机制对齐 Socrates `pending_registrations`）：注册只写 pending + 32 位验证 token（24h），点击邮件链接才建号并自动登录——重复注册静默返回 ok（防枚举）；8-64 位密码、bcrypt 哈希
- **TUI/CLI 登录（设备码流，RFC 8628 风格）**：`astra login` 或 TUI 内 `/login` 调起浏览器打开官网授权页 → 用户在网页登录/注册后批准 → CLI 轮询拿到 bearer token 存入 `~/.config/astra/auth.json`（`ASTRA_CONFIG_DIR` 可重定向）；状态栏显示 `acct 邮箱`，`/whoami`、`/logout` 配套
- **服务器地址**：`auth_server` 配置键或 `ASTRA_AUTH_SERVER` 环境变量，默认 `http://localhost:8080`；邮件默认打印到服务器日志（ConsoleMailer），配 `SMTP_HOST/SMTP_PORT/SMTP_USER/SMTP_PASS/SMTP_FROM` 即发真实邮件
- **会话安全**：httpOnly + SameSite=Lax 的 `sid` cookie、POST 带 Origin 校验（CSRF）、Bearer token 30 天有效期、账号页可查看/吊销 token

## 配置

首次启动会在项目 `.astra/config.json` 写入默认配置。也可以在 `~/.config/astra/config.json` 放全局配置，项目配置覆盖全局。

```json
{
  "default_provider": "anthropic",
  "default_model": "claude-sonnet-4-20250514",
  "permission_mode": "ask",
  "max_iterations": 20,
  "auto_verify": true,
  "max_project_doc_bytes": 32768,
  "hooks": [
    {
      "event": "PreToolUse",
      "tools": ["run_command", "apply_patch"],
      "command": "sh -c 'echo tool=$TOOL_NAME'"
    }
  ],
  "mcp_servers": [
    {
      "id": "github",
      "command": "mcp-github",
      "args": [],
      "env": {},
      "tools": {
        "secret": { "disabled": true }
      }
    },
    {
      "id": "remote",
      "type": "http",
      "url": "https://mcp.example.com/mcp",
      "headers": { "Authorization": "Bearer ..." }
    }
  ],
  "providers": [
    {
      "id": "openai",
      "type": "openai-compatible",
      "name": "OpenAI",
      "base_url": "https://api.openai.com/v1",
      "api_key_env": "OPENAI_API_KEY",
      "models": ["gpt-4o", "gpt-4o-mini"]
    },
    {
      "id": "anthropic",
      "type": "anthropic",
      "api_key_env": "ANTHROPIC_AUTH_TOKEN",
      "models": ["claude-sonnet-4-20250514"]
    }
  ]
}
```

环境变量：`ASTRA_PROVIDER`、`ASTRA_MODEL`、`ASTRA_PERMISSION_MODE`，以及各 Provider 的 API Key 环境变量（`OPENAI_API_KEY`、`ANTHROPIC_AUTH_TOKEN`、`DEEPSEEK_API_KEY`、`DASHSCOPE_API_KEY`、`OPENROUTER_API_KEY` 等）。

MCP 服务器在 `.astra/config.json` 的 `mcp_servers` 数组配置（`command` + `args` + `env`），启动时自动连接并暴露工具；`/mcp` 查看连接状态与工具清单。

## 状态模型

| 实体 | 说明 |
| --- | --- |
| Goal | 目标、优先级、Acceptance Criteria、进度 |
| Claim | subject/predicate/object，状态机 UNKNOWN → HYPOTHESIS → SUPPORTED/CONTRADICTED → VERIFIED → STALE |
| Evidence | SOURCE_CODE / TEST_RESULT / BUILD_RESULT / RUNTIME_RESULT / …，绑定 CodeState |
| Unknown | impact / confidence / resolution_cost / dependency_weight / priority |
| Action | SEARCH / READ / EDIT / RUN_TEST / RUN_BUILD / …，记录 cost / risk / expected_info_gain / utility |
| Event | 追加式事件日志，驱动 State Reducer |

所有重要结论默认不能直接成为 VERIFIED，必须由 Evidence 支撑；代码变化后旧 Evidence 的有效性自动重新评估：每次编辑、每次 Run 开始、每次 Verify 后，引擎会把绑定旧代码状态（`CodeState`）的 Evidence 标记为 `STALE`，并同步把引用它的 VERIFIED/SUPPORTED Claim 降级为 `STALE`，直到重新验证（`/verify` 或自动验证）在最新状态上建立新 Claim。过渡走事件日志（`EVIDENCE_UPDATED`），可回放。

## 目录结构

```text
cmd/astra          CLI 入口（含 login/logout/whoami 设备码登录）
cmd/astra-auth     官网 + 账号认证服务器二进制
internal/auth      设备码流客户端、凭证存储、浏览器唤起
internal/authsrv   Auth 服务器（注册/验证/登录/会话/设备授权）+ 嵌入的官网
internal/core      State Core、Event Sourcing、State Compiler、Decision/Uncertainty
internal/knowledge 索引、符号提取、检索、Git
internal/llm       Provider 抽象、OpenAI 兼容流式、Anthropic 流式
internal/engine    Agent Runtime、工具、权限、验证、主循环
internal/tui       Bubble Tea TUI（聊天、斜杠命令、弹层、权限/提问）
```

## 路线图（对应设计文档 Phase）

Phase 0–8 已具备基础实现（Protocol/Event Model、Repository Index、Knowledge State、Claim+Evidence、Unknown/Risk、Basic Decision、Agent Runtime、Execution/Verification）。后续：Parallel Experiments、Information Gain、Adaptive Model Routing、Long-running Autonomous Tasks、PostgreSQL/pgvector。

## 远程构建所有编译与测试由 [GitHub Actions](.github/workflows/ci.yml) 在远程完成：push 到 `main` 即触发 `gofmt` 检查、`go vet`、`go build`、`go test`；打 `v*` tag 自动产出 Linux/macOS/Windows 二进制。

LLM 层（`internal/llm`）已有完整单测覆盖：Usage 双命名解析、OpenAI 兼容流式（文本增量、tool_call 分片拼接、usage 尾块、HTTP 错误码可重试性判定、畸形行跳过）、Anthropic SSE 流式（text/input_json delta、message_start usage、错误事件）。
