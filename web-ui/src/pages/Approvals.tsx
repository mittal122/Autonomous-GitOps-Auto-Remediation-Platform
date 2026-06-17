import { useCallback, useState } from 'react'
import { api, type PendingApproval } from '../api/client'
import { useInterval } from '../hooks/useInterval'
import { useToast } from '../hooks/useToast'
import { SkeletonCards } from '../components/Skeleton'

function ApprovalCard({ approval, onResolved }: { approval: PendingApproval; onResolved: () => void }) {
  const [reason, setReason] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)
  const { toast } = useToast()

  async function decide(approve: boolean) {
    setBusy(true)
    setErr(null)
    try {
      if (approve) {
        await api.approve(approval.request_id, reason)
        toast('Approval submitted successfully', 'success')
      } else {
        await api.reject(approval.request_id, reason)
        toast('Rejection submitted successfully', 'info')
      }
      onResolved()
    } catch (e: unknown) {
      const msg = (e as Error).message
      setErr(msg)
      toast(msg, 'error')
    } finally {
      setBusy(false)
    }
  }

  const p = approval.proposal
  const deadline = new Date(approval.deadline)
  const msLeft = deadline.getTime() - Date.now()
  const minLeft = Math.max(0, Math.round(msLeft / 60000))

  return (
    <div className="border border-gray-200 rounded-lg p-4 bg-white shadow-sm">
      <div className="flex items-start justify-between mb-2">
        <div>
          <span className="font-mono text-xs text-gray-400">{approval.request_id.slice(0, 12)}</span>
          <p className="font-semibold text-gray-800 mt-0.5">
            {p.params.action_type} on {p.namespace}/{p.resource}
          </p>
          <p className="text-sm text-gray-600">
            Failure: <code className="bg-gray-100 px-1 rounded">{p.failure_mode}</code>{' '}
            — Confidence: <strong>{(p.confidence * 100).toFixed(0)}%</strong>
          </p>
        </div>
        <span className={`text-xs font-medium px-2 py-0.5 rounded ${minLeft < 5 ? 'bg-red-100 text-red-700' : 'bg-yellow-100 text-yellow-700'}`}>
          {minLeft}m left
        </span>
      </div>
      <div className="mt-3 flex items-center gap-2">
        <input
          type="text"
          className="flex-1 border border-gray-300 rounded px-2 py-1 text-sm"
          placeholder="Reason (optional)"
          value={reason}
          onChange={(e) => setReason(e.target.value)}
          disabled={busy}
        />
        <button
          onClick={() => decide(true)}
          disabled={busy}
          className="px-3 py-1 bg-green-600 text-white rounded text-sm hover:bg-green-700 disabled:opacity-50"
        >
          Approve
        </button>
        <button
          onClick={() => decide(false)}
          disabled={busy}
          className="px-3 py-1 bg-red-600 text-white rounded text-sm hover:bg-red-700 disabled:opacity-50"
        >
          Reject
        </button>
      </div>
      {err && <p className="mt-2 text-sm text-red-600">{err}</p>}
    </div>
  )
}

export default function Approvals() {
  const [approvals, setApprovals] = useState<PendingApproval[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(() => {
    api.listApprovals()
      .then((data) => { setApprovals(data ?? []); setError(null) })
      .catch((e: Error) => setError(e.message))
      .finally(() => setLoading(false))
  }, [])

  useState(() => { load() })
  useInterval(load, 15_000)

  return (
    <div>
      <div className="flex items-center justify-between mb-4">
        <h1 className="text-2xl font-bold">Pending Approvals</h1>
        <span className="text-xs text-gray-400">auto-refresh 15s</span>
      </div>
      {loading && <SkeletonCards rows={3} />}
      {error && <p className="text-red-600 text-sm">Error: {error}</p>}
      {!loading && approvals.length === 0 && <p className="text-gray-500">No pending approvals.</p>}
      {!loading && approvals.length > 0 && (
        <div className="space-y-3">
          {approvals.map((a) => (
            <ApprovalCard key={a.request_id} approval={a} onResolved={load} />
          ))}
        </div>
      )}
    </div>
  )
}
