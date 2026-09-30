import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Download,
  FileCheck2,
  Link2,
  Trash2,
  Wand2,
  ArrowRight,
  Clock,
  ListChecks,
} from 'lucide-react'
import { api } from '../api/client'
import { useExamOptions } from '../api/hooks'
import GeneratePaperDialog from '../components/GeneratePaperDialog'
import {
  Badge,
  Button,
  Card,
  EmptyState,
  ErrorNote,
  Field,
  IconButton,
  Loading,
  Modal,
  Note,
  PageHeader,
  Pagination,
  Select,
  StatusPill,
  formatDate,
} from '../components/ui'
import { QualityBadge } from '../components/Quality'

export default function PapersList() {
  const queryClient = useQueryClient()
  const { data: exams = [] } = useExamOptions()

  const [page, setPage] = useState(1)
  const [filters, setFilters] = useState({ exam_id: '', status: 'all', type: 'all' })
  const [pickingExam, setPickingExam] = useState(false)
  const [generateFor, setGenerateFor] = useState(null)

  const { data, isLoading, error } = useQuery({
    queryKey: ['papers', { ...filters, page }],
    queryFn: () => api.getPapers({ ...filters, page, page_size: 24 }),
  })

  const papers = data?.data ?? []
  const meta = data?.meta

  const remove = useMutation({
    mutationFn: (id) => api.deletePaper(id),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['papers'] }),
  })

  const readyExams = exams.filter((exam) => exam.summary?.has_pattern)

  return (
    <div>
      <PageHeader
        title="Test papers"
        subtitle="Papers are assembled from an exam's pattern, drawing on its own questions plus anything it borrows."
        actions={
          <Button icon={Wand2} onClick={() => setPickingExam(true)} disabled={exams.length === 0}>
            Generate a paper
          </Button>
        }
      />

      <Card className="mb-5">
        <div className="grid gap-3 sm:grid-cols-3">
          <Field label="Exam">
            <Select
              value={filters.exam_id}
              onChange={(event) => {
                setFilters({ ...filters, exam_id: event.target.value })
                setPage(1)
              }}
            >
              <option value="">Every exam</option>
              {exams.map((exam) => (
                <option key={exam.id} value={exam.id}>
                  {exam.name}
                </option>
              ))}
            </Select>
          </Field>
          <Field label="Status">
            <Select
              value={filters.status}
              onChange={(event) => {
                setFilters({ ...filters, status: event.target.value })
                setPage(1)
              }}
            >
              <option value="all">Any status</option>
              <option value="draft">Draft</option>
              <option value="published">Published</option>
            </Select>
          </Field>
          <Field label="Flavour">
            <Select
              value={filters.type}
              onChange={(event) => {
                setFilters({ ...filters, type: event.target.value })
                setPage(1)
              }}
            >
              <option value="all">Any flavour</option>
              <option value="balanced">Balanced</option>
              <option value="easy">Easy</option>
              <option value="tough">Tough</option>
              <option value="previous_style">Previous-paper style</option>
              <option value="recent_trend">Recent trend</option>
              <option value="speed">Speed</option>
              <option value="mixed">Mixed</option>
            </Select>
          </Field>
        </div>
      </Card>

      <ErrorNote error={error} className="mb-4" />

      {isLoading ? (
        <Loading label="Loading papers" />
      ) : papers.length === 0 ? (
        <EmptyState
          icon={FileCheck2}
          title="No papers yet"
          message={
            readyExams.length === 0
              ? 'An exam needs an active pattern before a paper can be built from it. Set one on the exam first.'
              : 'Generate one from an exam that has a pattern and some answered questions.'
          }
          action={
            readyExams.length > 0 && (
              <Button icon={Wand2} onClick={() => setPickingExam(true)}>
                Generate a paper
              </Button>
            )
          }
        />
      ) : (
        <>
          <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
            {papers.map((paper) => (
              <PaperCard
                key={paper.id}
                paper={paper}
                onDelete={() => {
                  if (window.confirm(`Delete "${paper.title}"? The questions stay in the warehouse.`)) {
                    remove.mutate(paper.id)
                  }
                }}
              />
            ))}
          </div>
          <Pagination meta={meta} page={page} onChange={setPage} />
        </>
      )}

      {pickingExam && (
        <Modal title="Which exam?" size="sm" onClose={() => setPickingExam(false)}>
          <div className="space-y-3">
            {readyExams.length === 0 ? (
              <Note tone="amber">
                <p>
                  No exam has an active pattern yet. Open an exam and either enter its pattern or
                  derive one from a past paper.
                </p>
              </Note>
            ) : (
              readyExams.map((exam) => (
                <button
                  key={exam.id}
                  type="button"
                  onClick={() => {
                    setPickingExam(false)
                    setGenerateFor(exam)
                  }}
                  className="flex w-full items-center justify-between gap-3 rounded-lg border border-gray-200 p-3 text-left hover:border-primary-400 hover:bg-primary-50"
                >
                  <div className="min-w-0">
                    <p className="truncate font-medium text-gray-900">{exam.name}</p>
                    <p className="text-xs text-gray-500">
                      {exam.summary?.pattern_total_questions} question pattern ·{' '}
                      {exam.summary?.answered_questions ?? 0} answered questions available
                    </p>
                  </div>
                  <ArrowRight className="h-4 w-4 flex-shrink-0 text-gray-400" />
                </button>
              ))
            )}
            {exams.length > readyExams.length && (
              <p className="text-xs text-gray-500">
                {exams.length - readyExams.length} exam
                {exams.length - readyExams.length === 1 ? '' : 's'} hidden because they have no
                active pattern.
              </p>
            )}
          </div>
        </Modal>
      )}

      {generateFor && (
        <GeneratePaperDialogLoader exam={generateFor} onClose={() => setGenerateFor(null)} />
      )}
    </div>
  )
}

