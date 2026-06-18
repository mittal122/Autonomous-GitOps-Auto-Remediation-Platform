import { useCallback, useState } from 'react'
import { api, type IntegrationsSummary, type LokiTestResult } from '../api/client'
import { useToast } from '../hooks/useToast'
import { SkeletonCards } from '../components/Skeleton'

function StatusDot({ ok }: { ok: boolean }) {
  return <span className={`inline-block w-2 h-2 rounded-full ${ok ? 'bg-green-500' : 'bg-gray-300'}`} />
}

function KubernetesCard({ summary }: { summary: IntegrationsSummary }) {
  const k8s = summary.kubernetes
  return (
    <div className="border border-gray-200 rounded-lg p-4 bg-white shadow-sm">
      <div className="flex items-center justify-between mb-2">
        <h2 className="font-semibold text-gray-800">Kubernetes</h2>
        <span className="flex items-center gap-1.5 text-xs text-gray-500">
          <StatusDot ok={k8s.connected} /> {k8s.connected ? 'Connected' : 'Not connected'}
        </span>
      </div>
      <p className="text-sm text-gray-600">
        Mode: <strong>{k8s.in_cluster ? 'in-cluster' : 'kubeconfig'}</strong>
      </p>
      {k8s.server_version && <p className="text-sm text-gray-600">Server version: {k8s.server_version}</p>}
      {k8s.error && <p className="text-sm text-red-600 mt-1">{k8s.error}</p>}
      <p className="text-xs text-gray-400 mt-2">
        Detected automatically from IN_CLUSTER / KUBECONFIG — no credentials are entered here.
      </p>
    </div>
  )
}

function LokiCard({ summary, onSaved }: { summary: IntegrationsSummary; onSaved: () => void }) {
  const { toast } = useToast()
  const [addr, setAddr] = useState(summary.loki.status.addr ?? '')
  const [query, setQuery] = useState('')
  const [pollInterval, setPollInterval] = useState('30s')
  const [timeout, setTimeoutStr] = useState('10s')
  const [authHeader, setAuthHeader] = useState('')
  const [testing, setTesting] = useState(false)
  const [saving, setSaving] = useState(false)
  const [testResult, setTestResult] = useState<LokiTestResult | null>(null)

  async function test() {
    setTesting(true)
    setTestResult(null)
    try {
      const result = await api.testLokiIntegration({ addr, query, timeout })
      setTestResult(result)
    } catch (e: unknown) {
      setTestResult({ ok: false, message: (e as Error).message })
    } finally {
      setTesting(false)
    }
  }

  async function save() {
    setSaving(true)
    try {
      await api.saveLokiIntegration({
        addr, query, poll_interval: pollInterval, timeout,
        ...(authHeader ? { auth_header: authHeader } : {}),
      })
      toast('Loki settings saved and applied live', 'success')
      setAuthHeader('')
      onSaved()
    } catch (e: unknown) {
      toast((e as Error).message, 'error')
    } finally {
      setSaving(false)
    }
  }

  const status = summary.loki.status

  return (
    <div className="border border-gray-200 rounded-lg p-4 bg-white shadow-sm">
      <div className="flex items-center justify-between mb-2">
        <h2 className="font-semibold text-gray-800">Loki</h2>
        <span className="flex items-center gap-1.5 text-xs text-gray-500">
          <StatusDot ok={status.enabled} /> {status.enabled ? 'Enabled' : 'Disabled'}
        </span>
      </div>

      <div className="grid grid-cols-1 sm:grid-cols-2 gap-2 mt-3">
        <input
          className="border border-gray-300 rounded px-2 py-1.5 text-sm sm:col-span-2"
          placeholder="Loki URL (e.g. http://loki:3100)"
          value={addr}
          onChange={(e) => setAddr(e.target.value)}
        />
        <input
          className="border border-gray-300 rounded px-2 py-1.5 text-sm sm:col-span-2"
          placeholder={'Query (default: {namespace=~".+"})'}
          value={query}
          onChange={(e) => setQuery(e.target.value)}
        />
        <input
          className="border border-gray-300 rounded px-2 py-1.5 text-sm"
          placeholder="Poll interval (e.g. 30s)"
          value={pollInterval}
          onChange={(e) => setPollInterval(e.target.value)}
        />
        <input
          className="border border-gray-300 rounded px-2 py-1.5 text-sm"
          placeholder="Timeout (e.g. 10s)"
          value={timeout}
          onChange={(e) => setTimeoutStr(e.target.value)}
        />
        <input
          type="password"
          className="border border-gray-300 rounded px-2 py-1.5 text-sm sm:col-span-2"
          placeholder={summary.loki.configured ? 'Auth header (leave blank to keep existing)' : 'Auth header (optional)'}
          value={authHeader}
          onChange={(e) => setAuthHeader(e.target.value)}
        />
      </div>

      <div className="flex gap-2 mt-3">
        <button
          onClick={test}
          disabled={testing || !addr}
          className="px-3 py-1.5 border border-gray-300 rounded text-sm hover:bg-gray-50 disabled:opacity-50"
        >
          {testing ? 'Testing…' : 'Test connection'}
        </button>
        <button
          onClick={save}
          disabled={saving || !addr}
          className="px-3 py-1.5 bg-indigo-600 text-white rounded text-sm hover:bg-indigo-700 disabled:opacity-50"
        >
          {saving ? 'Saving…' : 'Save'}
        </button>
      </div>

      {testResult && (
        <p className={`mt-2 text-sm ${testResult.ok ? 'text-green-700' : 'text-red-600'}`}>
          {testResult.ok ? '✅' : '❌'} {testResult.message}
        </p>
      )}

      {status.enabled && (
        <div className="mt-3 pt-3 border-t border-gray-100 text-xs text-gray-500 space-y-0.5">
          {status.last_poll_at && <p>Last poll: {new Date(status.last_poll_at).toLocaleString()}</p>}
          <p>Signals on last poll: {status.last_signal_count}</p>
          {status.last_error && <p className="text-red-500">Last error: {status.last_error}</p>}
        </div>
      )}
    </div>
  )
}

