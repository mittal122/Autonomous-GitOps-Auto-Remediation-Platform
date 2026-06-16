import { useEffect, useState } from 'react'
import { api, type StatusResponse } from '../api/client'

function Badge({ ok, yes, no }: { ok: boolean; yes: string; no: string }) {
  return (
    <span className={`px-2 py-0.5 rounded text-xs font-medium ${ok ? 'bg-green-100 text-green-800' : 'bg-red-100 text-red-800'}`}>
      {ok ? yes : no}
    </span>
  )
}

export default function Status() {
  const [status, setStatus] = useState<StatusResponse | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [ksReason, setKsReason] = useState('')
  const [ksError, setKsError] = useState<string | null>(null)
  const [ksBusy, setKsBusy] = useState(false)

  function load() {
    setLoading(true)
    api.getStatus()
      .then(setStatus)
      .catch((e: Error) => setError(e.message))
      .finally(() => setLoading(false))
  }

  useEffect(load, [])

  async function toggleKillSwitch() {
    if (!status) return
    if (!ksReason.trim()) {
      setKsError('A reason is required to toggle the kill switch.')
      return
    }
    setKsBusy(true)
    setKsError(null)
    try {
      await api.setKillSwitch(!status.kill_switch_engaged, ksReason)
      setKsReason('')
      load()
    } catch (e: unknown) {
      setKsError((e as Error).message)
    } finally {
      setKsBusy(false)
    }
  }

  if (loading) return <p className="text-gray-500">Loading status...</p>
  if (error) return <p className="text-red-600">Error: {error}</p>
  if (!status) return null

  return (
    <div>
      <h1 className="text-2xl font-bold mb-4">System Status</h1>

      <div className="grid grid-cols-2 sm:grid-cols-3 gap-4 mb-6">
        <StatusCard label="Apply Enabled">
          <Badge ok={status.apply_enabled} yes="ENABLED" no="DRY-RUN" />
        </StatusCard>
        <StatusCard label="Kill Switch">
          <Badge ok={!status.kill_switch_engaged} yes="INACTIVE" no="ENGAGED" />
        </StatusCard>
        <StatusCard label="In-Flight Pipelines">
          <span className="text-xl font-bold">{status.in_flight_pipelines}</span>
        </StatusCard>
        <StatusCard label="Circuit Breaker">
          <Badge ok={!status.circuit_breaker_tripped} yes="OK" no="TRIPPED" />
          <p className="text-xs text-gray-500 mt-1">
            {status.circuit_breaker_count}/{status.circuit_breaker_max} in {status.circuit_breaker_window_sec}s window
          </p>
        </StatusCard>
      </div>

      <div className="border border-yellow-200 bg-yellow-50 rounded-lg p-4 max-w-md">
        <h2 className="font-semibold text-yellow-900 mb-1">Kill Switch (Admin Only)</h2>
        <p className="text-xs text-yellow-800 mb-3">
          Engaging the kill switch halts all pending remediation applies immediately.
          Every toggle is audited. Requires admin role.
        </p>
        <div className="flex gap-2 items-center">
          <input
            type="text"
            className="flex-1 border border-yellow-300 rounded px-2 py-1 text-sm bg-white"
            placeholder="Reason (required)"
            value={ksReason}
            onChange={(e) => setKsReason(e.target.value)}
            disabled={ksBusy}
          />
          <button
            onClick={toggleKillSwitch}
            disabled={ksBusy}
            className={`px-3 py-1 rounded text-sm font-medium text-white disabled:opacity-50 ${
              status.kill_switch_engaged
                ? 'bg-green-600 hover:bg-green-700'
                : 'bg-red-600 hover:bg-red-700'
            }`}
          >
            {status.kill_switch_engaged ? 'Disengage' : 'Engage'}
          </button>
        </div>
        {ksError && <p className="mt-2 text-sm text-red-700">{ksError}</p>}
      </div>
    </div>
  )
}

function StatusCard({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="border border-gray-200 rounded-lg p-4 bg-white shadow-sm">
      <p className="text-xs font-semibold text-gray-500 uppercase tracking-wide mb-2">{label}</p>
      {children}
    </div>
  )
}
