import { useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  ArrowLeft,
  CheckCircle2,
  FileSearch,
  FileStack,
  HelpCircle,
  ListOrdered,
  Pencil,
  Plus,
  Sparkles,
  Trash2,
  Wand2,
} from 'lucide-react'
import { api } from '../api/client'
import { useJobTracker } from '../api/hooks'
import AssociationEditor from '../components/AssociationEditor'
import Dropzone from '../components/Dropzone'
import PatternEditor from '../components/PatternEditor'
import GeneratePaperDialog from '../components/GeneratePaperDialog'
import {
  Badge,
  Button,
  Card,
  EmptyState,
  ErrorNote,
  IconButton,
  Loading,
  Modal,
  Note,
  PageHeader,
  ProgressBar,
  SectionTitle,
  Stat,
  StatusPill,
  formatDate,
} from '../components/ui'

const TABS = [
  { id: 'pattern', label: 'Pattern' },
  { id: 'material', label: 'Material' },
  { id: 'associations', label: 'Associations' },
]

export default function ExamDetail() {
  const { examId } = useParams()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [tab, setTab] = useState('pattern')
  const [generating, setGenerating] = useState(false)

  const { data, isLoading, error } = useQuery({
    queryKey: ['exam', examId],
    queryFn: () => api.getExam(examId),
    select: (payload) => payload?.data,
  })

  const remove = useMutation({
    mutationFn: () => api.deleteExam(examId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['exams'] })
      navigate('/exams')
    },
  })

  if (isLoading) return <Loading label="Loading exam" />
  if (error) return <ErrorNote error={error} />
  if (!data?.exam) return <ErrorNote error={{ message: 'Exam not found' }} />

  const exam = data.exam
  const summary = data.summary || {}
  const activePattern = (exam.patterns || []).find((p) => p.is_active)

  return (
    <div>
      <Link to="/exams" className="mb-4 inline-flex items-center gap-1 text-sm text-gray-500 hover:text-gray-700">
        <ArrowLeft className="h-4 w-4" /> All exams
      </Link>

      <PageHeader
        title={exam.name}
        subtitle={exam.description || `Code: ${exam.code}`}
        actions={
          <>
            <Button
              icon={Wand2}
              disabled={!summary.has_pattern}
              title={summary.has_pattern ? undefined : 'Set a pattern first'}
              onClick={() => setGenerating(true)}
            >
              Generate paper
            </Button>
            <IconButton
              icon={Trash2}
              tone="red"
              title="Delete exam"
              onClick={() => {
                if (
                  window.confirm(
                    'Delete this exam? Its pattern and associations go away. Questions stay in the warehouse under their subjects.'
                  )
                ) {
                  remove.mutate()
                }
              }}
            />
          </>
        }
      />

      <div className="mb-6 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Stat
          label="Questions available"
          value={summary.questions ?? 0}
          sub={`${summary.own_questions ?? 0} own · ${summary.borrowed_questions ?? 0} borrowed`}
          icon={HelpCircle}
          tone="primary"
        />
        <Stat
          label="With answers"
          value={summary.answered_questions ?? 0}
          sub="Only these reach a generated paper"
          icon={CheckCircle2}
          tone="green"
        />
        <Stat label="Documents" value={summary.documents ?? 0} icon={FileStack} tone="blue" />
        <Stat
          label="Pattern"
          value={summary.has_pattern ? `${summary.pattern_total_questions} Q` : 'none'}
          sub={activePattern ? `v${activePattern.version} · ${activePattern.name}` : 'Set one to generate papers'}
          icon={ListOrdered}
          tone={summary.has_pattern ? 'purple' : 'amber'}
        />
      </div>

      <div className="mb-5 flex gap-1 border-b border-gray-200">
        {TABS.map((item) => (
          <button
            key={item.id}
            type="button"
            onClick={() => setTab(item.id)}
            className={`-mb-px border-b-2 px-4 py-2.5 text-sm font-medium transition-colors ${
              tab === item.id
                ? 'border-primary-600 text-primary-700'
                : 'border-transparent text-gray-500 hover:text-gray-800'
            }`}
          >
            {item.label}
          </button>
        ))}
      </div>

      {tab === 'pattern' && <PatternTab exam={exam} />}
      {tab === 'material' && <MaterialTab exam={exam} />}
      {tab === 'associations' && <AssociationEditor examId={exam.id} examName={exam.name} />}

      {generating && (
        <GeneratePaperDialog
          exam={exam}
          pattern={activePattern}
          onClose={() => setGenerating(false)}
        />
      )}
    </div>
  )
}

