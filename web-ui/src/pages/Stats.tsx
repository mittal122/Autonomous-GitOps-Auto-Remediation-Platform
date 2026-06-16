import { useEffect, useState } from 'react'
import { api, type StatsResponse } from '../api/client'

export default function Stats() {
  const [stats, setStats] = useState<StatsResponse | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    api.getStats()
      .then(setStats)
      .catch((e: Error) => setError(e.message))
      .finally(() => setLoading(false))
  }, [])

  if (loading) return <p className="text-gray-500">Loading stats...</p>
  if (error) return <p className="text-red-600">Error: {error}</p>
  if (!stats) return null

  const rows = Object.entries(stats.by_failure_mode_action ?? {})

  return (
    <div>
      <h1 className="text-2xl font-bold mb-1">Remediation Stats</h1>
      <p className="text-gray-500 text-sm mb-4">
        Advisory only — success rates are observed outcomes, not prescriptive thresholds.
        Total recorded outcomes: <strong>{stats.total_outcomes}</strong>
      </p>
      {stats.note && (
        <div className="mb-4 p-3 bg-yellow-50 border border-yellow-200 rounded text-sm text-yellow-800">
          {stats.note}
        </div>
      )}
      {rows.length === 0 ? (
        <p className="text-gray-500">No outcome data yet.</p>
      ) : (
        <div className="overflow-x-auto rounded-lg border border-gray-200">
          <table className="min-w-full divide-y divide-gray-200 text-sm">
            <thead className="bg-gray-50">
              <tr>
                <th className="px-4 py-3 text-left font-semibold text-gray-600">Failure Mode / Action</th>
                <th className="px-4 py-3 text-right font-semibold text-gray-600">Attempts</th>
                <th className="px-4 py-3 text-right font-semibold text-gray-600">Successes</th>
                <th className="px-4 py-3 text-right font-semibold text-gray-600">Success Rate</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100 bg-white">
              {rows.map(([key, v]) => {
                const pct = (v.success_rate * 100).toFixed(0)
                const color =
                  v.success_rate >= 0.8
                    ? 'text-green-700'
                    : v.success_rate >= 0.5
                    ? 'text-yellow-700'
                    : 'text-red-700'
                return (
                  <tr key={key} className="hover:bg-gray-50">
                    <td className="px-4 py-3 font-mono text-xs">{key}</td>
                    <td className="px-4 py-3 text-right">{v.attempts}</td>
                    <td className="px-4 py-3 text-right">{v.successes}</td>
                    <td className={`px-4 py-3 text-right font-semibold ${color}`}>{pct}%</td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
