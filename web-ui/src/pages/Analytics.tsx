import { useState } from 'react'
import { api, type StatsResponse, type Incident } from '../api/client'
import { SkeletonStats } from '../components/Skeleton'

interface AnalyticsData {
  stats: StatsResponse
  incidents: Incident[]
}

function BarChart({ data }: { data: { label: string; value: number; color: string }[] }) {
  const max = Math.max(...data.map((d) => d.value), 1)
  return (
    <div className="space-y-2">
      {data.map(({ label, value, color }) => (
        <div key={label} className="flex items-center gap-3">
          <span className="text-xs text-gray-600 w-40 truncate shrink-0">{label}</span>
          <div className="flex-1 bg-gray-100 rounded-full h-4 overflow-hidden">
            <div
              className={`h-4 rounded-full transition-all ${color}`}
              style={{ width: `${(value / max) * 100}%` }}
            />
          </div>
          <span className="text-xs font-semibold text-gray-700 w-12 text-right">{value}%</span>
        </div>
      ))}
    </div>
  )
}

function StatCard({ label, value, sub }: { label: string; value: string | number; sub?: string }) {
  return (
    <div className="border border-gray-200 rounded-lg p-4 bg-white shadow-sm">
      <p className="text-xs font-semibold text-gray-500 uppercase tracking-wide mb-1">{label}</p>
      <p className="text-2xl font-bold text-gray-900">{value}</p>
      {sub && <p className="text-xs text-gray-500 mt-1">{sub}</p>}
    </div>
  )
}

export default function Analytics() {
  const [data, setData] = useState<AnalyticsData | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useState(() => {
    Promise.all([api.getStats(), api.listIncidents()])
      .then(([stats, incidents]) => setData({ stats, incidents: incidents ?? [] }))
      .catch((e: Error) => setError(e.message))
      .finally(() => setLoading(false))
  })

  if (loading) return <><h1 className="text-2xl font-bold mb-4">Analytics</h1><SkeletonStats /></>
  if (error) return <p className="text-red-600 text-sm">Error: {error}</p>
  if (!data) return null

  const { stats, incidents } = data

  // MTTR: average time from opened_at to updated_at for resolved incidents
  const resolved = incidents.filter((i) => i.status === 'resolved')
  const mttrMs = resolved.length > 0
    ? resolved.reduce((sum, i) => sum + (new Date(i.updated_at).getTime() - new Date(i.opened_at).getTime()), 0) / resolved.length
    : null
  const mttrMin = mttrMs != null ? (mttrMs / 60000).toFixed(1) : '—'

  const rows = Object.entries(stats.by_failure_mode_action ?? {})
  const totalAttempts = rows.reduce((s, [, v]) => s + v.attempts, 0)
  const totalSuccesses = rows.reduce((s, [, v]) => s + v.successes, 0)
  const overallRate = totalAttempts > 0 ? ((totalSuccesses / totalAttempts) * 100).toFixed(0) : '—'

  const barData = rows
    .sort((a, b) => b[1].attempts - a[1].attempts)
    .slice(0, 8)
    .map(([key, v]) => ({
      label: key,
      value: Math.round(v.success_rate * 100),
      color: v.success_rate >= 0.8 ? 'bg-green-500' : v.success_rate >= 0.5 ? 'bg-yellow-500' : 'bg-red-500',
    }))

  const bySeverity = incidents.reduce<Record<string, number>>((acc, i) => {
    acc[i.severity] = (acc[i.severity] ?? 0) + 1
    return acc
  }, {})

  return (
    <div>
      <h1 className="text-2xl font-bold mb-4">Analytics</h1>

      <div className="grid grid-cols-2 sm:grid-cols-4 gap-4 mb-8">
        <StatCard label="Total Incidents" value={incidents.length} />
        <StatCard label="Resolved" value={resolved.length} sub={`of ${incidents.length}`} />
        <StatCard label="Avg MTTR" value={mttrMin === '—' ? '—' : `${mttrMin}m`} sub="resolved incidents" />
        <StatCard label="Overall Success" value={overallRate === '—' ? '—' : `${overallRate}%`} sub={`${totalAttempts} attempts`} />
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-8">
        {barData.length > 0 && (
          <div>
            <h2 className="text-base font-semibold text-gray-800 mb-3">Success Rate by Failure Mode</h2>
            <BarChart data={barData} />
          </div>
        )}

        {Object.keys(bySeverity).length > 0 && (
          <div>
            <h2 className="text-base font-semibold text-gray-800 mb-3">Incidents by Severity</h2>
            <div className="space-y-2">
              {Object.entries(bySeverity)
                .sort((a, b) => b[1] - a[1])
                .map(([sev, count]) => {
                  const color = sev === 'critical' ? 'bg-red-500'
                    : sev === 'high' ? 'bg-orange-500'
                    : sev === 'medium' ? 'bg-yellow-500' : 'bg-green-500'
                  return (
                    <div key={sev} className="flex items-center gap-3">
                      <span className="text-xs text-gray-600 w-20 capitalize">{sev}</span>
                      <div className="flex-1 bg-gray-100 rounded-full h-4 overflow-hidden">
                        <div
                          className={`h-4 rounded-full ${color}`}
                          style={{ width: `${(count / incidents.length) * 100}%` }}
                        />
                      </div>
                      <span className="text-xs font-semibold text-gray-700 w-8 text-right">{count}</span>
                    </div>
                  )
                })}
            </div>
          </div>
        )}
      </div>

      {stats.note && (
        <div className="mt-6 p-3 bg-yellow-50 border border-yellow-200 rounded text-sm text-yellow-800">
          {stats.note}
        </div>
      )}
    </div>
  )
}
