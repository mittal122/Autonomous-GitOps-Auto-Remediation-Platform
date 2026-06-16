import { BrowserRouter, Routes, Route } from 'react-router-dom'
import Layout from './components/Layout'
import Dashboard from './pages/Dashboard'
import IncidentTrace from './pages/IncidentTrace'
import Approvals from './pages/Approvals'
import Stats from './pages/Stats'
import Status from './pages/Status'

export default function App() {
  return (
    <BrowserRouter>
      <Layout>
        <Routes>
          <Route path="/" element={<Dashboard />} />
          <Route path="/incidents/:id/trace" element={<IncidentTrace />} />
          <Route path="/approvals" element={<Approvals />} />
          <Route path="/stats" element={<Stats />} />
          <Route path="/status" element={<Status />} />
        </Routes>
      </Layout>
    </BrowserRouter>
  )
}
