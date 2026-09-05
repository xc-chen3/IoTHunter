# IoTHunter

[English](README.md) | 中文

IoTHunter 是一个面向物联网安全研究的能力隔离型多智能体研究系统。它把研究过程拆成可审计的控制平面、智能体、能力模块和工具运行环境，以漏洞发现（Finding）和证据（Evidence）作为事实中心，支持任务恢复、最小权限、人工审批和报告生成。

这个仓库是一个可以直接运行的核心最小版本，不依赖 PostgreSQL、Redis、Docker 或大模型服务。默认使用本地 JSON 状态文件，适合快速试用、开发能力插件和在 GitHub 上公开讨论架构。生产部署可以把同样的接口替换为 PostgreSQL、对象存储、容器沙箱和远程工作进程。

![IoTHunter 架构图](IoTHunterArch.png)

## 设计目标

架构文档里的核心闭环在 MVP 中已经可运行：

~~~text
Target → Task → Agent → Capability → Tool/Worker → Evidence → Finding → Report
~~~

边界保持为：

~~~text
Agent       智能体，负责理解、决策和评估
Capability  能力模块，负责提供可测试、可复用的专业能力
Tool        工具，负责具体执行
System      系统，负责调度、权限、状态、审计和恢复
~~~

关键原则：

- Finding 是研究事实的主对象，Evidence 是每个结论的来源。
- Agent 不直接运行 shell、修改数据库或操作设备，只能请求 Capability。
- Capability 有输入输出契约和权限声明，工具和工作进程通过统一请求模型接入。
- Finding 和 Task 都有显式状态机，非法跳转会被拒绝。
- 设备或破坏性权限自动进入 Approval 队列，批准后任务恢复。
- 每个关键动作写入 Event 和只追加的 Audit Log。
- 数据模型不绑定模型供应商，后续可以接 OpenAI-compatible、Anthropic、本地模型或自定义 HTTP 服务。

## 当前功能

Go 控制服务：

- Workspace、Target、Task、Finding、Evidence、Artifact、Approval、Event、Audit Log 数据模型。
- JSON 文件持久化，原子写入，进程内并发保护。
- Capability Registry 和内置能力：target.fingerprint 被动指纹、finding.gate 质量门、report.generate Markdown 报告。
- 调度器和工作进程池，默认四个并发槽位，可配置。
- 权限检查、人工审批、任务阻塞与批准后恢复。
- Finding / Task 状态机和审计事件。
- REST 接口和命令行入口。
- 可重复运行的演示程序。

Python 能力工作进程：

- capability-workers/knowledge/worker.py 是无依赖的按行分隔 JSON 工作进程示例。
- 一行 JSON 请求对应一行 JSON 响应，可放入容器或远程工作进程。
- 该协议可以承载固件、二进制、协议、模糊测试、仿真等专业能力。

## 快速开始

要求 Go 1.22 及以上版本、Node.js 20 及以上版本和 npm。Python 工作进程只需要 Python 3.10 及以上版本。

~~~bash
go test ./...
go run ./cmd/iothunter demo --data .iothunter/state.json
~~~

演示程序会创建一个示例工作空间和目标，异步执行被动侦察，最后打印任务、发现和报告路径。报告会写到 .iothunter/reports/<workspace-id>.md。

启动 API：

~~~bash
go run ./cmd/iothunter serve --addr :8080 --data .iothunter/state.json
~~~

## 原生桌面客户端

IoTHunter 使用原生 Electron 桌面客户端，Go 控制平面作为本地 sidecar 进程运行。启动后会打开独立的应用窗口，不会跳转浏览器。客户端参考 MultiCa 的交互模式，提供固定工作区侧栏、标签页、前进后退和独立滚动的研究页面。

~~~bash
make build
npm --prefix desktop install
npm --prefix desktop start
~~~

桌面客户端会自动启动 loopback 地址上的 `bin/iothunter serve`，并将状态文件存储到当前平台的应用数据目录。如果已有 API 服务，可以设置 `IOTHUNTER_API_URL`，或者执行 `./bin/iothunter desktop --api-url http://127.0.0.1:8080` 连接它。

控制台还覆盖架构中的控制面对象：Agent 池、Skill 工作流、Evidence 和 Artifact 记录、Approval 队列、Event 流、Audit Log、CapabilityRun、ToolRun、GateDecision 和 Knowledge。Finding Gate，以及 Task 的暂停、恢复、重试和取消操作，都可以通过接口执行并在工作空间视图中查看。

