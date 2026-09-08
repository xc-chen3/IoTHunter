# IoTHunter

English | [中文](README.zh-CN.md)

IoTHunter is a local desktop platform for IoT security research. It brings together task orchestration, local AI runtimes, isolated analysis capabilities, controlled tool execution, and real peripheral sessions in one auditable client.

## Architecture

```text
Wails + React/TypeScript
          |
      local HTTP API / Wails bindings
          |
Go Control Plane (Commander, Scheduler, Task Engine, Event Bus)
          |
Capability Registry -> Tool Gateway / Python Worker
          |
Peripheral Manager -> Serial, TCP/SCPI and additional driver adapters
          |
SQLite state + artifacts + audit events
```

The complete research chain is recorded as:

```text
Workspace -> Target -> Conversation -> Task -> Agent -> Capability
          -> Tool / Worker / Peripheral -> Evidence / Artifact
          -> Finding -> Validation -> Report / Knowledge
```

The implementation follows `IoTHunter_Harness_Architecture_Design_v2.1_Peripheral_Fixed.md`.

## What is implemented

### Control plane

- Go 1.22 module with explicit Workspace, Target, Task, Agent, Capability, Tool, Finding, Evidence, Artifact, Approval, Event and Audit models.
- SQLite persistence by default (`.iothunter/state.db`) with legacy JSON store compatibility.
- Cancellable task execution with queued, assigned, running, paused, blocked, failed, completed and cancelled states.
- A plan is one ordered Task: every requested Capability becomes a node, consumes structured results from prior nodes, and contributes to the same Finding and final summary.
- Retry starts the task's declared capability after a fresh target, permission and lease check. Pause and cancel terminate the active Runtime or Worker process.
- Event stream and task detail endpoints expose scheduler nodes, AgentRun, CapabilityRun, ToolRun, output and final summary.
- Finding state machine, finding/validation quality gates, approval queue and Markdown SITREP reports.
- Workspaces are initialized with the documented `targets/`, `evidence/`, `artifacts/`, `tasks/`, `peripherals/`, `logs/` and `sitrep/` layout plus a `manifest.yaml`.
- Model and Prompt registries are persisted in SQLite; Prompt versions carry a SHA-256 digest and can be activated per prompt name.

### Agent and Runtime integration

- Local runtime discovery for Claude Code (`claude`), Codex CLI (`codex`), Grok CLI (`grok`) and Kiro CLI (`kiro-cli`).
- Non-interactive, bounded process sessions. The selected runtime can be bound to an Agent and is executed before the Agent's capability step.
- Runtime paths and version information are shown without returning credentials.

### Capability and Tool execution

- Capability Registry with software, analysis, validation, knowledge and peripheral entries.
- Python NDJSON worker at `capability-workers/knowledge/worker.py` implements real offline operations:
  - firmware image metadata, SHA-256, bounded ZIP/TAR extraction and archive inventory;
  - binary format identification and printable string search;
  - configuration review for high-risk keys;
  - protocol key/value parsing and route discovery;
  - graph-based taint reachability, CVSS calculation, bounded Fuzz seed generation, packet generation, PoC verification and knowledge results.
- `binary.decompile` invokes the host `objdump` executable through the Tool Gateway and persists the bounded disassembly as an Artifact.
- Go Tool Gateway executes only registered binaries with an argument vector, timeout, output limit and filesystem/network/destructive permission checks. `file` and `strings` are registered automatically when available.
- Tool definitions support `host`, `docker` and `podman` isolation. Container runs mount only the task working directory and default to a network-disabled container.
- File-backed capabilities run bounded host-tool observations through the Gateway and then receive structured results from the Python Worker. Findings retain the source artifact hash and evidence provenance.

### Peripheral plane

- `go.bug.st/serial` adapter for real UART/USB serial ports.
- TCP adapter for explicit host:port endpoints and SCPI-style instruments (`tcp`, `scpi`, `power`, `scope` kinds).
- Power measurement, bounded voltage/current settings and output commands use the same SCPI session. Voltage/current writes require configured `max_voltage`/`max_current` limits and human approval.
- Discovery, connect/disconnect, exclusive or shared-read leases, expiring sessions, type-specific configuration schemas and telemetry records.
- Telemetry can be read from history or subscribed to as an SSE stream at `/api/v1/peripheral-sessions/{id}/telemetry/stream`.
- `identity`, `read`, `write` and `drain` commands use the same Manager for UI, API, gRPC and Agent capability calls.
- Architecture-level peripheral capabilities include `serial.open/configure`, `power.read/measure/set_voltage/set_current/output/cycle`, `scope.configure/capture/measure`, `jlink.attach/reset/halt/read_memory`, `bluetooth.scan/capture`, and `packet.capture`.
- Shared-read sessions cannot configure or write. Physical/destructive capability requests enter the approval queue before invocation; read-only observations remain available through the same lease path.

## Desktop clients

The primary client is a Wails v2 desktop application with a React/TypeScript UI. The Electron shell remains available as a compatibility/development host and starts the same Go sidecar.

The UI contains the four architecture groups and thirteen entries:

```text
Workbench: Conversation management, Task management, Device management, Agent management
Peripheral management: Peripheral connections, Peripheral configuration, Protocol analysis
Vulnerability management: Vulnerability list, Vulnerability knowledge
Configuration: Runtime, Skills, Capability center, Settings
```

The interface defaults to English and switches to Chinese from the top bar. The workspace picker can create a workspace. The task panel can import a firmware or binary into the workspace artifact store before submitting a multi-step plan. Conversation, task, node output, live task events, runtime sessions, target devices, peripheral leases and findings are backed by the same local API.