function AlertmanagerCard({ summary, onChanged }: { summary: IntegrationsSummary; onChanged: () => void }) {
  const { toast } = useToast()
  const [applying, setApplying] = useState(false)
  const [testing, setTesting] = useState(false)
  const [applyResult, setApplyResult] = useState<{ applied: boolean; reason: string } | null>(null)
  const [copied, setCopied] = useState<'url' | 'yaml' | null>(null)

  const webhookURL = summary.alertmanager.webhook_url
  const yamlSnippet = `receivers:\n- name: autosre\n  webhook_configs:\n  - url: ${webhookURL}\n    send_resolved: true\n\nroute:\n  receiver: autosre\n`

  async function copy(text: string, which: 'url' | 'yaml') {
    await navigator.clipboard.writeText(text)
    setCopied(which)
    setTimeout(() => setCopied(null), 2000)
  }

  async function apply() {
    setApplying(true)
    try {
      const result = await api.applyAlertmanagerIntegration()
      setApplyResult(result)
      if (result.applied) toast('Applied via Prometheus Operator', 'success')
      onChanged()
    } catch (e: unknown) {
      toast((e as Error).message, 'error')
    } finally {
      setApplying(false)
    }
  }

  async function test() {
    setTesting(true)
    try {
      const result = await api.testAlertmanagerIntegration()
      toast(result.message, result.ok ? 'success' : 'error')
    } catch (e: unknown) {
      toast((e as Error).message, 'error')
    } finally {
      setTesting(false)
    }
  }

  return (
    <div className="border border-gray-200 rounded-lg p-4 bg-white shadow-sm">
      <div className="flex items-center justify-between mb-2">
        <h2 className="font-semibold text-gray-800">Alertmanager</h2>
        <span className="flex items-center gap-1.5 text-xs text-gray-500">
          <StatusDot ok={summary.alertmanager.operator_detected} />
          {summary.alertmanager.operator_detected ? 'Operator detected' : 'Operator not detected'}
        </span>
      </div>

      <label className="text-xs text-gray-500">Webhook URL</label>
      <div className="flex gap-2 mt-1">
        <input readOnly value={webhookURL} className="flex-1 border border-gray-300 rounded px-2 py-1.5 text-sm bg-gray-50 font-mono" />
        <button onClick={() => copy(webhookURL, 'url')} className="px-3 py-1.5 border border-gray-300 rounded text-sm hover:bg-gray-50">
          {copied === 'url' ? 'Copied!' : 'Copy'}
        </button>
      </div>

      <label className="text-xs text-gray-500 mt-3 block">Alertmanager config snippet</label>
      <div className="flex gap-2 mt-1">
        <pre className="flex-1 border border-gray-300 rounded px-2 py-1.5 text-xs bg-gray-50 overflow-x-auto whitespace-pre">{yamlSnippet}</pre>
        <button onClick={() => copy(yamlSnippet, 'yaml')} className="px-3 py-1.5 border border-gray-300 rounded text-sm hover:bg-gray-50 self-start">
          {copied === 'yaml' ? 'Copied!' : 'Copy'}
        </button>
      </div>

      <div className="flex gap-2 mt-3">
        <button
          onClick={apply}
          disabled={applying}
          className="px-3 py-1.5 bg-indigo-600 text-white rounded text-sm hover:bg-indigo-700 disabled:opacity-50"
        >
          {applying ? 'Applying…' : 'Apply automatically'}
        </button>
        <button
          onClick={test}
          disabled={testing}
          className="px-3 py-1.5 border border-gray-300 rounded text-sm hover:bg-gray-50 disabled:opacity-50"
        >
          {testing ? 'Sending…' : 'Send test webhook'}
        </button>
      </div>

      {applyResult && (
        <p className={`mt-2 text-sm ${applyResult.applied ? 'text-green-700' : 'text-gray-600'}`}>
          {applyResult.applied ? '✅' : 'ℹ️'} {applyResult.reason}
          {!applyResult.applied && ' — paste the snippet above into your Alertmanager config instead.'}
        </p>
      )}
    </div>
  )
}

export default function Integrations() {
  const [summary, setSummary] = useState<IntegrationsSummary | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(() => {
    api.getIntegrations()
      .then((data) => { setSummary(data); setError(null) })
      .catch((e: Error) => setError(e.message))
      .finally(() => setLoading(false))
  }, [])

  useState(() => { load() })

  return (
    <div>
      <h1 className="text-2xl font-bold mb-1">Integrations</h1>
      <p className="text-sm text-gray-500 mb-4">
        Configure telemetry sources here — no .env edits or restarts required.
      </p>

      {loading && <SkeletonCards rows={3} />}
      {error && <p className="text-red-600 text-sm">Error: {error}</p>}

      {summary && (
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
          <KubernetesCard summary={summary} />
          <LokiCard summary={summary} onSaved={load} />
          <div className="lg:col-span-2">
            <AlertmanagerCard summary={summary} onChanged={load} />
          </div>
        </div>
      )}
    </div>
  )
}
