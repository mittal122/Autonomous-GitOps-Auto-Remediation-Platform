import { useCallback, useState } from 'react'
import {
  api,
  type IntegrationsSummary,
  type LokiTestResult,
  type RevealCategory,
} from '../api/client'
import { useToast } from '../hooks/useToast'
import { SkeletonCards } from '../components/Skeleton'

function StatusDot({ ok }: { ok: boolean }) {
  return <span className={`inline-block w-2 h-2 rounded-full ${ok ? 'bg-green-500' : 'bg-gray-300'}`} />
}

// ---------------------------------------------------------------------------
// Reveal — a small "Show" button that fetches a secret on demand (admin-only
// server-side; non-admins get a 403 from the API, surfaced as a toast).
// ---------------------------------------------------------------------------

function RevealButton({ category, field }: { category: RevealCategory; field: string }) {
  const { toast } = useToast()
  const [value, setValue] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)

  async function reveal() {
    if (value) {
      setValue(null)
      return
    }
    setLoading(true)
    try {
      const result = await api.revealSecret(category, field)
      setValue(result.value)
    } catch (e: unknown) {
      toast((e as Error).message, 'error')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="inline-flex items-center gap-2">
      <button
        type="button"
        onClick={reveal}
        disabled={loading}
        className="text-xs text-indigo-600 hover:underline disabled:opacity-50"
      >
        {loading ? 'Loading…' : value ? 'Hide' : 'Show'}
      </button>
      {value && <code className="text-xs bg-gray-100 px-1.5 py-0.5 rounded">{value}</code>}
    </div>
  )
}

// ---------------------------------------------------------------------------
// Telemetry section: Kubernetes / Loki / Alertmanager
// ---------------------------------------------------------------------------

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
      setTestResult(await api.testLokiIntegration({ addr, query, timeout }))
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

      {summary.loki.configured && (
        <div className="mt-1">
          <RevealButton category="loki" field="auth_header" />
        </div>
      )}

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