编译 Go 控制服务和桌面客户端：

~~~bash
make build
./bin/iothunter client --server http://127.0.0.1:8080 health
./bin/iothunter client --server http://127.0.0.1:8080 capabilities
make build-all
~~~

make build 会生成当前平台的 bin/iothunter；make build-all 会生成 Linux amd64/arm64、macOS amd64/arm64 和 Windows amd64 客户端。客户端和服务端使用同一个二进制，通过第一个命令参数区分运行模式。

检查健康状态和能力列表：

~~~bash
curl http://127.0.0.1:8080/healthz
curl http://127.0.0.1:8080/api/v1/capabilities
curl http://127.0.0.1:8080/api/v1/tools
~~~

## 接口示例

创建工作空间：

~~~bash
curl -X POST http://127.0.0.1:8080/api/v1/workspaces \
  -H 'Content-Type: application/json' \
  -d '{"name":"router-research","owner":"alice","description":"authorized lab"}'
~~~

创建目标：

~~~bash
curl -X POST http://127.0.0.1:8080/api/v1/workspaces/W-xxxx/targets \
  -H 'Content-Type: application/json' \
  -d '{
    "name":"lab-router",
    "vendor":"ExampleVendor",
    "model":"Router X1",
    "address":"192.0.2.10",
    "transport":"http",
    "authorized":true
  }'
~~~

启动研究任务：

~~~bash
curl -X POST http://127.0.0.1:8080/api/v1/workspaces/W-xxxx/run \
  -H 'Content-Type: application/json' \
  -d '{"target_id":"T-xxxx"}'
~~~

查看工作空间聚合视图、任务、发现和报告：

~~~bash
curl http://127.0.0.1:8080/api/v1/workspaces/W-xxxx
curl http://127.0.0.1:8080/api/v1/tasks/T-xxxx
curl http://127.0.0.1:8080/api/v1/findings/F-xxxx
curl http://127.0.0.1:8080/api/v1/workspaces/W-xxxx/report
~~~

Finding 状态只能按状态机跳转。例如 Candidate 可以进入 Analyzing：

~~~bash
curl -X POST http://127.0.0.1:8080/api/v1/findings/F-xxxx \
  -H 'Content-Type: application/json' \
  -d '{"state":"analyzing"}'
~~~

Approval 接口：

~~~bash
curl http://127.0.0.1:8080/api/v1/approvals
curl -X POST http://127.0.0.1:8080/api/v1/approvals/APR-xxxx \
  -H 'Content-Type: application/json' \
  -d '{"status":"approved","actor":"alice"}'
~~~

Commander 计划和质量门：

~~~bash
curl -X POST http://127.0.0.1:8080/api/v1/workspaces/W-xxxx/plan \
  -H 'Content-Type: application/json' \
  -d '{"target_id":"T-xxxx","objective":"离线分析","capabilities":["target.fingerprint","web.route_discovery"]}'

curl -X POST http://127.0.0.1:8080/api/v1/findings/F-xxxx/gate \
  -H 'Content-Type: application/json' \
  -d '{"gate":"finding"}'
~~~

Agent、Skill、Knowledge、Event 和 Audit 接口分别位于 `/api/v1/agents`、`/api/v1/skills`、`/api/v1/knowledge`、`/api/v1/events` 和 `/api/v1/audit`。后续可以使用同一套结构化请求/结果模型替换内置离线能力。

## 数据和目录

默认运行目录：

~~~text
.iothunter/
├── state.json
└── reports/
~~~

状态文件使用临时文件加 rename 的方式更新。它适用于单进程本地研究和测试，不适合多个 API 实例共享写入；生产环境应替换 Store 实现。

仓库中的对象结构定义：

~~~text
schemas/task.schema.json
schemas/finding.schema.json
~~~

架构设计中的工作空间目录约定仍然适用于生产扩展：

~~~text
workspace/
├── targets/
├── findings/
├── evidence/
├── artifacts/
├── tasks/
├── capabilities/
├── knowledge/
├── skills/
├── approvals/
├── logs/
└── sitrep/
~~~

## 工作进程协议

工作进程使用按行分隔的 JSON，不依赖特定 RPC 框架。请求示例：

