# AgentMesh

**AgentMesh** 是一个基于 Go 构建的多模型提供商网关与 AI Agent 执行平台。它提供统一的模型接入接口、具备策略约束的 Model Gateway、本地 Coding Agent 进程适配器、可复用的 Tool System、持久化 Agent 任务调度，以及版本化 Workflow 执行引擎。

项目采用里程碑方式持续开发。本文档概述 **M1–M16** 已记录的能力与运行边界。详细设计决策、API 契约、数据库迁移和验收命令，请参阅 [`docs/milestones/`](docs/milestones/) 下的里程碑文档。

> English documentation: [`README.md`](README.md)

## 目录

- [核心能力](#核心能力)
- [整体架构](#整体架构)
- [环境要求](#环境要求)
- [快速开始](#快速开始)
- [配置说明](#配置说明)
- [模型提供商与 Model Gateway](#模型提供商与-model-gateway)
- [HTTP API 概览](#http-api-概览)
- [安全与运行边界](#安全与运行边界)
- [数据库与迁移](#数据库与迁移)
- [里程碑状态](#里程碑状态)
- [开发与验证](#开发与验证)
- [项目文档](#项目文档)

## 核心能力

| 模块 | 能力 |
|---|---|
| 模型提供商 | 支持 OpenAI、Anthropic、Gemini 和 Ollama 适配器 |
| Provider 实例 | 同一种 Provider 类型可以配置多个相互独立的实例 |
| Model Gateway | 模型解析、能力检查、输出 Token 限制、统一错误与流式事件 |
| 凭证管理 | 环境变量/数据库凭证解析、SQLite 加密存储、安全状态 API、Adapter 热更新 |
| Coding Agent | 支持 Claude Code、Codex CLI、Gemini CLI 和 OpenCode 进程适配 |
| Tool System | 工具注册、执行引擎、权限检查、审批机制、输入输出边界控制及内置工具 |
| Agent Runtime | 原生 Tool Calling、有步骤限制的 Agent Loop、任务取消、任务状态与消息持久化 |
| 持久化调度器 | 基于 SQLite 的任务队列、绑定所有者的 Lease、Heartbeat、重试边界及工具调用账本 |
| Workflow Runtime | 版本化 DAG 工作流、并行 Fan-out/Join、审批节点、持久化 Checkpoint |
| 可观测性 | Workflow 事件历史、SSE 事件流、节点执行历史和人工恢复能力 |
| 可靠性 | Fencing Token、拒绝过期 Worker 写入、有界 SSE 投递、多进程与崩溃恢复验收测试 |

## 整体架构

```text
                 ┌────────────────────────────┐
                 │        HTTP / CLI          │
                 └─────────────┬──────────────┘
                               │
             ┌─────────────────▼─────────────────┐
             │          Application API           │
             └──────────┬───────────────┬─────────┘
                        │               │
              ┌─────────▼──────┐  ┌─────▼──────────┐
              │  Model Gateway │  │ Agent / Workflow│
              └─────────┬──────┘  │    Runtime      │
                        │         └─────┬──────────┘
              ┌─────────▼──────┐        │
              │ Provider Runtime│  ┌────▼──────────┐
              │ + Adapter Factory│  │  Tool Engine  │
              └─────────┬──────┘  └────┬──────────┘
                        │              │
          ┌─────────────▼──────┐   ┌───▼────────────┐
          │ OpenAI / Anthropic │   │ Built-in Tools │
          │ Gemini / Ollama    │   │ + Tool Registry│
          └────────────────────┘   └────────────────┘

                    SQLite 持久化
       Provider/Model Catalog · Credentials · Tasks · Jobs
       Tool Ledger · Workflow Runs · Checkpoints
       Workflow Events · Execution History
```

Model Gateway 负责与 Provider 无关的模型执行策略。Agent Runtime 和 Workflow Runtime 构建在统一 Gateway 与 Tool Engine 之上。SQLite 用于本地持久化和任务协调；当前架构并未将 SQLite 定位为多主机分布式数据库。

## 环境要求

- Go **1.27.3**：与项目当前文档记录的版本一致。
- [`sqlc`](https://sqlc.dev/) **v1.31.1**：用于重新生成数据库查询代码。
- SQLite 使用 `modernc.org/sqlite`：**不需要 CGO 或 GCC**。

安装 sqlc：

```bash
go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1
sqlc version
```

## 快速开始

1. 克隆仓库并进入项目目录。
2. 复制示例配置文件。
3. 根据准备调用的模型配置相应 Provider 凭证。
4. 启动 AgentMesh。

```bash
cp configs/config.example.yaml configs/config.yaml

# 示例：通过环境变量配置 Provider 凭证
export OPENAI_PRIMARY_API_KEY="your-api-key"

go run ./cmd/agentmesh --config configs/config.yaml
```

服务监听地址由 `server.host` 和 `server.port` 配置；文档记录的默认地址为 `127.0.0.1:8080`。

健康检查：

```bash
curl http://127.0.0.1:8080/healthz
curl http://127.0.0.1:8080/readyz
```

具体配置字段以及 API 请求/响应契约，请以对应里程碑文档为准。当前 HTTP API 尚未提供认证与授权能力，因此请将监听地址限制在本机回环地址，不要直接暴露到公网。

## 配置说明

示例配置位于 [`configs/config.example.yaml`](configs/config.example.yaml)。复制为 `configs/config.yaml` 后，根据本地环境进行调整。

### Provider 与 Model 标识

AgentMesh 区分 Provider 厂商类型和具体配置实例：

- `providers[].type` 表示适配器类型，例如 `openai`、`anthropic`、`gemini` 或 `ollama`。
- `providers[].id` 表示一个具体配置实例，例如 `openai-primary` 或 `openai-backup`。

因此，同一 Provider 类型可以配置多个独立实例，并分别管理其模型、凭证和运行参数。

### 凭证管理

凭证支持从环境变量和加密存储中解析。凭证管理 API 只返回凭证状态，不返回密钥明文。SQLite 中保存的凭证使用 AES-256-GCM 加密；加密密钥由 KeyProvider 提供，不应与数据库文件一起存放，也不应提交到版本控制。

请妥善备份加密密钥。丢失密钥可能导致已存储凭证无法解密。

## 模型提供商与 Model Gateway

Model Gateway 为上层 Agent 和 Workflow 提供统一模型调用入口，并在 Provider Runtime 中选择对应适配器执行请求。

当前文档记录的模型适配器包括：

- OpenAI
- Anthropic
- Google Gemini
- Ollama

Gateway 负责模型解析、能力检查、输出 Token 限制、统一错误处理和流式事件转换。Provider 实例与模型目录相互区分，便于同一厂商配置多个实例。

## HTTP API 概览

以下列出项目文档中记录的主要 API。完整参数、请求体、响应结构和错误语义请查看对应里程碑文档。

| 功能 | 方法 | 路径 |
|---|---|---|
| 健康检查 | GET | `/healthz` |
| 就绪检查 | GET | `/readyz` |
| 创建 Agent 任务（同步） | POST | `/api/v1/agents/run` |
| 创建 Agent 队列任务 | POST | `/api/v1/agents/queue` |
| 查询 Agent Job | GET | `/api/v1/agents/jobs/:id` |
| 重试 Agent Job | POST | `/api/v1/agents/jobs/:id/retry` |
| 查询 Agent Task | GET | `/api/v1/agents/tasks/:id` |
| 取消 Agent Task | POST | `/api/v1/agents/tasks/:id/cancel` |
| 查询 Agent 消息 | GET | `/api/v1/agents/tasks/:id/messages` |
| 查询工具列表 | GET | `/api/v1/tools` |
| 调用工具 | POST | `/api/v1/tools/{name}/invoke` |
| 查询工具审计记录 | GET | `/api/v1/tools/audit?limit=100` |
| 创建/更新 Workflow 定义 | PUT | `/api/v1/workflows/:id` |
| 查询 Workflow 定义 | GET | `/api/v1/workflows/:id` |
| 创建 Workflow Run | POST | `/api/v1/workflows/:id/runs` |
| 查询 Workflow Runs | GET | `/api/v1/workflows/:id/runs` |
| 查询 Workflow Run | GET | `/api/v1/workflows/runs/:run_id` |
| 审批 Workflow Run | POST | `/api/v1/workflows/runs/:run_id/approve` |
| 拒绝 Workflow Run | POST | `/api/v1/workflows/runs/:run_id/reject` |
| 查询 Workflow 事件 | GET | `/api/v1/workflows/runs/:run_id/events` |
| Workflow SSE 事件流 | GET | `/api/v1/workflows/runs/:run_id/events/stream` |
| 查询 Workflow 历史 | GET | `/api/v1/workflows/runs/:run_id/history` |
| 查询节点执行记录 | GET | `/api/v1/workflows/runs/:run_id/nodes` |
| 人工恢复节点 | POST | `/api/v1/workflows/runs/:run_id/nodes/:node_id/recover` |

## 安全与运行边界

- **HTTP API 认证：** 当前 HTTP API 尚未提供认证和授权机制。建议仅绑定 `127.0.0.1`，并通过受控的反向代理或其他可信边界提供访问。
- **凭证安全：** 凭证在 SQLite 中加密存储；加密密钥必须独立保管，不能提交到 Git。
- **工具权限：** 文件访问受 Workspace 边界约束；Shell 执行采用显式命令白名单；写入和执行类操作应经过权限/审批控制。
- **SQLite 部署边界：** SQLite WAL 适合单机本地持久化和协调，不应据此推断支持多主机共享写入。
- **外部副作用：** 对第三方系统的外部操作无法普遍保证 Exactly-Once。对于结果不确定、可能已经产生外部副作用的调用，不应直接盲目重放。
- **工具审计：** 工具审计记录当前为内存实现，进程重启后不会保留；不要将其误认为持久化审计系统。
- **SSE：** 事件流采用有界批量读取和写入超时，客户端应支持断线重连及事件游标续传。

## 数据库与迁移

项目使用 SQLite 和 SQLC 管理数据访问：

- SQLite 驱动：`modernc.org/sqlite`，不依赖 CGO。
- SQL 查询代码：通过 `sqlc` 从 SQL 查询定义生成。
- Schema 变更：使用版本化 SQL Migration。
- 持久化范围包括 Provider/Model Catalog、Credentials、Agent Tasks/Jobs、Tool Invocation Ledger、Workflow Runs、Checkpoints、Workflow Events 和执行历史。

请按项目现有迁移顺序执行数据库迁移，不要在生产数据上随意重置或手工修改迁移版本。

## 里程碑状态

| 里程碑 | 主要交付 |
|---|---|
| M1-A ～ M1-C | Provider/Model Registry、Provider Runtime、Adapter Factory、Model Gateway 执行策略 |
| M1-D | Provider/Model Catalog 的 SQLite 持久化 |
| M1-E | HTTP Runtime 与 Catalog/Generation API |
| M1-F | 集成测试与基础验收 |
| M2 | 凭证领域、AES-256-GCM 加密存储、凭证解析、HTTP 管理 API、Adapter 热更新与连接测试 |
| M3 | Coding Agent CLI 适配器与 Workspace 管理 |
| M4 | Tool Registry、Tool Engine、权限控制、审批与内置工具 |
| M5 | Agent Loop、任务状态、取消、上下文管理与执行边界 |
| M6 | Provider 原生 Tool Calling、任务快照与消息持久化 |
| M7 | SQLite 持久化 Agent Job Queue、Lease、Heartbeat、恢复与工具调用账本 |
| M8 | 版本化 DAG Workflow、并行 Fan-out/Join、审批节点与持久化 Checkpoint |
| M9 | Workflow Event History、SSE、执行历史、人工恢复与可观测性 |
| M10 | Fencing Token、Lease Renewal、过期 Worker 写入保护与 SSE 背压控制 |
| M11 | 多进程竞争、Worker 崩溃恢复、过期写入拒绝及长任务 Lease 续期可靠性验收 |
| M12 | Goal 管理、成功条件、LLM Planner、Plan DAG 校验与不可变版本持久化 |
| M13 | Plan → Workflow 编译、Agent Profile 绑定、执行幂等预留与现有 Workflow Runtime 桥接 |
| M14 | Workflow 结果证据收集、成功条件评估、追加式评估记录与 Goal 完成状态 |

### M11 可靠性验收

M11 增加了面向关键故障场景的可靠性验收测试，覆盖：

- 多个独立 OS 进程竞争同一个 Workflow Job。
- Lease 持有者退出后，任务能够被后续 Worker 恢复。
- 旧 Fencing Token 的过期写入会被拒绝。
- 对结果不确定、可能已经产生外部副作用的工具调用，不进行无条件重放。
- 长时间运行的节点通过 Heartbeat 续期，避免仅因超过初始 Lease 时长而被错误接管。

运行验收脚本：

```bash
bash scripts/reliability/m11.sh
```

M11 的通过结果说明上述测试覆盖的场景通过了验收，不代表任意外部副作用都能实现 Exactly-Once，也不等同于已经完成所有生产负载、吞吐量和长期稳定性测试。

## 开发与验证

常用命令：

```bash
# 生成 SQLC 代码
sqlc generate

# 运行全部测试
go test ./...

# 运行竞态检测
go test -race ./...

# 静态检查
go vet ./...

# 编译
go build ./...

# M11 可靠性验收
bash scripts/reliability/m11.sh
```

请以当前仓库的 `go.mod`、配置示例和里程碑验收文档为最终依据。若本地 Go 版本与项目要求不一致，应先统一工具链版本，再判断构建或测试结果。

## 项目文档

- [`README.md`](README.md)：英文项目说明。
- [`docs/milestones/`](docs/milestones/)：各里程碑的设计、实现记录、API 契约和验收说明。
- [`configs/config.example.yaml`](configs/config.example.yaml)：示例配置文件。
- [`scripts/reliability/m11.sh`](scripts/reliability/m11.sh)：M11 可靠性验收脚本。

---

**AgentMesh** 的目标是提供统一的模型接入、可控的工具执行、可恢复的 Agent 任务调度，以及具备持久化与可观测性的 Workflow Runtime。

## M12 — Goal & Planning Core

M12 增加结构化 Goal、目标约束与成功条件、独立的 Planner 接口、基于 Model Gateway 的 LLM Planner、严格 JSON 输出解析、Plan DAG 校验、按 Goal 递增的不可变 Plan 版本、SQLite 持久化，以及 Goal/Plan HTTP API。

M12 只负责生成、校验和保存 Plan，不会执行 Plan Step，也不会创建 Workflow Run。Plan → Workflow DAG 的编译与执行桥接留给 M13。详细数据结构、API 路由、生命周期和验收命令见 [`docs/milestones/M12.md`](docs/milestones/M12.md)。

### M13 — Plan → Workflow Compiler

M13 将 M12 的不可变 Plan 版本编译为现有 M8 Workflow DAG，并通过 M8–M11 的持久化 Runtime 提交执行。新增 Plan 版本执行预留、确定性 Run ID、Agent/Condition/Approval/End 节点映射、显式 Agent Profile 绑定，以及通过纯 Start 节点支持多根 DAG。不会新增第二套队列，也不宣称支持节点级自动重试或任意外部副作用的 exactly-once。详见 [`docs/milestones/M13.md`](docs/milestones/M13.md)。

### M14 — Goal Outcome Evaluation & Completion

M14 补齐 Goal 生命周期闭环：读取终态 Workflow Run，将成功节点输出作为证据交给 LLM，对 Goal 的成功条件逐项评估；证据必须引用真实节点 ID 和节点输出中的精确文本；评估记录追加保存；只有 Workflow Run 成功且所有必需条件均满足时，才会在同一 SQLite 事务中将 Goal 标记为完成。失败、拒绝或取消的 Workflow Run 不会完成 Goal。详见 [`docs/milestones/M14.md`](docs/milestones/M14.md)。

新增接口：

- `POST /api/v1/goals/:id/evaluations`，请求体 `{"run_id":"...","model_id":"..."}`
- `GET /api/v1/goals/:id/evaluations`
- `GET /api/v1/goals/:id/evaluations/:run_id`

数据库迁移：`000014_goal_evaluations`。


## M15 — Reflection & Evidence-Grounded Learning

M15 在 M14 Goal Evaluation 之后增加 Reflection 层。它综合 Goal、不可变 Evaluation 和 Workflow 节点执行观察，提炼成功模式、失败原因、改进机会和风险。每条 Insight 都必须引用真实节点输出或错误中的精确文本，并在持久化前进行校验。Reflection 按 Goal + Run 幂等保存，并通过 SQLite Trigger 保持追加写入。M15 不会自动修改 Plan、Tool Policy、Agent Profile，也不等同于长期 Memory。

新增 API：

- `POST /api/v1/goals/:id/reflections`，请求体 `{"run_id":"...","model_id":"..."}`
- `GET /api/v1/goals/:id/reflections`
- `GET /api/v1/goals/:id/reflections/:run_id`

数据库迁移：`000015_goal_reflections`。详细设计见 `docs/milestones/M15.md`。

## M16 — Memory System（记忆系统）

M16 增加有证据来源的情景记忆（Episodic）、语义记忆（Semantic）和程序记忆（Procedural）。M15 Reflection 经验首先进入不可检索的 Candidate 状态，必须显式审核晋升后才能被 Agent 检索；候选可以拒绝，已激活记忆可以归档。SQLite 保存来源、生命周期和不可变的原始经验内容。首版使用确定性的词项检索，不引入向量数据库，并将有长度限制、明确标记为非指令的记忆上下文注入新创建的 M13 Workflow Run；Agent Scope 记忆按 Agent Profile 分别注入对应节点，不会跨 Profile 混用。数据库迁移为 `000016_memory_system` 和 `000017_memory_source_integrity`。详细规则、验收覆盖和限制见 `docs/milestones/M16.md`。

## M17–M22：AI Resource Pool & Gateway

M17–M22 在现有 Provider/Model Gateway 旁边新增独立的多账号资源池，不改变 M1–M16 的 Provider/Model ID 语义：

- Provider：协议类型（`openai-compatible`、`anthropic`、`gemini`）。
- Endpoint：实际 Base URL、超时和优先级。
- Credential：单个 API Key，使用与现有 credential store 相同的 AES-256-GCM 主密钥加密；列表只返回脱敏 Key。
- Model：AgentMesh 对外模型别名；每个 Credential 通过 `ai_credential_models` 映射到真实上游模型 ID。
- Router：按模型过滤可用 Credential，支持 priority、round-robin、weighted-round-robin、least-connections，执行有界故障切换和并发限制。
- Health / Usage：记录失败、冷却、延迟、请求和 Token 用量。

新增 migration：`000018_ai_resource_pool`。`sqlc.yaml` 已包含 000017 和 000018，查询定义位于 `queries/ai_resources.sql`。AI Resource repository 目前使用参数化 `database/sql` 查询，以便将密文解密、候选资源组合和一次请求的健康/用量写入事务；原有 M1–M16 sqlc 访问层保持不变。

### 管理 API

管理 API：`/api/v1/resources`

- `POST/GET /providers`、`PUT/DELETE /providers/:id`
- `POST/GET /endpoints`、`PUT/DELETE /endpoints/:id`
- `POST/GET /credentials`、`PUT /credentials/:id`（轮换密钥）
- `POST /credentials/bulk`（原子批量导入，最多 500 条）
- `PATCH /credentials/:id/status`、`DELETE /credentials/:id`、`POST /credentials/:id/test`
- `POST/GET /models`、`PUT/DELETE /models/:id`
- `PUT/GET /bindings`
- `PUT /policies/:model_id`、`GET /policies`
- `GET /health`、`POST /health/check/:id`、`GET /usage`、`GET /requests`

统一推理入口：`POST /v1/chat/completions` 和简化的 `POST /v1/responses`。当前同步推理支持 OpenAI-compatible、Anthropic Messages、Gemini generateContent；`stream=true` 明确返回不支持错误，不会伪装成流式响应。已有 M1–M16 Gateway 的流式调用保持不变。协议原生工具调用、多模态和各厂商特有参数不由此兼容层承诺。

如果设置 `AGENTMESH_API_TOKEN`，上述资源管理与新 Gateway 路由都要求 `Authorization: Bearer <token>`。不设置时按 Local-first 本地访问部署；不要在未配置 Token 的情况下将监听地址暴露到公网。

### 初始资源导入顺序

1. 创建 Provider，例如 `ctaigw`，协议按实际渠道选择 `openai-compatible`。
2. 创建 Endpoint，例如 `https://ai.ctaigw.cn/v1`。
3. 通过 `/credentials/bulk` 导入已轮换的 Key；每条记录必须有唯一 ID、Provider、Endpoint、名称、API Key、权重和并发上限。
4. 创建模型别名 `glm-5.3-flash` 和 `deepseek-v4.1-flash`。
5. 对每个已验证 Credential 分别创建两条 Binding，`upstream_model` 填服务商实际要求的模型 ID。
6. 通过 `/credentials/:id/test` 验证 Endpoint 可达，再调用 `/v1/chat/completions`。

不要将聊天中曾经暴露的旧 Key 直接导入；先在对应服务商轮换，再使用新 Key。不要把 API Key 放进 YAML、Git、日志或截图。

### Weighted routing 示例

```json
{
  "strategy": "weighted_round_robin",
  "max_retries": 2,
  "failure_threshold": 5,
  "cooldown_seconds": 30
}
```

策略配置通过 `PUT /api/v1/resources/policies/{model_id}` 设置。429 会按冷却时间暂时跳过；401 将 Credential 标记为 invalid；网络错误及 5xx 按阈值熔断。已经开始向客户端输出的流式请求不做跨 Key 续传；新资源 Gateway 当前只提供同步响应。


## M23–M28 Agent Runtime 与 Tool System

请先阅读 [`M23-M28-STATUS.md`](M23-M28-STATUS.md)。新增 Agent Definition API 位于 `/api/v1/agent-definitions`。Shell 与 Go 工具必须配置 Docker Sandbox 镜像；HTTPS 工具必须显式配置 `agent.tools.allowed_http_domains`，默认拒绝所有目标域名。

## M29–M34：上下文与工作流智能化

M29–M34 的实现范围、API、配置、验证状态和已知限制见 [`M29-M34-STATUS.md`](M29-M34-STATUS.md)。

- 对话上下文：`/api/v1/conversations`
- 上下文组装与预算：`POST /api/v1/conversations/:id/context`
- Workflow YAML：`/api/v1/workflows/:id/yaml`
- Workflow Graph：`/api/v1/workflows/:id/graph`
- Workflow 可视化编辑器：`/workflow-editor`
- Workflow 模板：`/api/v1/workflow-templates`

## M35–M41：Coding Agent 执行系统

项目已在 M1–M34 基础上增加 Coding Task、Git Worktree 隔离、复用 Agent Runtime 的代码执行、Git Patch 管理、Docker 测试沙箱、测试失败自修复，以及 HTTP API / CLI 集成。详见 [`M35-M41-STATUS.md`](./M35-M41-STATUS.md)。

```bash
agentmesh-cli coding create --id fix-login --repo /path/to/repo --model your-model-id --prompt "修复登录问题并添加回归测试"
agentmesh-cli coding run fix-login --timeout 30m
agentmesh-cli coding list
```
