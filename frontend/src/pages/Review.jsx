import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Check,
  ClipboardCheck,
  RefreshCw,
  RotateCcw,
  ShieldCheck,
  Sparkles,
  X,
} from 'lucide-react'
import { api } from '../api/client'
import { useModelStatus, useReviewSummary, useSubjectOptions } from '../api/hooks'
import {
  Badge,
  Button,
  Card,
  EmptyState,
  ErrorNote,
  Field,
  Loading,
  Note,
  PageHeader,
  Pagination,
  Select,
  Stat,
  Textarea,
} from '../components/ui'
import {
  ConfidenceMeter,
  Collapsible,
  IssueList,
  QualityBadge,
  SourceWindow,
  qualityState,
} from '../components/Quality'

/**
 * Review is the queue of everything that is not cleared for delivery.
 *
 * It exists because the alternative to surfacing uncertain content is hiding it,
 * and hidden uncertainty is what puts a broken question in front of a student. A
 * reviewer works through the list, sees exactly why each item was held and what
 * the source actually said, and accepts or rejects it on the record.
 */
export default function Review() {
  const queryClient = useQueryClient()
  const [page, setPage] = useState(1)
  const [filters, setFilters] = useState({
    quality_status: '',
    subject_id: '',
    issue_code: '',
    severity: '',
  })
  const [openId, setOpenId] = useState(null)

  const { data: summary } = useReviewSummary()
  const { data: subjects = [] } = useSubjectOptions()
  const { data: model } = useModelStatus()

  const params = { ...filters, page, page_size: 20 }
  const { data, isLoading, error, refetch, isFetching } = useQuery({
    queryKey: ['review', params],
    queryFn: () => api.getReviewQueue(params),
  })

  const questions = data?.data ?? []
  const pageMeta = data?.meta?.page

  const requalify = useMutation({
    mutationFn: (body) => api.requalify(body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['jobs'] })
      queryClient.invalidateQueries({ queryKey: ['review'] })
    },
  })

  const statuses = summary?.questions_by_status || {}
  const topIssues = useMemo(() => (summary?.by_issue || []).slice(0, 12), [summary])

  const setFilter = (key) => (event) => {
    setFilters((prev) => ({ ...prev, [key]: event.target.value }))
    setPage(1)
  }

  return (
    <div>
      <PageHeader
        title="Review"
        subtitle="Everything the engine could not clear for delivery on its own, with the reason for each. Nothing here can reach a generated paper until it is resolved."
        actions={
          <>
            <Button variant="secondary" onClick={() => refetch()} disabled={isFetching}>
              <RefreshCw className={`h-4 w-4 ${isFetching ? 'animate-spin' : ''}`} />
              Refresh
            </Button>
            <Button
              onClick={() => requalify.mutate({})}
              disabled={requalify.isPending}
              title="Re-run every quality check over the backlog, including model review when configured"
            >
              <ClipboardCheck className="h-4 w-4" />
              {requalify.isPending ? 'Queueing…' : 'Re-check backlog'}
            </Button>
          </>
        }
      />

      <div className="mb-6 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Stat
          label="Ready to use"
          value={statuses.pass ?? 0}
          sub="Passed every check"
          icon={ShieldCheck}
          tone="green"
        />
        <Stat
          label="Needs review"
          value={statuses.review ?? 0}
          sub="Waiting on a decision"
          tone="amber"
        />
        <Stat label="Rejected" value={statuses.failed ?? 0} sub="Defects found" tone="red" />
        <Stat
          label="Not checked"
          value={statuses.unchecked ?? 0}
          sub="Validation has not run"
          tone="purple"
        />
      </div>

      {requalify.isSuccess && (
        <Note tone="blue">
          <p className="font-medium">Re-check queued.</p>
          <p>
            Progress appears in the job feed. Model review{' '}
            {requalify.data?.data?.model_will_run ? 'will run' : 'will not run'} for this
            pass.
          </p>
        </Note>
      )}

      {model && !model.configured && (
        <Note tone="amber">
          <p className="font-medium">No language model is configured.</p>
          <p>
            The deterministic checks still run and still gate delivery. Checks that need
            judgement — answer validity, ambiguity, semantic contamination, duplicate
            wording — are reported as not run rather than counted as passes. Set{' '}
            <code className="rounded bg-white/60 px-1">LLM_BASE_URL</code> and{' '}
            <code className="rounded bg-white/60 px-1">LLM_MODEL</code> to turn them on.
          </p>
        </Note>
      )}
      {model?.configured && model.reachable === false && (
        <Note tone="amber">
          <p className="font-medium">The configured model is unreachable.</p>
          <p className="break-words">{model.error}</p>
        </Note>
      )}

      {topIssues.length > 0 && (
        <Card className="my-6">
          <p className="mb-3 text-sm font-semibold text-gray-900">
            Most common findings
          </p>
          <p className="mb-3 text-xs text-gray-500">
            Fixing the commonest cause clears the most content. A finding from the
            extractor usually means a source or alias problem; one from the checks
            usually means the question itself.
          </p>
          <div className="flex flex-wrap gap-2">
            {topIssues.map((issue) => (
              <button
                key={`${issue.code}-${issue.severity}`}
                type="button"
                onClick={() => {
                  setFilters((prev) => ({ ...prev, issue_code: issue.code }))
                  setPage(1)
                }}
                className={`rounded-full border px-3 py-1 text-xs transition-colors ${
                  filters.issue_code === issue.code
                    ? 'border-primary-400 bg-primary-50 text-primary-800'
                    : 'border-gray-200 bg-white text-gray-700 hover:bg-gray-50'
                }`}
                title={`${issue.severity} · found by ${issue.source}`}
              >
                <span className="font-mono">{issue.code}</span>
                <span className="ml-1.5 font-semibold">{issue.count}</span>
              </button>
            ))}
          </div>
        </Card>
      )}

      <Card className="mb-6">
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <Field label="Verdict">
            <Select value={filters.quality_status} onChange={setFilter('quality_status')}>
              <option value="">Everything not ready</option>
              <option value="failed">Rejected</option>
              <option value="review">Needs review</option>
              <option value="unchecked">Not checked</option>
              <option value="pass">Ready</option>
            </Select>
          </Field>
          <Field label="Subject">
            <Select value={filters.subject_id} onChange={setFilter('subject_id')}>
              <option value="">All subjects</option>
              {subjects.map((subject) => (
                <option key={subject.id} value={subject.id}>
                  {subject.name}
                </option>
              ))}
            </Select>
          </Field>
          <Field label="Severity">
            <Select value={filters.severity} onChange={setFilter('severity')}>
              <option value="">Any severity</option>
              <option value="critical">Critical</option>
              <option value="major">Major</option>
              <option value="minor">Minor</option>
            </Select>
          </Field>
          <Field label="Finding" hint={filters.issue_code ? 'Filtered' : 'Pick one above'}>
            <div className="flex gap-2">
              <Select value={filters.issue_code} onChange={setFilter('issue_code')}>
                <option value="">Any finding</option>
                {(summary?.by_issue || []).map((issue) => (
                  <option key={issue.code} value={issue.code}>
                    {issue.code}
                  </option>
                ))}
              </Select>
              {filters.issue_code && (
                <Button
                  variant="ghost"
                  onClick={() => setFilters((prev) => ({ ...prev, issue_code: '' }))}
                >
                  <X className="h-4 w-4" />
                </Button>
              )}
            </div>
          </Field>
        </div>
      </Card>

      <ErrorNote error={error} className="mb-6" />

      {isLoading ? (
        <Loading label="Loading the review queue" />
      ) : questions.length === 0 ? (
        <EmptyState
          icon={ShieldCheck}
          title="Nothing waiting"
          message="Every question has been validated and cleared. Anything new will appear here automatically."
        />
      ) : (
        <div className="space-y-4">
          {questions.map((question) => (
            <ReviewItem
              key={question.id}
              question={question}
              open={openId === question.id}
              onToggle={() => setOpenId(openId === question.id ? null : question.id)}
            />
          ))}
        </div>
      )}

      {pageMeta && pageMeta.total_pages > 1 && (
        <div className="mt-6">
          <Pagination page={pageMeta.page} totalPages={pageMeta.total_pages} onChange={setPage} />
        </div>
      )}
    </div>
  )
}

