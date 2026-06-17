import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useToast } from '../hooks/useToast'

export default function Login() {
  const [token, setToken] = useState(localStorage.getItem('autosre_token') ?? '')
  const [saved, setSaved] = useState(false)
  const { toast } = useToast()
  const navigate = useNavigate()

  function save() {
    if (!token.trim()) {
      toast('Token cannot be empty', 'error')
      return
    }
    localStorage.setItem('autosre_token', token.trim())
    setSaved(true)
    toast('Token saved — you are now authenticated', 'success')
    setTimeout(() => navigate('/'), 800)
  }

  function clear() {
    localStorage.removeItem('autosre_token')
    setToken('')
    setSaved(false)
    toast('Token cleared', 'info')
  }

  const hasToken = !!localStorage.getItem('autosre_token')

  return (
    <div className="max-w-md mx-auto mt-12">
      <h1 className="text-2xl font-bold mb-2">Authentication</h1>
      <p className="text-gray-500 text-sm mb-6">
        Enter a Bearer token with <code className="bg-gray-100 px-1 rounded">viewer</code>,{' '}
        <code className="bg-gray-100 px-1 rounded">operator</code>, or{' '}
        <code className="bg-gray-100 px-1 rounded">admin</code> role claim.
        Tokens are stored in <code className="bg-gray-100 px-1 rounded">localStorage</code> only.
      </p>

      {hasToken && !saved && (
        <div className="mb-4 p-3 bg-green-50 border border-green-200 rounded text-sm text-green-800">
          A token is currently stored. You can replace it below or clear it.
        </div>
      )}

      <div className="space-y-3">
        <label className="block text-sm font-medium text-gray-700">
          Bearer Token
          <textarea
            className="mt-1 w-full border border-gray-300 rounded px-3 py-2 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-indigo-400 resize-none"
            rows={4}
            placeholder="eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9..."
            value={token}
            onChange={(e) => { setToken(e.target.value); setSaved(false) }}
          />
        </label>
        <div className="flex gap-2">
          <button
            onClick={save}
            className="flex-1 bg-indigo-600 text-white rounded py-2 text-sm font-medium hover:bg-indigo-700"
          >
            Save Token
          </button>
          {hasToken && (
            <button
              onClick={clear}
              className="px-4 bg-gray-100 text-gray-700 rounded py-2 text-sm font-medium hover:bg-gray-200"
            >
              Clear
            </button>
          )}
        </div>
      </div>

      <div className="mt-6 p-3 bg-yellow-50 border border-yellow-200 rounded text-xs text-yellow-800">
        <strong>Dev mode:</strong> When OIDC is disabled the agent accepts any token and grants viewer access.
        Admin operations require a valid token with the admin role claim.
      </div>
    </div>
  )
}