function TelemetrySection({ summary, onChanged }: { summary: IntegrationsSummary; onChanged: () => void }) {
  return (
    <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
      <KubernetesCard summary={summary} />
      <LokiCard summary={summary} onSaved={onChanged} />
      <div className="lg:col-span-2">
        <AlertmanagerCard summary={summary} onChanged={onChanged} />
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// AI Provider section
// ---------------------------------------------------------------------------

function AIProviderSection({ summary, onChanged }: { summary: IntegrationsSummary; onChanged: () => void }) {
  const { toast } = useToast()
  const [provider, setProvider] = useState(summary.llm.provider ?? 'nim')
  const [apiKey, setApiKey] = useState('')
  const [model, setModel] = useState(provider === 'nim' ? 'meta/llama-3.3-70b-instruct' : 'gemini-1.5-flash')
  const [testing, setTesting] = useState(false)
  const [saving, setSaving] = useState(false)
  const [testResult, setTestResult] = useState<{ ok: boolean; message: string } | null>(null)

  async function test() {
    setTesting(true)
    setTestResult(null)
    try {
      setTestResult(await api.testLLMIntegration({ provider, api_key: apiKey, model }))
    } catch (e: unknown) {
      setTestResult({ ok: false, message: (e as Error).message })
    } finally {
      setTesting(false)
    }
  }

  async function save() {
    setSaving(true)
    try {
      await api.saveLLMIntegration({ provider, model, ...(apiKey ? { api_key: apiKey } : {}) })
      toast('AI provider settings saved and applied live', 'success')
      setApiKey('')
      onChanged()
    } catch (e: unknown) {
      toast((e as Error).message, 'error')
    } finally {
      setSaving(false)
    }
  }

  async function disable() {
    setSaving(true)
    try {
      await api.saveLLMIntegration({ provider: '' })
      toast('AI provider disabled — using rule-based fallback', 'info')
      onChanged()
    } catch (e: unknown) {
      toast((e as Error).message, 'error')
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="border border-gray-200 rounded-lg p-4 bg-white shadow-sm max-w-2xl">
      <div className="flex items-center justify-between mb-2">
        <h2 className="font-semibold text-gray-800">AI Provider</h2>
        <span className="flex items-center gap-1.5 text-xs text-gray-500">
          <StatusDot ok={summary.llm.configured} />
          {summary.llm.configured ? `Active: ${summary.llm.provider}` : 'Rule-based fallback (no AI)'}
        </span>
      </div>
      <p className="text-sm text-gray-600 mb-3">
        Diagnosis works without this — it falls back to deterministic rules. Configure a provider for
        AI-reasoned root-cause analysis.
      </p>

      <div className="flex gap-2 mb-2">
        <select
          className="border border-gray-300 rounded px-2 py-1.5 text-sm"
          value={provider}
          onChange={(e) => {
            setProvider(e.target.value)
            setModel(e.target.value === 'nim' ? 'meta/llama-3.3-70b-instruct' : 'gemini-1.5-flash')
          }}
        >
          <option value="nim">NVIDIA NIM</option>
          <option value="gemini">Google Gemini</option>
        </select>
        <input
          className="flex-1 border border-gray-300 rounded px-2 py-1.5 text-sm"
          placeholder="Model"
          value={model}
          onChange={(e) => setModel(e.target.value)}
        />
      </div>

      <input
        type="password"
        className="w-full border border-gray-300 rounded px-2 py-1.5 text-sm mb-1"
        placeholder={summary.llm.configured ? 'API key (leave blank to keep existing)' : 'API key'}
        value={apiKey}
        onChange={(e) => setApiKey(e.target.value)}
      />
      {summary.llm.configured && <RevealButton category="llm" field="api_key" />}

      <div className="flex gap-2 mt-3">
        <button
          onClick={test}
          disabled={testing || !apiKey}
          className="px-3 py-1.5 border border-gray-300 rounded text-sm hover:bg-gray-50 disabled:opacity-50"
        >
          {testing ? 'Testing…' : 'Test connection'}
        </button>
        <button
          onClick={save}
          disabled={saving || (!apiKey && !summary.llm.configured)}
          className="px-3 py-1.5 bg-indigo-600 text-white rounded text-sm hover:bg-indigo-700 disabled:opacity-50"
        >
          {saving ? 'Saving…' : 'Save'}
        </button>
        {summary.llm.configured && (
          <button onClick={disable} disabled={saving} className="px-3 py-1.5 text-sm text-red-600 hover:underline disabled:opacity-50">
            Disable
          </button>
        )}
      </div>

      {testResult && (
        <p className={`mt-2 text-sm ${testResult.ok ? 'text-green-700' : 'text-red-600'}`}>
          {testResult.ok ? '✅' : '❌'} {testResult.message}
        </p>
      )}
    </div>
  )
}

// ---------------------------------------------------------------------------
// Notifications section
// ---------------------------------------------------------------------------

function NotificationsSection({ summary, onChanged }: { summary: IntegrationsSummary; onChanged: () => void }) {
  const { toast } = useToast()
  const [botToken, setBotToken] = useState('')
  const [signingSecret, setSigningSecret] = useState('')
  const [channelId, setChannelId] = useState('')
  const [routingKey, setRoutingKey] = useState('')
  const [savingSlack, setSavingSlack] = useState(false)
  const [savingPD, setSavingPD] = useState(false)
  const [testingSlack, setTestingSlack] = useState(false)
  const [testingPD, setTestingPD] = useState(false)
  const [slackResult, setSlackResult] = useState<{ ok: boolean; message: string } | null>(null)
  const [pdResult, setPdResult] = useState<{ ok: boolean; message: string } | null>(null)

  async function saveSlack() {
    setSavingSlack(true)
    try {
      await api.saveNotificationsIntegration({
        ...(channelId ? { slack_channel_id: channelId } : {}),
        ...(botToken ? { slack_bot_token: botToken } : {}),
        ...(signingSecret ? { slack_signing_secret: signingSecret } : {}),
      })
      toast('Slack settings saved and applied live', 'success')
      setBotToken('')
      setSigningSecret('')
      onChanged()
    } catch (e: unknown) {
      toast((e as Error).message, 'error')
    } finally {
      setSavingSlack(false)
    }
  }

  async function testSlack() {
    setTestingSlack(true)
    setSlackResult(null)
    try {
      setSlackResult(await api.testNotificationsIntegration('slack', { slack_bot_token: botToken }))
    } catch (e: unknown) {
      setSlackResult({ ok: false, message: (e as Error).message })
    } finally {
      setTestingSlack(false)
    }
  }

  async function savePagerDuty() {
    setSavingPD(true)
    try {
      await api.saveNotificationsIntegration({
        pagerduty_routing_key: routingKey,
      })
      toast('PagerDuty settings saved and applied live', 'success')
      setRoutingKey('')
      onChanged()
    } catch (e: unknown) {
      toast((e as Error).message, 'error')
    } finally {
      setSavingPD(false)
    }
  }

  async function testPagerDuty() {
    setTestingPD(true)
    setPdResult(null)
    try {
      setPdResult(await api.testNotificationsIntegration('pagerduty', { pagerduty_routing_key: routingKey }))
    } catch (e: unknown) {
      setPdResult({ ok: false, message: (e as Error).message })
    } finally {
      setTestingPD(false)
    }
  }

  return (
    <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
      <div className="border border-gray-200 rounded-lg p-4 bg-white shadow-sm">
        <div className="flex items-center justify-between mb-2">
          <h2 className="font-semibold text-gray-800">Slack</h2>
          <span className="flex items-center gap-1.5 text-xs text-gray-500">
            <StatusDot ok={summary.notifications.slack_configured} />
            {summary.notifications.slack_configured ? 'Configured' : 'Not configured'}
          </span>
        </div>
        <input
          type="password"
          className="w-full border border-gray-300 rounded px-2 py-1.5 text-sm mb-2"
          placeholder="Bot token (xoxb-...)"
          value={botToken}
          onChange={(e) => setBotToken(e.target.value)}
        />
        <input
          type="password"
          className="w-full border border-gray-300 rounded px-2 py-1.5 text-sm mb-2"
          placeholder="Signing secret"
          value={signingSecret}
          onChange={(e) => setSigningSecret(e.target.value)}
        />
        <input
          className="w-full border border-gray-300 rounded px-2 py-1.5 text-sm mb-1"
          placeholder="Channel ID (e.g. C01234ABCDE)"
          value={channelId}
          onChange={(e) => setChannelId(e.target.value)}
        />
        {summary.notifications.slack_configured && (
          <div className="flex gap-3 mb-2">
            <RevealButton category="notifications" field="slack_bot_token" />
            <RevealButton category="notifications" field="slack_signing_secret" />
          </div>
        )}
        <div className="flex gap-2 mt-2">
          <button
            onClick={testSlack}
            disabled={testingSlack || !botToken}
            className="px-3 py-1.5 border border-gray-300 rounded text-sm hover:bg-gray-50 disabled:opacity-50"
          >
            {testingSlack ? 'Testing…' : 'Test connection'}
          </button>
          <button
            onClick={saveSlack}
            disabled={savingSlack}
            className="px-3 py-1.5 bg-indigo-600 text-white rounded text-sm hover:bg-indigo-700 disabled:opacity-50"
          >
            {savingSlack ? 'Saving…' : 'Save'}
          </button>
        </div>
        {slackResult && (
          <p className={`mt-2 text-sm ${slackResult.ok ? 'text-green-700' : 'text-red-600'}`}>
            {slackResult.ok ? '✅' : '❌'} {slackResult.message}
          </p>
        )}
      </div>

      <div className="border border-gray-200 rounded-lg p-4 bg-white shadow-sm">
        <div className="flex items-center justify-between mb-2">
          <h2 className="font-semibold text-gray-800">PagerDuty</h2>
          <span className="flex items-center gap-1.5 text-xs text-gray-500">
            <StatusDot ok={summary.notifications.pagerduty_configured} />
            {summary.notifications.pagerduty_configured ? 'Configured' : 'Not configured'}
          </span>
        </div>
        <input
          type="password"
          className="w-full border border-gray-300 rounded px-2 py-1.5 text-sm mb-1"
          placeholder="Events API v2 routing key (32 characters)"
          value={routingKey}
          onChange={(e) => setRoutingKey(e.target.value)}
        />
        {summary.notifications.pagerduty_configured && <RevealButton category="notifications" field="pagerduty_routing_key" />}
        <div className="flex gap-2 mt-2">
          <button
            onClick={testPagerDuty}
            disabled={testingPD || !routingKey}
            className="px-3 py-1.5 border border-gray-300 rounded text-sm hover:bg-gray-50 disabled:opacity-50"
          >
            {testingPD ? 'Validating…' : 'Validate format'}
          </button>
          <button
            onClick={savePagerDuty}
            disabled={savingPD || !routingKey}
            className="px-3 py-1.5 bg-indigo-600 text-white rounded text-sm hover:bg-indigo-700 disabled:opacity-50"
          >
            {savingPD ? 'Saving…' : 'Save'}
          </button>
        </div>
        {pdResult && (
          <p className={`mt-2 text-sm ${pdResult.ok ? 'text-green-700' : 'text-red-600'}`}>
            {pdResult.ok ? '✅' : '❌'} {pdResult.message}
          </p>
        )}
        <p className="text-xs text-gray-400 mt-2">
          PagerDuty has no harmless connectivity ping — this only validates the key's format.
        </p>
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// GitOps & Remediation section
// ---------------------------------------------------------------------------

function GitOpsSection({ summary, onChanged }: { summary: IntegrationsSummary; onChanged: () => void }) {
  const { toast } = useToast()
  const [repoPath, setRepoPath] = useState('')
  const [remoteURL, setRemoteURL] = useState('')
  const [authToken, setAuthToken] = useState('')
  const [branch, setBranch] = useState('main')
  const [botName, setBotName] = useState('autosre-bot')
  const [botEmail, setBotEmail] = useState('autosre-bot@localhost')
  const [saving, setSaving] = useState(false)
  const [testing, setTesting] = useState(false)
  const [testResult, setTestResult] = useState<{ ok: boolean; message: string } | null>(null)

  async function test() {
    setTesting(true)
    setTestResult(null)
    try {
      setTestResult(await api.testGitOpsIntegration({ remote_url: remoteURL, auth_token: authToken }))
    } catch (e: unknown) {
      setTestResult({ ok: false, message: (e as Error).message })
    } finally {
      setTesting(false)
    }
  }

  async function save() {
    setSaving(true)
    try {
      await api.saveGitOpsIntegration({
        repo_path: repoPath, remote_url: remoteURL, branch, bot_name: botName, bot_email: botEmail,
        ...(authToken ? { auth_token: authToken } : {}),
      })
      toast('GitOps settings saved and applied live', 'success')
      setAuthToken('')
      onChanged()
    } catch (e: unknown) {
      toast((e as Error).message, 'error')
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="border border-gray-200 rounded-lg p-4 bg-white shadow-sm max-w-2xl">
      <div className="flex items-center justify-between mb-2">
        <h2 className="font-semibold text-gray-800">GitOps & Remediation</h2>
        <span className="flex items-center gap-1.5 text-xs text-gray-500">
          <StatusDot ok={summary.gitops.configured} />
          {summary.gitops.configured ? 'Configured' : 'Not configured'}
        </span>
      </div>
      <p className="text-sm text-gray-600 mb-3">
        Where automated remediation commits go. ArgoCD syncs this repo to the cluster.
      </p>

      <input
        className="w-full border border-gray-300 rounded px-2 py-1.5 text-sm mb-2"
        placeholder="Repo path (e.g. /data/gitops)"
        value={repoPath}
        onChange={(e) => setRepoPath(e.target.value)}
      />
      <input
        className="w-full border border-gray-300 rounded px-2 py-1.5 text-sm mb-2"
        placeholder="Remote URL (e.g. https://github.com/org/gitops.git)"
        value={remoteURL}
        onChange={(e) => setRemoteURL(e.target.value)}
      />
      <input
        type="password"
        className="w-full border border-gray-300 rounded px-2 py-1.5 text-sm mb-2"
        placeholder="Auth token (GitHub PAT)"
        value={authToken}
        onChange={(e) => setAuthToken(e.target.value)}
      />
      {summary.gitops.configured && (
        <div className="mb-2">
          <RevealButton category="gitops" field="auth_token" />
        </div>
      )}
      <div className="grid grid-cols-2 gap-2 mb-2">
        <input className="border border-gray-300 rounded px-2 py-1.5 text-sm" placeholder="Branch" value={branch} onChange={(e) => setBranch(e.target.value)} />
        <input className="border border-gray-300 rounded px-2 py-1.5 text-sm" placeholder="Bot name" value={botName} onChange={(e) => setBotName(e.target.value)} />
      </div>
      <input
        className="w-full border border-gray-300 rounded px-2 py-1.5 text-sm mb-2"
        placeholder="Bot email"
        value={botEmail}
        onChange={(e) => setBotEmail(e.target.value)}
      />

      <div className="flex gap-2 mt-2">
        <button
          onClick={test}
          disabled={testing || !remoteURL}
          className="px-3 py-1.5 border border-gray-300 rounded text-sm hover:bg-gray-50 disabled:opacity-50"
        >
          {testing ? 'Testing…' : 'Test connection'}
        </button>
        <button
          onClick={save}
          disabled={saving || !repoPath}
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
    </div>
  )
}

// ---------------------------------------------------------------------------
// Safety Controls section
// ---------------------------------------------------------------------------

function SafetySection({ summary, onChanged }: { summary: IntegrationsSummary; onChanged: () => void }) {
  const { toast } = useToast()
  const [busy, setBusy] = useState(false)

  async function toggleApply() {
    setBusy(true)
    try {
      await api.setSafety(!summary.safety.apply_enabled, 'toggled from Settings page')
      toast(`Apply ${!summary.safety.apply_enabled ? 'enabled' : 'disabled'}`, 'success')
      onChanged()
    } catch (e: unknown) {
      toast((e as Error).message, 'error')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="border border-gray-200 rounded-lg p-4 bg-white shadow-sm max-w-xl">
      <h2 className="font-semibold text-gray-800 mb-3">Safety Controls</h2>

      <div className="flex items-center justify-between py-2 border-b border-gray-100">
        <div>
          <p className="text-sm font-medium text-gray-700">Apply Enabled</p>
          <p className="text-xs text-gray-500">
            When off, the agent only dry-runs remediations — no real GitOps commits.
          </p>
        </div>
        <button
          onClick={toggleApply}
          disabled={busy}
          className={`px-3 py-1.5 rounded text-sm disabled:opacity-50 ${
            summary.safety.apply_enabled ? 'bg-green-600 text-white hover:bg-green-700' : 'bg-gray-200 text-gray-700 hover:bg-gray-300'
          }`}
        >
          {summary.safety.apply_enabled ? 'Enabled' : 'Disabled'}
        </button>
      </div>

      <div className="flex items-center justify-between py-2">
        <div>
          <p className="text-sm font-medium text-gray-700">Kill Switch</p>
          <p className="text-xs text-gray-500">Halts all remediation immediately, regardless of Apply Enabled.</p>
        </div>
        <span className={`text-sm font-medium ${summary.safety.kill_switch_engaged ? 'text-red-600' : 'text-gray-500'}`}>
          {summary.safety.kill_switch_engaged ? 'Engaged' : 'Off'}
        </span>
      </div>
      <p className="text-xs text-gray-400 mt-2">
        The Kill Switch has its own dedicated control on the System Status page.
      </p>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Page shell
// ---------------------------------------------------------------------------

type SectionKey = 'telemetry' | 'ai' | 'notifications' | 'gitops' | 'safety'

const SECTIONS: { key: SectionKey; label: string }[] = [
  { key: 'telemetry', label: 'Telemetry' },
  { key: 'ai', label: 'AI Provider' },
  { key: 'notifications', label: 'Notifications' },
  { key: 'gitops', label: 'GitOps & Remediation' },
  { key: 'safety', label: 'Safety Controls' },
]

export default function Settings() {
  const [section, setSection] = useState<SectionKey>('telemetry')
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
      <h1 className="text-2xl font-bold mb-1">Settings</h1>
      <p className="text-sm text-gray-500 mb-4">
        Everything here is configured through this page — no .env edits or restarts required.
      </p>

      {loading && <SkeletonCards rows={3} />}
      {error && <p className="text-red-600 text-sm">Error: {error}</p>}

      {summary && (
        <div className="flex gap-6">
          <nav className="w-48 shrink-0">
            <ul className="space-y-1">
              {SECTIONS.map((s) => (
                <li key={s.key}>
                  <button
                    onClick={() => setSection(s.key)}
                    className={`w-full text-left px-3 py-2 rounded text-sm ${
                      section === s.key ? 'bg-indigo-600 text-white' : 'text-gray-700 hover:bg-gray-100'
                    }`}
                  >
                    {s.label}
                  </button>
                </li>
              ))}
            </ul>
          </nav>

          <div className="flex-1 min-w-0">
            {section === 'telemetry' && <TelemetrySection summary={summary} onChanged={load} />}
            {section === 'ai' && <AIProviderSection summary={summary} onChanged={load} />}
            {section === 'notifications' && <NotificationsSection summary={summary} onChanged={load} />}
            {section === 'gitops' && <GitOpsSection summary={summary} onChanged={load} />}
            {section === 'safety' && <SafetySection summary={summary} onChanged={load} />}
          </div>
        </div>
      )}
    </div>
  )
}
