import { useCallback, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, type Incident } from '../api/client'
import { useInterval } from '../hooks/useInterval'
import { SkeletonTable } from '../components/Skeleton'

const REFRESH_MS = 30_000

const severityColor: Record<string, string> = {
  critical: 'bg-red-100 text-red-800',
  high: 'bg-orange-100 text-orange-800',
  medium: 'bg-yellow-100 text-yellow-800',
  low: 'bg-green-100 text-green-800',
}

const ALL = 'all'

export default function Dashboard() {
  const [incidents, setIncidents] = useState<Incident[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [lastRefresh, setLastRefresh] = useState<Date | null>(null)
  const [search, setSearch] = useState('')
  const [severityFilter, setSeverityFilter] = useState(ALL)
  const [statusFilter, setStatusFilter] = useState(ALL)

  const load = useCallback(() => {
    api.listIncidents()
      .then((data) => {
        setIncidents(data ?? [])
        setLastRefresh(new Date())
        setError(null)
      })
      .catch((e: Error) => setError(e.message))
      .finally(() => setLoading(false))
  }, [])

  // initial load
  useState(() => { load() })
  // auto-refresh every 30s
  useInterval(load, REFRESH_MS)

  const filtered = incidents.filter((inc) => {
    if (severityFilter !== ALL && inc.severity !== severityFilter) return false
    if (statusFilter !== ALL && inc.status !== statusFilter) return false
    if (search) {
      const q = search.toLowerCase()
      return (
        inc.id.toLowerCase().includes(q) ||
        inc.status.toLowerCase().includes(q) ||
        (inc.affected_resources ?? []).some((r) => r.toLowerCase().includes(q))
      )
    }
    return true
  })

  const severities = [...new Set(incidents.map((i) => i.severity))]
  const statuses = [...new Set(incidents.map((i) => i.status))]

  return (
    <div>
      <div className="flex items-center justify-between mb-4">
        <h1 className="text-2xl font-bold">Incidents</h1>
        <span className="text-xs text-gray-400">
          {lastRefresh ? `Updated ${lastRefresh.toLocaleTimeString()}` : 'Loading…'}
          {' · '}auto-refresh 30s
        </span>
      </div>

      {/* Filters */}
      <div className="flex flex-wrap gap-2 mb-4">
        <input
          type="text"
          placeholder="Search ID, status, resource…"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          className="border border-gray-300 rounded px-3 py-1.5 text-sm w-56 focus:outline-none focus:ring-2 focus:ring-indigo-400"
        />
        <select
          value={severityFilter}
          onChange={(e) => setSeverityFilter(e.target.value)}
          className="border border-gray-300 rounded px-2 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-400"
        >
          <option value={ALL}>All severities</option>
          {severities.map((s) => <option key={s} value={s}>{s}</option>)}
        </select>
        <select
          value={statusFilter}
          onChange={(e) => setStatusFilter(e.target.value)}
          className="border border-gray-300 rounded px-2 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-400"
        >
          <option value={ALL}>All statuses</option>
          {statuses.map((s) => <option key={s} value={s}>{s}</option>)}
        </select>
        <button onClick={load} className="text-sm text-indigo-600 hover:underline px-1">
          Refresh
        </button>
      </div>

      {loading && <SkeletonTable rows={5} />}
      {error && <p className="text-red-600 text-sm">Error: {error}</p>}

      {!loading && filtered.length === 0 && (
        <p className="text-gray-500 text-sm">
          {incidents.length === 0 ? 'No incidents found.' : 'No incidents match the current filters.'}
        </p>
      )}

      {!loading && filtered.length > 0 && (
        <div className="overflow-x-auto rounded-lg border border-gray-200">
          <table className="min-w-full divide-y divide-gray-200 text-sm">
            <thead className="bg-gray-50">
              <tr>
                <th className="px-4 py-3 text-left font-semibold text-gray-600">ID</th>
                <th className="px-4 py-3 text-left font-semibold text-gray-600">Severity</th>
                <th className="px-4 py-3 text-left font-semibold text-gray-600">Status</th>
                <th className="px-4 py-3 text-left font-semibold text-gray-600">Resources</th>
                <th className="px-4 py-3 text-left font-semibold text-gray-600">Created</th>
                <th className="px-4 py-3"></th>
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100 bg-white">
              {filtered.map((inc) => (
                <tr key={inc.id} className="hover:bg-gray-50">
                  <td className="px-4 py-3 font-mono text-xs">{inc.id.slice(0, 12)}</td>
                  <td className="px-4 py-3">
                    <span className={`px-2 py-0.5 rounded text-xs font-medium ${severityColor[inc.severity] ?? 'bg-gray-100 text-gray-700'}`}>
                      {inc.severity}
                    </span>
                  </td>
                  <td className="px-4 py-3 text-gray-700">{inc.status}</td>
                  <td className="px-4 py-3 text-gray-600 text-xs">
                    {(inc.affected_resources ?? []).join(', ') || '—'}
                  </td>
                  <td className="px-4 py-3 text-gray-500 text-xs">
                    {new Date(inc.created_at).toLocaleString()}
                  </td>
                  <td className="px-4 py-3">
                    <Link to={`/incidents/${inc.id}/trace`} className="text-indigo-600 hover:underline text-xs">
                      Trace
                    </Link>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          <div className="px-4 py-2 text-xs text-gray-400 bg-gray-50">
            Showing {filtered.length} of {incidents.length} incidents
          </div>
        </div>
      )}
    </div>
  )
}
