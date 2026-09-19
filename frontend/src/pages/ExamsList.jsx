import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import {
  GraduationCap,
  Plus,
  Search,
  FileStack,
  HelpCircle,
  Link2,
  FileCheck2,
  ListOrdered,
  ArrowRight,
} from 'lucide-react'
import { api } from '../api/client'
import ExamWizard from '../components/ExamWizard'
import {
  Badge,
  Button,
  Card,
  EmptyState,
  ErrorNote,
  Input,
  Loading,
  PageHeader,
  Pagination,
  StatusPill,
} from '../components/ui'

export default function ExamsList() {
  const [search, setSearch] = useState('')
  const [page, setPage] = useState(1)
  const [wizardOpen, setWizardOpen] = useState(false)

  const { data, isLoading, error } = useQuery({
    queryKey: ['exams', { search, page }],
    queryFn: () => api.getExams({ search, page, page_size: 24 }),
  })

  const exams = data?.data ?? []
  const meta = data?.meta

  return (
    <div>
      <PageHeader
        title="Exams"
        subtitle="Each exam defines its own pattern and decides which other exams it may borrow content from. The engine ships with none."
        actions={
          <Button icon={Plus} onClick={() => setWizardOpen(true)}>
            New exam
          </Button>
        }
      />

      <div className="mb-5 max-w-sm">
        <div className="relative">
          <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-gray-400" />
          <Input
            className="pl-9"
            placeholder="Search exams…"
            value={search}
            onChange={(event) => {
              setSearch(event.target.value)
              setPage(1)
            }}
          />
        </div>
      </div>

      <ErrorNote error={error} className="mb-4" />

      {isLoading ? (
        <Loading label="Loading exams" />
      ) : exams.length === 0 ? (
        <EmptyState
          icon={GraduationCap}
          title={search ? 'No exams match that search' : 'No exams yet'}
          message={
            search
              ? 'Try a different name or code.'
              : 'Create your first exam. You can either type its pattern in or drop in a past paper and let the engine work it out.'
          }
          action={
            !search && (
              <Button icon={Plus} onClick={() => setWizardOpen(true)}>
                Create an exam
              </Button>
            )
          }
        />
      ) : (
        <>
          <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
            {exams.map((exam) => (
              <ExamCard key={exam.id} exam={exam} />
            ))}
          </div>
          <Pagination meta={meta} page={page} onChange={setPage} />
        </>
      )}

      {wizardOpen && <ExamWizard onClose={() => setWizardOpen(false)} />}
    </div>
  )
}

function ExamCard({ exam }) {
  const summary = exam.summary || {}
  const ready = summary.has_pattern && summary.questions > 0

  return (
    <Card className="flex flex-col">
      <div className="mb-3 flex items-start justify-between gap-3">
        <div className="min-w-0">
          <Link
            to={`/exams/${exam.id}`}
            className="block truncate font-semibold text-gray-900 hover:text-primary-700"
          >
            {exam.name}
          </Link>
          <code className="text-xs text-gray-500">{exam.code}</code>
        </div>
        <StatusPill status={exam.status} />
      </div>

      {exam.description && (
        <p className="mb-3 line-clamp-2 text-xs text-gray-500">{exam.description}</p>
      )}

      <dl className="mb-4 grid grid-cols-2 gap-2 text-sm">
        <Metric icon={HelpCircle} label="Questions" value={summary.questions ?? 0} />
        <Metric icon={FileStack} label="Documents" value={summary.documents ?? 0} />
        <Metric icon={Link2} label="Borrowed" value={summary.borrowed_questions ?? 0} />
        <Metric icon={FileCheck2} label="Papers" value={summary.papers ?? 0} />
      </dl>

      <div className="mb-4 flex flex-wrap gap-1.5">
        {summary.has_pattern ? (
          <Badge tone="green">
            <ListOrdered className="h-3 w-3" />
            Pattern: {summary.pattern_total_questions} questions
          </Badge>
        ) : (
          <Badge tone="amber">No pattern yet</Badge>
        )}
        {summary.answered_questions > 0 && (
          <Badge tone="blue">{summary.answered_questions} answered</Badge>
        )}
        {summary.associations > 0 && (
          <Badge tone="purple">
            {summary.associations} association{summary.associations === 1 ? '' : 's'}
          </Badge>
        )}
      </div>

      <div className="mt-auto flex items-center justify-between border-t border-gray-100 pt-3">
        <span className="text-xs text-gray-500">
          {ready ? 'Ready to generate papers' : 'Needs a pattern and content'}
        </span>
        <Link to={`/exams/${exam.id}`}>
          <Button variant="ghost" icon={ArrowRight} className="px-2">
            Open
          </Button>
        </Link>
      </div>
    </Card>
  )
}

function Metric({ icon: Icon, label, value }) {
  return (
    <div className="flex items-center gap-2">
      <Icon className="h-3.5 w-3.5 flex-shrink-0 text-gray-400" />
      <div className="min-w-0">
        <dt className="truncate text-xs text-gray-500">{label}</dt>
        <dd className="font-semibold tabular-nums text-gray-900">{value}</dd>
      </div>
    </div>
  )
}
