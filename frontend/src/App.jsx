import { BrowserRouter as Router, Routes, Route, Navigate, Link } from 'react-router-dom'
import Layout from './components/Layout'
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

export default function App() {
  return (
    <Router>
      <Routes>
        <Route path="/" element={<Layout />}>
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
