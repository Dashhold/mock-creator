import { Link } from 'react-router-dom'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import {
  Activity,
  CheckCircle2,
  FileCheck2,
  FileStack,
  GraduationCap,
  HelpCircle,
  ListOrdered,
  RotateCcw,
  UploadCloud,
  Wand2,
  XCircle,
  Ban,
} from 'lucide-react'
import { api } from '../api/client'
import { useOverview, useReviewSummary } from '../api/hooks'
import {
  Badge,
  Button,
  Card,
  ErrorNote,
  IconButton,
  Loading,
  Note,
  PageHeader,
  ProgressBar,
  SectionTitle,
  Stat,
  StatusPill,
  relativeTime,
} from '../components/ui'
import { QUALITY_STATES } from '../components/Quality'

const JOB_LABELS = {
  ingest: 'Reading document',
  pattern_analysis: 'Deriving pattern',
  paper_build: 'Building paper',
  question_audit: 'Reviewing questions',
  paper_qa: 'Reviewing paper',
}

export default function Dashboard() {
  const { data, isLoading, error } = useOverview()
  const { data: reviewSummary } = useReviewSummary()

  if (isLoading) return <Loading label="Loading" />
  if (error) return <ErrorNote error={error} />

  const counts = data?.counts || {}
  const activeJobs = data?.active_jobs || []
  const recentJobs = data?.recent_jobs || []
  const subjects = data?.subjects || []

  const empty = (counts.exams ?? 0) === 0 && (counts.documents ?? 0) === 0

  return (
    <div>
      <PageHeader
        title="Dashboard"
        subtitle="A content engine for exam papers. It learns each exam from the documents you give it."
      />

      {empty ? (
        <GettingStarted />
      ) : (
        <>
          <div className="mb-6 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
            <Stat
              label="Questions ready"
              value={(reviewSummary?.questions_by_status?.pass ?? 0).toLocaleString()}
              sub={`of ${(counts.questions ?? 0).toLocaleString()} extracted · ${
                counts.questions_answered ?? 0
              } answered`}
              icon={HelpCircle}
              tone="primary"
            />
            <Stat
              label="Documents"
              value={counts.documents ?? 0}
              sub={`${counts.documents_ready ?? 0} processed${
                counts.documents_failed ? ` · ${counts.documents_failed} failed` : ''
              }`}
              icon={FileStack}
              tone="blue"
            />
            <Stat
              label="Exams"
              value={counts.exams ?? 0}
              sub={`${counts.patterns ?? 0} pattern${counts.patterns === 1 ? '' : 's'} · ${
                counts.associations ?? 0
              } association${counts.associations === 1 ? '' : 's'}`}
              icon={GraduationCap}
              tone="purple"
            />
            <Stat
              label="Papers"
              value={counts.papers ?? 0}
              sub="Generated from patterns"
              icon={FileCheck2}
              tone="green"
            />
          </div>

          {/* The quality split is the headline number, not the total. A dashboard
              reporting "1,600 questions" when 400 of them cannot be used is
              reporting the wrong thing. */}
          <QualitySnapshot summary={reviewSummary} />

          {counts.questions_pending > 0 && (
            <Note tone="blue" icon={CheckCircle2}>
              <p>
                {counts.questions_pending} question
                {counts.questions_pending === 1 ? '' : 's'} are waiting for review.{' '}
                <Link to="/review" className="font-medium underline">
                  Open the review queue
                </Link>
                .
              </p>
            </Note>
          )}

          <div className="mt-6 grid gap-6 lg:grid-cols-[1fr_380px]">
            <div>
              <SectionTitle hint="Questions filed by subject, across every exam.">
                The warehouse
              </SectionTitle>
              {subjects.length === 0 ? (
                <Card>
                  <p className="text-sm text-gray-500">
                    No questions yet. Upload a question paper and the engine will fill this in.
                  </p>
                </Card>
              ) : (
                <Card padded={false} className="divide-y divide-gray-100">
                  {subjects.slice(0, 12).map((subject) => {
                    const answeredPct = subject.total
                      ? Math.round((subject.answered / subject.total) * 100)
                      : 0
                    return (
                      <Link
                        key={subject.subject_id}
                        to={`/warehouse?subject_id=${subject.subject_id}`}
                        className="flex items-center gap-4 px-4 py-3 transition-colors hover:bg-gray-50"
                      >
                        <div className="min-w-0 flex-1">
                          <p className="truncate text-sm font-medium text-gray-900">
                            {subject.name}
                          </p>
                          <div className="mt-1">
                            <ProgressBar
                              value={answeredPct}
                              tone={answeredPct > 80 ? 'green' : 'amber'}
                            />
                          </div>
                        </div>
                        <div className="text-right">
                          <p className="text-sm font-semibold tabular-nums text-gray-900">
                            {subject.total}
                          </p>
                          <p className="text-xs text-gray-500">{answeredPct}% answered</p>
                        </div>
                      </Link>
                    )
                  })}
                </Card>
              )}

              <div className="mt-4 flex flex-wrap gap-2">
                <Link to="/documents">
                  <Button variant="secondary" icon={UploadCloud}>
                    Upload material
                  </Button>
                </Link>
                <Link to="/exams">
                  <Button variant="secondary" icon={ListOrdered}>
                    Manage exams
                  </Button>
                </Link>
                <Link to="/papers">
                  <Button variant="secondary" icon={Wand2}>
                    Generate a paper
                  </Button>
                </Link>
              </div>
            </div>

            <div>
              <SectionTitle hint="Everything long-running shows up here.">Activity</SectionTitle>
              <JobFeed activeJobs={activeJobs} recentJobs={recentJobs} />
            </div>
          </div>
        </>
      )}
    </div>
  )
}

