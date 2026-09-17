import { createContext, FormEvent, ReactNode, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react';
import { createRoot } from 'react-dom/client';
import {
  Activity, Bot, BookOpen, Boxes, Bug, Cable, ChevronLeft, ChevronRight,
  CirclePlus, Cpu, FileUp, Languages, ListTodo, MessageSquareText, PanelLeft,
  Pause, Play, RefreshCw, RotateCcw, Search, Send, Settings,
  ShieldCheck, SlidersHorizontal, Sparkles, Usb, X,
  type LucideIcon,
} from 'lucide-react';
import './styles.css';

let API = window.iothunter?.apiBase || 'http://127.0.0.1:18080';

type ID = string;
type Workspace = { id: ID; name: string; owner: string; description?: string; created_at: string };
type Target = { id: ID; name: string; vendor?: string; model?: string; address?: string; transport?: string; authorized: boolean };
type Node = { id: ID; name: string; kind: string; status: string; summary?: string; output?: Record<string, unknown> };
type Task = { id: ID; objective: string; type: string; status: string; assigned_agent?: string; summary?: string; output?: Record<string, unknown>; nodes?: Node[]; target_id?: ID; finding_id?: ID; conversation_id?: ID; required_capabilities?: string[]; created_at: string; updated_at: string };
type EventRecord = { id: ID; type: string; task_id?: ID; payload?: Record<string, unknown>; created_at: string };
type Message = { id: ID; role: string; content: string; references?: Record<string, ID[]>; created_at: string };
type Conversation = { id: ID; title: string; status: string; messages?: Message[]; task_ids?: ID[]; updated_at: string };
type Peripheral = { id: ID; name: string; kind: string; driver?: string; transport?: string; address?: string; port?: string; status: string; occupied_by?: string; config?: Record<string, unknown>; active_config?: Record<string, unknown>; safety_limits?: Record<string, unknown> };
type PeripheralDescriptor = { id: string; kind: string; name: string; driver?: string; endpoint?: string; metadata?: Record<string, unknown> };
type PeripheralSession = { session_id: string; workspace_id?: string; peripheral_id: string; kind: string; owner_type: string; owner_id: string; mode: string; status: string; expires_at: string };
type PeripheralInvokeResult = { status: string; data?: Record<string, unknown>; bytes?: string; message?: string };
type Agent = { id: ID; role: string; model?: string; runtime_id?: string; enabled: boolean; status: string; max_concurrency: number; permissions?: { device?: boolean; destructive?: boolean; network?: boolean; filesystem?: string } };
type Runtime = { id: ID; name: string; provider: string; command: string; path?: string; available: boolean; status: string; version?: string; auth_state?: string; error?: string };
type Capability = { id: ID; category: string; description: string; runtime: string; implementation: string; permissions: Record<string, unknown> };
type Finding = { finding_id: ID; title: string; state: string; priority: string; score: number; confidence: number; target_id?: ID; created_at: string };
type Artifact = { artifact_id: ID; name: string; type: string; path: string; sha256: string; size: number };
type Approval = { id: ID; task_id: ID; capability_id: ID; reason: string; status: string; requested_at: string };
type Detail = { workspace: Workspace; targets: Target[]; tasks: Task[]; findings: Finding[]; evidence: unknown[]; artifacts: Artifact[]; conversations: Conversation[]; peripherals: Peripheral[]; attachments: unknown[]; captures: unknown[]; events: EventRecord[]; devices: unknown[] };
type Registry = { agents: Agent[]; runtimes: Runtime[]; capabilities: Capability[]; tools: unknown[]; skills: unknown[]; knowledge: unknown[]; approvals: Approval[]; events: EventRecord[]; audit: unknown[] };

async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(API + path, { headers: { 'Content-Type': 'application/json', ...(init?.headers || {}) }, ...init });
  const body = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(body.error || response.statusText || 'Request failed');
  return body as T;
}

async function resolveWailsAPI() {
  const binding = (window as unknown as { go?: { main?: { App?: { APIBase?: () => Promise<string> } } } }).go?.main?.App;
  if (!binding?.APIBase) return;
  try {
    const value = await binding.APIBase();
    if (value) API = value;
  } catch {
    // The Electron shell does not expose Wails bindings and keeps its sidecar URL.
  }
}

const groups: { title: string; entries: [string, string, LucideIcon][] }[] = [
  { title: '工作台', entries: [['overview', '对话管理', MessageSquareText], ['tasks', '任务管理', ListTodo], ['devices', '设备管理', Cpu], ['agents', '智能体管理', Bot]] },
  { title: '外设管理', entries: [['connections', '外设连接', Cable], ['peripheral-config', '外设配置', SlidersHorizontal], ['protocols', '协议分析', Activity]] },
  { title: '漏洞管理', entries: [['vulnerabilities', '漏洞列表', Bug], ['vuln-knowledge', '漏洞知识库', BookOpen]] },
  { title: '配置', entries: [['runtime', '运行时', Cpu], ['skills', 'Skills', Sparkles], ['capabilities', '能力中心', Boxes], ['settings', '设置', Settings]] },
];
const english: Record<string, string> = { '工作台': 'Workbench', '外设管理': 'Peripheral management', '漏洞管理': 'Vulnerability management', '配置': 'Configuration', '对话管理': 'Conversation management', '任务管理': 'Task management', '设备管理': 'Device management', '智能体管理': 'Agent management', '外设连接': 'Peripheral connections', '外设配置': 'Peripheral configuration', '协议分析': 'Protocol analysis', '漏洞列表': 'Vulnerability list', '漏洞知识库': 'Vulnerability knowledge', '运行时': 'Runtime', '能力中心': 'Capability center', '设置': 'Settings', '工作区': 'Workspace', '新建工作区': 'New workspace', '刷新': 'Refresh', '连接中': 'Connected', '离线': 'Offline', '发送': 'Send', '创建任务': 'Create task', '选择目标': 'Select target', '能力': 'Capability', '目标描述': 'Objective', '运行任务': 'Run task', '新建对话': 'New conversation', '从消息创建任务': 'Create task from message', '实时执行': 'Run local runtime', '任务节点': 'Task nodes', '节点输出': 'Node output', '任务总结': 'Task summary', '实时更新': 'Live updates', '发现外设': 'Discover peripherals', '连接': 'Connect', '断开': 'Disconnect', '新增目标设备': 'Add target device', '保存': 'Save', '暂无数据': 'No data', '确定': 'Apply', '名称': 'Name', '厂商': 'Vendor', '型号': 'Model', '地址或标识': 'Address or identifier', '传输方式': 'Transport', '驱动': 'Driver', '端口': 'Port', '状态': 'Status', '当前占用': 'Occupied by', '描述': 'Description', '版本': 'Version', '运行': 'Run', '检查': 'Check', '探测帮助': 'Probe help', '绑定': 'Bind', '解除绑定': 'Unbind', '请输入问题': 'Ask about the IoT target, firmware, or protocol', '研究目标': 'Research objective', '执行智能体': 'Assigned agent', '语言': 'Language', '中文': 'Chinese', '英文': 'English', '本地控制平面': 'Local control plane', '与 Commander 和专业 Agent 交流，并将需要执行的请求转为可追踪任务。': 'Talk to Commander and specialist agents, then turn executable requests into traceable tasks.', '查看 Agent、Capability、节点输出、事件和任务总结，支持暂停、恢复、重试和取消。': 'Track agents, capabilities, node output, events, and task summaries with pause, resume, retry, and cancel controls.', '管理被研究的 IoT 目标设备，并维护与实验外设的关系。': 'Manage IoT targets under study and maintain their relationships with lab peripherals.', '发现、连接和管理 UART、程控电源、示波器、J-Link 与其他实验外设。': 'Discover, connect, and manage UART adapters, power supplies, scopes, J-Link probes, and other lab peripherals.', '根据驱动能力读取、校验并应用外设参数和安全边界。': 'Read, validate, and apply peripheral settings and safety limits from the driver schema.', '区分原始采集数据、解析结果和研究判断，支持后续证据关联。': 'Separate raw captures, parser output, and research judgement for evidence review.', '管理候选问题、验证证据、风险评级和报告状态。': 'Manage candidate issues, validation evidence, risk ratings, and report state.', '配置 Agent 职责、模型运行时和允许使用的能力。': 'Configure agent roles, model runtimes, and allowed capabilities.', '发现主机上的 Claude、Codex、Grok、Kiro，并通过受控非交互会话执行 Agent 请求。': 'Discover Claude, Codex, Grok, and Kiro on the host and run controlled non-interactive agent requests.', '查看系统注册的结构化对象、来源、实现方式和可用状态。': 'Inspect registered objects, sources, implementations, and availability.', '管理界面偏好、工作区存储和本地控制平面策略。': 'Manage interface preferences, workspace storage, and local control-plane policies.' };
const tr = (lang: 'zh' | 'en', value: string) => lang === 'zh' ? value : (english[value] || value);
const escDate = (value?: string) => value ? new Date(value).toLocaleString() : '-';
const statusClass = (value?: string) => ['failed', 'cancelled', 'rejected'].includes(value || '') ? 'danger' : ['queued', 'running', 'blocked', 'paused'].includes(value || '') ? 'warn' : '';
const LanguageContext = createContext<'zh' | 'en'>('en');

function Pill({ value }: { value?: string }) { return <span className={`pill ${statusClass(value)}`}>{value || 'unknown'}</span>; }
function Empty({ children }: { children?: ReactNode }) { const lang = useContext(LanguageContext); return <div className="empty-state">{children || (lang === 'zh' ? '暂无数据' : 'No data')}</div>; }
function Card({ title, subtitle, children, action }: { title: string; subtitle?: string; children: ReactNode; action?: ReactNode }) { return <section className="card"><div className="card-head"><div><h2>{title}</h2>{subtitle && <p>{subtitle}</p>}</div>{action}</div>{children}</section>; }

