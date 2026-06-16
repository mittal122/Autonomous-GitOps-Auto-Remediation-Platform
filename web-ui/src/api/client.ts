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
}