function JobFeed({ activeJobs, recentJobs }) {
  const queryClient = useQueryClient()

  const cancel = useMutation({
    mutationFn: (id) => api.cancelJob(id),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['overview'] }),
  })
  const retry = useMutation({
    mutationFn: (id) => api.retryJob(id),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['overview'] }),
  })

  if (activeJobs.length === 0 && recentJobs.length === 0) {
    return (
      <Card>
        <div className="flex items-center gap-2 text-sm text-gray-500">
          <Activity className="h-4 w-4" />
          Nothing has run yet.
        </div>
      </Card>
    )
  }

  return (
    <div className="space-y-3">
      {activeJobs.map((job) => (
        <Card key={job.id}>
          <div className="mb-2 flex items-start justify-between gap-2">
            <div className="min-w-0">
              <p className="truncate text-sm font-medium text-gray-900">
                {JOB_LABELS[job.type] || job.type}
              </p>
              <p className="truncate text-xs text-gray-500">
                {job.document?.title || job.exam?.name || `Job ${job.id}`}
              </p>
            </div>
            <IconButton
              icon={Ban}
              title="Cancel this job"
              tone="red"
              onClick={() => cancel.mutate(job.id)}
            />
          </div>
          <ProgressBar value={job.progress} label={job.stage} />
          {job.total > 0 && (
            <p className="mt-1 text-xs text-gray-500">
              {job.processed} of {job.total} done
            </p>
          )}
        </Card>
      ))}

      {recentJobs.length > 0 && (
        <Card padded={false} className="divide-y divide-gray-100">
          {recentJobs.map((job) => (
            <div key={job.id} className="flex items-start gap-3 px-4 py-3">
              {job.status === 'completed' ? (
                <CheckCircle2 className="mt-0.5 h-4 w-4 flex-shrink-0 text-green-600" />
              ) : (
                <XCircle className="mt-0.5 h-4 w-4 flex-shrink-0 text-red-500" />
              )}
              <div className="min-w-0 flex-1">
                <p className="truncate text-sm text-gray-900">
                  {JOB_LABELS[job.type] || job.type}
                </p>
                <p className="truncate text-xs text-gray-500">
                  {job.document?.title || job.exam?.name || `Job ${job.id}`} ·{' '}
                  {relativeTime(job.finished_at || job.created_at)}
                </p>
                {job.error && (
                  <p className="mt-1 line-clamp-2 text-xs text-red-600" title={job.error}>
                    {job.error}
                  </p>
                )}
              </div>
              {job.status === 'failed' && (
                <IconButton
                  icon={RotateCcw}
                  title="Run it again"
                  tone="primary"
                  onClick={() => retry.mutate(job.id)}
                />
              )}
              {job.status !== 'failed' && <StatusPill status={job.status} />}
            </div>
          ))}
        </Card>
      )}
    </div>
  )
}

function GettingStarted() {
  const steps = [
    {
      title: 'Create an exam',
      body: 'Name the exam you are building for. The engine has no built-in list of exams; everything comes from you.',
      to: '/exams',
      cta: 'Create an exam',
      icon: GraduationCap,
    },
    {
      title: 'Give it a pattern',
      body: 'Type the section breakdown in, or drop in a past paper and let the engine work out the sections, counts and marking scheme for itself.',
      to: '/exams',
      cta: 'Set up a pattern',
      icon: ListOrdered,
    },
    {
      title: 'Upload your material',
      body: 'Papers, answer keys, books, notes, scans. PDF, Word, PowerPoint, Excel, HTML, text, images. Scanned pages go through OCR automatically.',
      to: '/documents',
      cta: 'Upload documents',
      icon: UploadCloud,
    },
    {
      title: 'Generate papers',
      body: 'The generator fills each section of the pattern from the warehouse, borrowing from associated exams where you allow it.',
      to: '/papers',
      cta: 'Generate a paper',
      icon: Wand2,
    },
  ]

  return (
    <div>
      <Note>
        <p className="font-medium">Nothing here yet</p>
        <p>
          Four steps get you from an empty database to a generated paper. Each one is reversible, and
          you can come back to any of them later.
        </p>
      </Note>

      <div className="mt-5 grid gap-4 sm:grid-cols-2">
        {steps.map((step, index) => (
          <Card key={step.title} className="flex flex-col">
            <div className="mb-2 flex items-center gap-2">
              <span className="flex h-6 w-6 items-center justify-center rounded-full bg-primary-100 text-xs font-bold text-primary-700">
                {index + 1}
              </span>
              <step.icon className="h-4 w-4 text-primary-600" />
              <p className="font-medium text-gray-900">{step.title}</p>
            </div>
            <p className="mb-4 text-sm text-gray-600">{step.body}</p>
            <Link to={step.to} className="mt-auto">
              <Button variant={index === 0 ? 'primary' : 'secondary'}>{step.cta}</Button>
            </Link>
          </Card>
        ))}
      </div>

      <Card className="mt-4">
        <SectionTitle hint="Recognition data lives in the database, not in code.">
          A note on how it stays exam-agnostic
        </SectionTitle>
        <p className="text-sm text-gray-600">
          The parser works out each document's own conventions: how questions are numbered, how
          options are labelled, how many there are, and where the answer key sits. Section headings
          are matched against subject aliases you can edit on the{' '}
          <Link to="/taxonomy" className="font-medium text-primary-700 underline">
            Taxonomy
          </Link>{' '}
          page. Supporting a new exam means adding data, not changing the software.
        </p>
      </Card>
    </div>
  )
}