function PatternTab({ exam }) {
  const queryClient = useQueryClient()
  const [editing, setEditing] = useState(null)
  const [creating, setCreating] = useState(false)
  const [analysisJob, setAnalysisJob] = useState(null)

  const patterns = exam.patterns || []

  const analyze = useMutation({
    mutationFn: () => api.analyzePattern(exam.id, { activate: true, ingest_after: true }),
    onSuccess: (payload) => setAnalysisJob(payload?.data?.job?.id),
  })

  const { job } = useJobTracker(analysisJob, {
    invalidate: [['exam', String(exam.id)], ['documents']],
  })

  const activate = useMutation({
    mutationFn: (patternId) => api.activatePattern(exam.id, patternId),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['exam', String(exam.id)] }),
  })

  const remove = useMutation({
    mutationFn: (patternId) => api.deletePattern(exam.id, patternId),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['exam', String(exam.id)] }),
  })

  const running = job && !['completed', 'failed', 'cancelled'].includes(job.status)

  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-center gap-2">
        <Button icon={Plus} variant="secondary" onClick={() => setCreating(true)}>
          New pattern
        </Button>
        <Button icon={FileSearch} variant="secondary" loading={analyze.isPending || running} onClick={() => analyze.mutate()}>
          Derive from papers
        </Button>
        <span className="text-xs text-gray-500">
          Deriving reads every question paper attached to this exam.
        </span>
      </div>

      <ErrorNote error={analyze.error} />

      {running && (
        <Card>
          <p className="mb-1 text-sm font-medium text-gray-900">Analysing papers</p>
          <p className="mb-2 text-xs text-gray-500">{job.stage}</p>
          <ProgressBar value={job.progress} />
        </Card>
      )}

      {job?.status === 'completed' && job.result?.draft && (
        <Note tone="blue" icon={Sparkles}>
          <p className="font-medium">
            Pattern v{job.result.version} derived from {job.result.documents_used} paper
            {job.result.documents_used === 1 ? '' : 's'} at{' '}
            {Math.round((job.result.draft.confidence || 0) * 100)}% confidence.
          </p>
          {job.result.draft.warnings?.map((warning, index) => (
            <p key={index} className="text-xs">• {warning}</p>
          ))}
        </Note>
      )}
      {job?.status === 'failed' && <ErrorNote error={{ message: job.error }} />}

      {patterns.length === 0 ? (
        <EmptyState
          icon={ListOrdered}
          title="No pattern yet"
          message="A pattern is how the generator knows what a paper looks like: which subjects, how many questions each, and the marking scheme."
          action={
            <div className="flex gap-2">
              <Button icon={Plus} onClick={() => setCreating(true)}>
                Enter one
              </Button>
              <Button variant="secondary" icon={FileSearch} onClick={() => analyze.mutate()}>
                Derive from papers
              </Button>
            </div>
          }
        />
      ) : (
        <div className="space-y-3">
          {patterns.map((pattern) => (
            <Card key={pattern.id}>
              <div className="mb-3 flex flex-wrap items-start justify-between gap-3">
                <div>
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="font-semibold text-gray-900">{pattern.name}</span>
                    <Badge tone="gray">v{pattern.version}</Badge>
                    {pattern.is_active && <Badge tone="green">active</Badge>}
                    <Badge tone={pattern.source === 'derived' ? 'blue' : 'purple'}>
                      {pattern.source === 'derived'
                        ? `derived from ${pattern.derived_from_count} paper${pattern.derived_from_count === 1 ? '' : 's'}`
                        : 'entered by hand'}
                    </Badge>
                    {pattern.source === 'derived' && (
                      <Badge tone={pattern.confidence >= 0.7 ? 'green' : 'amber'}>
                        {Math.round(pattern.confidence * 100)}% confidence
                      </Badge>
                    )}
                  </div>
                  <p className="mt-1 text-xs text-gray-500">
                    {pattern.total_questions} questions · {pattern.total_marks} marks ·{' '}
                    {pattern.duration_min || '—'} min · {pattern.option_count || '?'} options ·
                    created {formatDate(pattern.created_at)}
                  </p>
                </div>
                <div className="flex items-center gap-1">
                  {!pattern.is_active && (
                    <Button variant="secondary" onClick={() => activate.mutate(pattern.id)}>
                      Make active
                    </Button>
                  )}
                  <IconButton icon={Pencil} title="Edit pattern" tone="primary" onClick={() => setEditing(pattern)} />
                  <IconButton
                    icon={Trash2}
                    title="Delete pattern"
                    tone="red"
                    onClick={() => {
                      if (window.confirm('Delete this pattern version?')) remove.mutate(pattern.id)
                    }}
                  />
                </div>
              </div>

              <div className="overflow-hidden rounded-lg border border-gray-200">
                <table className="w-full text-sm">
                  <thead className="bg-gray-50 text-xs uppercase tracking-wide text-gray-500">
                    <tr>
                      <th className="px-3 py-2 text-left">Section</th>
                      <th className="px-3 py-2 text-left">Subject</th>
                      <th className="px-3 py-2 text-right">Questions</th>
                      <th className="px-3 py-2 text-right">Weightage</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-gray-100">
                    {(pattern.sections || []).map((section) => (
                      <tr key={section.id}>
                        <td className="px-3 py-2 text-gray-900">{section.name}</td>
                        <td className="px-3 py-2">
                          {section.subject ? (
                            <span className="text-gray-600">{section.subject.name}</span>
                          ) : (
                            <Badge tone="amber">not mapped</Badge>
                          )}
                        </td>
                        <td className="px-3 py-2 text-right tabular-nums">{section.question_count}</td>
                        <td className="px-3 py-2 text-right tabular-nums">
                          {Number(section.weightage).toFixed(1)}%
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>

              {pattern.notes && <p className="mt-2 text-xs text-gray-500">{pattern.notes}</p>}
            </Card>
          ))}
        </div>
      )}

      {(creating || editing) && (
        <Modal
          title={editing ? `Edit ${editing.name}` : 'New pattern'}
          size="lg"
          onClose={() => {
            setCreating(false)
            setEditing(null)
          }}
        >
          <PatternEditor
            examId={exam.id}
            pattern={editing}
            onSaved={() => {
              setCreating(false)
              setEditing(null)
            }}
            onCancel={() => {
              setCreating(false)
              setEditing(null)
            }}
          />
        </Modal>
      )}
    </div>
  )
}

function MaterialTab({ exam }) {
  const { data, isLoading } = useQuery({
    queryKey: ['documents', 'exam', String(exam.id)],
    queryFn: () => api.getDocuments({ exam_id: exam.id, page_size: 100 }),
    select: (payload) => payload?.data ?? [],
    refetchInterval: 5000,
  })

  const documents = data ?? []

  return (
    <div className="grid gap-6 lg:grid-cols-[1fr_360px]">
      <div>
        <SectionTitle hint="Everything attached to this exam. Question papers feed both the pattern and the warehouse.">
          Documents
        </SectionTitle>
        {isLoading ? (
          <Loading label="Loading documents" className="py-10" />
        ) : documents.length === 0 ? (
          <EmptyState
            icon={FileStack}
            title="Nothing uploaded for this exam"
            message="Upload question papers, answer keys, books or notes. Anything the converter can read becomes searchable content."
          />
        ) : (
          <div className="space-y-2">
            {documents.map((doc) => (
              <div
                key={doc.id}
                className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-gray-200 bg-white p-3"
              >
                <div className="min-w-0">
                  <p className="truncate text-sm font-medium text-gray-900">{doc.title}</p>
                  <p className="text-xs text-gray-500">
                    {doc.kind.replace(/_/g, ' ')} · {doc.extension.toUpperCase()}
                    {doc.year ? ` · ${doc.year}` : ''}
                    {doc.question_count ? ` · ${doc.question_count} questions` : ''}
                  </p>
                  {doc.notes && <p className="mt-0.5 text-xs text-gray-400">{doc.notes}</p>}
                </div>
                <div className="flex items-center gap-2">
                  {doc.active_job && (
                    <div className="w-28">
                      <ProgressBar value={doc.active_job.progress} label={doc.active_job.stage} />
                    </div>
                  )}
                  <StatusPill status={doc.status} />
                </div>
              </div>
            ))}
          </div>
        )}
      </div>

      <Card>
        <SectionTitle hint="Files are stored in the database, so nothing has to be copied onto the server.">
          Add material
        </SectionTitle>
        <Dropzone defaultExamId={exam.id} lockExam compact />
      </Card>
    </div>
  )
}
