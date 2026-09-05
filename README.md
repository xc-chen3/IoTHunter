# IoTHunter

[English](README.en.md) | 中文

IoTHunter 是一个面向 IoT 安全研究的 Capability-Isolated Multi-Agent Research Harness。它把研究过程拆成可审计的控制平面、Agent、Capability 和 Tool Runtime，以 Finding / Evidence 作为事实中心，支持任务恢复、最小权限、人工审批和报告生成。

这个仓库是一个可以直接运行的核心 MVP，不依赖 PostgreSQL、Redis、Docker 或大模型服务。默认使用本地 JSON 状态文件，适合快速试用、开发能力插件和在 GitHub 上公开讨论架构。生产部署可以把同样的接口替换为 PostgreSQL、对象存储、容器沙箱和远程 worker。

> 本项目是 IoTHunter，与 IoTBec 无关。默认示例只做被动元数据指纹，不连接真实设备，也不发送网络探测流量。对真实设备进行研究前，必须确认授权范围并单独配置网络、设备和破坏性权限。

![IoTHunter architecture](IoTHunterArch.png)

## 设计目标

架构文档里的核心闭环在 MVP 中已经可运行：

~~~text
Target → Task → Agent → Capability → Tool/Worker → Evidence → Finding → Report
~~~

边界保持为：

~~~text
Agent       负责理解、决策和评估
Capability  负责可测试、可复用的专业能力
Tool        负责具体执行
Harness     负责调度、权限、状态、审计和恢复
~~~

关键原则：

- Finding 是研究事实的主对象，Evidence 是每个结论的来源。
- Agent 不直接运行 shell、改数据库或操作设备，只能请求 Capability。
- Capability 有输入输出契约和权限声明，Tool/Worker 通过统一请求模型接入。
- Finding 和 Task 都有显式状态机，非法跳转会被拒绝。
- 设备或破坏性权限自动进入 Approval 队列，批准后任务恢复。
- 每个关键动作写入 Event 和 append-only Audit Log。
- 数据模型不绑定模型供应商，后续可以接 OpenAI-compatible、Anthropic、本地模型或自定义 HTTP 服务。

## 当前包含的功能

Go 控制平面：

- Workspace、Target、Task、Finding、Evidence、Artifact、Approval、Event、Audit Log 数据模型。
- JSON 文件持久化，原子写入，进程内并发保护。
- Capability Registry 和内置能力：target.fingerprint 被动指纹、finding.gate 质量门、report.generate Markdown 报告。
- Scheduler/Worker Pool，默认四个并发槽位，可配置。
- 权限检查、人工审批、任务阻塞与批准后恢复。
- Finding / Task 状态机和审计事件。
- REST API 和命令行入口。
- 可重复运行的 demo。

Python 能力 worker：

- capability-workers/knowledge/worker.py 是无依赖 NDJSON worker 示例。
- 一行 JSON 请求对应一行 JSON 响应，可放入容器或远程 worker。
- 该协议可以承载 firmware、binary、protocol、fuzz、emulation 等专业能力。

## 快速开始

要求 Go 1.22+。Python worker 只需要 Python 3.10+。

~~~bash
go test ./...
go run ./cmd/iothunter demo --data .iothunter/state.json
~~~

demo 会创建一个示例 Workspace 和 Target，异步执行被动 Recon，最后打印 Task、Finding 和报告路径。报告会写到 .iothunter/reports/<workspace-id>.md。

启动 API：

~~~bash
go run ./cmd/iothunter serve --addr :8080 --data .iothunter/state.json
~~~

编译成独立客户端/服务端二进制：

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

## API 示例

创建 Workspace：

~~~bash
curl -X POST http://127.0.0.1:8080/api/v1/workspaces \
  -H 'Content-Type: application/json' \
  -d '{"name":"router-research","owner":"alice","description":"authorized lab"}'
~~~

创建 Target：

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

查看 Workspace 聚合视图、Task、Finding 和报告：

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

## 数据和目录

默认运行目录：

~~~text
.iothunter/
├── state.json
└── reports/
~~~

状态文件使用临时文件加 rename 的方式更新。它适用于单进程本地研究和测试，不适合多个 API 实例共享写入；生产环境应替换 Store 实现。

仓库中的对象 Schema：

~~~text
schemas/task.schema.json
schemas/finding.schema.json
~~~

架构设计中的 workspace 目录约定仍然适用于生产扩展：

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

## Worker 协议

Worker 使用 newline-delimited JSON，不依赖特定 RPC 框架。请求示例：

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

运行示例 worker：

~~~bash
printf '%s\n' '{"request_id":"REQ-1","capability_id":"knowledge.search","inputs":{"query":"sprintf","corpus":["strcpy","sprintf"]}}' \
  | python3 capability-workers/knowledge/worker.py
~~~

生产环境建议由 Tool Gateway 做这些事情：

1. 校验请求 JSON 和 Capability schema。
2. 合并 Agent、Task、Capability、Tool 四层权限，取最小集合。
3. 选择容器、虚拟机或远程 worker。
4. 限制 CPU、内存、磁盘、网络、Linux capabilities 和运行时长。
5. 收集 stdout、stderr、artifact hash 和结构化 Evidence。
6. 把 Tool Run、Capability Run 和结果写入审计日志。

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

MVP 默认只注册被动、无网络、无设备、非破坏性能力。示例地址 192.0.2.10 是文档保留地址，不代表真实目标。

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
2. 给 Capability 注册一个 CapabilityExecutor，或实现 NDJSON/Python worker。
3. 在执行器中只使用传入的资源，不从 Agent 输入拼接任意 shell。
4. 为成功、权限拒绝、超时和结构化输出错误写测试。
5. 用 Evidence 记录可追溯的结果，用 Artifact 保存大文件并计算 SHA-256。
6. 通过 Commander/Task 调度能力，不在 HTTP handler 中直接执行专业工具。

模型适配器、PostgreSQL、对象存储、SSE/WebSocket、容器沙箱和 Web UI 是下一阶段的替换点。当前核心 API 不把这些基础设施写死，便于渐进式升级。

## 从 MVP 到生产

建议按架构文档的阶段推进：

1. IoTHunter Core：接入真正的 Agent Runtime、Model Adapter 和持久化仓库。
2. Capability Isolation：实现 Tool Gateway、容器/VM Sandbox 和远程 Worker。
3. Validation：增加 fuzz、emulation、packet、device capability，并接 Approval Manager。
4. Knowledge & Skill：增加 Vendor Profile、历史漏洞、Pattern、Skill 和检索。
5. Platform：增加 Web UI、SITREP 流式事件、指标、分布式调度和 Device Manager。

生产系统还应增加认证授权、租户隔离、密钥管理、对象存储、限流、OpenTelemetry、Prometheus、数据库迁移和备份策略。

## 许可证与贡献

项目使用 MIT License，见 LICENSE。欢迎通过 Issue 和 Pull Request 讨论新的 Capability、Worker、数据模型和实验方法。提交代码前请运行 go test ./... 和 go vet ./...。

安全问题请不要公开发布可直接攻击真实设备的细节；请先通过私下渠道联系维护者，并说明受影响版本、复现前提和修复建议。
