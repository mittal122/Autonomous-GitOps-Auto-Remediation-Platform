import { Link, useLocation } from 'react-router-dom'

const nav = [
  { to: '/', label: 'Dashboard' },
  { to: '/approvals', label: 'Approvals' },
  { to: '/stats', label: 'Stats' },
  { to: '/status', label: 'System Status' },
]

export default function Layout({ children }: { children: React.ReactNode }) {
  const { pathname } = useLocation()
  return (
    <div className="min-h-screen flex flex-col">
      <header className="bg-indigo-700 text-white shadow">
        <div className="max-w-7xl mx-auto px-4 py-3 flex items-center gap-8">
          <span className="font-bold text-lg tracking-tight">AutoSRE</span>
          <nav className="flex gap-4">
            {nav.map(({ to, label }) => (
              <Link
                key={to}
                to={to}
                className={`text-sm font-medium px-2 py-1 rounded transition-colors ${
                  pathname === to
                    ? 'bg-indigo-900 text-white'
                    : 'text-indigo-200 hover:text-white hover:bg-indigo-600'
                }`}
              >
                {label}
              </Link>
            ))}
          </nav>
        </div>
      </header>
      <main className="flex-1 max-w-7xl mx-auto w-full px-4 py-6">{children}</main>
    </div>
  )
}
