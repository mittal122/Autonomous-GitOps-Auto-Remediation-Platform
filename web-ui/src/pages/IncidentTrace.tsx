import { useEffect, useState } from 'react'
import { useParams, Link } from 'react-router-dom'
import { api, type AuditEvent } from '../api/client'

const stageColors: Record<string, string> = {
  Detected: 'bg-blue-100 text-blue-800',
  Diagnosed: 'bg-purple-100 text-purple-800',
  Decided: 'bg-indigo-100 text-indigo-800',
  ApprovalRequested: 'bg-yellow-100 text-yellow-800',
  ApprovalResolved: 'bg-orange-100 text-orange-800',
  DryRun: 'bg-gray-100 text-gray-700',
  Applied: 'bg-green-100 text-green-800',
  Verified: 'bg-teal-100 text-teal-800',
  Notified: 'bg-sky-100 text-sky-800',
  Escalated: 'bg-red-100 text-red-800',
}

function EventRow({ ev }: { ev: AuditEvent }) {
  const [open, setOpen] = useState(false)
  return (
    <>
      <tr
        className="hover:bg-gray-50 cursor-pointer"
        onClick={() => setOpen(!open)}
      >
        <td className="px-4 py-2 text-xs text-gray-500 whitespace-nowrap">
          {new Date(ev.timestamp).toLocaleTimeString()}
        </td>
        <td className="px-4 py-2">
          <span className={`px-2 py-0.5 rounded text-xs font-medium ${stageColors[ev.stage] ?? 'bg-gray-100 text-gray-700'}`}>
            {ev.stage}
          </span>
        </td>
        <td className="px-4 py-2 text-sm">{ev.outcome}</td>
        <td className="px-4 py-2 font-mono text-xs text-gray-400">{ev.trace_id.slice(0, 8)}</td>
        <td className="px-4 py-2 text-xs text-gray-400">{open ? '▲' : '▼'}</td>
      </tr>
      {open && (
        <tr className="bg-gray-50">
          <td colSpan={5} className="px-4 py-2">
            <pre className="text-xs overflow-x-auto whitespace-pre-wrap text-gray-700">
              {JSON.stringify(ev.details, null, 2)}
            </pre>
          </td>
        </tr>
      )}
    </>
  )
}

export default function IncidentTrace() {
  const { id } = useParams<{ id: string }>()
  const [events, setEvents] = useState<AuditEvent[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (!id) return
    api.getTrace(id)
      .then((r) => setEvents(r.events ?? []))
      .catch((e: Error) => setError(e.message))
      .finally(() => setLoading(false))
  }, [id])

  if (loading) return <p className="text-gray-500">Loading trace...</p>
  if (error) return <p className="text-red-600">Error: {error}</p>

  return (
    <div>
      <div className="flex items-center gap-3 mb-4">
        <Link to="/" className="text-indigo-600 hover:underline text-sm">← Incidents</Link>
        <h1 className="text-xl font-bold">Audit Trace — {id}</h1>
        <span className="text-gray-400 text-sm">({events.length} events)</span>
      </div>
      {events.length === 0 ? (
        <p className="text-gray-500">No audit events found for this incident.</p>
      ) : (
        <div className="overflow-x-auto rounded-lg border border-gray-200">
          <table className="min-w-full divide-y divide-gray-200 text-sm">
            <thead className="bg-gray-50">
              <tr>
                <th className="px-4 py-2 text-left text-xs font-semibold text-gray-600">Time</th>
                <th className="px-4 py-2 text-left text-xs font-semibold text-gray-600">Stage</th>
                <th className="px-4 py-2 text-left text-xs font-semibold text-gray-600">Outcome</th>
                <th className="px-4 py-2 text-left text-xs font-semibold text-gray-600">Trace ID</th>
                <th className="px-4 py-2"></th>
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100 bg-white">
              {events.map((ev, i) => <EventRow key={i} ev={ev} />)}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
