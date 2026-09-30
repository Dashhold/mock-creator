import { useMemo, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  AlertTriangle,
  CheckCircle2,
  Link2,
  Search,
  ThumbsDown,
  ThumbsUp,
  Trash2,
  Warehouse as WarehouseIcon,
  X,
} from 'lucide-react'
import { api } from '../api/client'
import { useExamOptions, useSubjectOptions } from '../api/hooks'
import {
  Badge,
  Button,
  Card,
  EmptyState,
  ErrorNote,
  Field,
  IconButton,
  Input,
  Loading,
  Note,
  PageHeader,
  Pagination,
  Select,
  StatusPill,
  difficultyTone,
} from '../components/ui'
import { IssueList, QualityBadge } from '../components/Quality'

const EMPTY_FILTERS = {
  exam_id: '',
  subject_id: '',
  difficulty: 'all',
  origin: 'all',
  status: 'all',
  quality_status: 'all',
  has_answer: '',
  search: '',
  sort: 'newest',
}

/**
 * Warehouse is the single view of every question the engine holds, filed under
 * its subject and tagged with every exam it serves. A question borrowed by four
 * exams appears once, with four tags.
 */
export default function Warehouse() {
  const queryClient = useQueryClient()
  const { data: exams = [] } = useExamOptions()
  const { data: subjects = [] } = useSubjectOptions()

  // Links from the dashboard arrive pre-filtered, e.g. /warehouse?status=pending,
  // so the query string seeds the filter state.
  const [searchParams, setSearchParams] = useSearchParams()
  const [filters, setFilters] = useState(() => {
    const initial = { ...EMPTY_FILTERS }
    Object.keys(EMPTY_FILTERS).forEach((key) => {
      const value = searchParams.get(key)
      if (value) initial[key] = value
    })
    return initial
  })
  const [page, setPage] = useState(1)
  const [selected, setSelected] = useState(() => new Set())

  const setFilter = (changes) => {
    setFilters((current) => {
      const next = { ...current, ...changes }
      // Keep the address bar in step so a filtered view can be shared.
      const params = new URLSearchParams()
      Object.entries(next).forEach(([key, value]) => {
        if (value && value !== 'all' && value !== EMPTY_FILTERS[key]) params.set(key, value)
      })
      setSearchParams(params, { replace: true })
      return next
    })
    setPage(1)
    setSelected(new Set())
  }

  const { data: summary } = useQuery({
    queryKey: ['warehouse', filters.exam_id],
    queryFn: () => api.getWarehouse({ exam_id: filters.exam_id || undefined }),
    select: (payload) => payload?.data,
  })

  const { data, isLoading, error } = useQuery({
    queryKey: ['questions', { ...filters, page }],
    queryFn: () => api.getQuestions({ ...filters, page, page_size: 25 }),
  })

  const questions = data?.data ?? []
  const meta = data?.meta

  const refresh = () => {
    queryClient.invalidateQueries({ queryKey: ['questions'] })
    queryClient.invalidateQueries({ queryKey: ['warehouse'] })
    queryClient.invalidateQueries({ queryKey: ['overview'] })
    setSelected(new Set())
  }

  const bulk = useMutation({
    mutationFn: (body) => api.bulkUpdateQuestions({ question_ids: [...selected], ...body }),
    onSuccess: refresh,
  })

  const remove = useMutation({
    mutationFn: (id) => api.deleteQuestion(id),
    onSuccess: refresh,
  })

  const review = useMutation({
    mutationFn: ({ id, verdict }) =>
      api.reviewQuestion(id, { verdict, reviewer_type: 'human', is_factual: verdict === 'approved' }),
    onSuccess: refresh,
  })

  // Group the page by subject so the list reads as a filing cabinet rather than
  // one long undifferentiated stream.
  const grouped = useMemo(() => {
    const groups = new Map()
    questions.forEach((question) => {
      const key = question.subject?.id || question.subject_id || 0
      if (!groups.has(key)) {
        groups.set(key, {
          id: key,
          name: question.subject?.name || 'Unfiled',
          code: question.subject?.code || '',
          items: [],
        })
      }
      groups.get(key).items.push(question)
    })
    return [...groups.values()]
  }, [questions])

  const toggle = (id) =>
    setSelected((current) => {
      const next = new Set(current)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })

  const activeFilterCount = Object.entries(filters).filter(
    ([key, value]) => value && value !== 'all' && key !== 'sort'
  ).length

  return (
    <div>
      <PageHeader
        title="Warehouse"
        subtitle="Every question the engine holds, filed by subject and tagged with the exams that can use it."
      />

      {summary?.unlinked_questions > 0 && (
        <Note tone="amber" icon={AlertTriangle}>
          <p className="font-medium">
            {summary.unlinked_questions} question
            {summary.unlinked_questions === 1 ? '' : 's'} no exam can reach
          </p>
          <p>
            These came from documents with no exam attached. Set an exam on the document, or
            associate an exam with the one it came from, and they become available.
          </p>
        </Note>
      )}

      {/* Subject rollups double as the primary filter. */}
      {summary?.subjects?.length > 0 && (
        <div className="my-5 grid gap-3 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
          {summary.subjects.map((subject) => {
            const active = String(filters.subject_id) === String(subject.subject_id)
            return (
              <button
                key={subject.subject_id}
                type="button"
                onClick={() =>
                  setFilter({ subject_id: active ? '' : String(subject.subject_id) })
                }
                className={`rounded-xl border p-4 text-left transition-all ${
                  active
                    ? 'border-primary-500 bg-primary-50 shadow-sm'
                    : 'border-gray-200 bg-white hover:border-primary-300'
                }`}
              >
                <div className="flex items-start justify-between gap-2">
                  <p className="min-w-0 truncate font-medium text-gray-900">{subject.name}</p>
                  <span className="text-lg font-bold tabular-nums text-gray-900">
                    {subject.total}
                  </span>
                </div>
                <div className="mt-2 flex flex-wrap gap-1">
                  {/* "Ready" is the number that matters when planning a paper.
                      The total includes questions the generator will refuse. */}
                  <Badge
                    tone="green"
                    title="Passed every quality check and can be used in a paper"
                  >
                    {subject.deliverable ?? 0} ready
                  </Badge>
                  {subject.needs_review > 0 && (
                    <Badge tone="amber" title="Held for a reviewer's decision">
                      {subject.needs_review} review
                    </Badge>
                  )}
                  {subject.failed > 0 && (
                    <Badge tone="red" title="Rejected: defects that would reach a student">
                      {subject.failed} rejected
                    </Badge>
                  )}
                  {subject.unchecked > 0 && (
                    <Badge tone="gray" title="Validation has not run yet">
                      {subject.unchecked} unchecked
                    </Badge>
                  )}
                </div>
                <div className="mt-2 flex gap-2 text-xs text-gray-500">
                  <span title="Questions with a known answer">{subject.answered} answered</span>
                  <span className="text-gray-300">·</span>
                  <span>E {subject.easy}</span>
                  <span>M {subject.medium}</span>
                  <span>H {subject.hard}</span>
                </div>
              </button>
            )
          })}
        </div>
      )}

      <Card className="mb-5">
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
          <Field label="Search">
            <div className="relative">
              <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-gray-400" />
              <Input
                className="pl-9"
                placeholder="Question text"
                value={filters.search}
                onChange={(event) => setFilter({ search: event.target.value })}
              />
            </div>
          </Field>
          <Field label="Exam" hint="Includes questions borrowed through associations.">
            <Select
              value={filters.exam_id}
              onChange={(event) => setFilter({ exam_id: event.target.value })}
            >
              <option value="">Every exam</option>
              {exams.map((exam) => (
                <option key={exam.id} value={exam.id}>
                  {exam.name}
                </option>
              ))}
            </Select>
          </Field>
          <Field label="Subject">
            <Select
              value={filters.subject_id}
              onChange={(event) => setFilter({ subject_id: event.target.value })}
            >
              <option value="">Every subject</option>
              {subjects.map((subject) => (
                <option key={subject.id} value={subject.id}>
                  {subject.name}
                </option>
              ))}
            </Select>
          </Field>
          <Field label="Status">
            <Select
              value={filters.status}
              onChange={(event) => setFilter({ status: event.target.value })}
            >
              <option value="all">Any status</option>
              <option value="approved">Approved</option>
              <option value="pending">Pending review</option>
              <option value="rejected">Rejected</option>
            </Select>
          </Field>
          <Field label="Difficulty">
            <Select
              value={filters.difficulty}
              onChange={(event) => setFilter({ difficulty: event.target.value })}
            >
              <option value="all">Any difficulty</option>
              <option value="easy">Easy</option>
              <option value="medium">Medium</option>
              <option value="hard">Hard</option>
            </Select>
          </Field>
          <Field label="Origin">
            <Select
              value={filters.origin}
              onChange={(event) => setFilter({ origin: event.target.value })}
            >
              <option value="all">Any origin</option>
              <option value="extracted">Read from a document</option>
              <option value="manual">Written by hand</option>
              <option value="generated">Generated</option>
            </Select>
          </Field>
          <Field
            label="Quality"
            hint="Only questions marked ready can go into a paper."
          >
            <Select
              value={filters.quality_status}
              onChange={(event) => setFilter({ quality_status: event.target.value })}
            >
              <option value="all">Any verdict</option>
              <option value="pass">Ready to use</option>
              <option value="review">Needs review</option>
              <option value="failed">Rejected</option>
              <option value="unchecked">Not checked</option>
            </Select>
          </Field>
          <Field label="Answer" hint="A question needs an answer to be marked.">
            <Select
              value={filters.has_answer}
              onChange={(event) => setFilter({ has_answer: event.target.value })}
            >
              <option value="">Answered or not</option>
              <option value="true">Has an answer</option>
              <option value="false">Missing an answer</option>
            </Select>
          </Field>
          <Field label="Sort">
            <Select
              value={filters.sort}
              onChange={(event) => setFilter({ sort: event.target.value })}
            >
              <option value="newest">Newest first</option>
              <option value="oldest">Oldest first</option>
              <option value="number">Question number</option>
              <option value="subject">Subject</option>
            </Select>
          </Field>
        </div>

        {activeFilterCount > 0 && (
          <div className="mt-3 flex items-center gap-2 border-t border-gray-100 pt-3">
            <span className="text-xs text-gray-500">
              {activeFilterCount} filter{activeFilterCount === 1 ? '' : 's'} active
            </span>
            <Button variant="ghost" icon={X} onClick={() => setFilter(EMPTY_FILTERS)}>
              Clear
            </Button>
          </div>
        )}
      </Card>

      {selected.size > 0 && (
        <div className="mb-4 flex flex-wrap items-center gap-2 rounded-lg border border-primary-200 bg-primary-50 p-3">
          <span className="text-sm font-medium text-primary-900">
            {selected.size} selected
          </span>
          <Button
            variant="secondary"
            icon={ThumbsUp}
            loading={bulk.isPending}
            onClick={() => bulk.mutate({ status: 'approved' })}
          >
            Approve
          </Button>
          <Button
            variant="secondary"
            icon={ThumbsDown}
            loading={bulk.isPending}
            onClick={() => bulk.mutate({ status: 'rejected' })}
          >
            Reject
          </Button>
          <Select
            className="w-auto py-1.5 text-xs"
            value=""
            onChange={(event) => {
              if (event.target.value) bulk.mutate({ difficulty: event.target.value })
            }}
          >
            <option value="">Set difficulty…</option>
            <option value="easy">Easy</option>
            <option value="medium">Medium</option>
            <option value="hard">Hard</option>
          </Select>
          <Select
            className="w-auto py-1.5 text-xs"
            value=""
            onChange={(event) => {
              if (event.target.value) bulk.mutate({ subject_id: Number(event.target.value) })
            }}
          >
            <option value="">Move to subject…</option>
            {subjects.map((subject) => (
              <option key={subject.id} value={subject.id}>
                {subject.name}
              </option>
            ))}
          </Select>
          <Button variant="ghost" onClick={() => setSelected(new Set())}>
            Clear selection
          </Button>
        </div>
      )}

      <ErrorNote error={error || bulk.error || remove.error} className="mb-4" />

      {isLoading ? (
        <Loading label="Loading questions" />
      ) : questions.length === 0 ? (
        <EmptyState
          icon={WarehouseIcon}
          title={activeFilterCount ? 'Nothing matches those filters' : 'The warehouse is empty'}
          message={
            activeFilterCount
              ? 'Loosen a filter, or check whether the material for this subject has been processed yet.'
              : 'Upload a question paper on the Documents page. Once it is processed, its questions land here.'
          }
        />
      ) : (
        <>
          <div className="space-y-6">
            {grouped.map((group) => (
              <div key={group.id}>
                <div className="mb-2 flex items-center gap-2">
                  <h2 className="text-sm font-semibold uppercase tracking-wide text-gray-500">
                    {group.name}
                  </h2>
                  <Badge tone="gray">{group.items.length} on this page</Badge>
                </div>
                <div className="space-y-3">
                  {group.items.map((question) => (
                    <QuestionRow
                      key={question.id}
                      question={question}
                      selected={selected.has(question.id)}
                      onToggle={() => toggle(question.id)}
                      onReview={(verdict) => review.mutate({ id: question.id, verdict })}
                      onDelete={() => {
                        if (window.confirm('Delete this question permanently?')) {
                          remove.mutate(question.id)
                        }
                      }}
                    />
                  ))}
                </div>
              </div>
            ))}
          </div>
          <Pagination meta={meta} page={page} onChange={setPage} />
        </>
      )}
    </div>
  )
}

