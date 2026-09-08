# IoTHunter

[English](README.md) | 中文

IoTHunter 是面向 IoT 安全研究的本地桌面平台。它将任务编排、本地 AI 运行时、隔离分析能力、受控工具执行和真实外设会话整合到一个可审计的客户端中。

## 架构

```text
Wails + React/TypeScript
          |
      本地 HTTP API / Wails Binding
          |
Go 控制平面（Commander、Scheduler、Task Engine、Event Bus）
          |
能力注册中心 -> Tool Gateway / Python Worker
          |
外设管理器 -> 串口、TCP/SCPI 及其他驱动适配器
          |
SQLite 状态库 + 工件 + 审计事件
```

系统记录完整研究链路：

```text
工作区 -> 目标设备 -> 对话 -> 任务 -> 智能体 -> 能力
      -> 工具 / Worker / 外设 -> 证据 / 工件
      -> Finding -> 验证 -> 报告 / 知识
```

项目实现依据 `IoTHunter_Harness_Architecture_Design_v2.1_Peripheral_Fixed.md`。

## 已实现功能

### 控制平面

- Go 1.22 模块，包含 Workspace、Target、Task、Agent、Capability、Tool、Finding、Evidence、Artifact、Approval、Event 和 Audit 模型。
- 默认使用 SQLite（`.iothunter/state.db`），同时兼容旧 JSON 存储。
- 任务支持排队、分配、运行、暂停、阻塞、失败、完成和取消；暂停与取消会终止当前 Runtime 或 Worker 进程。
- 一个计划对应一个有序 Task；每个能力都是可追踪节点，并将结构化结果传给后续节点，最终汇总到同一个 Finding。
- 重试会依据任务声明的能力重新检查目标、权限和租约后再调度。
- 任务详情和事件流包含调度节点、AgentRun、CapabilityRun、ToolRun、节点输出和最终总结。
- Finding 状态机、质量门、审批队列和 Markdown SITREP 报告。
- 创建工作区时会初始化文档规定的 `targets/`、`evidence/`、`artifacts/`、`tasks/`、`peripherals/`、`logs/`、`sitrep/` 目录，并生成 `manifest.yaml`。
- 模型注册表和 Prompt 注册表会持久化到 SQLite；每个 Prompt 版本保存 SHA-256 摘要，并可按名称激活。

### 智能体和运行时

- 自动发现 Claude Code（`claude`）、Codex CLI（`codex`）、Grok CLI（`grok`）和 Kiro CLI（`kiro-cli`）。
- 使用有超时和输出上限的非交互进程会话。运行时可以绑定到 Agent，并在能力步骤前真实执行。
- 页面只展示可执行文件路径、版本和状态，不返回凭据。

### 能力和工具

- 能力注册中心覆盖固件、二进制、协议、配置、验证、知识和外设类别。
- `capability-workers/knowledge/worker.py` 已实现固件镜像元数据、SHA-256、ZIP/TAR 目录读取、二进制格式识别、字符串检索、配置风险检查、协议解析、路由提取，以及污点、CVSS、Fuzz、仿真、数据包、PoC 和知识检索的结构化结果。
- Worker 支持有边界的 ZIP/TAR 固件解包、基于图的污点可达性分析、CVSS 计算、Fuzz 种子生成、数据包生成和 PoC 校验，输出会保存为可追踪工件。
- `binary.decompile` 会通过 Tool Gateway 调用主机上的 `objdump`，并将有大小限制的反汇编文本保存为工件。
- Go Tool Gateway 只执行已注册的可执行文件，使用参数数组、超时、输出上限和权限校验。系统会自动注册主机上可用的 `file` 和 `strings`。
- Tool 定义支持 `host`、`docker` 和 `podman` 隔离模式。容器只挂载任务工作目录，并默认关闭网络。
- 文件能力会通过 Gateway 记录受限的主机工具观察，再交给 Python Worker 分析；Finding 保留工件哈希和证据来源。

### 外设平面

