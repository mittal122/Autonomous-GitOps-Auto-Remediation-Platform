import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { api, type IntegrationsSummary, type LokiTestResult } from '../api/client'

const SETUP_DISMISSED_KEY = 'autosre_setup_dismissed'

type StepKey = 'welcome' | 'loki' | 'alertmanager' | 'done'
const STEPS: StepKey[] = ['welcome', 'loki', 'alertmanager', 'done']

function StepIndicator({ current }: { current: StepKey }) {
  const idx = STEPS.indexOf(current)
  return (
    <div className="flex items-center gap-2 mb-6">
      {STEPS.map((s, i) => (
        <div key={s} className="flex items-center gap-2">
          <div
            className={`w-7 h-7 rounded-full flex items-center justify-center text-xs font-semibold ${
              i <= idx ? 'bg-indigo-600 text-white' : 'bg-gray-200 text-gray-500'
            }`}
          >
            {i + 1}
          </div>
          {i < STEPS.length - 1 && <div className={`w-10 h-0.5 ${i < idx ? 'bg-indigo-600' : 'bg-gray-200'}`} />}
        </div>
      ))}
    </div>
  )
}

function WelcomeStep({ onNext, onSkip }: { onNext: () => void; onSkip: () => void }) {
  return (
    <div>
      <h2 className="text-xl font-bold mb-2">Welcome to AutoSRE</h2>
      <p className="text-gray-600 mb-4">
        Let's connect your telemetry sources. This takes about two minutes — nothing here requires
        editing files or restarting any service.
      </p>
      <div className="flex gap-2">
        <button onClick={onNext} className="px-4 py-2 bg-indigo-600 text-white rounded text-sm hover:bg-indigo-700">
          Get started
        </button>
        <button onClick={onSkip} className="px-4 py-2 border border-gray-300 rounded text-sm hover:bg-gray-50">
          Skip for now
        </button>
      </div>
    </div>
  )
}

function LokiStep({ onNext, onBack }: { onNext: () => void; onBack: () => void }) {
  const [addr, setAddr] = useState('')
  const [testing, setTesting] = useState(false)
  const [saving, setSaving] = useState(false)
  const [result, setResult] = useState<LokiTestResult | null>(null)
  const [saved, setSaved] = useState(false)

  async function test() {
    setTesting(true)
    setResult(null)
    try {
      setResult(await api.testLokiIntegration({ addr }))
    } catch (e: unknown) {
      setResult({ ok: false, message: (e as Error).message })
    } finally {
      setTesting(false)
    }
  }

  async function saveAndNext() {
    if (!addr) { onNext(); return }
    setSaving(true)
    try {
      await api.saveLokiIntegration({ addr })
      setSaved(true)
      onNext()
    } catch (e: unknown) {
      setResult({ ok: false, message: (e as Error).message })
    } finally {
      setSaving(false)
    }
  }

  return (
    <div>
      <h2 className="text-xl font-bold mb-2">Connect Loki</h2>
      <p className="text-gray-600 mb-4 text-sm">
        Loki lets AutoSRE detect failures from your application logs (OOMKilled, CrashLoopBackOff,
        DNS errors, and more). Leave this blank and click Next to skip it for now — you can always
        configure it later from the Settings page.
      </p>
      <input
        className="w-full border border-gray-300 rounded px-3 py-2 text-sm mb-2"
        placeholder="Loki URL (e.g. http://loki:3100)"
        value={addr}
        onChange={(e) => setAddr(e.target.value)}
      />
      <div className="flex gap-2 mb-2">
        <button
          onClick={test}
          disabled={testing || !addr}
          className="px-3 py-1.5 border border-gray-300 rounded text-sm hover:bg-gray-50 disabled:opacity-50"
        >
          {testing ? 'Testing…' : 'Test connection'}
        </button>
      </div>
      {result && (
        <p className={`text-sm mb-2 ${result.ok ? 'text-green-700' : 'text-red-600'}`}>
          {result.ok ? '✅' : '❌'} {result.message}
        </p>
      )}
      {saved && <p className="text-sm text-green-700 mb-2">✅ Saved and applied live</p>}
      <div className="flex gap-2 mt-4">
        <button onClick={onBack} className="px-4 py-2 border border-gray-300 rounded text-sm hover:bg-gray-50">
          Back
        </button>
        <button
          onClick={saveAndNext}
          disabled={saving}
          className="px-4 py-2 bg-indigo-600 text-white rounded text-sm hover:bg-indigo-700 disabled:opacity-50"
        >
          {saving ? 'Saving…' : addr ? 'Save and continue' : 'Skip'}
        </button>
      </div>
    </div>
  )
}