/** GeneratePaperDialogLoader fetches the exam's active pattern before opening. */
function GeneratePaperDialogLoader({ exam, onClose }) {
  const { data, isLoading } = useQuery({
    queryKey: ['exam', String(exam.id)],
    queryFn: () => api.getExam(exam.id),
    select: (payload) => payload?.data,
  })

  if (isLoading) {
    return (
      <Modal title={`Generate a paper for ${exam.name}`} size="md" onClose={onClose}>
        <Loading label="Loading the exam pattern" className="py-10" />
      </Modal>
    )
  }

  const full = data?.exam || exam
  const pattern = (full.patterns || []).find((p) => p.is_active)
  return <GeneratePaperDialog exam={full} pattern={pattern} onClose={onClose} />
}

function PaperCard({ paper, onDelete }) {
  let analytics = null
  try {
    analytics = typeof paper.analytics === 'string' ? JSON.parse(paper.analytics) : paper.analytics
  } catch {
    analytics = null
  }
  const shortfalls = analytics?.shortfalls?.length || 0

  return (
    <Card className="flex flex-col">
      <div className="mb-3 flex items-start justify-between gap-2">
        <div className="min-w-0">
          <Link
            to={`/papers/${paper.id}`}
            className="block truncate font-semibold text-gray-900 hover:text-primary-700"
          >
            {paper.title}
          </Link>
          <p className="truncate text-xs text-gray-500">{paper.exam?.name}</p>
        </div>
        <div className="flex flex-col items-end gap-1">
          <StatusPill status={paper.status} />
          {/* Whether the paper can be delivered is more important than whether it
              is a draft, so it is shown on the card. */}
          <QualityBadge status={paper.quality_status} score={paper.quality_score} />
        </div>
      </div>

      <div className="mb-3 grid grid-cols-3 gap-2 text-sm">
        <Metric icon={ListChecks} label="Questions" value={paper.total_questions} />
        <Metric icon={FileCheck2} label="Marks" value={paper.total_marks} />
        <Metric icon={Clock} label="Minutes" value={paper.duration_min || '—'} />
      </div>

      <div className="mb-4 flex flex-wrap gap-1.5">
        <Badge tone="gray">{String(paper.type || '').replace(/_/g, ' ')}</Badge>
        {analytics?.borrowed_count > 0 && (
          <Badge tone="blue">
            <Link2 className="h-3 w-3" />
            {analytics.borrowed_count} borrowed
          </Badge>
        )}
        {shortfalls > 0 && <Badge tone="amber">{shortfalls} short section{shortfalls === 1 ? '' : 's'}</Badge>}
      </div>

      <div className="mt-auto flex items-center justify-between border-t border-gray-100 pt-3">
        <span className="text-xs text-gray-500">{formatDate(paper.created_at)}</span>
        <div className="flex items-center gap-0.5">
          <a
            href={api.exportUrl(paper.id, { draft: paper.quality_status !== 'pass' })}
            download
          >
            <IconButton
              icon={Download}
              title={
                paper.quality_status === 'pass'
                  ? 'Export as markdown'
                  : 'Export as a draft, stamped as not cleared for delivery'
              }
            />
          </a>
          <IconButton icon={Trash2} tone="red" title="Delete paper" onClick={onDelete} />
          <Link to={`/papers/${paper.id}`}>
            <IconButton icon={ArrowRight} tone="primary" title="Open paper" />
          </Link>
        </div>
      </div>
    </Card>
  )
}

function Metric({ icon: Icon, label, value }) {
  return (
    <div className="flex items-center gap-1.5">
      <Icon className="h-3.5 w-3.5 flex-shrink-0 text-gray-400" />
      <div className="min-w-0">
        <p className="truncate text-xs text-gray-500">{label}</p>
        <p className="font-semibold tabular-nums text-gray-900">{value}</p>
      </div>
    </div>
  )
}
