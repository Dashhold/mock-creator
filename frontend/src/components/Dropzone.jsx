import { useCallback, useMemo, useRef, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { UploadCloud, File, FileText, X, CheckCircle2, AlertTriangle } from 'lucide-react'
import { api } from '../api/client'
import { useCapabilities, useExamOptions } from '../api/hooks'
import {
  Badge,
  Button,
  Checkbox,
  ErrorNote,
  Field,
  Input,
  ProgressBar,
  Select,
  Textarea,
  formatBytes,
} from './ui'

const KIND_LABELS = {
  question_paper: 'Question paper',
  answer_key: 'Answer key',
  solution: 'Solutions',
  book: 'Book or reference',
  notes: 'Notes',
  syllabus: 'Syllabus',
  other: 'Other',
}

const OCR_MODE_HINTS = {
  auto: 'Measure the text layer and only run OCR on pages that need it. Best default.',
  force: 'Run OCR on every page. Slower, but the right choice for photocopied scans.',
  off: 'Never run OCR. Fastest, and only works on documents with real text.',
}

const MODES = [
  { id: 'files', label: 'Upload files' },
  { id: 'text', label: 'Paste text' },
]

/**
 * Dropzone is the single upload surface. Files go straight into the database
 * through the API, so there is nothing to place on disk beforehand. Text can be
 * pasted instead of uploaded; either way what the system keeps is the text.
 */
export default function Dropzone({
  defaultKind = 'question_paper',
  defaultExamId = '',
  lockExam = false,
  lockKind = false,
  onUploaded,
  compact = false,
}) {
  const queryClient = useQueryClient()
  const inputRef = useRef(null)
  const { data: capabilities } = useCapabilities()
  const { data: exams = [] } = useExamOptions()

  const [mode, setMode] = useState('files')
  const [files, setFiles] = useState([])
  const [pasted, setPasted] = useState({ title: '', text: '' })
  const [dragging, setDragging] = useState(false)
  const [progress, setProgress] = useState(0)
  const [outcomes, setOutcomes] = useState(null)
  const [form, setForm] = useState({
    kind: defaultKind,
    exam_id: defaultExamId ? String(defaultExamId) : '',
    year: '',
    ocr_mode: '',
    ocr_engine: '',
    auto_ingest: true,
  })

  const extensions = capabilities?.upload?.allowed_extensions || []
  const maxBytes = capabilities?.upload?.max_bytes || 0
  const keepOriginals = Boolean(capabilities?.upload?.keep_originals)
  const ocrEngines = capabilities?.converter?.ocr_engines || []
  const ocrModes = capabilities?.converter?.ocr_modes || ['auto', 'force', 'off']
  const kinds = capabilities?.document_kinds || Object.keys(KIND_LABELS)

  const accept = useMemo(
    () => extensions.map((ext) => `.${ext}`).join(','),
    [extensions]
  )

  const addFiles = useCallback(
    (incoming) => {
      const next = Array.from(incoming).filter((file) => {
        if (!extensions.length) return true
        const ext = file.name.split('.').pop()?.toLowerCase()
        return extensions.includes(ext)
      })
      setOutcomes(null)
      setFiles((current) => {
        const seen = new Set(current.map((f) => `${f.name}:${f.size}`))
        return [...current, ...next.filter((f) => !seen.has(`${f.name}:${f.size}`))]
      })
    },
    [extensions]
  )

  const rejected = useMemo(() => {
    if (!extensions.length) return []
    return []
  }, [extensions])

  const afterSave = (payload) => {
    const results = payload?.data ?? []
    setOutcomes(results)
    queryClient.invalidateQueries({ queryKey: ['documents'] })
    queryClient.invalidateQueries({ queryKey: ['jobs'] })
    queryClient.invalidateQueries({ queryKey: ['overview'] })
    if (onUploaded) onUploaded(results)
  }

  const upload = useMutation({
    mutationFn: () =>
      api.uploadDocuments(
        {
          files,
          kind: form.kind,
          examId: form.exam_id || undefined,
          year: form.year || undefined,
          ocrMode: form.ocr_mode || undefined,
          ocrEngine: form.ocr_engine || undefined,
          autoIngest: form.auto_ingest,
        },
        setProgress
      ),
    onSuccess: (payload) => {
      setFiles([])
      setProgress(0)
      afterSave(payload)
    },
  })

  const saveText = useMutation({
    mutationFn: () =>
      api.createTextDocument({
        title: pasted.title.trim(),
        text: pasted.text,
        kind: form.kind,
        examId: form.exam_id || undefined,
        year: form.year || undefined,
        autoIngest: form.auto_ingest,
      }),
    onSuccess: (payload) => {
      setPasted({ title: '', text: '' })
      afterSave(payload)
    },
  })

  const onDrop = (event) => {
    event.preventDefault()
    setDragging(false)
    if (event.dataTransfer?.files?.length) addFiles(event.dataTransfer.files)
  }

  const totalSize = files.reduce((sum, file) => sum + file.size, 0)
  const tooLarge = maxBytes > 0 && files.some((file) => file.size > maxBytes)
  const hasText = pasted.text.trim().length > 0
  const isText = mode === 'text'

  const describe = (outcome) => {
    if (outcome.error) return outcome.error
    if (outcome.duplicate) {
      return isText
        ? 'The same text was saved earlier, so no new document was created'
        : 'Already uploaded earlier, so no new document was created'
    }
    if (outcome.job_id) {
      return isText ? 'Text saved and queued for question extraction' : 'Stored and queued for processing'
    }
    return isText ? 'Text saved' : 'Stored'
  }

  return (
    <div className="space-y-4">
      <div
        role="group"
        aria-label="How to add material"
        className="inline-flex rounded-lg border border-gray-200 bg-gray-50 p-0.5 text-sm"
      >
        {MODES.map((option) => (
          <button
            key={option.id}
            type="button"
            aria-pressed={mode === option.id}
            onClick={() => {
              setMode(option.id)
              setOutcomes(null)
            }}
            className={`rounded-md px-3 py-1.5 font-medium transition-colors ${
              mode === option.id
                ? 'bg-white text-primary-700 shadow-sm'
                : 'text-gray-500 hover:text-gray-800'
            }`}
          >
            {option.label}
          </button>
        ))}
      </div>

      {isText ? (
        <div className="space-y-3">
          <Field
            label="Title"
            htmlFor="dropzone-text-title"
            hint="Optional. The first line of the text is used when this is left blank."
          >
            <Input
              id="dropzone-text-title"
              placeholder="e.g. Reasoning practice set 3"
              value={pasted.title}
              onChange={(event) => setPasted({ ...pasted, title: event.target.value })}
            />
          </Field>
          <Field
            label="Text"
            htmlFor="dropzone-text-body"
            required
            hint="Questions, an answer key, notes or any other material, as plain text."
          >
            <Textarea
              id="dropzone-text-body"
              rows={compact ? 6 : 10}
              className="font-mono text-xs"
              placeholder={'1. Which of the following ...\n(a) ...\n(b) ...'}
              value={pasted.text}
              onChange={(event) => {
                setPasted({ ...pasted, text: event.target.value })
                setOutcomes(null)
              }}
            />
          </Field>
        </div>
      ) : (
        <>
          <div
            onDragOver={(event) => {
              event.preventDefault()
              setDragging(true)
            }}
            onDragLeave={() => setDragging(false)}
            onDrop={onDrop}
            onClick={() => inputRef.current?.click()}
            role="button"
            tabIndex={0}
            onKeyDown={(event) => {
              if (event.key === 'Enter' || event.key === ' ') inputRef.current?.click()
            }}
            className={`cursor-pointer rounded-xl border-2 border-dashed p-6 text-center transition-colors ${
              dragging
                ? 'border-primary-500 bg-primary-50'
                : 'border-gray-300 bg-white hover:border-primary-400 hover:bg-gray-50'
            }`}
          >
            <UploadCloud className="mx-auto mb-2 h-8 w-8 text-primary-500" />
            <p className="text-sm font-medium text-gray-800">
              Drop files here, or click to choose
            </p>
            <p className="mx-auto mt-1 max-w-md text-xs text-gray-500">
              {extensions.length
                ? `${extensions.slice(0, 10).join(', ')}${extensions.length > 10 ? ' and more' : ''}`
                : 'PDF, Word, PowerPoint, Excel, HTML, text and images'}
              {maxBytes ? ` · up to ${formatBytes(maxBytes)} each` : ''}
            </p>
            {!keepOriginals && (
              <p className="mx-auto mt-1 max-w-md text-xs text-gray-400">
                Only the extracted text is kept. Each file is dropped once it has been read.
              </p>
            )}
            <input
              ref={inputRef}
              type="file"
              multiple
              accept={accept || undefined}
              className="hidden"
              onChange={(event) => {
                addFiles(event.target.files)
                event.target.value = ''
              }}
            />
          </div>

          {files.length > 0 && (
            <div className="space-y-2 rounded-lg border border-gray-200 bg-white p-3">
              <div className="flex items-center justify-between text-xs text-gray-500">
                <span>
                  {files.length} file{files.length === 1 ? '' : 's'} · {formatBytes(totalSize)}
                </span>
                <button
                  type="button"
                  className="text-gray-500 hover:text-gray-700"
                  onClick={() => setFiles([])}
                >
                  Clear all
                </button>
              </div>
              {files.map((file, index) => (
                <div key={`${file.name}-${index}`} className="flex items-center gap-2 text-sm">
                  <File className="h-4 w-4 flex-shrink-0 text-gray-400" />
                  <span className="min-w-0 flex-1 truncate text-gray-800">{file.name}</span>
                  <span className="text-xs text-gray-500">{formatBytes(file.size)}</span>
                  {maxBytes > 0 && file.size > maxBytes && (
                    <Badge tone="red">Too large</Badge>
                  )}
                  <button
                    type="button"
                    aria-label={`Remove ${file.name}`}
                    className="rounded p-1 text-gray-400 hover:bg-gray-100 hover:text-gray-600"
                    onClick={() => setFiles((current) => current.filter((_, i) => i !== index))}
                  >
                    <X className="h-3.5 w-3.5" />
                  </button>
                </div>
              ))}
            </div>
          )}
        </>
      )}

      <div className={`grid gap-3 ${compact ? 'sm:grid-cols-2' : 'sm:grid-cols-2 lg:grid-cols-3'}`}>
        {!lockKind && (
          <Field label="What is this?">
            <Select
              value={form.kind}
              onChange={(event) => setForm({ ...form, kind: event.target.value })}
            >
              {kinds.map((kind) => (
                <option key={kind} value={kind}>
                  {KIND_LABELS[kind] || kind}
                </option>
              ))}
            </Select>
          </Field>
        )}

        {!lockExam && (
          <Field label="Exam" hint="Optional. Unattached files stay reusable reference material.">
            <Select
              value={form.exam_id}
              onChange={(event) => setForm({ ...form, exam_id: event.target.value })}
            >
              <option value="">No exam</option>
              {exams.map((exam) => (
                <option key={exam.id} value={exam.id}>
                  {exam.name}
                </option>
              ))}
            </Select>
          </Field>
        )}

        <Field label="Year" hint="Used to match an answer key to its paper.">
          <Input
            type="number"
            min="1900"
            max="2100"
            placeholder="2024"
            value={form.year}
            onChange={(event) => setForm({ ...form, year: event.target.value })}
          />
        </Field>

        {!isText && (
          <Field label="OCR" hint={OCR_MODE_HINTS[form.ocr_mode || 'auto']}>
            <Select
              value={form.ocr_mode}
              onChange={(event) => setForm({ ...form, ocr_mode: event.target.value })}
            >
              <option value="">Automatic (recommended)</option>
              {ocrModes
                .filter((mode) => mode !== 'auto')
                .map((mode) => (
                  <option key={mode} value={mode}>
                    {mode === 'force' ? 'Force OCR on every page' : 'Skip OCR entirely'}
                  </option>
                ))}
            </Select>
          </Field>
        )}

        {!isText && ocrEngines.length > 1 && (
          <Field label="OCR engine">
            <Select
              value={form.ocr_engine}
              onChange={(event) => setForm({ ...form, ocr_engine: event.target.value })}
            >
              <option value="">Default</option>
              {ocrEngines.map((engine) => (
                <option key={engine} value={engine}>
                  {engine}
                </option>
              ))}
            </Select>
          </Field>
        )}
      </div>

      <Checkbox
        label="Process immediately"
        hint={
          isText
            ? 'Extract questions as soon as the text is saved. Turn off to review it first.'
            : 'Convert and extract questions as soon as the upload lands. Turn off to review the file first.'
        }
        checked={form.auto_ingest}
        onChange={(event) => setForm({ ...form, auto_ingest: event.target.checked })}
      />

      {!isText && upload.isPending && <ProgressBar value={progress} label="Uploading" />}
      <ErrorNote error={isText ? saveText.error : upload.error} />

      {isText ? (
        <Button
          icon={FileText}
          loading={saveText.isPending}
          disabled={!hasText}
          onClick={() => saveText.mutate()}
        >
          Save text
        </Button>
      ) : (
        <Button
          icon={UploadCloud}
          loading={upload.isPending}
          disabled={files.length === 0 || tooLarge}
          onClick={() => upload.mutate()}
        >
          {files.length > 1 ? `Upload ${files.length} files` : 'Upload'}
        </Button>
      )}

      {outcomes && outcomes.length > 0 && (
        <div className="space-y-1.5 rounded-lg border border-gray-200 bg-gray-50 p-3 text-sm">
          {outcomes.map((outcome, index) => (
            <div key={index} className="flex items-start gap-2">
              {outcome.error ? (
                <AlertTriangle className="mt-0.5 h-4 w-4 flex-shrink-0 text-amber-600" />
              ) : (
                <CheckCircle2 className="mt-0.5 h-4 w-4 flex-shrink-0 text-green-600" />
              )}
              <div className="min-w-0">
                <p className="truncate font-medium text-gray-800">
                  {isText ? outcome.document?.title || outcome.filename : outcome.filename}
                </p>
                <p className="text-xs text-gray-600">{describe(outcome)}</p>
              </div>
            </div>
          ))}
        </div>
      )}
      {rejected.length > 0 && <ErrorNote error={{ message: rejected.join(', ') }} />}
    </div>
  )
}
