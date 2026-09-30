import { useState, useEffect } from 'react'
import { BrowserRouter as Router, Routes, Route, Navigate, Link } from 'react-router-dom'
import Layout from './components/Layout'
import Login from './pages/Login'
import Dashboard from './pages/Dashboard'
import ExamsList from './pages/ExamsList'
import ExamDetail from './pages/ExamDetail'
import Documents from './pages/Documents'
import WarehousePage from './pages/Warehouse'
import Review from './pages/Review'
import PapersList from './pages/PapersList'
import PaperDetail from './pages/PaperDetail'
import Taxonomy from './pages/Taxonomy'
import { Button, EmptyState } from './components/ui'
import { Compass } from 'lucide-react'

function NotFound() {
  return (
    <EmptyState
      icon={Compass}
      title="Nothing here"
      message="That page does not exist."
      action={
        <Link to="/dashboard">
          <Button>Back to dashboard</Button>
        </Link>
      }
    />
  )
}

// Protected Route wrapper
function ProtectedRoute({ children, isAuthenticated }) {
  if (!isAuthenticated) {
    return <Navigate to="/login" replace />
  }
  return children
}

export default function App() {
  const [isAuthenticated, setIsAuthenticated] = useState(false)
  const [isChecking, setIsChecking] = useState(true)

  useEffect(() => {
    // Check if user is already logged in
    const authStatus = localStorage.getItem('isAuthenticated')
    setIsAuthenticated(authStatus === 'true')
    setIsChecking(false)
  }, [])

  const handleLogin = () => {
    setIsAuthenticated(true)
  }

  const handleLogout = () => {
    localStorage.removeItem('isAuthenticated')
    setIsAuthenticated(false)
  }

  // Show nothing while checking auth status
  if (isChecking) {
    return (
      <div className="min-h-screen flex items-center justify-center bg-slate-900">
        <div className="w-8 h-8 border-2 border-blue-500/30 border-t-blue-500 rounded-full animate-spin" />
      </div>
    )
  }

  return (
    <Router>
      <Routes>
        {/* Login Route */}
        <Route 
          path="/login" 
          element={
            isAuthenticated ? 
              <Navigate to="/dashboard" replace /> : 
              <Login onLogin={handleLogin} />
          } 
        />

        {/* Protected Routes */}
        <Route 
          path="/" 
          element={
            <ProtectedRoute isAuthenticated={isAuthenticated}>
              <Layout onLogout={handleLogout} />
            </ProtectedRoute>
          }
        >
          <Route index element={<Navigate to="/dashboard" replace />} />
          <Route path="dashboard" element={<Dashboard />} />

          <Route path="exams" element={<ExamsList />} />
          <Route path="exams/:examId" element={<ExamDetail />} />

          <Route path="documents" element={<Documents />} />
          <Route path="warehouse" element={<WarehousePage />} />
          <Route path="review" element={<Review />} />

          <Route path="papers" element={<PapersList />} />
          <Route path="papers/:paperId" element={<PaperDetail />} />

          <Route path="taxonomy" element={<Taxonomy />} />
          <Route path="*" element={<NotFound />} />
        </Route>
      </Routes>
    </Router>
  )
}