function AlertmanagerStep({ onNext, onBack }: { onNext: () => void; onBack: () => void }) {
  const [info, setInfo] = useState<IntegrationsSummary['alertmanager'] | null>(null)
  const [yamlSnippet, setYamlSnippet] = useState('')
  const [copied, setCopied] = useState(false)
  const [applying, setApplying] = useState(false)
  const [applyResult, setApplyResult] = useState<{ applied: boolean; reason: string } | null>(null)

  useState(() => {
    api.getAlertmanagerIntegration().then((data) => {
      setInfo({ webhook_url: data.webhook_url, operator_detected: data.operator_detected })
      setYamlSnippet(data.yaml_snippet)
    })
  })

  async function copy() {
    if (!info) return
    await navigator.clipboard.writeText(yamlSnippet)
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }

  async function apply() {
    setApplying(true)
    try {
      setApplyResult(await api.applyAlertmanagerIntegration())
    } catch (e: unknown) {
      setApplyResult({ applied: false, reason: (e as Error).message })
    } finally {
      setApplying(false)
    }
  }

  return (
    <div>
      <h2 className="text-xl font-bold mb-2">Connect Alertmanager</h2>
      <p className="text-gray-600 mb-4 text-sm">
        Point your Prometheus Alertmanager at this webhook URL so its alerts become AutoSRE
        incidents. If your cluster runs the Prometheus Operator, AutoSRE can apply this
        automatically.
      </p>

      {info && (
        <>
          <label className="text-xs text-gray-500">Webhook URL</label>
          <input readOnly value={info.webhook_url} className="w-full border border-gray-300 rounded px-3 py-2 text-sm font-mono bg-gray-50 mb-3" />

          <div className="flex gap-2 mb-2">
            <button
              onClick={apply}
              disabled={applying}
              className="px-3 py-1.5 bg-indigo-600 text-white rounded text-sm hover:bg-indigo-700 disabled:opacity-50"
            >
              {applying ? 'Applying…' : 'Apply automatically'}
            </button>
            <button onClick={copy} className="px-3 py-1.5 border border-gray-300 rounded text-sm hover:bg-gray-50">
              {copied ? 'Copied!' : 'Copy YAML snippet'}
            </button>
          </div>

          {applyResult && (
            <p className={`text-sm mb-2 ${applyResult.applied ? 'text-green-700' : 'text-gray-600'}`}>
              {applyResult.applied ? '✅' : 'ℹ️'} {applyResult.reason}
              {!applyResult.applied && ' — paste the YAML snippet into your Alertmanager config instead.'}
            </p>
          )}
        </>
      )}

      <div className="flex gap-2 mt-4">
        <button onClick={onBack} className="px-4 py-2 border border-gray-300 rounded text-sm hover:bg-gray-50">
          Back
        </button>
        <button onClick={onNext} className="px-4 py-2 bg-indigo-600 text-white rounded text-sm hover:bg-indigo-700">
          Continue
        </button>
      </div>
    </div>
  )
}

function DoneStep({ onFinish }: { onFinish: () => void }) {
  return (
    <div>
      <h2 className="text-xl font-bold mb-2">Setup complete</h2>
      <p className="text-gray-600 mb-4 text-sm">
        AutoSRE is now watching for incidents. You can revisit and change any of this at any time
        from the Settings page.
      </p>
      <button onClick={onFinish} className="px-4 py-2 bg-indigo-600 text-white rounded text-sm hover:bg-indigo-700">
        Go to Dashboard
      </button>
    </div>
  )
}

export default function Setup() {
  const navigate = useNavigate()
  const [step, setStep] = useState<StepKey>('welcome')

  function dismiss() {
    localStorage.setItem(SETUP_DISMISSED_KEY, '1')
    navigate('/')
  }

  return (
    <div className="max-w-xl mx-auto mt-4">
      <StepIndicator current={step} />
      <div className="border border-gray-200 rounded-lg p-6 bg-white shadow-sm">
        {step === 'welcome' && <WelcomeStep onNext={() => setStep('loki')} onSkip={dismiss} />}
        {step === 'loki' && <LokiStep onNext={() => setStep('alertmanager')} onBack={() => setStep('welcome')} />}
        {step === 'alertmanager' && <AlertmanagerStep onNext={() => setStep('done')} onBack={() => setStep('loki')} />}
        {step === 'done' && <DoneStep onFinish={dismiss} />}
      </div>
    </div>
  )
}