function App() {
  const [lang, setLang] = useState<'zh' | 'en'>((localStorage.getItem('iothunter.lang') as 'zh' | 'en') || 'en');
  const [page, setPage] = useState('overview');
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  const [selectedWorkspace, setSelectedWorkspace] = useState<ID | null>(null);
  const [detail, setDetail] = useState<Detail | null>(null);
  const [registry, setRegistry] = useState<Registry>({ agents: [], runtimes: [], capabilities: [], tools: [], skills: [], knowledge: [], approvals: [], events: [], audit: [] });
  const [selectedConversation, setSelectedConversation] = useState<ID | null>(null);
  const [selectedTask, setSelectedTask] = useState<ID | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false);

  const say = useCallback((message: string) => { setNotice(message); window.setTimeout(() => setNotice(null), 3200); }, []);
  const loadRegistry = useCallback(async () => {
    const [agents, runtimes, capabilities, tools, skills, knowledge, approvals, events, audit] = await Promise.all([
      api<{ agents: Agent[] }>('/api/v1/agents'), api<{ runtimes: Runtime[] }>('/api/v1/runtimes'), api<{ capabilities: Capability[] }>('/api/v1/capabilities'), api<{ tools: unknown[] }>('/api/v1/tools'), api<{ skills: unknown[] }>('/api/v1/skills'), api<{ knowledge: unknown[] }>('/api/v1/knowledge'), api<{ approvals: Approval[] }>('/api/v1/approvals'), api<{ events: EventRecord[] }>('/api/v1/events'), api<{ audit: unknown[] }>('/api/v1/audit'),
    ]);
    setRegistry({ agents: agents.agents || [], runtimes: runtimes.runtimes || [], capabilities: capabilities.capabilities || [], tools: tools.tools || [], skills: skills.skills || [], knowledge: knowledge.knowledge || [], approvals: approvals.approvals || [], events: events.events || [], audit: audit.audit || [] });
  }, []);
  const loadDetail = useCallback(async (workspaceID: ID | null) => {
    if (!workspaceID) { setDetail(null); return; }
    const value = await api<Detail>(`/api/v1/workspaces/${encodeURIComponent(workspaceID)}`);
    setDetail(value);
    if (!selectedConversation && value.conversations?.[0]) setSelectedConversation(value.conversations[0].id);
    if (!selectedTask && value.tasks?.[0]) setSelectedTask(value.tasks[0].id);
  }, [selectedConversation, selectedTask]);
  const loadWorkspaces = useCallback(async () => {
    const result = await api<{ workspaces: Workspace[] }>('/api/v1/workspaces');
    setWorkspaces(result.workspaces || []);
    if (!selectedWorkspace && result.workspaces?.[0]) setSelectedWorkspace(result.workspaces[0].id);
    if (selectedWorkspace && !result.workspaces.some((item) => item.id === selectedWorkspace)) setSelectedWorkspace(result.workspaces?.[0]?.id || null);
  }, [selectedWorkspace]);
  const refresh = useCallback(async () => { try { await loadWorkspaces(); await loadRegistry(); if (selectedWorkspace) await loadDetail(selectedWorkspace); } catch (error) { say((error as Error).message); } }, [loadDetail, loadRegistry, loadWorkspaces, say, selectedWorkspace]);

  useEffect(() => { loadWorkspaces().catch((error) => say((error as Error).message)); loadRegistry().catch((error) => say((error as Error).message)); }, [loadRegistry, loadWorkspaces, say]);
  useEffect(() => { if (selectedWorkspace) loadDetail(selectedWorkspace).catch((error) => say((error as Error).message)); }, [loadDetail, say, selectedWorkspace]);
  useEffect(() => {
    if (!selectedWorkspace || !detail?.tasks?.some((task) => ['queued', 'assigned', 'running', 'blocked'].includes(task.status))) return;
    const timer = window.setInterval(() => loadDetail(selectedWorkspace).catch(() => undefined), 1200);
    return () => window.clearInterval(timer);
  }, [detail?.tasks, loadDetail, selectedWorkspace]);
  useEffect(() => { if (page !== 'tasks' || !selectedTask) return; const source = new EventSource(`${API}/api/v1/tasks/${encodeURIComponent(selectedTask)}/events`); source.onmessage = () => { if (selectedWorkspace) loadDetail(selectedWorkspace).catch(() => undefined); }; source.onerror = () => source.close(); return () => source.close(); }, [loadDetail, page, selectedTask, selectedWorkspace]);

  const workspace = detail?.workspace || workspaces.find((item) => item.id === selectedWorkspace) || null;
  const selectWorkspace = async (value: string) => {
    if (value === '__new__') {
      const name = window.prompt(tr(lang, '新建工作区'), 'iot-research');
      if (!name?.trim()) return;
      try { const created = await api<Workspace>('/api/v1/workspaces', { method: 'POST', body: JSON.stringify({ name: name.trim(), owner: 'local', description: 'IoT security research workspace' }) }); setSelectedWorkspace(created.id); setDetail(null); say(lang === 'zh' ? '工作区已创建' : 'Workspace created'); } catch (error) { say((error as Error).message); }
      return;
    }
    setSelectedWorkspace(value || null); setSelectedConversation(null); setSelectedTask(null);
  };
  const navigate = (value: string) => { setPage(value); if (value !== 'tasks') setSelectedTask(null); };
  const activeEntry = groups.flatMap((group) => group.entries.map((entry) => ({ group: group.title, entry }))).find(({ entry }) => entry[0] === page);

  return <LanguageContext.Provider value={lang}><div className={`react-layout ${sidebarCollapsed ? 'sidebar-collapsed' : ''}`}>
    <aside className="react-sidebar"><div className="brand"><img src="/logo2.png" alt="IoTHunter" /><div className="brand-copy"><strong>IoTHunter</strong><small>IoT SECURITY RESEARCH</small></div></div><div className="nav-scroll">{groups.map((group) => <div className="nav-group" key={group.title}><div className="nav-label">{tr(lang, group.title)}</div><nav className="nav">{group.entries.map(([id, label, Icon]) => <button key={id} title={tr(lang, label)} className={`nav-button ${page === id ? 'active' : ''}`} onClick={() => navigate(id)}><span className="icon"><Icon size={16} strokeWidth={1.8} /></span><span className="nav-text">{tr(lang, label)}</span></button>)}</nav></div>)}</div><div className="sidebar-footer"><span className="online-dot" /><span className="nav-text">{tr(lang, '本地控制平面')}</span><span className="version">v0.4</span></div></aside>
    <main className="react-main"><header className="react-topbar"><button className="icon-button sidebar-toggle" title={sidebarCollapsed ? 'Expand sidebar' : 'Collapse sidebar'} onClick={() => setSidebarCollapsed((value) => !value)}><PanelLeft size={17} /></button><div className="window-controls"><button aria-label="Back" onClick={() => window.history.back()}><ChevronLeft size={17} /></button><button aria-label="Forward" onClick={() => window.history.forward()}><ChevronRight size={17} /></button></div><div className="topbar-location"><span>{tr(lang, activeEntry?.group || '工作台')}</span><strong>{tr(lang, activeEntry?.entry[1] || '对话管理')}</strong></div><div className="topbar-spacer" /><select className="workspace-picker" value={selectedWorkspace || ''} onChange={(event) => selectWorkspace(event.target.value)}><option value="">{tr(lang, '工作区')}</option>{workspaces.map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}<option value="__new__">＋ {tr(lang, '新建工作区')}</option></select><button className="icon-button top-action" title={lang === 'zh' ? 'Switch to English' : '切换为中文'} onClick={() => { const next = lang === 'zh' ? 'en' : 'zh'; setLang(next); localStorage.setItem('iothunter.lang', next); }}><Languages size={17} /><span>{lang === 'zh' ? 'EN' : '中'}</span></button><button className="icon-button refresh" title={tr(lang, '刷新')} onClick={() => refresh()}><RefreshCw size={17} /></button></header><div className="react-content"><ViewRouter lang={lang} page={page} detail={detail} registry={registry} selectedConversation={selectedConversation} selectedTask={selectedTask} setSelectedConversation={setSelectedConversation} setSelectedTask={setSelectedTask} refresh={refresh} say={say} workspace={workspace} selectedWorkspace={selectedWorkspace} /></div></main>{notice && <div className="toast show">{notice}</div>}
  </div></LanguageContext.Provider>;
}

type ViewProps = { lang: 'zh' | 'en'; detail: Detail | null; registry: Registry; selectedConversation: ID | null; selectedTask: ID | null; setSelectedConversation: (id: ID) => void; setSelectedTask: (id: ID) => void; refresh: () => Promise<void>; say: (message: string) => void; workspace: Workspace | null; selectedWorkspace: ID | null };
function ViewRouter({ page, ...props }: ViewProps & { page: string }) {
  switch (page) {
    case 'overview': return <ConversationView {...props} />;
    case 'tasks': return <TaskView {...props} />;
    case 'devices': return <DeviceView {...props} />;
    case 'connections': return <ConnectionView {...props} />;
    case 'peripheral-config': return <PeripheralConfigView {...props} />;
    case 'protocols': return <ProtocolView {...props} />;
    case 'vulnerabilities': return <FindingView {...props} />;
    case 'vuln-knowledge': return <RegistryView {...props} title="漏洞知识库" rows={props.registry.knowledge} />;
    case 'agents': return <AgentView {...props} />;
    case 'runtime': return <RuntimeView {...props} />;
    case 'skills': return <SkillsView {...props} />;
    case 'capabilities': return <CapabilitiesView {...props} />;
    case 'settings': return <SettingsView {...props} />;
    default: return <Empty />;
  }
}

function Page({ lang, group, title, description, children, action }: { lang: 'zh' | 'en'; group: string; title: string; description: string; children: ReactNode; action?: ReactNode }) { return <div className="react-page"><div className="page-head"><div className="page-title"><h1>{tr(lang, title)}</h1><p>{tr(lang, description)}</p></div><div className="actions">{action}</div></div><div className="page-body" data-section={group}>{children}</div></div>; }

