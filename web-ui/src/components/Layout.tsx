import { Link, useLocation } from 'react-router-dom'

const nav = [
  { to: '/', label: 'Incidents' },
  { to: '/approvals', label: 'Approvals' },
  { to: '/audit', label: 'Audit Log' },
  { to: '/analytics', label: 'Analytics' },
  { to: '/stats', label: 'Stats' },
  { to: '/integrations', label: 'Integrations' },
  { to: '/status', label: 'System' },
]

export default function Layout({ children }: { children: React.ReactNode }) {
  const { pathname } = useLocation()
  const hasToken = !!localStorage.getItem('autosre_token')

  return (
    <div className="min-h-screen flex flex-col bg-gray-50">
      <header className="bg-indigo-700 text-white shadow sticky top-0 z-40">
        <div className="max-w-7xl mx-auto px-4 py-3 flex items-center gap-6">
          <Link to="/" className="font-bold text-lg tracking-tight shrink-0">
            AutoSRE
          </Link>
          <nav className="flex gap-1 flex-wrap">
            {nav.map(({ to, label }) => {
              const active = to === '/' ? pathname === '/' : pathname.startsWith(to)
              return (
                <Link
                  key={to}
                  to={to}
                  className={`text-sm font-medium px-2.5 py-1 rounded transition-colors ${
                    active
                      ? 'bg-indigo-900 text-white'
                      : 'text-indigo-200 hover:text-white hover:bg-indigo-600'
                  }`}
                >
                  {label}
                </Link>
              )
            })}
          </nav>
          <div className="ml-auto shrink-0">
            <Link
              to="/login"
              className={`text-sm px-3 py-1 rounded border transition-colors ${
                hasToken
                  ? 'border-green-400 text-green-300 hover:bg-indigo-600'
                  : 'border-yellow-400 text-yellow-300 hover:bg-indigo-600'
              }`}
            >
              {hasToken ? 'Authenticated' : 'Login'}
            </Link>
          </div>
        </div>
      </header>

      <main className="flex-1 max-w-7xl mx-auto w-full px-4 py-6">{children}</main>

      <footer className="border-t border-gray-200 bg-white text-center py-3 text-xs text-gray-400">
        AutoSRE — Autonomous GitOps & Auto-Remediation Platform
      </footer>
    </div>
  )
}
