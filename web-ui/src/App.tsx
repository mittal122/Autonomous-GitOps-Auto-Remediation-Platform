import { BrowserRouter, Routes, Route } from 'react-router-dom'
import { ToastProvider } from './components/Toast'
import { ErrorBoundary } from './components/ErrorBoundary'
import Layout from './components/Layout'
import Dashboard from './pages/Dashboard'
import IncidentTrace from './pages/IncidentTrace'
import Approvals from './pages/Approvals'
import Stats from './pages/Stats'
import Status from './pages/Status'
import AuditLog from './pages/AuditLog'
import Analytics from './pages/Analytics'
import Login from './pages/Login'

export default function App() {
  return (
    <ErrorBoundary>
    <ToastProvider>
      <BrowserRouter>
        <Layout>
          <ErrorBoundary>
          <Routes>
            <Route path="/" element={<Dashboard />} />
            <Route path="/incidents/:id/trace" element={<IncidentTrace />} />
            <Route path="/approvals" element={<Approvals />} />
            <Route path="/stats" element={<Stats />} />
            <Route path="/status" element={<Status />} />
            <Route path="/audit" element={<AuditLog />} />
            <Route path="/analytics" element={<Analytics />} />
            <Route path="/login" element={<Login />} />
          </Routes>
          </ErrorBoundary>
        </Layout>
      </BrowserRouter>
    </ToastProvider>
    </ErrorBoundary>
  )
}