function ConversationView({ lang, detail, registry, selectedConversation, setSelectedConversation, refresh, say, selectedWorkspace, workspace }: ViewProps) {
  const conversations = detail?.conversations || [];
  const current = conversations.find((item) => item.id === selectedConversation) || conversations[0];
  const commander = registry.agents.find((item) => item.id === 'commander-default');
  const availableRuntimes = registry.runtimes.filter((item) => item.available && item.status === 'available');
  const [content, setContent] = useState('');
  const [createTask, setCreateTask] = useState(false);
  const [targetID, setTargetID] = useState('');
  const [runtimeID, setRuntimeID] = useState('');
  const [sending, setSending] = useState(false);
  const [pendingContent, setPendingContent] = useState('');
  const [conversationSearch, setConversationSearch] = useState('');
  const messagesRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (runtimeID && availableRuntimes.some((item) => item.id === runtimeID)) return;
    const preferred = availableRuntimes.find((item) => item.id === commander?.runtime_id) || availableRuntimes[0];
    setRuntimeID(preferred?.id || '');
  }, [availableRuntimes, commander?.runtime_id, runtimeID]);

  useEffect(() => {
    const container = messagesRef.current;
    if (!container) return;
    container.scrollTop = container.scrollHeight;
  }, [current?.id, current?.messages?.length, pendingContent, sending]);

  const send = async (event: FormEvent) => {
    event.preventDefault();
    const prompt = content.trim();
    if (!selectedWorkspace || !prompt || sending) return;
    if (!runtimeID) {
      say(lang === 'zh' ? '没有可用的本地运行时，请先在“运行时”页面完成配置' : 'No local runtime is available. Configure one on the Runtime page.');
      return;
    }
    if (createTask && !targetID) {
      say(lang === 'zh' ? '创建任务前请选择目标设备' : 'Select a target before creating a task.');
      return;
    }
    setSending(true);
    setPendingContent(prompt);
    try {
      let conversationID = current?.id;
      if (!conversationID) {
        const created = await api<Conversation>(`/api/v1/workspaces/${selectedWorkspace}/conversations`, { method: 'POST', body: JSON.stringify({ title: prompt.slice(0, 48) }) });
        conversationID = created.id;
        setSelectedConversation(created.id);
      }
      if (commander?.runtime_id !== runtimeID) {
        await api(`/api/v1/agents/commander-default`, { method: 'POST', body: JSON.stringify({ runtime_id: runtimeID }) });
      }
      const response = await api<{ conversation: Conversation; runtime?: { status: string; error?: string }; task?: Task }>(`/api/v1/conversations/${conversationID}/message`, {
        method: 'POST',
        body: JSON.stringify({ content: prompt, create_task: createTask, run_runtime: true, runtime_id: runtimeID, target_id: targetID }),
      });
      setContent('');
      await refresh();
      if (response.runtime?.status === 'failed') {
        say(response.runtime.error || (lang === 'zh' ? '本地运行时执行失败' : 'Local runtime failed'));
      } else {
        say(response.task ? (lang === 'zh' ? '已回复并创建任务' : 'Reply received and task created') : (lang === 'zh' ? '已收到回复' : 'Reply received'));
      }
    } catch (error) {
      say((error as Error).message);
    } finally {
      setPendingContent('');
      setSending(false);
    }
  };

  const runtime = availableRuntimes.find((item) => item.id === runtimeID);
  const createConversation = async () => { if (!selectedWorkspace) return; try { const created = await api<Conversation>(`/api/v1/workspaces/${selectedWorkspace}/conversations`, { method: 'POST', body: JSON.stringify({ title: lang === 'zh' ? '新建对话' : 'New conversation' }) }); setSelectedConversation(created.id); await refresh(); } catch (error) { say((error as Error).message); } };
  const visibleConversations = conversations.filter((item) => item.title.toLowerCase().includes(conversationSearch.trim().toLowerCase()));
  return <Page lang={lang} group="工作台" title="对话管理" description="与 Commander 和专业 Agent 交流，并将需要执行的请求转为可追踪任务。">
    {!selectedWorkspace ? <Empty>{tr(lang, '请先选择工作区')}</Empty> : <div className="conversation-shell-react">
      <section className="conversation-list-react">
        <div className="pane-head"><div><strong>{lang === 'zh' ? '对话' : 'Chats'}</strong><span>{conversations.length}</span></div><button className="icon-button primary-icon" title={tr(lang, '新建对话')} onClick={createConversation}><CirclePlus size={17} /></button></div>
        <label className="pane-search"><Search size={15} /><input value={conversationSearch} onChange={(event) => setConversationSearch(event.target.value)} placeholder={lang === 'zh' ? '搜索对话' : 'Search conversations'} /></label>
        <div className="conversation-thread-list">{visibleConversations.length ? visibleConversations.map((item) => { const last = item.messages?.[item.messages.length - 1]; return <button key={item.id} className={`list-button ${item.id === current?.id ? 'selected' : ''}`} onClick={() => setSelectedConversation(item.id)}><span className="thread-avatar"><MessageSquareText size={15} /></span><span className="thread-copy"><strong>{item.title}</strong><small>{last?.content || (lang === 'zh' ? '暂无消息' : 'No messages yet')}</small></span><time>{item.updated_at ? new Date(item.updated_at).toLocaleDateString([], { month: 'numeric', day: 'numeric' }) : ''}</time></button>; }) : <Empty>{lang === 'zh' ? '没有找到对话' : 'No conversations found'}</Empty>}</div>
      </section>
      <section className="conversation-main-react">
        <div className="conversation-session-head"><span className="session-avatar"><Bot size={18} /></span><div><strong>{current?.title || (lang === 'zh' ? '新对话' : 'New conversation')}</strong><small>Commander · {runtime?.name || (lang === 'zh' ? '未配置运行时' : 'Runtime not configured')}</small></div><span className={`session-state ${runtime ? 'online' : ''}`}>{runtime ? (lang === 'zh' ? '就绪' : 'Ready') : (lang === 'zh' ? '离线' : 'Offline')}</span></div>
        <div className="conversation-messages" ref={messagesRef}>
          {current?.messages?.length ? current.messages.map((item) => <div key={item.id} className={`message-row ${item.role === 'user' ? 'user' : 'assistant'}`}><span className="message-avatar">{item.role === 'user' ? (lang === 'zh' ? '我' : 'ME') : <Bot size={16} />}</span><div className="message"><span className="message-role">{item.role === 'user' ? (lang === 'zh' ? '你' : 'You') : 'Commander'}{item.references?.runtimes?.[0] ? ` · ${item.references.runtimes[0]}` : ''}</span><div className="message-body">{item.content}</div><time>{escDate(item.created_at)}</time></div></div>) : <div className="chat-empty"><span className="session-avatar large"><Bot size={23} /></span><strong>{lang === 'zh' ? '从一个 IoT 研究问题开始' : 'Start with an IoT research question'}</strong><p>{lang === 'zh' ? '可以询问固件、协议或已连接的实验设备。' : 'Ask about firmware, protocols, or connected lab equipment.'}</p></div>}
          {sending && <><div className="message-row user pending"><span className="message-avatar">{lang === 'zh' ? '我' : 'ME'}</span><div className="message"><span className="message-role">{lang === 'zh' ? '你' : 'You'}</span><div className="message-body">{pendingContent}</div></div></div><div className="message-row assistant thinking"><span className="message-avatar"><Bot size={16} /></span><div className="message"><span className="message-role">Commander · {runtime?.name || runtimeID}</span><div className="thinking-line"><span className="thinking-dot" /><span>{lang === 'zh' ? '正在调用本地运行时…' : 'Running the local runtime…'}</span></div></div></div></>}
        </div>
        <form className="conversation-compose" onSubmit={send}>
          <div className="composer-box"><textarea value={content} onChange={(event) => setContent(event.target.value)} onKeyDown={(event) => { if (event.key === 'Enter' && !event.shiftKey) { event.preventDefault(); event.currentTarget.form?.requestSubmit(); } }} placeholder={tr(lang, '请输入问题')} required disabled={sending} /><div className="composer-toolbar"><select value={runtimeID} onChange={(event) => setRuntimeID(event.target.value)} disabled={sending} title={lang === 'zh' ? 'Commander 运行时' : 'Commander runtime'}><option value="">{lang === 'zh' ? '未配置运行时' : 'No runtime'}</option>{availableRuntimes.map((item) => <option key={item.id} value={item.id}>{item.name} · {item.version || item.status}</option>)}</select><select value={targetID} onChange={(event) => setTargetID(event.target.value)} disabled={sending}><option value="">{tr(lang, '选择目标')}</option>{(detail?.targets || []).map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}</select><label className="inline-check composer-task"><input type="checkbox" checked={createTask} onChange={(event) => setCreateTask(event.target.checked)} disabled={sending} /><span>{tr(lang, '创建任务')}</span></label><button className="send-button" title={tr(lang, '发送')} disabled={sending || !runtimeID || !content.trim()}>{sending ? <span className="thinking-dot" /> : <Send size={17} />}</button></div></div><div className="composer-foot"><span>{lang === 'zh' ? 'Enter 发送 · Shift+Enter 换行' : 'Enter to send · Shift+Enter for a new line'}</span><span>{workspace?.name}</span></div>
        </form>
      </section>
    </div>}
  </Page>;
}

