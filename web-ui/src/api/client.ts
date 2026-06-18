// Typed API client for the AutoSRE backend.
// All writes (approve/reject/kill-switch) route through the existing fail-closed
// approval registry — there is no direct path to cluster writes from this client.

const BASE = '/api/v1'

function authHeader(): Record<string, string> {
  const token = localStorage.getItem('autosre_token')
  return token ? { Authorization: `Bearer ${token}` } : {}
}

async function get<T>(path: string): Promise<T> {
  const res = await fetch(BASE + path, { headers: authHeader() })
  if (!res.ok) throw new Error(`GET ${path}: ${res.status} ${res.statusText}`)
  return res.json() as Promise<T>
}

async function post<T>(path: string, body?: unknown): Promise<T> {
  const res = await fetch(BASE + path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeader() },
    body: body != null ? JSON.stringify(body) : undefined,
  })
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error((err as { error: string }).error ?? res.statusText)
  }
  return res.json() as Promise<T>
}

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export interface Incident {
  id: string
  severity: string
  affected_resources: string[]
  status: string
  created_at: string
  updated_at: string
  signals?: unknown[]
}

export interface AuditEvent {
  timestamp: string
  trace_id: string
  incident_id: string
  stage: string
  outcome: string
  details: Record<string, string>
}

export interface TraceResponse {
  incident_id: string
  events: AuditEvent[]
  count: number
}

export interface PendingApproval {
  request_id: string
  requested_at: string
  deadline: string
  proposal: {
    incident_id: string
    resource: string
    namespace: string
    failure_mode: string
    confidence: number
    params: { action_type: string }
  }
}

export interface StatusResponse {
  apply_enabled: boolean
  kill_switch_engaged: boolean
  in_flight_pipelines: number
  circuit_breaker_tripped: boolean
  circuit_breaker_count: number
  circuit_breaker_max: number
  circuit_breaker_window_sec: number
}

export interface StatsResponse {
  total_outcomes: number
  by_failure_mode_action: Record<string, {
    attempts: number
    successes: number
    success_rate: number
  }>
  note?: string
}

export interface ApproveResponse {
  request_id: string
  decision: string
  approver: string
}

export interface KillSwitchResponse {
  kill_switch_engaged: boolean
  previous: boolean
  operator: string
}

export interface AuditListResponse {
  events: AuditEvent[]
  count: number
  limit: number
  offset: number
}

export interface KubernetesStatus {
  connected: boolean
  in_cluster: boolean
  server_version?: string
  error?: string
}

export interface LokiStatus {
  enabled: boolean
  addr?: string
  last_poll_at?: string
  last_error?: string
  last_signal_count: number
}

export interface LokiIntegration {
  configured: boolean
  addr?: string
  query?: string
  poll_interval?: string
  timeout?: string
  has_auth_header: boolean
  status: LokiStatus
}

export interface LokiTestResult {
  ok: boolean
  message: string
  sample_lines?: string[]
}

export interface AlertmanagerIntegration {
  webhook_url: string
  yaml_snippet: string
  operator_detected: boolean
}

export interface SafetyStatus {
  apply_enabled: boolean
  kill_switch_engaged: boolean
}

export interface IntegrationsSummary {
  loki: { configured: boolean; status: LokiStatus }
  alertmanager: { webhook_url: string; operator_detected: boolean }
  kubernetes: KubernetesStatus
  llm: { configured: boolean; provider?: string }
  notifications: { slack_configured: boolean; pagerduty_configured: boolean }
  gitops: { configured: boolean }
  safety: SafetyStatus
  any_configured: boolean
}

export interface SaveLokiRequest {
  addr: string
  query?: string
  poll_interval?: string
  timeout?: string
  auth_header?: string
}

export interface LLMIntegration {
  configured: boolean
  provider?: string
  model?: string
  timeout_seconds?: number
  has_api_key: boolean
}

export interface SaveLLMRequest {
  provider: string // "nim" | "gemini" | ""
  api_key?: string
  model?: string
  timeout_seconds?: number
}

export interface LLMTestResult {
  ok: boolean
  message: string
}

export interface NotificationsIntegration {
  configured: boolean
  slack_channel_id?: string
  has_slack_bot_token: boolean
  has_slack_signing_secret: boolean
  has_pagerduty_routing_key: boolean
}

export interface SaveNotificationsRequest {
  slack_bot_token?: string
  slack_signing_secret?: string
  slack_channel_id?: string
  pagerduty_routing_key?: string
}

export interface NotificationsTestResult {
  ok: boolean
  message: string
}