~~~json
{
  "request_id": "REQ-1",
  "task_id": "TASK-1",
  "agent_id": "analysis-1",
  "capability_id": "knowledge.search",
  "objective": "search known patterns",
  "inputs": {
    "query": "sprintf",
    "corpus": ["strcpy", "sprintf", "memcpy"]
  },
  "permissions": {
    "network": false,
    "filesystem": "workspace-readonly",
    "device": false,
    "destructive": false
  }
}
~~~

运行示例工作进程：

~~~bash
printf '%s\n' '{"request_id":"REQ-1","capability_id":"knowledge.search","inputs":{"query":"sprintf","corpus":["strcpy","sprintf"]}}' \
  | python3 capability-workers/knowledge/worker.py
~~~

生产环境建议由工具网关完成以下工作：

1. 校验请求 JSON 和 Capability 结构定义。
2. 合并 Agent、Task、Capability、Tool 四层权限，取最小集合。
3. 选择容器、虚拟机或远程工作进程。
4. 限制 CPU、内存、磁盘、网络、Linux capabilities 和运行时长。
5. 收集标准输出、标准错误、工件哈希和结构化 Evidence。
6. 把工具运行、能力运行和结果写入审计日志。

## 状态机

Finding：

~~~text
Hypothesis → Candidate → Analyzing → ReadyForValidation
                                      ↓
                              Validating → Validated
                                      ↓
                              Reportable → Reported → KnowledgeCaptured
~~~

分析不足可以回退到 Candidate，验证失败可以回到 Analyzing 或 Candidate；任何不在允许边上的转换都会返回冲突错误。

Task：

~~~text
Queued → Assigned → Running → Completed
                         ├── Failed → Queued
                         ├── Blocked → Queued
                         └── Paused → Running
~~~

## 安全边界

最小版本默认只注册被动、无网络、无设备、非破坏性能力。示例地址 192.0.2.10 是文档保留地址，不代表真实目标。

接入真实设备时，应同时满足：

~~~text
Target 已授权
AND Task 允许设备权限
AND Capability 允许设备权限
AND Tool 允许设备权限
AND Device 当前可用并被锁定
AND 高风险动作已获得人工批准
~~~

本项目不会替研究者决定授权范围。任何设备验证、压力测试、Flash 写入、配置修改或可能造成持久影响的 PoC，都应在隔离实验室和明确授权下执行。

## 开发

常用命令：

~~~bash
go test ./...
go vet ./...
gofmt -w cmd internal
go run ./cmd/iothunter capabilities
go run ./cmd/iothunter demo
~~~

推荐扩展方式：

1. 在 internal/core 中定义 Capability 的输入、输出和 PermissionSet。
2. 给 Capability 注册一个 CapabilityExecutor，或实现按行分隔 JSON/Python 工作进程。
3. 在执行器中只使用传入的资源，不从 Agent 输入拼接任意 shell 命令。
4. 为成功、权限拒绝、超时和结构化输出错误写测试。
5. 用 Evidence 记录可追溯的结果，用 Artifact 保存大文件并计算 SHA-256。
6. 通过 Commander/Task 调度能力，不在 HTTP 处理器中直接执行专业工具。

模型适配器、PostgreSQL、对象存储、SSE/WebSocket、容器沙箱和网页界面是下一阶段的替换点。当前核心接口不把这些基础设施写死，便于渐进式升级。

## 从最小版本到生产

建议按架构文档的阶段推进：

1. IoTHunter 核心：接入真正的智能体运行时、模型适配器和持久化仓库。
2. 能力隔离：实现工具网关、容器/虚拟机沙箱和远程工作进程。
3. 验证能力：增加模糊测试、仿真、数据包、设备能力，并接入审批管理器。
4. 知识与技能：增加厂商画像、历史漏洞、模式、技能和检索。
5. 平台能力：增加网页界面、SITREP 流式事件、指标、分布式调度和设备管理器。

生产系统还应增加身份认证、租户隔离、密钥管理、对象存储、限流、OpenTelemetry、Prometheus、数据库迁移和备份策略。

## 许可证与贡献

项目使用 MIT 许可证，见 LICENSE。欢迎通过 Issue 和 Pull Request 讨论新的能力模块、工作进程、数据模型和实验方法。提交代码前请运行 go test ./... 和 go vet ./...。

安全问题请不要公开发布可直接攻击真实设备的细节；请先通过私下渠道联系维护者，并说明受影响版本、复现前提和修复建议。
