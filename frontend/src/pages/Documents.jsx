import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Download,
  FileStack,
  Play,
  RefreshCw,
  Search,
  Trash2,
  UploadCloud,
  Eye,
  ScanText,
  Repeat,
} from 'lucide-react'
import { api } from '../api/client'
import { useExamOptions } from '../api/hooks'
import Dropzone from '../components/Dropzone'
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
  Modal,
  Note,
  PageHeader,
  Pagination,
  ProgressBar,
  Select,
  StatusPill,
  Table,
  formatBytes,
  formatDate,
  relativeTime,
} from '../components/ui'

const KIND_LABELS = {
  question_paper: 'Question paper',
  answer_key: 'Answer key',
  solution: 'Solutions',
  book: 'Book',
  notes: 'Notes',
  syllabus: 'Syllabus',
  other: 'Other',
}

export default function Documents() {
  const queryClient = useQueryClient()
  const { data: exams = [] } = useExamOptions()

  const [uploadOpen, setUploadOpen] = useState(false)
  const [inspecting, setInspecting] = useState(null)
  const [page, setPage] = useState(1)
  const [filters, setFilters] = useState({ search: '', kind: 'all', status: 'all', exam_id: '' })

  const { data, isLoading, error } = useQuery({
    queryKey: ['documents', { ...filters, page }],
    queryFn: () => api.getDocuments({ ...filters, page, page_size: 25 }),
    // Polling keeps status and progress live while anything is processing.
    refetchInterval: (query) => {
      const rows = query.state.data?.data ?? []
      return rows.some((doc) => !['completed', 'failed'].includes(doc.status)) ? 3000 : 20000
    },
  })

  const documents = data?.data ?? []
  const meta = data?.meta

  const refresh = () => {
    queryClient.invalidateQueries({ queryKey: ['documents'] })
    queryClient.invalidateQueries({ queryKey: ['jobs'] })
  }

  const ingest = useMutation({
    mutationFn: ({ id, body }) => api.ingestDocument(id, body),
    onSuccess: refresh,
  })
  const reparse = useMutation({
    mutationFn: (id) => api.reparseDocument(id),
    onSuccess: refresh,
  })
  const remove = useMutation({
    mutationFn: (id) => api.deleteDocument(id),
    onSuccess: () => {
      refresh()
      queryClient.invalidateQueries({ queryKey: ['warehouse'] })
      queryClient.invalidateQueries({ queryKey: ['questions'] })
    },
  })
  const attach = useMutation({
    mutationFn: ({ id, examId }) =>
      api.updateDocument(id, examId ? { exam_id: Number(examId) } : { detach_exam: true }),
    onSuccess: refresh,
  })

  return (
    <div>
      <PageHeader
        title="Documents"
        subtitle="Upload any readable file. It is stored in the database, converted, and mined for questions. Nothing needs to be placed on the server by hand."
        actions={
          <Button icon={UploadCloud} onClick={() => setUploadOpen(true)}>
            Upload
          </Button>
        }
      />

      <Card className="mb-5">
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
          <Field label="Search">
            <div className="relative">
              <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-gray-400" />
              <Input
                className="pl-9"
                placeholder="Title or filename"
                value={filters.search}
                onChange={(event) => {
                  setFilters({ ...filters, search: event.target.value })
                  setPage(1)
                }}
              />
            </div>
          </Field>
          <Field label="Kind">
            <Select
              value={filters.kind}
              onChange={(event) => {
                setFilters({ ...filters, kind: event.target.value })
                setPage(1)
              }}
            >
              <option value="all">All kinds</option>
              {Object.entries(KIND_LABELS).map(([value, label]) => (
                <option key={value} value={value}>
                  {label}
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
              <option value="pending">Pending</option>
              <option value="queued">Queued</option>
              <option value="processing">Processing</option>
              <option value="completed">Ready</option>
              <option value="failed">Failed</option>
            </Select>
          </Field>
          <Field label="Exam">
            <Select
              value={filters.exam_id}
              onChange={(event) => {
                setFilters({ ...filters, exam_id: event.target.value })
                setPage(1)
              }}
            >
              <option value="">Any exam</option>
              {exams.map((exam) => (
                <option key={exam.id} value={exam.id}>
                  {exam.name}
                </option>
              ))}
            </Select>
          </Field>
        </div>
      </Card>

      <ErrorNote error={error} className="mb-4" />
      <ErrorNote error={ingest.error || reparse.error || remove.error} className="mb-4" />

      {isLoading ? (
        <Loading label="Loading documents" />
      ) : documents.length === 0 ? (
        <EmptyState
          icon={FileStack}
          title="No documents yet"
          message="Upload a question paper, an answer key, a book, notes, or a scan. Whatever the converter can read becomes content you can build papers from."
          action={
            <Button icon={UploadCloud} onClick={() => setUploadOpen(true)}>
              Upload your first file
            </Button>
          }
        />
      ) : (
        <>
          <Table
            head={[
              'Document',
              'Kind',
              'Exam',
              { label: 'Questions', align: 'right' },
              'Status',
              { label: '', align: 'right' },
            ]}
          >
            {documents.map((doc) => (
              <tr key={doc.id} className="hover:bg-gray-50">
                <td className="px-4 py-3">
                  <p className="font-medium text-gray-900">{doc.title}</p>
                  <p className="text-xs text-gray-500">
                    {doc.filename} · {formatBytes(doc.size_bytes)}
                    {doc.page_count ? ` · ${doc.page_count} pages` : ''}
                    {doc.year ? ` · ${doc.year}` : ''} · {relativeTime(doc.created_at)}
                  </p>
                  {/* How the document was read, and how well. A low confidence
                      score is the reason its questions land in review, so it
                      belongs next to the document rather than buried. */}
                  {(doc.engine || doc.extraction_confidence > 0) && (
                    <p className="mt-1 flex flex-wrap items-center gap-1.5">
                      {doc.engine && (
                        <Badge
                          tone="gray"
                          title={
                            doc.engine === 'geometry'
                              ? 'Read straight from the PDF text layer with exact character positions'
                              : 'Read with the layout model, which infers reading order'
                          }
                        >
                          {doc.engine}
                        </Badge>
                      )}
                      {doc.extraction_confidence > 0 && (
                        <Badge
                          tone={
                            doc.extraction_confidence >= 0.85
                              ? 'green'
                              : doc.extraction_confidence >= 0.65
                              ? 'amber'
                              : 'red'
                          }
                          title="How reliably this document could be read"
                        >
                          {Math.round(doc.extraction_confidence * 100)}% confidence
                        </Badge>
                      )}
                      {doc.flagged_count > 0 && (
                        <Badge tone="amber" title="Questions from this document held for review">
                          {doc.flagged_count} flagged
                        </Badge>
                      )}
                    </p>
                  )}
                  {doc.notes && <p className="mt-0.5 text-xs text-gray-400">{doc.notes}</p>}
                  {(doc.warnings || []).slice(0, 2).map((warning, index) => (
                    <p key={index} className="mt-0.5 text-xs text-amber-700">
                      {warning}
                    </p>
                  ))}
                </td>
                <td className="px-4 py-3">
                  <Badge tone={doc.kind === 'answer_key' ? 'purple' : 'blue'}>
                    {KIND_LABELS[doc.kind] || doc.kind}
                  </Badge>
                </td>
                <td className="px-4 py-3">
                  <Select
                    className="min-w-[150px] py-1 text-xs"
                    value={doc.exam_id || ''}
                    onChange={(event) => attach.mutate({ id: doc.id, examId: event.target.value })}
                  >
                    <option value="">No exam</option>
                    {exams.map((exam) => (
                      <option key={exam.id} value={exam.id}>
                        {exam.name}
                      </option>
                    ))}
                  </Select>
                </td>
                <td className="px-4 py-3 text-right tabular-nums text-gray-700">
                  {doc.question_count || '—'}
                </td>
                <td className="px-4 py-3">
                  {doc.active_job ? (
                    <div className="w-32">
                      <ProgressBar value={doc.active_job.progress} label={doc.active_job.stage} />
                    </div>
                  ) : (
                    <StatusPill status={doc.status} />
                  )}
                </td>
                <td className="px-4 py-3">
                  <div className="flex justify-end gap-0.5">
                    <IconButton
                      icon={Eye}
                      title="Inspect conversion"
                      tone="primary"
                      onClick={() => setInspecting(doc)}
                    />
                    {doc.status === 'completed' ? (
                      <IconButton
                        icon={RefreshCw}
                        title="Re-extract questions from the cached conversion"
                        onClick={() => reparse.mutate(doc.id)}
                      />
                    ) : (
                      <IconButton
                        icon={Play}
                        title="Process now"
                        tone="green"
                        disabled={Boolean(doc.active_job)}
                        onClick={() => ingest.mutate({ id: doc.id, body: {} })}
                      />
                    )}
                    {/* Re-reading with the other engine is the first thing to try
                        when a document came out badly, so it is one click from the
                        list rather than buried in a dialog. */}
                    {doc.engine && doc.engine !== 'plain-text' && (
                      <IconButton
                        icon={Repeat}
                        title={`Re-read with the ${
                          doc.engine === 'geometry' ? 'layout model' : 'text layer'
                        } instead`}
                        onClick={() =>
                          ingest.mutate({
                            id: doc.id,
                            body: {
                              engine: doc.engine === 'geometry' ? 'docling' : 'geometry',
                              reconvert: true,
                              no_fallback: true,
                            },
                          })
                        }
                      />
                    )}
                    {(doc.status === 'failed' || doc.extraction_confidence < 0.7) && (
                      <IconButton
                        icon={ScanText}
                        title="Retry with OCR forced on every page"
                        tone="primary"
                        onClick={() =>
                          ingest.mutate({
                            id: doc.id,
                            body: { ocr_mode: 'force', reconvert: true },
                          })
                        }
                      />
                    )}
                    <a href={api.downloadUrl(doc.id)} download>
                      <IconButton icon={Download} title="Download the original" />
                    </a>
                    <IconButton
                      icon={Trash2}
                      tone="red"
                      title="Delete"
                      onClick={() => {
                        if (
                          window.confirm(
                            `Delete "${doc.title}"? Its questions go too, unless they are already used in a paper.`
                          )
                        ) {
                          remove.mutate(doc.id)
                        }
                      }}
                    />
                  </div>
                </td>
              </tr>
            ))}
          </Table>
          <Pagination meta={meta} page={page} onChange={setPage} />
        </>
      )}

      {uploadOpen && (
        <Modal
          title="Upload documents"
          subtitle="PDF, Word, PowerPoint, Excel, HTML, text and images. Scans are handled with OCR."
          size="md"
          onClose={() => setUploadOpen(false)}
        >
          <Dropzone onUploaded={() => refresh()} />
        </Modal>
      )}

      {inspecting && <InspectModal doc={inspecting} onClose={() => setInspecting(null)} />}
    </div>
  )
}

/** InspectModal shows what the converter actually produced for a document. */
function InspectModal({ doc, onClose }) {
  const [tab, setTab] = useState('summary')

  const { data: detail } = useQuery({
    queryKey: ['document', String(doc.id)],
    queryFn: () => api.getDocument(doc.id),
    select: (payload) => payload?.data,
  })

  const { data: conversion, isLoading: loadingText, error: textError } = useQuery({
    queryKey: ['document', String(doc.id), 'conversion'],
    queryFn: () => api.getConversion(doc.id, { limit: 60000 }),
    select: (payload) => payload?.data,
    enabled: tab === 'text',
    retry: false,
  })

  const { data: blocks } = useQuery({
    queryKey: ['document', String(doc.id), 'blocks'],
    queryFn: () => api.getBlocks(doc.id, { page_size: 100 }),
    select: (payload) => payload?.data ?? [],
    enabled: tab === 'blocks',
  })

  const summary = detail?.conversion
  const jobs = detail?.jobs ?? []

  return (
    <Modal title={doc.title} subtitle={doc.filename} size="lg" onClose={onClose}>
      <div className="mb-4 flex gap-1 border-b border-gray-200">
        {[
          { id: 'summary', label: 'Summary' },
          { id: 'text', label: 'Converted text' },
          { id: 'blocks', label: 'Structure' },
        ].map((item) => (
          <button
            key={item.id}
            type="button"
            onClick={() => setTab(item.id)}
            className={`-mb-px border-b-2 px-3 py-2 text-sm font-medium ${
              tab === item.id
                ? 'border-primary-600 text-primary-700'
                : 'border-transparent text-gray-500 hover:text-gray-800'
            }`}
          >
            {item.label}
          </button>
        ))}
      </div>

      {tab === 'summary' && (
        <div className="space-y-4">
          {summary ? (
            <div className="grid grid-cols-2 gap-3 text-sm sm:grid-cols-3">
              <Cell label="Engine" value={`${summary.engine} ${summary.engine_version || ''}`} />
              <Cell label="OCR" value={summary.ocr_applied ? summary.ocr_engine || 'applied' : 'not needed'} />
              <Cell label="Pages" value={summary.page_count || '—'} />
              <Cell label="Characters" value={(summary.char_count || 0).toLocaleString()} />
              <Cell label="Tables" value={summary.table_count || 0} />
              <Cell
                label="Text layer"
                value={`${Math.round((summary.text_ratio || 0) * 100)}%`}
              />
            </div>
          ) : (
            <Note tone="amber">
              <p>This document has not been converted yet.</p>
            </Note>
          )}

          <div>
            <p className="mb-2 text-sm font-semibold text-gray-900">Questions extracted</p>
            <p className="text-sm text-gray-600">
              {detail?.question_count ?? 0} question{detail?.question_count === 1 ? '' : 's'} in the
              warehouse from this document.
            </p>
          </div>

          {jobs.length > 0 && (
            <div>
              <p className="mb-2 text-sm font-semibold text-gray-900">Processing history</p>
              <div className="space-y-1.5">
                {jobs.map((job) => (
                  <div
                    key={job.id}
                    className="flex flex-wrap items-center justify-between gap-2 rounded border border-gray-200 px-3 py-2 text-xs"
                  >
                    <span className="text-gray-600">
                      {job.type.replace(/_/g, ' ')} · {formatDate(job.created_at)}
                    </span>
                    <div className="flex items-center gap-2">
                      {job.error && (
                        <span className="max-w-sm truncate text-red-600" title={job.error}>
                          {job.error}
                        </span>
                      )}
                      <StatusPill status={job.status} />
                    </div>
                  </div>
                ))}
              </div>
            </div>
          )}
        </div>
      )}

      {tab === 'text' && (
        <div>
          {loadingText ? (
            <Loading label="Loading converted text" className="py-10" />
          ) : textError ? (
            <Note tone="amber">
              <p>No conversion is stored for this document yet.</p>
            </Note>
          ) : (
            <>
              {conversion?.truncated && (
                <p className="mb-2 text-xs text-gray-500">
                  Showing the first {(conversion.markdown || '').length.toLocaleString()} characters.
                </p>
              )}
              <pre className="max-h-[50vh] overflow-auto whitespace-pre-wrap rounded-lg bg-gray-900 p-4 text-xs leading-relaxed text-gray-100">
                {conversion?.markdown || 'Nothing was extracted.'}
              </pre>
            </>
          )}
        </div>
      )}

      {tab === 'blocks' && (
        <div className="space-y-1.5">
          {!blocks ? (
            <Loading label="Loading structure" className="py-10" />
          ) : blocks.length === 0 ? (
            <Note tone="amber">
              <p>No structural blocks were recorded for this document.</p>
            </Note>
          ) : (
            blocks.map((block) => (
              <div key={block.id} className="rounded border border-gray-200 p-2 text-xs">
                <div className="mb-1 flex items-center gap-2">
                  <Badge tone={block.block_type === 'heading' ? 'primary' : 'gray'}>
                    {block.block_type}
                  </Badge>
                  <span className="text-gray-400">page {block.page_no || '?'}</span>
                </div>
                <p className="line-clamp-3 text-gray-700">{block.text}</p>
              </div>
            ))
          )}
        </div>
      )}
    </Modal>
  )
}

function Cell({ label, value }) {
  return (
    <div>
      <p className="text-xs text-gray-500">{label}</p>
      <p className="font-medium text-gray-900">{value}</p>
    </div>
  )
}