## Requirements

- Go 1.22 or newer
- Node.js 20 or newer and npm
- Python 3.10 or newer for Python capabilities
- Linux Wails builds additionally require WebKitGTK development packages. On Debian/Ubuntu:

```bash
sudo apt install libwebkit2gtk-4.1-dev libsoup-3.0-dev
```

The default built-in capabilities do not require a model service or external database. A local AI runtime is used only when one is installed and bound to an Agent.

## Quick start

Run tests, build the Go control plane and start the local API:

```bash
make build
./bin/iothunter serve --addr 127.0.0.1:18080 --grpc-addr 127.0.0.1:19090 --data .iothunter/state.db
```

In another terminal, build and start the Electron compatibility client:

```bash
make client-install
npm --prefix desktop start
```

The native Wails client is built with:

```bash
make wails-build-linux
./bin/iothunter-wails
```

`make wails-build-linux` builds the Vite bundle, copies it into the Wails embed directory and produces `bin/iothunter-wails`. The binary contains the React assets produced by that build. Electron packages include the Go sidecar, React bundle, Python workers and `logo2.png`.

Useful commands:

```bash
make test
make vet
make frontend-build
make desktop-package
make desktop-dist
go run ./cmd/iothunter demo --data .iothunter/state.db
```

## End-to-end API example

Start the server, import an input artifact, then submit an ordered file-backed plan:

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

Follow execution:

```bash
curl "$base/api/v1/tasks/TASK-xxxx/detail"
curl -N "$base/api/v1/tasks/TASK-xxxx/events"
curl "$base/api/v1/workspaces/$workspace_id/report"
```

The task detail contains scheduler and capability nodes, ToolRun records, Python Worker output, Evidence, Artifact hashes and the generated Finding. Conversation messages can create the same task through `POST /api/v1/conversations/{id}/message` with `create_task: true`.

## API surface

| Area | Endpoints |
| --- | --- |
| Health and registries | `/healthz`, `/api/v1/capabilities` (list/register/test), `/api/v1/tools` (list/register/run), `/api/v1/agents`, `/api/v1/runtimes`, `/api/v1/models`, `/api/v1/prompts`, `/api/v1/skills` (list/register/run), `/api/v1/knowledge` |
| Workspaces and targets | `/api/v1/workspaces`, `/api/v1/workspaces/{id}`, `/targets`, `/devices`, `/attachments` |
| Artifacts | `/api/v1/workspaces/{id}/artifacts` (import and list) |
| Protocol captures | `/api/v1/workspaces/{id}/captures` (read a leased session and persist raw/parsed output) |
| Conversations | `/api/v1/conversations`, `/api/v1/conversations/{id}`, `/message`, `/events` (SSE) |
| Tasks | `/api/v1/workspaces/{id}/plan`, `/run`, `/api/v1/tasks/{id}`, `/detail`, `/events`, `/pause`, `/resume`, `/retry`, `/cancel` |
| Findings | `/api/v1/findings/{id}`, `/evidence`, `/validate`, `/gate` |
| Peripherals | `/api/v1/peripherals/discover`, `/connect`, `/api/v1/peripheral-sessions`, `/config`, `/invoke`, `/telemetry`, `/telemetry/stream` (SSE), `/api/v1/telemetry` (durable query) |
| IoT projections | `/api/v1/iot/summary`, `/devices`, `/peripherals`, `/vulnerabilities`, `/artifacts` |
| Internal RPC | gRPC `CapabilityWorker.Execute` and `PeripheralService.Invoke` from `proto/` |

## Project layout

```text
cmd/iothunter/             CLI and server entry point
internal/core/              Go domain model, Store, Engine and REST API
internal/tools/             Tool Gateway
internal/worker/            Python worker supervisor
internal/peripherals/       Serial/TCP adapters, Session and Lease manager
internal/rpc/               gRPC services
proto/                      Protobuf contracts
gen/proto/                  Generated Go protobuf bindings
capability-workers/         Python capability workers
desktop/frontend/           React + TypeScript client
desktop/wails/              Wails v2 native client
desktop/renderer/           Electron compatibility renderer
schemas/                    Task and Finding JSON Schemas
```

## Extending IoTHunter

To add a capability, define an ID, version, JSON input/output schema and minimum `PermissionSet`; register a Go executor or a Worker implementation; validate resource boundaries; persist Evidence and Artifact provenance; and add tests for success, denial, timeout and malformed output. Agent code must request a Capability rather than opening a file, shell, network socket or peripheral directly.

To add a peripheral, implement the `Adapter` and `Handle` interfaces in `internal/peripherals`, register it with `Manager`, expose its configuration schema, and make every command return structured telemetry. UI actions and Agent capabilities must continue to use the Manager so leases and safety checks remain consistent.

## Security model

The control plane applies the intersection of Agent, Task, Capability and Tool permissions. Read-only device observations can run directly; physical or destructive changes require a human Approval record and device-specific hard limits. Tool commands use an allow-listed executable plus argument array and never pass user input through a shell. Worker output is bounded and decoded as structured JSON. Peripheral handles are held only by expiring Manager sessions, and shared-read leases cannot write.

Network, physical-device and destructive validation should be configured for an isolated lab with an explicit authorization scope. The application records the decision and evidence trail; it does not infer authorization from an address or a model name.

## License

IoTHunter is released under the MIT License. See [LICENSE](LICENSE).
