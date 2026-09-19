import { Outlet, NavLink } from 'react-router-dom'
import {
  LayoutDashboard,
  GraduationCap,
  FileStack,
  Warehouse,
  FileCheck2,
  FolderTree,
  Activity,
  AlertCircle,
  CheckCircle2,
  ShieldAlert,
  Sparkles,
} from 'lucide-react'
import { useActiveJobs, useConverterHealth, useModelStatus, useReviewSummary } from '../api/hooks'
import { Badge, ProgressBar } from './ui'

const NAV = [
  { to: '/dashboard', icon: LayoutDashboard, label: 'Dashboard', hint: 'What the engine holds' },
  { to: '/exams', icon: GraduationCap, label: 'Exams', hint: 'Patterns and associations' },
  { to: '/documents', icon: FileStack, label: 'Documents', hint: 'Upload and process' },
  { to: '/warehouse', icon: Warehouse, label: 'Warehouse', hint: 'Every question' },
  // The review count sits in the navigation because a backlog nobody sees is a
  // backlog nobody clears, and unresolved content cannot be used.
  { to: '/review', icon: ShieldAlert, label: 'Review', hint: 'Held for a decision', counter: 'review' },
  { to: '/papers', icon: FileCheck2, label: 'Papers', hint: 'Generated output' },
  { to: '/taxonomy', icon: FolderTree, label: 'Taxonomy', hint: 'Subjects and aliases' },
]

export default function Layout() {
  const { data: activeJobs = [] } = useActiveJobs()
  const { data: health, isError: healthError } = useConverterHealth()
  const { data: reviewSummary } = useReviewSummary()
  const { data: model } = useModelStatus()

  const converterReady = !healthError && health?.available && health?.models_ready !== false
  const byStatus = reviewSummary?.questions_by_status || {}
  const waiting = (byStatus.review || 0) + (byStatus.failed || 0) + (byStatus.unchecked || 0)
  const counters = { review: waiting }

  return (
    <div className="flex min-h-screen bg-gray-50">
      <aside className="fixed flex h-full w-64 flex-col border-r border-gray-200 bg-white">
        <div className="border-b border-gray-200 px-5 py-5">
          <h1 className="text-lg font-bold text-primary-700">Mock Creator</h1>
          <p className="mt-0.5 text-xs text-gray-500">Exam content engine</p>
        </div>

        <nav className="flex-1 overflow-y-auto py-3">
          {NAV.map(({ to, icon: Icon, label, hint, counter }) => {
            const count = counter ? counters[counter] : 0
            return (
              <NavLink
                key={to}
                to={to}
                className={({ isActive }) =>
                  `flex items-start gap-3 px-5 py-2.5 transition-colors ${
                    isActive
                      ? 'border-r-2 border-primary-600 bg-primary-50 text-primary-700'
                      : 'text-gray-700 hover:bg-gray-50'
                  }`
                }
              >
                <Icon className="mt-0.5 h-4 w-4 flex-shrink-0" />
                <span className="min-w-0 flex-1">
                  <span className="flex items-center justify-between gap-2">
                    <span className="text-sm font-medium">{label}</span>
                    {count > 0 && (
                      <span className="rounded-full bg-amber-100 px-1.5 text-xs font-semibold text-amber-800">
                        {count > 999 ? '999+' : count}
                      </span>
                    )}
                  </span>
                  <span className="block truncate text-xs text-gray-400">{hint}</span>
                </span>
              </NavLink>
            )
          })}
        </nav>

        {/* The work feed lives in the chrome so progress is visible from any page. */}
        <div className="border-t border-gray-200 p-4">
          {activeJobs.length > 0 ? (
            <div className="space-y-2">
              <div className="flex items-center gap-2 text-xs font-medium text-gray-700">
                <Activity className="h-3.5 w-3.5 animate-pulse text-primary-600" />
                {activeJobs.length} job{activeJobs.length === 1 ? '' : 's'} running
              </div>
              {activeJobs.slice(0, 3).map((job) => (
                <div key={job.id} className="space-y-1">
                  <p className="truncate text-xs text-gray-500" title={job.stage}>
                    {job.document?.title || job.exam?.name || job.type}
                  </p>
                  <ProgressBar value={job.progress} />
                </div>
              ))}
            </div>
          ) : (
            <div className="flex items-center gap-2 text-xs text-gray-500">
              <Activity className="h-3.5 w-3.5" />
              No jobs running
            </div>
          )}

          <div className="mt-3 flex flex-wrap items-center gap-2 border-t border-gray-100 pt-3">
            {converterReady ? (
              <Badge tone="green">
                <CheckCircle2 className="h-3 w-3" /> Converter ready
              </Badge>
            ) : (
              <Badge tone="amber" title={health?.error || 'The converter is starting or unreachable'}>
                <AlertCircle className="h-3 w-3" />
                {healthError || health?.available === false ? 'Converter offline' : 'Converter warming up'}
              </Badge>
            )}
            {/* Whether model review is on is stated permanently. A paper reviewed
                by the rules alone must not look like one a model also read. */}
            {model?.configured ? (
              model.reachable === false ? (
                <Badge tone="red" title={model.error}>
                  <AlertCircle className="h-3 w-3" /> Model unreachable
                </Badge>
              ) : (
                <Badge tone="purple" title={`${model.model} at ${model.base_url}`}>
                  <Sparkles className="h-3 w-3" /> Model review on
                </Badge>
              )
            ) : (
              <Badge
                tone="gray"
                title="Deterministic checks only. Judgement-based checks are reported as not run, never as passes."
              >
                <Sparkles className="h-3 w-3" /> Checks only
              </Badge>
            )}
          </div>
        </div>
      </aside>

      <main className="ml-64 flex-1 p-8">
        <Outlet />
      </main>
    </div>
  )
}