function TaskView({ lang, detail, registry, selectedTask, setSelectedTask, refresh, say, selectedWorkspace }: ViewProps) {
  const tasks = detail?.tasks || []; const current = tasks.find((item) => item.id === selectedTask) || tasks[0];
  const [targetID, setTargetID] = useState(''); const [capabilityIDs, setCapabilityIDs] = useState<string[]>([]); const [objective, setObjective] = useState(''); const [inputsJSON, setInputsJSON] = useState('{}'); const [artifactPath, setArtifactPath] = useState(''); const [permissions, setPermissions] = useState({ network: false, device: false, destructive: false });
  const [taskSearch, setTaskSearch] = useState('');
  const [showCreate, setShowCreate] = useState(false);
  useEffect(() => { if (!targetID && detail?.targets?.[0]) setTargetID(detail.targets[0].id); if (!capabilityIDs.length && registry.capabilities?.[0]) setCapabilityIDs([registry.capabilities[0].id]); }, [capabilityIDs.length, detail?.targets, registry.capabilities, targetID]);
  const create = async (event: FormEvent) => { event.preventDefault(); if (!selectedWorkspace || !targetID || !capabilityIDs.length || !objective.trim()) return; try { const inputs = JSON.parse(inputsJSON || '{}'); if (!inputs || Array.isArray(inputs)) throw new Error('Inputs must be a JSON object'); const response = await api<{ tasks: Task[] }>(`/api/v1/workspaces/${selectedWorkspace}/plan`, { method: 'POST', body: JSON.stringify({ target_id: targetID, objective, capabilities: capabilityIDs, inputs, permissions: { filesystem: 'workspace-readonly', ...permissions }, budget: { max_runtime_seconds: 600, max_tool_calls: 20 } }) }); setObjective(''); setShowCreate(false); if (response.tasks?.[0]) setSelectedTask(response.tasks[0].id); await refresh(); say(lang === 'zh' ? '任务已创建并开始执行' : 'Task created and execution started'); } catch (error) { say((error as Error).message); } };
  const importArtifact = async () => { if (!selectedWorkspace || !artifactPath.trim()) return; try { const artifact = await api<Artifact>(`/api/v1/workspaces/${selectedWorkspace}/artifacts`, { method: 'POST', body: JSON.stringify({ path: artifactPath.trim(), type: 'input' }) }); const value = JSON.parse(inputsJSON || '{}'); setInputsJSON(JSON.stringify({ ...value, artifact_id: artifact.artifact_id, path: artifact.path }, null, 2)); await refresh(); say(lang === 'zh' ? '文件已导入工作区' : 'Artifact imported into workspace'); } catch (error) { say((error as Error).message); } };
  const action = async (name: string) => { if (!current) return; try { await api(`/api/v1/tasks/${current.id}/${name}`, { method: 'POST', body: '{}' }); await refresh(); } catch (error) { say((error as Error).message); } };
  const decide = async (approval: Approval, status: 'approved' | 'rejected') => { try { await api(`/api/v1/approvals/${approval.id}`, { method: 'POST', body: JSON.stringify({ status, actor: 'desktop-ui' }) }); await refresh(); } catch (error) { say((error as Error).message); } };
  const currentApprovals = registry.approvals.filter((item) => item.task_id === current?.id && item.status === 'pending');
  const visibleTasks = tasks.filter((item) => item.objective.toLowerCase().includes(taskSearch.trim().toLowerCase()));
  const taskEvents = (detail?.events || []).filter((event) => event.task_id === current?.id).slice(-20).reverse();
  return <Page lang={lang} group="工作台" title="任务管理" description="查看 Agent、Capability、节点输出、事件和任务总结，支持暂停、恢复、重试和取消。" action={<button className="btn primary" onClick={() => setShowCreate(true)}><CirclePlus size={16} />{tr(lang, '创建任务')}</button>}>
    <div className="task-shell-react">
      <section className="task-list-panel-react">
        <div className="pane-head"><div><strong>{lang === 'zh' ? '任务' : 'Tasks'}</strong><span>{tasks.length}</span></div></div>
        <label className="pane-search"><Search size={15} /><input value={taskSearch} onChange={(event) => setTaskSearch(event.target.value)} placeholder={lang === 'zh' ? '搜索任务' : 'Search tasks'} /></label>
        <div className="task-list-react">{visibleTasks.length ? visibleTasks.map((item) => <button key={item.id} className={`task-list-row-react ${item.id === current?.id && !showCreate ? 'selected' : ''}`} onClick={() => { setSelectedTask(item.id); setShowCreate(false); }}><span className={`task-status-dot ${statusClass(item.status)} ${item.status}`} /><span className="queue-main"><strong>{item.objective}</strong><small>{item.required_capabilities?.join(' → ') || item.type}</small><time>{escDate(item.updated_at)}</time></span><Pill value={item.status} /></button>) : <Empty>{lang === 'zh' ? '没有找到任务' : 'No tasks found'}</Empty>}</div>
      </section>
      <section className="task-main-react">
        {showCreate ? <div className="task-create-surface">
          <div className="detail-toolbar"><div><strong>{tr(lang, '创建任务')}</strong><span>{lang === 'zh' ? '规划能力步骤并立即执行' : 'Plan capability steps and start execution'}</span></div><button className="icon-button" title={lang === 'zh' ? '关闭' : 'Close'} onClick={() => setShowCreate(false)}><X size={17} /></button></div>
          <form className="task-create-form" onSubmit={create}>
            <section className="form-section"><h3>{lang === 'zh' ? '基本信息' : 'Basics'}</h3><div className="form-grid"><label>{tr(lang, '选择目标')}<select value={targetID} onChange={(event) => setTargetID(event.target.value)} required><option value="">{tr(lang, '选择目标')}</option>{(detail?.targets || []).map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label><label className="field-wide">{tr(lang, '目标描述')}<textarea value={objective} onChange={(event) => setObjective(event.target.value)} rows={4} required placeholder={lang === 'zh' ? '例如：分析固件 Web 攻击面' : 'For example: analyze the firmware web attack surface'} /></label></div></section>
            <section className="form-section"><h3>{lang === 'zh' ? '执行步骤' : 'Execution steps'}</h3><label>{lang === 'zh' ? '能力（可多选）' : 'Capabilities (multi-select)'}<select multiple size={Math.min(7, Math.max(4, registry.capabilities.length))} value={capabilityIDs} onChange={(event) => setCapabilityIDs(Array.from(event.currentTarget.selectedOptions, (option) => option.value))} required>{registry.capabilities.map((item) => <option key={item.id} value={item.id}>{item.id} · {item.description}</option>)}</select></label><label>{lang === 'zh' ? '输入 JSON' : 'Input JSON'}<textarea className="code-input" value={inputsJSON} onChange={(event) => setInputsJSON(event.target.value)} rows={6} placeholder={'{"path":"/workspace/firmware.bin"}'} /></label><div className="artifact-import"><label><FileUp size={15} />{lang === 'zh' ? '导入本地文件' : 'Import local artifact'}<input value={artifactPath} onChange={(event) => setArtifactPath(event.target.value)} placeholder="/path/to/firmware.bin" /></label><button type="button" className="btn" onClick={importArtifact}>{lang === 'zh' ? '导入' : 'Import'}</button></div></section>
            <section className="form-section"><h3><ShieldCheck size={15} />{lang === 'zh' ? '本次权限' : 'Run permissions'}</h3><div className="permission-grid"><label><input type="checkbox" checked={permissions.network} onChange={(event) => setPermissions({ ...permissions, network: event.target.checked })} /> Network</label><label><input type="checkbox" checked={permissions.device} onChange={(event) => setPermissions({ ...permissions, device: event.target.checked })} /> Device</label><label><input type="checkbox" checked={permissions.destructive} onChange={(event) => setPermissions({ ...permissions, destructive: event.target.checked, device: event.target.checked || permissions.device })} /> Destructive</label></div></section>
            <div className="sticky-form-actions"><button type="button" className="btn" onClick={() => setShowCreate(false)}>{lang === 'zh' ? '取消' : 'Cancel'}</button><button className="btn primary"><Play size={15} />{tr(lang, '运行任务')}</button></div>
          </form>
        </div> : current ? <div className="task-detail-surface">
          <div className="detail-toolbar"><div><strong>{current.objective}</strong><span>{current.id} · {escDate(current.updated_at)}</span></div><div className="actions">{['queued', 'running', 'paused'].includes(current.status) && <button className="btn" onClick={() => action(current.status === 'paused' ? 'resume' : 'pause')}>{current.status === 'paused' ? <Play size={15} /> : <Pause size={15} />}{current.status === 'paused' ? (lang === 'zh' ? '恢复' : 'Resume') : (lang === 'zh' ? '暂停' : 'Pause')}</button>}{['failed', 'blocked', 'cancelled'].includes(current.status) && <button className="btn" onClick={() => action('retry')}><RotateCcw size={15} />{lang === 'zh' ? '重试' : 'Retry'}</button>}{!['completed', 'cancelled'].includes(current.status) && <button className="btn danger-button" onClick={() => action('cancel')}><X size={15} />{lang === 'zh' ? '取消' : 'Cancel'}</button>}</div></div>
          <div className="task-summary"><div><span>{tr(lang, '状态')}</span><Pill value={current.status} /></div><div><span>{tr(lang, '执行智能体')}</span><strong>{current.assigned_agent || '-'}</strong></div><div><span>{lang === 'zh' ? '能力数' : 'Capabilities'}</span><strong>{current.required_capabilities?.length || 0}</strong></div><div><span>{lang === 'zh' ? '节点进度' : 'Node progress'}</span><strong>{current.nodes?.filter((node) => node.status === 'completed').length || 0} / {current.nodes?.length || 0}</strong></div></div>
          {currentApprovals.map((approval) => <div className="approval-row" key={approval.id}><ShieldCheck size={18} /><div><strong>{approval.capability_id}</strong><p>{approval.reason}</p></div><button className="btn primary" onClick={() => decide(approval, 'approved')}>{lang === 'zh' ? '批准' : 'Approve'}</button><button className="btn" onClick={() => decide(approval, 'rejected')}>{lang === 'zh' ? '拒绝' : 'Reject'}</button></div>)}
          <div className="task-detail-scroll"><section className="task-section"><div className="section-title"><h3>{tr(lang, '任务节点')}</h3><span>{current.nodes?.length || 0}</span></div>{current.nodes?.length ? <div className="task-timeline">{current.nodes.map((node, index) => <div className={`task-node-react ${node.status}`} key={node.id}><div className="timeline-rail"><span>{index + 1}</span></div><div className="task-node-content"><div className="task-node-head"><strong>{node.name}</strong><Pill value={node.status} /></div><p>{node.summary || (lang === 'zh' ? '等待节点输出' : 'Waiting for node output')}</p>{node.output && <details open={node.status === 'failed'}><summary>{tr(lang, '节点输出')}</summary><pre>{JSON.stringify(node.output, null, 2)}</pre></details>}</div></div>)}</div> : <Empty />}</section>
            <section className="task-section"><div className="section-title"><h3>{tr(lang, '任务总结')}</h3></div><div className="task-result">{current.summary || (lang === 'zh' ? '任务完成后将在这里生成总结。' : 'A summary will appear here when the task completes.')}</div>{current.output && <pre>{JSON.stringify(current.output, null, 2)}</pre>}</section>
            <section className="task-section"><div className="section-title"><h3>{tr(lang, '实时更新')}</h3><span>{taskEvents.length}</span></div><div className="event-list">{taskEvents.length ? taskEvents.map((event) => <div className="event-row" key={event.id}><time>{escDate(event.created_at)}</time><strong>{event.type}</strong></div>) : <Empty />}</div></section>
          </div>
        </div> : <Empty>{lang === 'zh' ? '选择任务或创建新任务' : 'Select a task or create a new one'}</Empty>}
      </section>
    </div>
  </Page>;
}

function DeviceView({ lang, detail, refresh, say, selectedWorkspace }: ViewProps) {
  const [form, setForm] = useState({ name: '', vendor: '', model: '', transport: '', address: '' });
  const submit = async (event: FormEvent) => { event.preventDefault(); if (!selectedWorkspace) return; try { await api(`/api/v1/workspaces/${selectedWorkspace}/targets`, { method: 'POST', body: JSON.stringify({ ...form, authorized: true }) }); setForm({ name: '', vendor: '', model: '', transport: '', address: '' }); await refresh(); say(lang === 'zh' ? '目标设备已添加' : 'Target added'); } catch (error) { say((error as Error).message); } };
  return <Page lang={lang} group="工作台" title="设备管理" description="管理被研究的 IoT 目标设备，并维护与实验外设的关系。"><Card title={lang === 'zh' ? '目标设备' : 'Target devices'} subtitle={detail?.workspace?.name}>{detail?.targets?.length ? <div className="table-wrap"><table><thead><tr>{['名称', '厂商 / 型号', '地址', '传输方式', '授权'].map((item) => <th key={item}>{tr(lang, item)}</th>)}</tr></thead><tbody>{detail.targets.map((item) => <tr key={item.id}><td><strong>{item.name}</strong><div className="mono">{item.id}</div></td><td>{item.vendor} {item.model}</td><td>{item.address || '-'}</td><td>{item.transport || '-'}</td><td><Pill value={item.authorized ? 'authorized' : 'unverified'} /></td></tr>)}</tbody></table></div> : <Empty />}</Card><Card title={tr(lang, '新增目标设备')}><form className="form" onSubmit={submit}><div className="form-grid">{Object.entries(form).map(([key, value]) => <label key={key}>{tr(lang, key === 'name' ? '名称' : key === 'vendor' ? '厂商' : key === 'model' ? '型号' : key === 'transport' ? '传输方式' : '地址或标识')}<input value={value} onChange={(event) => setForm({ ...form, [key]: event.target.value })} required={key === 'name'} /></label>)}</div><button className="btn primary">{tr(lang, '新增目标设备')}</button></form></Card></Page>;
}

function ConnectionView({ lang, detail, refresh, say, selectedWorkspace }: ViewProps) {
  const [form, setForm] = useState({ name: '', kind: 'serial', driver: 'go.bug.st/serial', port: '', target_id: '', role: 'debug' });
  const [discovered, setDiscovered] = useState<PeripheralDescriptor[]>([]);
  const [sessions, setSessions] = useState<PeripheralSession[]>([]);
  const [selectedPeripheral, setSelectedPeripheral] = useState('');
  const [terminalInput, setTerminalInput] = useState('');
  const [terminalOutput, setTerminalOutput] = useState('');
  const [identity, setIdentity] = useState<Record<string, unknown> | null>(null);
  const [discovering, setDiscovering] = useState(false);
  const [connecting, setConnecting] = useState('');
  const [monitoring, setMonitoring] = useState(false);
  const [txMode, setTxMode] = useState<'text' | 'hex'>('text');
  const [rxMode, setRxMode] = useState<'text' | 'hex'>('text');
  const [lineEnding, setLineEnding] = useState<'none' | 'lf' | 'crlf'>('none');

  const loadSessions = useCallback(async () => {
    const value = await api<{ sessions: PeripheralSession[] }>('/api/v1/peripheral-sessions');
    setSessions(value.sessions || []);
    return value.sessions || [];
  }, []);
  useEffect(() => { loadSessions().catch(() => setSessions([])); }, [detail?.peripherals, loadSessions]);
  useEffect(() => {
    if (!selectedPeripheral && detail?.peripherals?.[0]) setSelectedPeripheral(detail.peripherals[0].id);
    if (selectedPeripheral && detail?.peripherals && !detail.peripherals.some((item) => item.id === selectedPeripheral)) setSelectedPeripheral(detail.peripherals[0]?.id || '');
  }, [detail?.peripherals, selectedPeripheral]);

  const selected = detail?.peripherals?.find((item) => item.id === selectedPeripheral);
  const activeSession = sessions.find((value) => value.peripheral_id === selectedPeripheral);
  useEffect(() => {
    if (!activeSession) { setIdentity(null); return; }
    api<PeripheralInvokeResult>(`/api/v1/peripheral-sessions/${activeSession.session_id}/invoke`, { method: 'POST', body: JSON.stringify({ command: 'identity', args: {} }) })
      .then((result) => setIdentity(result.data || null))
      .catch(() => setIdentity(null));
  }, [activeSession?.session_id]);

  const attachTarget = async (peripheralID: string, targetID: string) => {
    if (!targetID || !selectedWorkspace) return;
    await api(`/api/v1/workspaces/${selectedWorkspace}/attachments`, { method: 'POST', body: JSON.stringify({ target_device_id: targetID, peripheral_id: peripheralID, role: form.role }) });
  };
  const registerPeripheral = async (value: { name: string; kind: string; driver?: string; endpoint?: string }, targetID = form.target_id) => {
    if (!selectedWorkspace) throw new Error(lang === 'zh' ? '请先选择工作区' : 'Select a workspace first');
    const existing = detail?.peripherals?.find((item) => item.kind === value.kind && (item.port || item.address) === value.endpoint);
    if (existing) {
      setSelectedPeripheral(existing.id);
      say(lang === 'zh' ? '该端口已注册，已选中现有外设' : 'This endpoint is already registered and has been selected');
      return existing;
    }
    const created = await api<Peripheral>(`/api/v1/workspaces/${selectedWorkspace}/peripherals`, {
      method: 'POST',
      body: JSON.stringify({ name: value.name, kind: value.kind, driver: value.driver, port: value.endpoint, capabilities: [], config: value.kind === 'serial' ? { baud_rate: 115200, data_bits: 8, stop_bits: 1, parity: 'none', read_timeout_ms: 250 } : {} }),
    });
    await attachTarget(created.id, targetID);
    setSelectedPeripheral(created.id);
    await refresh();
    return created;
  };
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    try {
      await registerPeripheral({ name: form.name, kind: form.kind, driver: form.driver, endpoint: form.port });
      setForm((value) => ({ ...value, name: '', port: '' }));
      say(lang === 'zh' ? '外设已注册' : 'Peripheral registered');
    } catch (error) { say((error as Error).message); }
  };
  const discover = async () => {
    setDiscovering(true);
    try {
      const result = await api<{ peripherals: PeripheralDescriptor[] }>('/api/v1/peripherals/discover', { method: 'POST', body: JSON.stringify({ kind: form.kind }) });
      setDiscovered(result.peripherals || []);
      if (!result.peripherals?.length) say(lang === 'zh' ? '未发现可用外设' : 'No peripherals discovered');
    } catch (error) { say((error as Error).message); } finally { setDiscovering(false); }
  };
  const useDiscovered = async (item: PeripheralDescriptor) => {
    try {
      const created = await registerPeripheral({ name: item.name, kind: item.kind, driver: item.driver, endpoint: item.endpoint });
      say(created.status === 'offline' ? (lang === 'zh' ? '外设已注册，可以连接' : 'Peripheral registered and ready to connect') : (lang === 'zh' ? '已选中外设' : 'Peripheral selected'));
    } catch (error) { say((error as Error).message); }
  };
  const connect = async (item: Peripheral) => {
    setConnecting(item.id);
    setSelectedPeripheral(item.id);
    setMonitoring(false);
    setIdentity(null);
    try {
      const currentSessions = await loadSessions();
      const current = currentSessions.find((value) => value.peripheral_id === item.id);
      if (current) {
        await api(`/api/v1/peripheral-sessions/${current.session_id}/disconnect`, { method: 'POST', body: '{}' });
        setSessions((values) => values.filter((value) => value.session_id !== current.session_id));
        say(lang === 'zh' ? '外设已断开' : 'Peripheral disconnected');
      } else {
        const result = await api<{ session: PeripheralSession }>(`/api/v1/peripherals/${item.id}/connect`, { method: 'POST', body: JSON.stringify({ owner_type: 'user', owner_id: 'desktop-ui', mode: 'exclusive', lease_ttl_seconds: 7200 }) });
        setSessions((values) => [...values.filter((value) => value.peripheral_id !== item.id), result.session]);
        say(lang === 'zh' ? '外设连接成功' : 'Peripheral connected');
      }
      await refresh();
    } catch (error) { say((error as Error).message); } finally { setConnecting(''); }
  };
  const readBytes = async (session: PeripheralSession) => {
    const result = await api<PeripheralInvokeResult>(`/api/v1/peripheral-sessions/${session.session_id}/invoke`, { method: 'POST', body: JSON.stringify({ command: 'read', args: { max_bytes: 16384 } }) });
    if (!result.bytes) return false;
    const raw = Uint8Array.from(atob(result.bytes), (character) => character.charCodeAt(0));
    const received = rxMode === 'hex' ? Array.from(raw, (byte) => byte.toString(16).padStart(2, '0')).join(' ') : new TextDecoder().decode(raw);
    if (received) setTerminalOutput((value) => `${value}${value ? '\n' : ''}[RX ${new Date().toLocaleTimeString()}] ${received}`);
    return Boolean(received);
  };
  const readTerminal = async () => {
    if (!activeSession) return;
    try { await readBytes(activeSession); } catch (error) { say((error as Error).message); }
  };
  useEffect(() => {
    if (!monitoring || !activeSession) return;
    let stopped = false;
    let reading = false;
    const poll = async () => {
      if (stopped || reading) return;
      reading = true;
      try { await readBytes(activeSession); } catch (error) { if (!stopped) { setMonitoring(false); say((error as Error).message); } } finally { reading = false; }
    };
    poll();
    const timer = window.setInterval(poll, 350);
    return () => { stopped = true; window.clearInterval(timer); };
  }, [activeSession?.session_id, monitoring, rxMode]);
  const sendTerminal = async () => {
    if (!activeSession || !terminalInput) return;
    try {
      let raw: Uint8Array;
      if (txMode === 'hex') {
        const compact = terminalInput.replace(/0x/gi, '').replace(/[\s,:-]+/g, '');
        if (!compact || compact.length % 2 !== 0 || !/^[0-9a-f]+$/i.test(compact)) throw new Error(lang === 'zh' ? '十六进制数据必须由完整字节组成' : 'Hex data must contain complete bytes');
        raw = Uint8Array.from(compact.match(/.{2}/g) || [], (value) => Number.parseInt(value, 16));
      } else {
        const suffix = lineEnding === 'lf' ? '\n' : lineEnding === 'crlf' ? '\r\n' : '';
        raw = new TextEncoder().encode(terminalInput + suffix);
      }
      let binary = '';
      raw.forEach((byte) => { binary += String.fromCharCode(byte); });
      await api(`/api/v1/peripheral-sessions/${activeSession.session_id}/invoke`, { method: 'POST', body: JSON.stringify({ command: 'write', args: { data_base64: btoa(binary) } }) });
      const shown = txMode === 'hex' ? Array.from(raw, (byte) => byte.toString(16).padStart(2, '0')).join(' ') : new TextDecoder().decode(raw).replace(/\r/g, '\\r').replace(/\n/g, '\\n');
      setTerminalOutput((value) => `${value}${value ? '\n' : ''}[TX ${new Date().toLocaleTimeString()}] ${shown}`);
      setTerminalInput('');
    } catch (error) { say((error as Error).message); }
  };

  return <Page lang={lang} group="外设管理" title="外设连接" description="发现、连接和管理 UART、程控电源、示波器、J-Link 与其他实验外设。" action={<button className="btn primary" onClick={discover} disabled={discovering}><Usb size={16} />{discovering ? (lang === 'zh' ? '发现中…' : 'Discovering…') : tr(lang, '发现外设')}</button>}>
    <div className="peripheral-shell-react">
      <section className="peripheral-list-panel-react">
        <div className="pane-head"><div><strong>{lang === 'zh' ? '外设' : 'Peripherals'}</strong><span>{detail?.peripherals?.length || 0}</span></div></div>
        {discovered.length > 0 && <div className="discovery-panel"><div className="section-title"><h3>{lang === 'zh' ? '发现的端点' : 'Discovered endpoints'}</h3><span>{discovered.length}</span></div>{discovered.map((item) => { const registered = detail?.peripherals?.some((value) => value.kind === item.kind && (value.port || value.address) === item.endpoint); return <button className="discovered-row" key={item.id} onClick={() => useDiscovered(item)}><span className="peripheral-icon"><Usb size={16} /></span><span><strong>{item.name}</strong><small>{item.endpoint || item.id}</small><small>{[item.metadata?.vendor_id && `VID ${item.metadata.vendor_id}`, item.metadata?.product_id && `PID ${item.metadata.product_id}`].filter(Boolean).join(' · ')}</small></span><Pill value={registered ? 'registered' : 'available'} /></button>; })}</div>}
        <div className="peripheral-list">{detail?.peripherals?.length ? detail.peripherals.map((item) => { const connected = sessions.some((value) => value.peripheral_id === item.id); return <button type="button" className={`peripheral-row ${item.id === selectedPeripheral ? 'selected' : ''}`} key={item.id} onClick={() => { setSelectedPeripheral(item.id); setMonitoring(false); setIdentity(null); }}><span className="peripheral-icon"><Cable size={16} /></span><span className="list-main"><strong>{item.name}</strong><small>{item.kind} · {item.port || item.address || '-'}</small></span><span className={`device-dot ${connected ? 'online' : ''}`} title={connected ? 'Connected' : 'Offline'} /></button>; }) : <Empty />}</div>
        <details className="manual-registration"><summary><CirclePlus size={15} />{lang === 'zh' ? '手动添加外设' : 'Add peripheral manually'}</summary><form className="form manual-peripheral-form" onSubmit={submit}><label>{tr(lang, '名称')}<input value={form.name} onChange={(event) => setForm({ ...form, name: event.target.value })} required /></label><div className="form-grid"><label>{lang === 'zh' ? '类型' : 'Kind'}<select value={form.kind} onChange={(event) => setForm({ ...form, kind: event.target.value })}><option value="serial">serial</option><option value="power">power</option><option value="scope">scope</option><option value="jlink">jlink</option><option value="bluetooth">bluetooth</option></select></label><label>{tr(lang, '驱动')}<input value={form.driver} onChange={(event) => setForm({ ...form, driver: event.target.value })} /></label></div><label>{tr(lang, '端口')}<input value={form.port} onChange={(event) => setForm({ ...form, port: event.target.value })} placeholder="/dev/ttyUSB0 or host:port" required /></label><label>{tr(lang, '目标设备')}<select value={form.target_id} onChange={(event) => setForm({ ...form, target_id: event.target.value })}><option value="">{tr(lang, '选择目标')}</option>{(detail?.targets || []).map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label><button className="btn primary">{lang === 'zh' ? '注册外设' : 'Register peripheral'}</button></form></details>
      </section>
      <section className="peripheral-main-react">
        {selected ? <><div className="detail-toolbar peripheral-detail-head"><span className="session-avatar"><Cable size={18} /></span><div><strong>{selected.name}</strong><span>{selected.kind} · {selected.port || selected.address || '-'}</span></div><Pill value={activeSession ? 'connected' : selected.status} /><button className={`btn ${activeSession ? 'danger-button' : 'primary'}`} disabled={connecting === selected.id} onClick={() => connect(selected)}>{connecting === selected.id ? (lang === 'zh' ? '处理中…' : 'Working…') : activeSession ? tr(lang, '断开') : tr(lang, '连接')}</button></div>
          <div className="peripheral-facts"><div><span>{tr(lang, '驱动')}</span><strong>{selected.driver || '-'}</strong></div><div><span>{lang === 'zh' ? '会话' : 'Session'}</span><strong className="mono">{activeSession?.session_id || '-'}</strong></div><div><span>{lang === 'zh' ? '租约模式' : 'Lease mode'}</span><strong>{activeSession?.mode || '-'}</strong></div><div><span>{lang === 'zh' ? '租约到期' : 'Lease expires'}</span><strong>{activeSession ? escDate(activeSession.expires_at) : '-'}</strong></div></div>
          {identity && <div className="identity-grid">{Object.entries(identity).map(([key, value]) => <div key={key}><span>{key}</span><strong>{String(value)}</strong></div>)}</div>}
          {activeSession && selected.kind === 'serial' ? <div className="serial-workspace"><div className="terminal-toolbar"><div className="section-title"><h3>{lang === 'zh' ? '串口终端' : 'Serial terminal'}</h3><span className={`monitor-state ${monitoring ? 'active' : ''}`}>{monitoring ? (lang === 'zh' ? '正在接收' : 'Receiving') : (lang === 'zh' ? '已暂停' : 'Paused')}</span></div><label>RX<select value={rxMode} onChange={(event) => setRxMode(event.target.value as 'text' | 'hex')}><option value="text">Text</option><option value="hex">Hex</option></select></label><button className="btn" onClick={readTerminal}>{lang === 'zh' ? '读取一次' : 'Read once'}</button><button className={`btn ${monitoring ? 'danger-button' : ''}`} onClick={() => setMonitoring((value) => !value)}>{monitoring ? (lang === 'zh' ? '停止监听' : 'Stop') : (lang === 'zh' ? '连续监听' : 'Monitor')}</button><button className="btn" onClick={() => setTerminalOutput('')}>{lang === 'zh' ? '清空' : 'Clear'}</button></div><pre className="terminal-output">{terminalOutput || (lang === 'zh' ? '等待串口数据' : 'Waiting for serial data')}</pre><div className="terminal-compose"><select value={txMode} onChange={(event) => setTxMode(event.target.value as 'text' | 'hex')}><option value="text">Text</option><option value="hex">Hex</option></select>{txMode === 'text' && <select value={lineEnding} onChange={(event) => setLineEnding(event.target.value as 'none' | 'lf' | 'crlf')}><option value="none">No ending</option><option value="lf">LF</option><option value="crlf">CRLF</option></select>}<input value={terminalInput} onChange={(event) => setTerminalInput(event.target.value)} onKeyDown={(event) => { if (event.key === 'Enter') { event.preventDefault(); sendTerminal(); } }} placeholder={txMode === 'hex' ? '55 aa 00 ff' : (lang === 'zh' ? '输入要发送的文本' : 'Text to send')} /><button className="send-button" title={tr(lang, '发送')} onClick={sendTerminal} disabled={!terminalInput}><Send size={17} /></button></div></div> : <div className="peripheral-empty"><span className="session-avatar large"><Cable size={23} /></span><strong>{activeSession ? (lang === 'zh' ? '该外设无串口终端' : 'No serial terminal for this peripheral') : (lang === 'zh' ? '外设尚未连接' : 'Peripheral is disconnected')}</strong><p>{activeSession ? (lang === 'zh' ? '请在外设配置或协议分析中继续。' : 'Continue in Peripheral configuration or Protocol analysis.') : (lang === 'zh' ? '连接后才会创建受控会话和独占租约。' : 'Connecting creates a controlled session and exclusive lease.')}</p></div>}
        </> : <Empty>{lang === 'zh' ? '从左侧选择一个外设' : 'Select a peripheral from the list'}</Empty>}
      </section>
    </div>
  </Page>;
}

function PeripheralConfigView({ lang, detail, refresh, say }: ViewProps) {
  const [selected, setSelected] = useState(''); const [config, setConfig] = useState('{}'); const [schema, setSchema] = useState<Record<string, unknown> | null>(null); const [sessions, setSessions] = useState<{ session_id: string; peripheral_id: string; mode: string }[]>([]);
  const item = detail?.peripherals?.find((value) => value.id === selected) || detail?.peripherals?.[0];
  const session = sessions.find((value) => value.peripheral_id === item?.id);
  useEffect(() => { if (item && !selected) setSelected(item.id); }, [item, selected]);
  useEffect(() => { if (!item) return; api<Record<string, unknown>>(`/api/v1/peripherals/${item.id}/schema`).then(setSchema).catch(() => setSchema(null)); api<{ sessions: { session_id: string; peripheral_id: string; mode: string }[] }>('/api/v1/peripheral-sessions').then((value) => setSessions(value.sessions || [])).catch(() => setSessions([])); const source = session ? `/api/v1/peripheral-sessions/${session.session_id}/config` : `/api/v1/peripherals/${item.id}`; api<{ config?: Record<string, unknown> }>(source).then((value) => setConfig(JSON.stringify(value.config || item.config || {}, null, 2))).catch(() => setConfig(JSON.stringify(item.config || {}, null, 2))); }, [item?.id, session?.session_id]);
  const savePreset = async () => { if (!item) return; try { const value = JSON.parse(config); await api(`/api/v1/peripherals/${item.id}`, { method: 'POST', body: JSON.stringify({ config: value }) }); await refresh(); say(lang === 'zh' ? '预设已保存，尚未应用到会话' : 'Preset saved; it has not been applied to the live session'); } catch (error) { say((error as Error).message); } };
  const applySession = async () => { if (!item || !session) { say(lang === 'zh' ? '请先建立独占会话' : 'Open an exclusive session first'); return; } try { const value = JSON.parse(config); await api(`/api/v1/peripheral-sessions/${session.session_id}/config`, { method: 'PUT', body: JSON.stringify(value) }); const readback = await api<{ config: Record<string, unknown> }>(`/api/v1/peripheral-sessions/${session.session_id}/config`); setConfig(JSON.stringify(readback.config || value, null, 2)); await refresh(); say(lang === 'zh' ? '配置已应用并回读确认' : 'Configuration applied and read back'); } catch (error) { say((error as Error).message); } };
  return <Page lang={lang} group="外设管理" title="外设配置" description="根据驱动能力读取、校验并应用外设参数和安全边界。"><Card title={lang === 'zh' ? '参数配置' : 'Parameter configuration'}>{item ? <div className="form"><label>{tr(lang, '外设')}<select value={item.id} onChange={(event) => { setSelected(event.target.value); const next = detail?.peripherals?.find((value) => value.id === event.target.value); setConfig(JSON.stringify(next?.config || {}, null, 2)); }}>{(detail?.peripherals || []).map((value) => <option key={value.id} value={value.id}>{value.name}</option>)}</select></label><div className="detail-row"><span>{lang === 'zh' ? '实时会话' : 'Live session'}</span><strong>{session ? `${session.session_id} · ${session.mode}` : (lang === 'zh' ? '未连接' : 'Not connected')}</strong></div><label>Config JSON<textarea value={config} onChange={(event) => setConfig(event.target.value)} rows={12} /></label><div className="form-actions"><button type="button" className="btn" onClick={savePreset}>{lang === 'zh' ? '保存预设' : 'Save preset'}</button><button type="button" className="btn primary" onClick={applySession} disabled={!session}>{lang === 'zh' ? '应用到会话' : 'Apply to session'}</button></div>{schema && <details><summary>{lang === 'zh' ? '驱动配置 Schema' : 'Driver configuration schema'}</summary><pre>{JSON.stringify(schema, null, 2)}</pre></details>}</div> : <Empty>{tr(lang, '请先注册外设')}</Empty>}</Card></Page>;
}
function ProtocolView({ lang, detail, refresh, say }: ViewProps) { const [sessionID, setSessionID] = useState(''); const [source, setSource] = useState('serial.read'); const [maxBytes, setMaxBytes] = useState('65536'); const [sessions, setSessions] = useState<{ session_id: string; peripheral_id: string; mode: string }[]>([]); useEffect(() => { api<{ sessions: { session_id: string; peripheral_id: string; mode: string }[] }>('/api/v1/peripheral-sessions').then((value) => { setSessions(value.sessions || []); if (!sessionID && value.sessions?.[0]) setSessionID(value.sessions[0].session_id); }).catch(() => setSessions([])); }, [detail?.peripherals, sessionID]); const capture = async () => { if (!sessionID) return; try { await api(`/api/v1/workspaces/${detail?.workspace.id}/captures`, { method: 'POST', body: JSON.stringify({ session_id: sessionID, source, max_bytes: Number(maxBytes) || 65536 }) }); await refresh(); say(lang === 'zh' ? '采集已完成并保存为证据工件' : 'Capture completed and saved as an evidence artifact'); } catch (error) { say((error as Error).message); } }; return <Page lang={lang} group="外设管理" title="协议分析" description="区分原始采集数据、解析结果和研究判断，支持后续证据关联。"><Card title={lang === 'zh' ? '启动受控采集' : 'Start controlled capture'}><div className="form-grid"><label>{lang === 'zh' ? '外设会话' : 'Peripheral session'}<select value={sessionID} onChange={(event) => setSessionID(event.target.value)}><option value="">{lang === 'zh' ? '选择会话' : 'Select session'}</option>{sessions.map((item) => <option key={item.session_id} value={item.session_id}>{item.session_id} · {item.peripheral_id}</option>)}</select></label><label>{lang === 'zh' ? '数据源' : 'Source'}<input value={source} onChange={(event) => setSource(event.target.value)} /></label><label>{lang === 'zh' ? '最大字节数' : 'Max bytes'}<input type="number" min="1" max="8388608" value={maxBytes} onChange={(event) => setMaxBytes(event.target.value)} /></label></div><button className="btn primary" onClick={capture} disabled={!sessionID}>{lang === 'zh' ? '读取并保存' : 'Read and save'}</button></Card><Card title={lang === 'zh' ? '采集记录' : 'Captures'}>{detail?.captures?.length ? <div className="table-wrap"><table><thead><tr><th>ID</th><th>{lang === 'zh' ? '来源' : 'Source'}</th><th>{lang === 'zh' ? '状态' : 'Status'}</th><th>{lang === 'zh' ? '解析结果' : 'Parsed output'}</th></tr></thead><tbody>{detail.captures.map((capture) => <tr key={(capture as { id: string }).id}><td className="mono">{(capture as { id: string }).id}</td><td>{(capture as { source: string }).source}</td><td><Pill value={(capture as { status: string }).status} /></td><td><pre>{JSON.stringify((capture as { parsed?: unknown }).parsed || {}, null, 2)}</pre></td></tr>)}</tbody></table></div> : <Empty>{lang === 'zh' ? '尚无采集记录；请先连接外设并启动受控采集。' : 'No captures yet. Connect a peripheral and start an explicit capture.'}</Empty>}</Card></Page>; }
function FindingView({ lang, detail }: ViewProps) { return <Page lang={lang} group="漏洞管理" title="漏洞列表" description="管理候选问题、验证证据、风险评级和报告状态。"><Card title={lang === 'zh' ? 'Finding 注册表' : 'Finding register'}>{detail?.findings?.length ? <div className="table-wrap"><table><thead><tr>{['标题', '状态', '优先级', '评分', '置信度', '创建时间'].map((item) => <th key={item}>{tr(lang, item)}</th>)}</tr></thead><tbody>{detail.findings.map((item) => <tr key={item.finding_id}><td><strong>{item.title}</strong><div className="mono">{item.finding_id}</div></td><td><Pill value={item.state} /></td><td>{item.priority}</td><td>{item.score}</td><td>{item.confidence}</td><td>{escDate(item.created_at)}</td></tr>)}</tbody></table></div> : <Empty />}</Card></Page>; }
function AgentView({ lang, registry, refresh, say }: ViewProps) {
  const bind = async (agent: Agent, runtimeID: string) => { try { await api(`/api/v1/agents/${agent.id}`, { method: 'POST', body: JSON.stringify({ runtime_id: runtimeID }) }); await refresh(); } catch (error) { say((error as Error).message); } };
  const setPermission = async (agent: Agent, key: 'device' | 'network' | 'destructive', enabled: boolean) => { try { await api(`/api/v1/agents/${agent.id}`, { method: 'POST', body: JSON.stringify({ permissions: { ...(agent.permissions || {}), filesystem: agent.permissions?.filesystem || 'workspace-readonly', [key]: enabled } }) }); await refresh(); } catch (error) { say((error as Error).message); } };
  return <Page lang={lang} group="工作台" title="智能体管理" description="配置 Agent 职责、模型运行时和允许使用的能力。"><Card title={lang === 'zh' ? 'Agent 池' : 'Agent pool'}>{registry.agents.map((agent) => <div className="list-row agent-row" key={agent.id}><div className="list-main"><strong>{agent.role}</strong><div className="subtle">{agent.id} · {agent.status}</div></div><select value={agent.runtime_id || ''} onChange={(event) => bind(agent, event.target.value)}><option value="">{tr(lang, '未绑定')}</option>{registry.runtimes.filter((item) => item.available).map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}</select><label className="inline-check"><input type="checkbox" checked={Boolean(agent.permissions?.device)} onChange={(event) => setPermission(agent, 'device', event.target.checked)} /> {lang === 'zh' ? '设备' : 'Device'}</label><label className="inline-check"><input type="checkbox" checked={Boolean(agent.permissions?.network)} onChange={(event) => setPermission(agent, 'network', event.target.checked)} /> Network</label><label className="inline-check"><input type="checkbox" checked={Boolean(agent.permissions?.destructive)} onChange={(event) => setPermission(agent, 'destructive', event.target.checked)} /> {lang === 'zh' ? '高风险' : 'Destructive'}</label><Pill value={agent.enabled ? 'enabled' : 'disabled'} /></div>)}</Card></Page>;
}
function RuntimeView({ lang, registry, refresh, say }: ViewProps) { const [prompt, setPrompt] = useState(''); const [runtimeID, setRuntimeID] = useState('codex'); const [output, setOutput] = useState(''); const run = async () => { try { const result = await api<{ output: string }>(`/api/v1/runtimes/${runtimeID}/run`, { method: 'POST', body: JSON.stringify({ prompt }) }); setOutput(result.output || ''); } catch (error) { say((error as Error).message); } }; const action = async (id: string, name: string) => { try { await api(`/api/v1/runtimes/${id}/${name}`, { method: 'POST', body: '{}' }); await refresh(); } catch (error) { say((error as Error).message); } }; return <Page lang={lang} group="配置" title="运行时" description="发现主机上的 Claude、Codex、Grok、Kiro，并通过受控非交互会话执行 Agent 请求。"><Card title={lang === 'zh' ? '主机运行时' : 'Host runtimes'}>{registry.runtimes.map((item) => <div className="list-row" key={item.id}><div className="list-main"><strong>{item.name}</strong><div className="subtle">{item.path || item.command} · {item.version || item.status}</div></div><Pill value={item.status} /><button className="btn" onClick={() => action(item.id, 'check')}>{tr(lang, '检查')}</button><button className="btn" onClick={() => action(item.id, 'probe')}>{tr(lang, '探测帮助')}</button></div>)}</Card><Card title={lang === 'zh' ? '运行一次会话' : 'Run a session'}><div className="form"><label>{lang === 'zh' ? '运行时' : 'Runtime'}<select value={runtimeID} onChange={(event) => setRuntimeID(event.target.value)}>{registry.runtimes.map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label><label>{lang === 'zh' ? '提示' : 'Prompt'}<textarea value={prompt} onChange={(event) => setPrompt(event.target.value)} placeholder={tr(lang, '请输入问题')} /></label><button className="btn primary" onClick={run}>{tr(lang, '运行')}</button>{output && <pre>{output}</pre>}</div></Card></Page>; }
function RegistryView({ lang, title, rows }: ViewProps & { title: string; rows: unknown[] }) { return <Page lang={lang} group="配置" title={title} description={lang === 'zh' ? '查看系统注册的结构化对象、来源、实现方式和可用状态。' : 'Inspect registered structured objects, implementations, and availability.'}><Card title={title}>{rows?.length ? <pre>{JSON.stringify(rows, null, 2)}</pre> : <Empty />}</Card></Page>; }
function SkillsView({ lang, registry, refresh, say, selectedWorkspace, detail }: ViewProps) {
  const [name, setName] = useState(''); const [steps, setSteps] = useState('firmware.inventory\nbinary.search_string'); const [objective, setObjective] = useState('Run this skill on the selected IoT target'); const [targetID, setTargetID] = useState('');
  useEffect(() => { if (!targetID && detail?.targets?.[0]) setTargetID(detail.targets[0].id); }, [detail?.targets, targetID]);
  const create = async (event: FormEvent) => { event.preventDefault(); try { await api('/api/v1/skills', { method: 'POST', body: JSON.stringify({ name, steps: steps.split(/\r?\n|,/).map((item) => item.trim()).filter(Boolean), description: 'User-defined IoT research workflow' }) }); setName(''); await refresh(); say(lang === 'zh' ? 'Skill 已注册' : 'Skill registered'); } catch (error) { say((error as Error).message); } };
  const run = async (skillID: string) => { if (!selectedWorkspace || !targetID) { say(lang === 'zh' ? '请先选择工作区和目标设备' : 'Select a workspace and target first'); return; } try { await api(`/api/v1/skills/${skillID}/run`, { method: 'POST', body: JSON.stringify({ workspace_id: selectedWorkspace, target_id: targetID, objective }) }); await refresh(); say(lang === 'zh' ? 'Skill 已创建任务' : 'Skill task created'); } catch (error) { say((error as Error).message); } };
  return <Page lang={lang} group="配置" title="Skills" description={lang === 'zh' ? '把多个能力组合成可追踪、可复用的 IoT 研究工作流。' : 'Compose multiple capabilities into reusable, traceable IoT workflows.'}><Card title={lang === 'zh' ? '已安装 Skills' : 'Installed skills'}>{registry.skills?.length ? <div className="table-wrap"><table><thead><tr><th>{tr(lang, '名称')}</th><th>{tr(lang, '版本')}</th><th>{lang === 'zh' ? '步骤' : 'Steps'}</th><th>{tr(lang, '状态')}</th><th /></tr></thead><tbody>{registry.skills.map((raw) => { const item = raw as { id: string; name: string; version: string; steps?: string[]; enabled?: boolean }; return <tr key={item.id}><td><strong>{item.name}</strong><div className="mono">{item.id}</div></td><td>{item.version}</td><td className="mono">{item.steps?.join(' → ')}</td><td><Pill value={item.enabled === false ? 'disabled' : 'enabled'} /></td><td><button className="btn" onClick={() => run(item.id)}>{lang === 'zh' ? '运行' : 'Run'}</button></td></tr>; })}</tbody></table></div> : <Empty />}</Card><Card title={lang === 'zh' ? '注册 Skill' : 'Register skill'}><form className="form" onSubmit={create}><label>{tr(lang, '名称')}<input value={name} onChange={(event) => setName(event.target.value)} placeholder="firmware-web-surface" required /></label><label>{lang === 'zh' ? '能力步骤（每行一个）' : 'Capability steps (one per line)'}<textarea value={steps} onChange={(event) => setSteps(event.target.value)} rows={5} required /></label><label>{tr(lang, '研究目标')}<input value={objective} onChange={(event) => setObjective(event.target.value)} /></label><button className="btn primary">{lang === 'zh' ? '注册' : 'Register'}</button></form></Card></Page>;
}
function CapabilitiesView({ lang, registry, refresh, say }: ViewProps) {
  const [selected, setSelected] = useState(''); const [objective, setObjective] = useState('Check the supplied IoT research input'); const [inputs, setInputs] = useState('{}'); const capability = registry.capabilities.find((item) => item.id === selected) || registry.capabilities[0];
  useEffect(() => { if (!selected && registry.capabilities[0]) setSelected(registry.capabilities[0].id); }, [registry.capabilities, selected]);
  const test = async () => { if (!capability) return; try { const value = JSON.parse(inputs || '{}'); await api(`/api/v1/capabilities/${capability.id}/test`, { method: 'POST', body: JSON.stringify({ objective, inputs: value }) }); await refresh(); say(lang === 'zh' ? '能力测试已完成' : 'Capability test completed'); } catch (error) { say((error as Error).message); } };
  return <Page lang={lang} group="配置" title="能力中心" description={lang === 'zh' ? '查看能力契约、权限、运行时，并对能力执行受控测试。' : 'Inspect capability contracts, permissions, runtimes, and run controlled tests.'}><Card title={lang === 'zh' ? '能力注册表' : 'Capability registry'}>{registry.capabilities?.length ? <div className="table-wrap"><table><thead><tr><th>ID</th><th>{lang === 'zh' ? '类别' : 'Category'}</th><th>{tr(lang, '描述')}</th><th>{lang === 'zh' ? '运行时' : 'Runtime'}</th><th>{lang === 'zh' ? '权限' : 'Permissions'}</th></tr></thead><tbody>{registry.capabilities.map((item) => <tr key={item.id} onClick={() => setSelected(item.id)} className={item.id === capability?.id ? 'selected-row' : ''}><td className="mono">{item.id}</td><td>{item.category}</td><td>{item.description}</td><td>{item.runtime}</td><td className="mono">{JSON.stringify(item.permissions)}</td></tr>)}</tbody></table></div> : <Empty />}</Card><Card title={lang === 'zh' ? '受控测试' : 'Controlled test'}>{capability ? <div className="form"><label>{lang === 'zh' ? '能力' : 'Capability'}<select value={capability.id} onChange={(event) => setSelected(event.target.value)}>{registry.capabilities.map((item) => <option key={item.id} value={item.id}>{item.id}</option>)}</select></label><label>{tr(lang, '目标描述')}<input value={objective} onChange={(event) => setObjective(event.target.value)} /></label><label>Inputs JSON<textarea value={inputs} onChange={(event) => setInputs(event.target.value)} rows={7} /></label><button type="button" className="btn primary" onClick={test}>{lang === 'zh' ? '执行测试' : 'Run test'}</button></div> : <Empty />}</Card></Page>;
}
function SettingsView({ lang }: ViewProps) { return <Page lang={lang} group="配置" title="设置" description="管理界面偏好、工作区存储和本地控制平面策略。"><Card title={lang === 'zh' ? '客户端设置' : 'Client settings'}><div className="detail-row"><span>{tr(lang, '语言')}</span><strong>{lang === 'zh' ? tr(lang, '中文') : tr(lang, '英文')}</strong></div><div className="detail-row"><span>{lang === 'zh' ? '存储' : 'Storage'}</span><strong>SQLite</strong></div><div className="detail-row"><span>{lang === 'zh' ? '安全模式' : 'Safety mode'}</span><strong>Permission + Approval</strong></div></Card></Page>; }

resolveWailsAPI().finally(() => createRoot(document.getElementById('root')!).render(<App />));