export interface GitOpsIntegration {
  configured: boolean
  repo_path?: string
  remote_url?: string
  branch?: string
  bot_name?: string
  bot_email?: string
  has_auth_token: boolean
  has_ssh_key_path: boolean
}

export interface SaveGitOpsRequest {
  repo_path: string
  remote_url?: string
  auth_token?: string
  ssh_key_path?: string
  bot_name?: string
  bot_email?: string
  branch?: string
}

export interface GitOpsTestResult {
  ok: boolean
  message: string
}

export type RevealCategory = 'llm' | 'notifications' | 'gitops' | 'loki'

// ---------------------------------------------------------------------------
// API calls
// ---------------------------------------------------------------------------

export const api = {
  listIncidents: () => get<Incident[]>('/incidents'),
  getIncident: (id: string) => get<Incident>(`/incidents/${id}`),
  getTrace: (incidentId: string) => get<TraceResponse>(`/incidents/${incidentId}/trace`),
  listApprovals: () => get<PendingApproval[]>('/approvals/pending'),
  approve: (id: string, reason: string) =>
    post<ApproveResponse>(`/approvals/${id}/approve`, { reason }),
  reject: (id: string, reason: string) =>
    post<ApproveResponse>(`/approvals/${id}/reject`, { reason }),
  getStatus: () => get<StatusResponse>('/status'),
  getStats: () => get<StatsResponse>('/stats'),
  setKillSwitch: (engaged: boolean, reason: string) =>
    post<KillSwitchResponse>('/control/kill-switch', { engaged, reason }),
  listAudit: (params: { incidentId?: string; stage?: string; limit?: number; offset?: number } = {}) => {
    const q = new URLSearchParams()
    if (params.incidentId) q.set('incident_id', params.incidentId)
    if (params.stage) q.set('stage', params.stage)
    if (params.limit != null) q.set('limit', String(params.limit))
    if (params.offset != null) q.set('offset', String(params.offset))
    return get<AuditListResponse>(`/audit?${q}`)
  },

  getIntegrations: () => get<IntegrationsSummary>('/integrations'),
  getKubernetesStatus: () => get<KubernetesStatus>('/integrations/kubernetes'),

  getLokiIntegration: () => get<LokiIntegration>('/integrations/loki'),
  saveLokiIntegration: (req: SaveLokiRequest) => post<{ saved: boolean; addr: string }>('/integrations/loki', req),
  testLokiIntegration: (req: SaveLokiRequest) => post<LokiTestResult>('/integrations/loki/test', req),

  getAlertmanagerIntegration: () => get<AlertmanagerIntegration>('/integrations/alertmanager'),
  applyAlertmanagerIntegration: () =>
    post<{ applied: boolean; reason: string; webhook_url: string }>('/integrations/alertmanager/apply'),
  testAlertmanagerIntegration: () => post<{ ok: boolean; message: string }>('/integrations/alertmanager/test'),

  getLLMIntegration: () => get<LLMIntegration>('/integrations/llm'),
  saveLLMIntegration: (req: SaveLLMRequest) => post<{ saved: boolean; provider: string }>('/integrations/llm', req),
  testLLMIntegration: (req: SaveLLMRequest) => post<LLMTestResult>('/integrations/llm/test', req),

  getNotificationsIntegration: () => get<NotificationsIntegration>('/integrations/notifications'),
  saveNotificationsIntegration: (req: SaveNotificationsRequest) =>
    post<{ saved: boolean }>('/integrations/notifications', req),
  testNotificationsIntegration: (channel: 'slack' | 'pagerduty', creds: { slack_bot_token?: string; pagerduty_routing_key?: string }) =>
    post<NotificationsTestResult>('/integrations/notifications/test', { channel, ...creds }),

  getGitOpsIntegration: () => get<GitOpsIntegration>('/integrations/gitops'),
  saveGitOpsIntegration: (req: SaveGitOpsRequest) => post<{ saved: boolean; repo_path: string }>('/integrations/gitops', req),
  testGitOpsIntegration: (req: { remote_url: string; auth_token?: string; ssh_key_path?: string }) =>
    post<GitOpsTestResult>('/integrations/gitops/test', req),

  getSafety: () => get<SafetyStatus>('/integrations/safety'),
  setSafety: (applyEnabled: boolean, reason: string) =>
    post<SafetyStatus>('/integrations/safety', { apply_enabled: applyEnabled, reason }),

  revealSecret: (category: RevealCategory, field: string) =>
    post<{ value: string }>('/integrations/reveal', { category, field }),
}