function ReviewItem({ question, open, onToggle }) {
  const queryClient = useQueryClient()
  const [notes, setNotes] = useState('')
  const [difficulty, setDifficulty] = useState('')

  const issues = question.quality_issues || []
  const state = qualityState(question.quality_status)

  const { data: provenance, isLoading: loadingProvenance } = useQuery({
    queryKey: ['question', question.id, 'provenance'],
    queryFn: () => api.getProvenance(question.id),
    enabled: open,
    select: (payload) => payload?.data,
  })

  const resolve = useMutation({
    mutationFn: (body) => api.resolveQuestion(question.id, body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['review'] })
      queryClient.invalidateQueries({ queryKey: ['warehouse'] })
      queryClient.invalidateQueries({ queryKey: ['overview'] })
    },
  })

  const act = (decision) =>
    resolve.mutate({ decision, notes, difficulty, reviewer: 'reviewer' })

  return (
    <Card padded={false}>
      <button
        type="button"
        onClick={onToggle}
        className="flex w-full items-start gap-3 p-4 text-left hover:bg-gray-50"
      >
        <div className="min-w-0 flex-1">
          <div className="mb-1.5 flex flex-wrap items-center gap-2">
            <QualityBadge status={question.quality_status} score={question.quality_score} />
            {question.subject && <Badge tone="blue">{question.subject.name}</Badge>}
            {question.question_number > 0 && (
              <span className="text-xs text-gray-500">
                Q{question.question_number}
                {question.page_no > 0 ? ` · page ${question.page_no}` : ''}
              </span>
            )}
            {question.extract_engine && (
              <Badge tone="gray" title="Which engine read this question">
                {question.extract_engine}
              </Badge>
            )}
            {question.model_checked && (
              <Badge tone="purple" title={`Reviewed by ${question.model_name}`}>
                <Sparkles className="h-3 w-3" /> model
              </Badge>
            )}
            {!question.has_answer && <Badge tone="red">no answer</Badge>}
          </div>
          <p className="line-clamp-2 text-sm text-gray-900">
            {question.question_text || <em className="text-red-600">no question text</em>}
          </p>
          <p className="mt-1 text-xs text-gray-500">
            {issues.length} finding{issues.length === 1 ? '' : 's'} · {state.blurb}
          </p>
        </div>
      </button>

      {open && (
        <div className="space-y-5 border-t border-gray-200 p-4">
          <div className="grid gap-5 lg:grid-cols-2">
            <div className="space-y-3">
              <p className="text-xs font-semibold uppercase tracking-wide text-gray-500">
                As stored
              </p>
              {question.passage && (
                <Collapsible title="Shared passage">
                  <p className="whitespace-pre-wrap text-sm text-gray-700">
                    {question.passage.text}
                  </p>
                  <p className="mt-2 text-xs text-gray-500">
                    Covers questions {question.passage.from_question}–
                    {question.passage.to_question}
                    {question.passage.range_explicit
                      ? ' (range printed in the source)'
                      : ' (range inferred)'}
                  </p>
                </Collapsible>
              )}
              <p className="whitespace-pre-wrap rounded-lg bg-gray-50 p-3 text-sm text-gray-900">
                {question.question_text || '—'}
              </p>
              <ol className="space-y-1.5">
                {(question.options || []).map((option) => (
                  <li
                    key={option.id}
                    className={`flex gap-2 rounded px-2 py-1.5 text-sm ${
                      option.is_correct
                        ? 'bg-green-50 font-medium text-green-900'
                        : 'text-gray-700'
                    }`}
                  >
                    <span className="text-gray-500">({option.label})</span>
                    <span className="min-w-0 break-words">{option.text}</span>
                    {option.is_correct && <Check className="ml-auto h-4 w-4 text-green-600" />}
                  </li>
                ))}
              </ol>
              {question.trailing_text && (
                <div className="rounded-lg border border-amber-200 bg-amber-50 p-3">
                  <p className="text-xs font-semibold text-amber-900">
                    Text set aside after this question
                  </p>
                  <p className="mt-1 text-xs text-amber-800">{question.trailing_text}</p>
                </div>
              )}
              {question.explanation && (
                <Collapsible title="Explanation">
                  <p className="whitespace-pre-wrap text-sm text-gray-700">
                    {question.explanation}
                  </p>
                </Collapsible>
              )}
            </div>

            <div className="space-y-4">
              <div>
                <p className="mb-2 text-xs font-semibold uppercase tracking-wide text-gray-500">
                  Why it was held
                </p>
                <IssueList issues={issues} />
              </div>

              <ConfidenceMeter
                value={question.extraction_confidence}
                label="Extraction confidence"
                hint="How cleanly the parser read this question, independent of whether the content is any good."
              />

              {loadingProvenance ? (
                <Loading label="Loading the source" className="py-6" />
              ) : (
                provenance && (
                  <>
                    <Collapsible title="What the document actually said" defaultOpen>
                      <SourceWindow window={provenance.source_window} />
                      {provenance.document && (
                        <p className="mt-2 text-xs text-gray-500">
                          {provenance.document.title} · read with{' '}
                          {provenance.document.engine} · lines{' '}
                          {provenance.extraction?.source_first_line}–
                          {provenance.extraction?.source_last_line}
                        </p>
                      )}
                    </Collapsible>
                    {provenance.duplicates?.length > 0 && (
                      <Collapsible
                        title={`Possible duplicates (${provenance.duplicates.length})`}
                      >
                        <ul className="space-y-2">
                          {provenance.duplicates.map((dupe) => (
                            <li key={dupe.question_id} className="text-sm text-gray-700">
                              <Badge tone={dupe.exact ? 'red' : 'amber'}>
                                {dupe.exact
                                  ? 'identical'
                                  : `${Math.round(dupe.similarity * 100)}% similar`}
                              </Badge>
                              <span className="ml-2">#{dupe.question_id}</span>
                              <p className="mt-0.5 text-xs text-gray-600">{dupe.stem}</p>
                            </li>
                          ))}
                        </ul>
                      </Collapsible>
                    )}
                    {provenance.question?.reviews?.length > 0 && (
                      <Collapsible
                        title={`Review history (${provenance.question.reviews.length})`}
                      >
                        <ul className="space-y-2">
                          {provenance.question.reviews.map((review) => (
                            <li key={review.id} className="text-sm">
                              <div className="flex flex-wrap items-center gap-2">
                                <Badge tone={review.reviewer_type === 'model' ? 'purple' : 'blue'}>
                                  {review.reviewer_type}
                                </Badge>
                                <span className="text-gray-700">{review.reviewer_name}</span>
                                {review.reviewer_type === 'model' && !review.grounded && (
                                  <Badge tone="red" title="The model quoted text that is not in this question">
                                    ungrounded
                                  </Badge>
                                )}
                                <span className="text-xs text-gray-500">
                                  {new Date(review.created_at).toLocaleString()}
                                </span>
                              </div>
                              {review.notes && (
                                <p className="mt-0.5 text-xs text-gray-600">{review.notes}</p>
                              )}
                            </li>
                          ))}
                        </ul>
                      </Collapsible>
                    )}
                  </>
                )
              )}
            </div>
          </div>

          <div className="space-y-3 border-t border-gray-200 pt-4">
            <ErrorNote error={resolve.error} />
            <div className="grid gap-3 sm:grid-cols-[1fr_auto]">
              <Field
                label="Reviewer note"
                hint="Recorded with your decision so the reasoning survives."
              >
                <Textarea
                  rows={2}
                  value={notes}
                  onChange={(event) => setNotes(event.target.value)}
                  placeholder="Why are you accepting or rejecting this?"
                />
              </Field>
              <Field
                label="Rate difficulty"
                hint="A human rating is what makes the difficulty mix meaningful."
              >
                <Select value={difficulty} onChange={(event) => setDifficulty(event.target.value)}>
                  <option value="">Leave unrated</option>
                  <option value="easy">Easy</option>
                  <option value="medium">Medium</option>
                  <option value="hard">Hard</option>
                </Select>
              </Field>
            </div>
            <div className="flex flex-wrap gap-2">
              <Button
                onClick={() => act('accept')}
                disabled={resolve.isPending}
                title="Clear this question for use despite the findings. Recorded as a human override."
              >
                <Check className="h-4 w-4" /> Accept for use
              </Button>
              <Button
                variant="danger"
                onClick={() => act('reject')}
                disabled={resolve.isPending}
                title="Keep this question out of every paper"
              >
                <X className="h-4 w-4" /> Reject
              </Button>
              <Button
                variant="secondary"
                onClick={() => act('recheck')}
                disabled={resolve.isPending}
                title="Run the checks again against the current text"
              >
                <RotateCcw className="h-4 w-4" /> Re-check
              </Button>
            </div>
          </div>
        </div>
      )}
    </Card>
  )
}