- 使用 `go.bug.st/serial` 连接真实 UART/USB 串口。
- TCP 适配器支持显式 host:port 和 SCPI 类仪器，外设类型可以使用 `tcp`、`scpi`、`power`、`scope`。
- 电源测量、电压/电流设置和输出控制都通过同一个 SCPI 会话执行；设置动作必须先配置 `max_voltage`/`max_current`，并经过人工审批。
- 支持发现、连接、断开、独占或只读共享租约、过期 Session、配置 Schema 和遥测记录。
- 遥测既可以查询历史记录，也可以通过 `/api/v1/peripheral-sessions/{id}/telemetry/stream` 订阅 SSE 实时流。
- `identity`、`read`、`write`、`drain` 命令由同一个 Manager 提供给 UI、HTTP、gRPC 和 Agent 能力。
- 已接入架构文档中的外设能力：`serial.open/configure`、`power.read/measure/set_voltage/set_current/output/cycle`、`scope.configure/capture/measure`、`jlink.attach/reset/halt/read_memory`、`bluetooth.scan/capture` 和 `packet.capture`。
- 只读共享 Session 不能修改配置或写入；物理或破坏性能力在执行前进入审批队列，普通只读观测仍通过同一租约路径执行。

## 桌面客户端

主客户端使用 Wails v2 + React/TypeScript。Electron 保留为兼容和开发承载，并启动同一个 Go sidecar。

侧边栏对应架构中的 4 组 13 个入口：

```text
工作台：对话管理、任务管理、设备管理、智能体管理
外设管理：外设连接、外设配置、协议分析
漏洞管理：漏洞列表、漏洞知识库
配置：运行时、Skills、能力中心、设置
```

界面默认显示英文，可在顶部切换中文。工作区下拉栏支持新建工作区。任务页面可以先把固件或二进制导入工作区工件目录，再提交多步骤分析计划。对话、任务、节点输出、任务事件、运行时、目标设备、外设租约和 Finding 都来自同一个本地控制平面。

## 环境要求

- Go 1.22 或更高版本
- Node.js 20 或更高版本以及 npm
- Python 3.10 或更高版本（执行 Python 能力时需要）
- Linux 编译 Wails 还需要 WebKitGTK 开发包：

```bash
sudo apt install libwebkit2gtk-4.1-dev libsoup-3.0-dev
```

内置能力不依赖 PostgreSQL、Redis、Docker 或外部大模型服务。只有在主机安装并绑定 Agent 后，才会调用本地 AI Runtime。

## 快速开始

```bash
make build
./bin/iothunter serve --addr 127.0.0.1:18080 --grpc-addr 127.0.0.1:19090 --data .iothunter/state.db
```

Electron 兼容客户端：

```bash
make client-install
npm --prefix desktop start
```

原生 Wails 客户端：

```bash
make wails-build-linux
./bin/iothunter-wails
```

`make wails-build-linux` 会先构建 Vite 前端，再复制到 Wails 嵌入目录并生成 `bin/iothunter-wails`。Electron 打包会包含 Go sidecar、React 前端、Python Worker 和 `logo2.png`。

常用命令：

```bash
make test
make vet
make frontend-build
make desktop-package
make desktop-dist
go run ./cmd/iothunter demo --data .iothunter/state.db
```

## API 链路示例

创建工作区、导入文件并提交有序分析计划：

```bash
base=http://127.0.0.1:18080
workspace=$(curl -fsS -X POST "$base/api/v1/workspaces" \
  -H 'Content-Type: application/json' \
  -d '{"name":"router-lab","owner":"local"}')
workspace_id=$(printf '%s' "$workspace" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
target=$(curl -fsS -X POST "$base/api/v1/workspaces/$workspace_id/targets" \
  -H 'Content-Type: application/json' \
  -d '{"name":"fixture-router","vendor":"Example","model":"R1","transport":"offline","authorized":true}')
target_id=$(printf '%s' "$target" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
artifact=$(curl -fsS -X POST "$base/api/v1/workspaces/$workspace_id/artifacts" \
  -H 'Content-Type: application/json' \
  -d '{"path":"/path/to/firmware.bin","type":"firmware"}')
artifact_id=$(printf '%s' "$artifact" | sed -n 's/.*"artifact_id":"\([^"]*\)".*/\1/p')
curl -fsS -X POST "$base/api/v1/workspaces/$workspace_id/plan" \
  -H 'Content-Type: application/json' \
  -d "{\"target_id\":\"$target_id\",\"objective\":\"inspect firmware\",\"capabilities\":[\"binary.identify\",\"binary.search_string\",\"firmware.config_scan\"],\"inputs\":{\"artifact_id\":\"$artifact_id\",\"query\":\"password\"},\"permissions\":{\"filesystem\":\"workspace-readonly\"}}"
```

