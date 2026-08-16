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
- **State Core**：`.astra/` 下的 Event Sourcing（`events.jsonl`）+ 物化状态（`state.json`），可回放、可恢复
- **Knowledge Engine**：文件/符号/测试索引（Go、Rust、Python、TS/JS、Java、Kotlin、C/C++、C#、PHP、Ruby），ripgrep 检索 + 符号匹配排序
- **Uncertainty Engine**：`priority = impact × uncertainty × dependency_weight ÷ resolution_cost`
- **Decision Engine**：`utility = goal_progress × goal_weight + info_gain × uncertainty_weight − cost × cost_weight − risk × risk_weight`，并给出 Next Best Action
- **Agent Runtime**：OpenAI 兼容协议（OpenAI / DeepSeek / Qwen / OpenRouter / Ollama / 本地模型）+ Anthropic Messages API，统一流式接口与工具调用
- **Permission 模型**：READ / WRITE / EXECUTE / NETWORK / CREDENTIAL / DEPLOY / DELETE；`ask | allow | deny` 三档 + 会话级 always 决策 + Plan 模式
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
./astra unknowns     # 查看按优先级排序的 Unknowns
./astra verify       # 跑测试/构建并记录证据
```

## 配置

首次启动会在项目 `.astra/config.json` 写入默认配置。也可以在 `~/.config/astra/config.json` 放全局配置，项目配置覆盖全局。

```json
{
  "default_provider": "anthropic",
  "default_model": "claude-sonnet-4-20250514",
  "permission_mode": "ask",
  "max_iterations": 20,
  "auto_verify": true,
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

## 状态模型

| 实体 | 说明 |
| --- | --- |
| Goal | 目标、优先级、Acceptance Criteria、进度 |
| Claim | subject/predicate/object，状态机 UNKNOWN → HYPOTHESIS → SUPPORTED/CONTRADICTED → VERIFIED → STALE |
| Evidence | SOURCE_CODE / TEST_RESULT / BUILD_RESULT / RUNTIME_RESULT / …，绑定 CodeState |
| Unknown | impact / confidence / resolution_cost / dependency_weight / priority |
| Action | SEARCH / READ / EDIT / RUN_TEST / RUN_BUILD / …，记录 cost / risk / expected_info_gain / utility |
| Event | 追加式事件日志，驱动 State Reducer |

所有重要结论默认不能直接成为 VERIFIED，必须由 Evidence 支撑；代码变化后旧 Evidence 的有效性需重新评估。

## 目录结构

```text
cmd/astra          CLI 入口
internal/core      State Core、Event Sourcing、State Compiler、Decision/Uncertainty
internal/knowledge 索引、符号提取、检索、Git
internal/llm       Provider 抽象、OpenAI 兼容流式、Anthropic 流式
internal/engine    Agent Runtime、工具、权限、验证、主循环
internal/tui       Bubble Tea TUI（聊天、斜杠命令、弹层、权限/提问）
```

## 路线图（对应设计文档 Phase）

Phase 0–8 已具备基础实现（Protocol/Event Model、Repository Index、Knowledge State、Claim+Evidence、Unknown/Risk、Basic Decision、Agent Runtime、Execution/Verification）。后续：Parallel Experiments、Information Gain、Adaptive Model Routing、Long-running Autonomous Tasks、PostgreSQL/pgvector。

## 远程构建

所有编译与测试由 [GitHub Actions](.github/workflows/ci.yml) 在远程完成：push 到 `main` 即触发 `gofmt` 检查、`go vet`、`go build`、`go test`；打 `v*` tag 自动产出 Linux/macOS/Windows 二进制。
