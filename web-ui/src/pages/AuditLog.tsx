import { useCallback, useState } from 'react'
import { api, type AuditEvent } from '../api/client'
import { SkeletonTable } from '../components/Skeleton'

const PAGE_SIZE = 50

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
  KillSwitchToggled: 'bg-red-100 text-red-800',
}

export default function AuditLog() {
  const [events, setEvents] = useState<AuditEvent[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(0)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [incidentSearch, setIncidentSearch] = useState('')
  const [stageFilter, setStageFilter] = useState('')
  const [expanded, setExpanded] = useState<number | null>(null)

  const load = useCallback((pg: number, inc: string, stage: string) => {
    setLoading(true)
    setError(null)
    const params = new URLSearchParams({
      limit: String(PAGE_SIZE),
      offset: String(pg * PAGE_SIZE),
    })
    if (inc) params.set('incident_id', inc)
    if (stage) params.set('stage', stage)

    const token = localStorage.getItem('autosre_token')
    const headers: Record<string, string> = {}
    if (token) headers['Authorization'] = `Bearer ${token}`

    fetch(`/api/v1/audit?${params}`, { headers })
      .then((r) => r.json())
      .then((data) => {
        setEvents(data.events ?? [])
        setTotal(data.count ?? 0)
      })
      .catch((e: Error) => setError(e.message))
      .finally(() => setLoading(false))
  }, [])

  // initial load
  useState(() => { load(0, '', '') })

  function search() {
    setPage(0)
    load(0, incidentSearch, stageFilter)
  }

  function goPage(p: number) {
    setPage(p)
    load(p, incidentSearch, stageFilter)
  }

  const stages = ['Detected', 'Diagnosed', 'Decided', 'ApprovalRequested', 'ApprovalResolved',
    'DryRun', 'Applied', 'Verified', 'Notified', 'Escalated', 'KillSwitchToggled']

  return (
    <div>
      <h1 className="text-2xl font-bold mb-4">Audit Log</h1>

      <div className="flex flex-wrap gap-2 mb-4">
        <input
          type="text"
          placeholder="Filter by incident ID…"
          value={incidentSearch}
          onChange={(e) => setIncidentSearch(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && search()}
          className="border border-gray-300 rounded px-3 py-1.5 text-sm w-56 focus:outline-none focus:ring-2 focus:ring-indigo-400"
        />
        <select
          value={stageFilter}
          onChange={(e) => setStageFilter(e.target.value)}
          className="border border-gray-300 rounded px-2 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-400"
        >
          <option value="">All stages</option>
          {stages.map((s) => <option key={s} value={s}>{s}</option>)}
        </select>
        <button
          onClick={search}
          className="bg-indigo-600 text-white px-3 py-1.5 rounded text-sm hover:bg-indigo-700"
        >
          Search
        </button>
      </div>

      {loading && <SkeletonTable rows={8} />}
      {error && <p className="text-red-600 text-sm">Error: {error}</p>}

      {!loading && events.length === 0 && (
        <p className="text-gray-500 text-sm">No audit events found.</p>
      )}

      {!loading && events.length > 0 && (
        <>
          <div className="overflow-x-auto rounded-lg border border-gray-200">
            <table className="min-w-full divide-y divide-gray-200 text-sm">
              <thead className="bg-gray-50">
                <tr>
                  <th className="px-4 py-2 text-left text-xs font-semibold text-gray-600">Time</th>
                  <th className="px-4 py-2 text-left text-xs font-semibold text-gray-600">Incident</th>
                  <th className="px-4 py-2 text-left text-xs font-semibold text-gray-600">Stage</th>
                  <th className="px-4 py-2 text-left text-xs font-semibold text-gray-600">Outcome</th>
                  <th className="px-4 py-2 text-left text-xs font-semibold text-gray-600">Trace</th>
                  <th className="px-4 py-2"></th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-100 bg-white">
                {events.map((ev, i) => (
                  <>
                    <tr key={i} className="hover:bg-gray-50 cursor-pointer" onClick={() => setExpanded(expanded === i ? null : i)}>
                      <td className="px-4 py-2 text-xs text-gray-500 whitespace-nowrap">
                        {new Date(ev.timestamp).toLocaleString()}
                      </td>
                      <td className="px-4 py-2 font-mono text-xs text-gray-600">{ev.incident_id.slice(0, 10)}</td>
                      <td className="px-4 py-2">
                        <span className={`px-2 py-0.5 rounded text-xs font-medium ${stageColors[ev.stage] ?? 'bg-gray-100 text-gray-700'}`}>
                          {ev.stage}
                        </span>
                      </td>
                      <td className="px-4 py-2 text-sm">{ev.outcome}</td>
                      <td className="px-4 py-2 font-mono text-xs text-gray-400">{ev.trace_id.slice(0, 8)}</td>
                      <td className="px-4 py-2 text-xs text-gray-400">{expanded === i ? '▲' : '▼'}</td>
                    </tr>
                    {expanded === i && (
                      <tr key={`${i}-detail`} className="bg-gray-50">
                        <td colSpan={6} className="px-4 py-2">
                          <pre className="text-xs overflow-x-auto whitespace-pre-wrap text-gray-700">
                            {JSON.stringify(ev.details, null, 2)}
                          </pre>
                        </td>
                      </tr>
                    )}
                  </>
                ))}
              </tbody>
            </table>
          </div>
          <div className="flex items-center justify-between mt-3 text-sm text-gray-500">
            <span>Showing {page * PAGE_SIZE + 1}–{page * PAGE_SIZE + events.length} of {total}</span>
            <div className="flex gap-2">
              <button disabled={page === 0} onClick={() => goPage(page - 1)} className="px-3 py-1 border rounded disabled:opacity-40 hover:bg-gray-50">Prev</button>
              <button disabled={events.length < PAGE_SIZE} onClick={() => goPage(page + 1)} className="px-3 py-1 border rounded disabled:opacity-40 hover:bg-gray-50">Next</button>
            </div>
          </div>
        </>
      )}
    </div>
  )
}