查看任务和实时事件：

```bash
curl "$base/api/v1/tasks/TASK-xxxx/detail"
curl -N "$base/api/v1/tasks/TASK-xxxx/events"
curl "$base/api/v1/workspaces/$workspace_id/report"
```

任务详情包含调度节点、能力节点、ToolRun、Python Worker 输出、Evidence、Artifact 哈希和 Finding。对话接口传入 `create_task: true` 时，也会进入同一条任务链。

## 接口范围

| 区域 | 接口 |
| --- | --- |
| 健康检查和注册表 | `/healthz`、`/api/v1/capabilities`（查询/注册/测试）、`/api/v1/tools`（查询/注册/运行）、`/api/v1/agents`、`/api/v1/runtimes`、`/api/v1/models`、`/api/v1/prompts`、`/api/v1/skills`（查询/注册/运行）、`/api/v1/knowledge` |
| 工作区和目标 | `/api/v1/workspaces`、`/api/v1/workspaces/{id}`、`/targets`、`/devices`、`/attachments` |
| 工件 | `/api/v1/workspaces/{id}/artifacts`（导入和列表） |
| 协议采集 | `/api/v1/workspaces/{id}/captures`（读取已租约会话并保存原始/解析结果） |
| 对话 | `/api/v1/conversations`、`/api/v1/conversations/{id}`、`/message`、`/events`（SSE） |
| 任务 | `/api/v1/workspaces/{id}/plan`、`/run`、`/api/v1/tasks/{id}`、`/detail`、`/events`、`/pause`、`/resume`、`/retry`、`/cancel` |
| Finding | `/api/v1/findings/{id}`、`/evidence`、`/validate`、`/gate` |
| 外设 | `/api/v1/peripherals/discover`、`/connect`、`/api/v1/peripheral-sessions`、`/config`、`/invoke`、`/telemetry`、`/telemetry/stream`（SSE）、`/api/v1/telemetry`（持久化查询） |
| IoT 投影 | `/api/v1/iot/summary`、`/devices`、`/peripherals`、`/vulnerabilities`、`/artifacts` |
| 内部 RPC | `proto/` 中定义的 gRPC `CapabilityWorker.Execute` 和 `PeripheralService.Invoke` |

## 目录结构

```text
cmd/iothunter/             CLI 和服务入口
internal/core/              Go 领域模型、Store、Engine、REST API
internal/tools/             Tool Gateway
internal/worker/            Python Worker 进程监管
internal/peripherals/       串口/TCP 适配器、Session、Lease
internal/rpc/               gRPC 服务
proto/                      Protobuf 契约
gen/proto/                  生成的 Go Protobuf 绑定
capability-workers/         Python 能力 Worker
desktop/frontend/           React + TypeScript 客户端
desktop/wails/              Wails v2 原生客户端
desktop/renderer/           Electron 兼容渲染器
schemas/                    Task 和 Finding JSON Schema
```

## 扩展开发

新增能力时，定义 ID、版本、输入输出 Schema 和最小权限，注册 Go Executor 或 Worker，实现资源边界校验，保存 Evidence/Artifact 来源，并为成功、拒绝、超时和错误输出添加测试。Agent 只能请求 Capability，不能直接打开文件、执行 shell、连接网络或操作外设。

新增外设时，实现 `internal/peripherals` 中的 `Adapter` 和 `Handle` 接口，向 Manager 注册，提供配置 Schema，并让每个命令返回结构化遥测。UI 和 Agent 能力都必须继续通过 Manager，以保持租约和安全限制一致。

## 安全模型

控制平面会取 Agent、Task、Capability 和 Tool 四层权限的交集。只读设备观测可以直接执行，物理或破坏性变更需要人工 Approval 和设备硬限制。工具使用已注册可执行文件和参数数组，不经过 shell；Worker 输出有大小限制并按结构化 JSON 解码；外设句柄只存在于有过期时间的 Manager Session 中，只读共享租约不能写入。

网络、物理设备和破坏性验证应在隔离实验环境中配置明确的授权范围。应用会记录权限决定和证据链，不会根据地址或型号自动推断授权。

## 许可证

IoTHunter 使用 MIT License，详见 [LICENSE](LICENSE)。