function QuestionRow({ question, selected, onToggle, onReview, onDelete }) {
  const [expanded, setExpanded] = useState(false)
  const options = question.options || []
  const tags = question.exam_tags || []

  return (
    <Card className={selected ? 'ring-2 ring-primary-400' : ''}>
      <div className="flex gap-3">
        <input
          type="checkbox"
          aria-label={`Select question ${question.id}`}
          checked={selected}
          onChange={onToggle}
          className="mt-1 h-4 w-4 flex-shrink-0 rounded border-gray-300 text-primary-600 focus:ring-primary-400"
        />

        <div className="min-w-0 flex-1">
          <p
            className={`text-sm leading-relaxed text-gray-900 ${expanded ? '' : 'line-clamp-3'}`}
            onClick={() => setExpanded((value) => !value)}
            role="button"
            tabIndex={0}
            onKeyDown={(event) => {
              if (event.key === 'Enter') setExpanded((value) => !value)
            }}
          >
            {question.question_number > 0 && (
              <span className="mr-1 font-semibold text-gray-500">{question.question_number}.</span>
            )}
            {question.question_text}
          </p>

          {options.length > 0 && (
            <div className="mt-2 grid gap-1 sm:grid-cols-2">
              {options.map((option) => (
                <div
                  key={option.id}
                  className={`flex items-start gap-1.5 rounded px-2 py-1 text-xs ${
                    option.is_correct
                      ? 'bg-green-50 font-medium text-green-800'
                      : 'text-gray-600'
                  }`}
                >
                  <span className="font-semibold">{option.label}.</span>
                  <span className="min-w-0">{option.text}</span>
                  {option.is_correct && (
                    <CheckCircle2 className="ml-auto h-3.5 w-3.5 flex-shrink-0 text-green-600" />
                  )}
                </div>
              ))}
            </div>
          )}

          {expanded && question.explanation && (
            <p className="mt-2 rounded bg-blue-50 p-2 text-xs text-blue-900">
              <span className="font-semibold">Explanation: </span>
              {question.explanation}
            </p>
          )}

          <div className="mt-3 flex flex-wrap items-center gap-1.5">
            {/* The quality verdict comes first: it is what decides whether this
                question can be used at all. */}
            <QualityBadge status={question.quality_status} score={question.quality_score} />
            <Badge tone={difficultyTone(question.difficulty)}>
              {question.difficulty}
              {question.difficulty_confidence <= 0 ? ' (unrated)' : ''}
            </Badge>
            <StatusPill status={question.status} />
            {!question.has_answer && <Badge tone="red">no answer</Badge>}
            {question.year && <Badge tone="gray">{question.year}</Badge>}
            {question.topic && <Badge tone="purple">{question.topic.name}</Badge>}
            <Badge tone="gray">{question.origin}</Badge>

            {/* Exam tags: every exam this question currently serves. */}
            {tags.map((tag) => (
              <Badge
                key={`${tag.exam_id}-${tag.link_type}`}
                tone={tag.link_type === 'direct' ? 'primary' : 'blue'}
                title={
                  tag.link_type === 'direct'
                    ? 'Extracted from this exam’s own material'
                    : 'Borrowed through an association'
                }
              >
                {tag.link_type === 'associated' && <Link2 className="h-3 w-3" />}
                {tag.name}
              </Badge>
            ))}
            {tags.length === 0 && <Badge tone="amber">no exam</Badge>}
          </div>

          {/* Findings are shown inline rather than only on the review page, so a
              defect is visible wherever the question is. */}
          {(question.quality_issues?.length > 0 || question.trailing_text) && (
            <div className="mt-3 space-y-2 border-t border-gray-100 pt-3">
              {question.quality_issues?.length > 0 && (
                <IssueList issues={question.quality_issues} compact={!expanded} limit={expanded ? undefined : 2} />
              )}
              {expanded && question.trailing_text && (
                <div className="rounded border border-amber-200 bg-amber-50 p-2">
                  <p className="text-xs font-semibold text-amber-900">Text set aside</p>
                  <p className="text-xs text-amber-800">{question.trailing_text}</p>
                </div>
              )}
              <Link
                to="/review"
                className="inline-block text-xs text-primary-700 hover:underline"
              >
                Open in review
              </Link>
            </div>
          )}
        </div>

        <div className="flex flex-shrink-0 flex-col gap-0.5">
          {question.status !== 'approved' && (
            <IconButton icon={ThumbsUp} tone="green" title="Approve" onClick={() => onReview('approved')} />
          )}
          {question.status !== 'rejected' && (
            <IconButton icon={ThumbsDown} tone="gray" title="Reject" onClick={() => onReview('rejected')} />
          )}
          <IconButton icon={Trash2} tone="red" title="Delete" onClick={onDelete} />
        </div>
      </div>
    </Card>
  )
}
