import { useMemo, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  ArrowLeft,
  CheckCircle2,
  Download,
  Eye,
  EyeOff,
  Link2,
  Send,
  Undo2,
} from 'lucide-react'
import { api } from '../api/client'
import { useModelStatus, usePaperQA } from '../api/hooks'
import {
  Badge,
  Button,
  Card,
  ErrorNote,
  Loading,
  Note,
  PageHeader,
  SectionTitle,
  Stat,
  StatusPill,
  formatDate,
} from '../components/ui'
import { QualityBadge, QualityGatePanel } from '../components/Quality'

export default function PaperDetail() {
  const { paperId } = useParams()
  const queryClient = useQueryClient()
  const [showAnswers, setShowAnswers] = useState(true)

  const { data: paper, isLoading, error } = useQuery({
    queryKey: ['paper', paperId],
    queryFn: () => api.getPaper(paperId),
    select: (payload) => payload?.data,
  })

  const { data: qa } = usePaperQA(paperId)
  const { data: model } = useModelStatus()

  const publish = useMutation({
    mutationFn: (status) => api.updatePaper(paperId, { status }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['paper', paperId] })
      queryClient.invalidateQueries({ queryKey: ['papers'] })
    },
  })

  const recheck = useMutation({
    mutationFn: () => api.runPaperQA(paperId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['paper', paperId] })
      queryClient.invalidateQueries({ queryKey: ['jobs'] })
    },
  })

  const publishable = qa?.publishable ?? paper?.quality_status === 'pass'

  const analytics = useMemo(() => {
    if (!paper?.analytics) return null
    try {
      return typeof paper.analytics === 'string' ? JSON.parse(paper.analytics) : paper.analytics
    } catch {
      return null
    }
  }, [paper])

  const sections = useMemo(() => {
    if (!paper?.items) return []
    const groups = []
    let current = null
    paper.items.forEach((item) => {
      if (!current || current.name !== item.section_name) {
        current = { name: item.section_name || 'Questions', items: [] }
        groups.push(current)
      }
      current.items.push(item)
    })
    return groups
  }, [paper])

  if (isLoading) return <Loading label="Loading paper" />
  if (error) return <ErrorNote error={error} />
  if (!paper) return <ErrorNote error={{ message: 'Paper not found' }} />

  return (
    <div>
      <Link to="/papers" className="mb-4 inline-flex items-center gap-1 text-sm text-gray-500 hover:text-gray-700">
        <ArrowLeft className="h-4 w-4" /> All papers
      </Link>

      <PageHeader
        title={paper.title}
        subtitle={`${paper.exam?.name || 'Unknown exam'} · built ${formatDate(paper.created_at)} · seed ${paper.seed}`}
        actions={
          <>
            <Button
              variant="secondary"
              icon={showAnswers ? EyeOff : Eye}
              onClick={() => setShowAnswers((value) => !value)}
            >
              {showAnswers ? 'Hide answers' : 'Show answers'}
            </Button>
            {/* Export offers the deliverable file only when the paper has passed.
                Otherwise the link explicitly asks for a draft, which the server
                stamps, so an unvetted paper cannot be mistaken for a final one. */}
            <a href={api.exportUrl(paper.id, { answers: true, draft: !publishable })} download>
              <Button variant="secondary" icon={Download}>
                {publishable ? 'Export' : 'Export draft'}
              </Button>
            </a>
            {paper.status === 'published' ? (
              <Button icon={Undo2} variant="secondary" loading={publish.isPending} onClick={() => publish.mutate('draft')}>
                Back to draft
              </Button>
            ) : (
              <Button
                icon={Send}
                loading={publish.isPending}
                disabled={!publishable}
                title={
                  publishable
                    ? 'Publish this paper'
                    : 'Blocked until the paper passes quality review'
                }
                onClick={() => publish.mutate('published')}
              >
                Publish
              </Button>
            )}
          </>
        }
      />

      <div className="mb-6 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Stat label="Questions" value={paper.total_questions} />
        <Stat label="Total marks" value={paper.total_marks} sub={paper.negative_marks ? `−${paper.negative_marks} per wrong answer` : 'No negative marking'} />
        <Stat label="Duration" value={paper.duration_min ? `${paper.duration_min} min` : '—'} />
        <Stat
          label="Status"
          value={
            <span className="flex flex-wrap items-center gap-1.5">
              <StatusPill status={paper.status} />
              <QualityBadge status={paper.quality_status} score={paper.quality_score} />
            </span>
          }
          sub={paper.pattern ? `Pattern v${paper.pattern.version}` : undefined}
        />
      </div>

      {/* The quality verdict comes before the paper itself. Whether this can be
          delivered is the first thing anyone opening it needs to know. */}
      <div className="mb-6">
        <ErrorNote error={recheck.error} className="mb-4" />
        <QualityGatePanel
          qa={qa || { quality_status: paper.quality_status, quality_score: paper.quality_score }}
          onRecheck={() => recheck.mutate()}
          rechecking={recheck.isPending}
          modelEnabled={model?.configured}
        />
      </div>

      {analytics && (
        <Card className="mb-6">
          <SectionTitle hint="Recorded when the paper was built, so it describes this exact paper.">
            Composition
          </SectionTitle>

          <div className="grid gap-5 lg:grid-cols-3">
            <div>
              <p className="mb-2 text-xs font-medium uppercase tracking-wide text-gray-500">
                Difficulty
              </p>
              <div className="flex flex-wrap gap-1.5">
                {Object.entries(analytics.difficulty_spread || {}).map(([level, count]) => (
                  <Badge key={level} tone={level === 'easy' ? 'green' : level === 'hard' ? 'red' : 'amber'}>
                    {level}: {count}
                  </Badge>
                ))}
              </div>
            </div>

            <div>
              <p className="mb-2 text-xs font-medium uppercase tracking-wide text-gray-500">
                Sections filled
              </p>
              <div className="space-y-1 text-sm">
                {(analytics.section_fill || []).map((fill, index) => (
                  <div key={index} className="flex items-center justify-between gap-2">
                    <span className="truncate text-gray-700">{fill.section}</span>
                    <span className="tabular-nums text-gray-500">
                      {fill.filled}/{fill.requested}
                      {fill.borrowed > 0 && (
                        <span className="ml-1 text-blue-600">({fill.borrowed} borrowed)</span>
                      )}
                    </span>
                  </div>
                ))}
              </div>
            </div>

            <div>
              <p className="mb-2 text-xs font-medium uppercase tracking-wide text-gray-500">
                Sources
              </p>
              {analytics.borrowed_count > 0 ? (
                <div className="space-y-1 text-sm">
                  {Object.entries(analytics.borrowed_from || {}).map(([name, count]) => (
                    <div key={name} className="flex items-center gap-1.5">
                      <Link2 className="h-3 w-3 text-blue-500" />
                      <span className="truncate text-gray-700">{name}</span>
                      <span className="ml-auto tabular-nums text-gray-500">{count}</span>
                    </div>
                  ))}
                </div>
              ) : (
                <p className="text-sm text-gray-500">Entirely from this exam's own material.</p>
              )}
              {Object.keys(analytics.year_spread || {}).length > 0 && (
                <div className="mt-2 flex flex-wrap gap-1">
                  {Object.entries(analytics.year_spread)
                    .sort(([a], [b]) => Number(b) - Number(a))
                    .slice(0, 8)
                    .map(([year, count]) => (
                      <Badge key={year} tone="gray">
                        {year}: {count}
                      </Badge>
                    ))}
                </div>
              )}
            </div>
          </div>

          {analytics.shortfalls?.length > 0 && (
            <div className="mt-4 space-y-1 border-t border-gray-100 pt-3">
              {analytics.shortfalls.map((shortfall, index) => (
                <p key={index} className="text-xs text-amber-800">
                  <strong>{shortfall.section}</strong>: {shortfall.reason}
                </p>
              ))}
            </div>
          )}
        </Card>
      )}

      {paper.notes && (
        <Note tone="gray">
          <p>{paper.notes}</p>
        </Note>
      )}

      <div className="space-y-6">
        {sections.map((section, sectionIndex) => (
          <div key={sectionIndex}>
            <h2 className="mb-3 border-b border-gray-200 pb-2 text-base font-semibold text-gray-900">
              {section.name}
              <span className="ml-2 text-sm font-normal text-gray-500">
                {section.items.length} question{section.items.length === 1 ? '' : 's'}
              </span>
            </h2>

            <div className="space-y-3">
              {section.items.map((item) => (
                <Card key={item.id}>
                  <div className="mb-2 flex flex-wrap items-center gap-1.5">
                    <Badge tone="primary">Q{item.sequence_no}</Badge>
                    <Badge tone="gray">
                      {item.marks} mark{item.marks === 1 ? '' : 's'}
                    </Badge>
                    {item.negative_marks > 0 && <Badge tone="red">−{item.negative_marks}</Badge>}
                    {item.source_kind === 'associated' && (
                      <Badge tone="blue" title="Borrowed from an associated exam">
                        <Link2 className="h-3 w-3" /> borrowed
                      </Badge>
                    )}
                    {item.question?.year && <Badge tone="gray">{item.question.year}</Badge>}
                    {/* A question whose verdict has changed since the paper was
                        built is surfaced here rather than only in the QA report. */}
                    {item.question && item.question.quality_status !== 'pass' && (
                      <QualityBadge status={item.question.quality_status} />
                    )}
                  </div>

                  {item.question ? (
                    <>
                      <p className="whitespace-pre-line text-sm leading-relaxed text-gray-900">
                        {item.question.question_text}
                      </p>

                      {item.question.options?.length > 0 && (
                        <div className="mt-2 grid gap-1 sm:grid-cols-2">
                          {item.question.options.map((option) => (
                            <div
                              key={option.id}
                              className={`flex items-start gap-1.5 rounded px-2 py-1 text-sm ${
                                showAnswers && option.is_correct
                                  ? 'bg-green-50 font-medium text-green-800'
                                  : 'text-gray-700'
                              }`}
                            >
                              <span className="font-semibold">{option.label}.</span>
                              <span className="min-w-0">{option.text}</span>
                              {showAnswers && option.is_correct && (
                                <CheckCircle2 className="ml-auto h-4 w-4 flex-shrink-0 text-green-600" />
                              )}
                            </div>
                          ))}
                        </div>
                      )}

                      {showAnswers && item.question.explanation && (
                        <p className="mt-2 rounded bg-blue-50 p-2 text-xs text-blue-900">
                          <span className="font-semibold">Explanation: </span>
                          {item.question.explanation}
                        </p>
                      )}
                    </>
                  ) : (
                    <p className="text-sm italic text-gray-400">
                      This question is no longer in the warehouse.
                    </p>
                  )}
                </Card>
              ))}
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}