/**
 * QualitySnapshot shows how much of the warehouse is actually usable.
 *
 * A total question count flatters the engine: it counts content that the
 * generator will refuse to pick. Splitting it by verdict is the honest version,
 * and it doubles as the route into the review queue.
 */
function QualitySnapshot({ summary }) {
  if (!summary) return null

  const byStatus = summary.questions_by_status || {}
  const papers = summary.papers_by_status || {}
  const order = ['pass', 'review', 'failed', 'unchecked']
  const total = order.reduce((sum, key) => sum + (byStatus[key] || 0), 0)
  if (total === 0) return null

  const barColors = {
    pass: 'bg-green-500',
    review: 'bg-amber-500',
    failed: 'bg-red-500',
    unchecked: 'bg-gray-400',
  }
  const held = (byStatus.review || 0) + (byStatus.failed || 0) + (byStatus.unchecked || 0)

  return (
    <Card className="mt-6">
      <div className="mb-3 flex flex-wrap items-baseline justify-between gap-2">
        <SectionTitle hint="Only questions that pass every check can be used in a generated paper.">
          Content quality
        </SectionTitle>
        <Link to="/review" className="text-sm font-medium text-primary-700 hover:underline">
          {held > 0 ? `Resolve ${held.toLocaleString()} held back` : 'Open review queue'}
        </Link>
      </div>

      <div className="flex h-2.5 w-full overflow-hidden rounded-full bg-gray-100">
        {order.map((key) => {
          const value = byStatus[key] || 0
          if (!value) return null
          return (
            <div
              key={key}
              className={barColors[key]}
              style={{ width: `${(value / total) * 100}%` }}
              title={`${QUALITY_STATES[key].label}: ${value.toLocaleString()}`}
            />
          )
        })}
      </div>

      <div className="mt-3 flex flex-wrap gap-x-5 gap-y-2">
        {order.map((key) => (
          <div key={key} className="flex items-center gap-1.5 text-sm">
            <span className={`h-2.5 w-2.5 rounded-full ${barColors[key]}`} />
            <span className="text-gray-700">{QUALITY_STATES[key].label}</span>
            <span className="font-semibold tabular-nums text-gray-900">
              {(byStatus[key] || 0).toLocaleString()}
            </span>
          </div>
        ))}
      </div>

      {(summary.by_issue || []).length > 0 && (
        <div className="mt-4 border-t border-gray-100 pt-3">
          <p className="mb-2 text-xs font-medium uppercase tracking-wide text-gray-500">
            Commonest findings
          </p>
          <div className="flex flex-wrap gap-1.5">
            {summary.by_issue.slice(0, 6).map((issue) => (
              <Badge
                key={`${issue.code}-${issue.severity}`}
                tone={
                  issue.severity === 'critical'
                    ? 'red'
                    : issue.severity === 'major'
                    ? 'amber'
                    : 'gray'
                }
                title={`found by ${issue.source}`}
              >
                {issue.code} {issue.count}
              </Badge>
            ))}
          </div>
        </div>
      )}

      {Object.keys(papers).length > 0 && (
        <div className="mt-4 border-t border-gray-100 pt-3">
          <p className="mb-2 text-xs font-medium uppercase tracking-wide text-gray-500">
            Papers
          </p>
          <div className="flex flex-wrap gap-1.5">
            {order.map((key) =>
              papers[key] ? (
                <Badge
                  key={key}
                  tone={
                    key === 'pass'
                      ? 'green'
                      : key === 'failed'
                      ? 'red'
                      : key === 'review'
                      ? 'amber'
                      : 'gray'
                  }
                  title={QUALITY_STATES[key].blurb}
                >
                  {papers[key]} {QUALITY_STATES[key].label.toLowerCase()}
                </Badge>
              ) : null
            )}
          </div>
        </div>
      )}
    </Card>
  )
}
