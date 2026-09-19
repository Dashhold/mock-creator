import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  ArrowRight,
  Check,
  FileSearch,
  ListOrdered,
  Sparkles,
  SkipForward,
} from 'lucide-react'
import { api } from '../api/client'
import { useJobTracker } from '../api/hooks'
import AssociationEditor from './AssociationEditor'
import Dropzone from './Dropzone'
import PatternEditor from './PatternEditor'
import {
  Badge,
  Button,
  Card,
  ErrorNote,
  Field,
  Input,
  Modal,
  Note,
  ProgressBar,
  Select,
  Textarea,
} from './ui'

const STEPS = [
  { id: 'details', label: 'Exam' },
  { id: 'pattern', label: 'Pattern' },
  { id: 'associations', label: 'Associations' },
]

/**
 * ExamWizard walks a user through setting up an exam from nothing: name it,
 * establish its pattern (typed in or inferred from real papers), then optionally
 * point it at exams it can borrow content from.
 *
 * The exam is created at the end of the first step so the later steps have
 * something real to attach documents and associations to.
 */
export default function ExamWizard({ onClose, onFinished }) {
  const queryClient = useQueryClient()
  const [step, setStep] = useState('details')
  const [exam, setExam] = useState(null)
  const [patternMode, setPatternMode] = useState(null)

  const finish = () => {
    queryClient.invalidateQueries({ queryKey: ['exams'] })
    queryClient.invalidateQueries({ queryKey: ['overview'] })
    if (onFinished) onFinished(exam)
    onClose()
  }

  const stepIndex = STEPS.findIndex((s) => s.id === step)

  return (
    <Modal
      title={exam ? `Set up ${exam.name}` : 'New exam'}
      subtitle="The engine starts knowing nothing about this exam. These steps tell it what it needs."
      onClose={onClose}
      size="lg"
    >
      <ol className="mb-6 flex items-center gap-2 text-sm">
        {STEPS.map((s, index) => {
          const done = index < stepIndex
          const active = index === stepIndex
          return (
            <li key={s.id} className="flex items-center gap-2">
              <span
                className={`flex h-6 w-6 items-center justify-center rounded-full text-xs font-semibold ${
                  done
                    ? 'bg-green-600 text-white'
                    : active
                    ? 'bg-primary-600 text-white'
                    : 'bg-gray-200 text-gray-600'
                }`}
              >
                {done ? <Check className="h-3.5 w-3.5" /> : index + 1}
              </span>
              <span className={active ? 'font-medium text-gray-900' : 'text-gray-500'}>
                {s.label}
              </span>
              {index < STEPS.length - 1 && <span className="mx-1 text-gray-300">›</span>}
            </li>
          )
        })}
      </ol>

      {step === 'details' && (
        <DetailsStep
          onCreated={(createdExam) => {
            setExam(createdExam)
            setStep('pattern')
          }}
        />
      )}

      {step === 'pattern' && exam && (
        <PatternStep
          exam={exam}
          mode={patternMode}
          setMode={setPatternMode}
          onDone={() => setStep('associations')}
        />
      )}

      {step === 'associations' && exam && (
        <div className="space-y-5">
          <Note>
            <p className="font-medium">Reuse content you already have</p>
            <p>
              A new exam has no questions of its own yet. Associating it with an exam that does lets
              it generate papers today, and you can narrow the link to a single subject.
            </p>
          </Note>
          <AssociationEditor examId={exam.id} examName={exam.name} />
          <div className="flex justify-end gap-2 border-t border-gray-200 pt-4">
            <Button icon={ArrowRight} onClick={finish}>
              Finish setup
            </Button>
          </div>
        </div>
      )}
    </Modal>
  )
}

function DetailsStep({ onCreated }) {
  const [form, setForm] = useState({ name: '', code: '', description: '', language: 'en' })

  const create = useMutation({
    mutationFn: () =>
      api.createExam({
        name: form.name,
        code: form.code || undefined,
        description: form.description || undefined,
        language: form.language || undefined,
      }),
    onSuccess: (payload) => onCreated(payload?.data),
  })

  return (
    <form
      className="space-y-4"
      onSubmit={(event) => {
        event.preventDefault()
        if (form.name.trim()) create.mutate()
      }}
    >
      <Field label="Exam name" required hint="Whatever you call it. The engine has no built-in list.">
        <Input
          autoFocus
          placeholder="State Police Constable 2026"
          value={form.name}
          onChange={(event) => setForm({ ...form, name: event.target.value })}
        />
      </Field>
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="Short code" hint="Derived from the name if you leave it blank.">
          <Input
            placeholder="state-police-2026"
            value={form.code}
            onChange={(event) => setForm({ ...form, code: event.target.value })}
          />
        </Field>
        <Field label="Language">
          <Select
            value={form.language}
            onChange={(event) => setForm({ ...form, language: event.target.value })}
          >
            <option value="en">English</option>
            <option value="hi">Hindi</option>
            <option value="en+hi">Bilingual</option>
            <option value="other">Other</option>
          </Select>
        </Field>
      </div>
      <Field label="Description">
        <Textarea
          placeholder="Anything worth remembering about this exam."
          value={form.description}
          onChange={(event) => setForm({ ...form, description: event.target.value })}
        />
      </Field>

      <ErrorNote error={create.error} />

      <div className="flex justify-end border-t border-gray-200 pt-4">
        <Button
          type="submit"
          icon={ArrowRight}
          loading={create.isPending}
          disabled={!form.name.trim()}
        >
          Create and continue
        </Button>
      </div>
    </form>
  )
}

function PatternStep({ exam, mode, setMode, onDone }) {
  if (!mode) {
    return (
      <div className="space-y-4">
        <p className="text-sm text-gray-600">
          A pattern says how many questions each subject contributes. Paper generation needs one.
        </p>
        <div className="grid gap-3 sm:grid-cols-3">
          <ChoiceCard
            icon={FileSearch}
            title="Derive it from papers"
            body="Upload one or more past papers and let the engine work out the sections, counts and marking."
            recommended
            onClick={() => setMode('derive')}
          />
          <ChoiceCard
            icon={ListOrdered}
            title="Enter it myself"
            body="Type the sections and question counts if you already know the blueprint."
            onClick={() => setMode('manual')}
          />
          <ChoiceCard
            icon={SkipForward}
            title="Later"
            body="Set the pattern up another time. You can still upload material now."
            onClick={onDone}
          />
        </div>
      </div>
    )
  }

  if (mode === 'manual') {
    return (
      <PatternEditor examId={exam.id} onSaved={onDone} onCancel={() => setMode(null)} />
    )
  }

  return <DeriveStep exam={exam} onDone={onDone} onBack={() => setMode(null)} />
}

function ChoiceCard({ icon: Icon, title, body, onClick, recommended }) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="flex h-full flex-col rounded-xl border border-gray-200 bg-white p-4 text-left transition-all hover:border-primary-400 hover:shadow-sm"
    >
      <div className="mb-2 flex items-center gap-2">
        <Icon className="h-5 w-5 text-primary-600" />
        {recommended && <Badge tone="primary">Recommended</Badge>}
      </div>
      <p className="font-medium text-gray-900">{title}</p>
      <p className="mt-1 text-xs text-gray-500">{body}</p>
    </button>
  )
}

/** DeriveStep uploads papers and then runs pattern inference over them. */
function DeriveStep({ exam, onDone, onBack }) {
  const [jobId, setJobId] = useState(null)

  const { data: documents = [], refetch } = useQuery({
    queryKey: ['documents', 'exam', String(exam.id)],
    queryFn: () => api.getDocuments({ exam_id: exam.id, kind: 'question_paper', page_size: 50 }),
    select: (payload) => payload?.data ?? [],
  })

  const analyze = useMutation({
    mutationFn: () => api.analyzePattern(exam.id, { activate: true, ingest_after: true }),
    onSuccess: (payload) => setJobId(payload?.data?.job?.id),
  })

  const { job } = useJobTracker(jobId, {
    invalidate: [['exam', String(exam.id)], ['patterns', String(exam.id)], ['documents']],
  })

  const result = job?.status === 'completed' ? job.result : null
  const draft = result?.draft

  return (
    <div className="space-y-5">
      <Note>
        <p className="font-medium">Drop in past papers</p>
        <p>
          Two or three papers give a much steadier pattern than one, because the engine keeps what
          the papers agree on. Upload them, then run the analysis.
        </p>
      </Note>

      <Dropzone
        defaultKind="question_paper"
        defaultExamId={exam.id}
        lockExam
        lockKind
        compact
        onUploaded={() => refetch()}
      />

      {documents.length > 0 && (
        <Card padded={false} className="p-4">
          <p className="mb-2 text-sm font-medium text-gray-900">
            {documents.length} paper{documents.length === 1 ? '' : 's'} attached to this exam
          </p>
          <ul className="space-y-1 text-sm text-gray-600">
            {documents.slice(0, 6).map((doc) => (
              <li key={doc.id} className="flex items-center justify-between gap-2">
                <span className="truncate">{doc.title}</span>
                <Badge tone={doc.status === 'completed' ? 'green' : 'amber'}>{doc.status}</Badge>
              </li>
            ))}
          </ul>
        </Card>
      )}

      {job && !['completed', 'failed', 'cancelled'].includes(job.status) && (
        <Card>
          <p className="mb-2 text-sm font-medium text-gray-900">Analysing papers</p>
          <p className="mb-2 text-xs text-gray-500">{job.stage}</p>
          <ProgressBar value={job.progress} />
          <p className="mt-2 text-xs text-gray-500">
            Scanned documents go through OCR first, which can take a few minutes per paper.
          </p>
        </Card>
      )}

      {job?.status === 'failed' && <ErrorNote error={{ message: job.error }} />}

      {draft && (
        <Card>
          <div className="mb-3 flex items-center gap-2">
            <Sparkles className="h-4 w-4 text-primary-600" />
            <p className="text-sm font-semibold text-gray-900">Pattern derived</p>
            <Badge tone={draft.confidence >= 0.7 ? 'green' : 'amber'}>
              {Math.round(draft.confidence * 100)}% confidence
            </Badge>
          </div>
          <div className="mb-3 grid grid-cols-2 gap-3 text-sm sm:grid-cols-4">
            <Metric label="Questions" value={draft.total_questions} />
            <Metric label="Sections" value={draft.sections?.length || 0} />
            <Metric
              label="Duration"
              value={draft.duration_min ? `${draft.duration_min} min` : 'not printed'}
            />
            <Metric label="Options" value={draft.option_count || 'unknown'} />
          </div>
          <div className="overflow-hidden rounded-lg border border-gray-200">
            <table className="w-full text-sm">
              <thead className="bg-gray-50 text-xs uppercase tracking-wide text-gray-500">
                <tr>
                  <th className="px-3 py-2 text-left">Section</th>
                  <th className="px-3 py-2 text-right">Questions</th>
                  <th className="px-3 py-2 text-right">Weightage</th>
                  <th className="px-3 py-2 text-right">Agreement</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-100">
                {(draft.sections || []).map((section, index) => (
                  <tr key={index}>
                    <td className="px-3 py-2">
                      <span className="text-gray-900">{section.name}</span>
                      {!section.subject_id && (
                        <Badge tone="amber" className="ml-2">
                          unmapped
                        </Badge>
                      )}
                    </td>
                    <td className="px-3 py-2 text-right tabular-nums">{section.question_count}</td>
                    <td className="px-3 py-2 text-right tabular-nums">
                      {Number(section.weightage).toFixed(1)}%
                    </td>
                    <td className="px-3 py-2 text-right tabular-nums text-gray-500">
                      {Math.round((section.agreement || 0) * 100)}%
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {draft.warnings?.length > 0 && (
            <ul className="mt-3 space-y-1 text-xs text-amber-800">
              {draft.warnings.map((warning, index) => (
                <li key={index}>• {warning}</li>
              ))}
            </ul>
          )}
        </Card>
      )}

      <ErrorNote error={analyze.error} />

      <div className="flex flex-wrap justify-between gap-2 border-t border-gray-200 pt-4">
        <Button variant="ghost" onClick={onBack}>
          Back
        </Button>
        <div className="flex gap-2">
          <Button
            icon={FileSearch}
            loading={analyze.isPending || (job && !['completed', 'failed', 'cancelled'].includes(job.status))}
            disabled={documents.length === 0}
            onClick={() => analyze.mutate()}
          >
            {draft ? 'Analyse again' : `Analyse ${documents.length || ''} paper${documents.length === 1 ? '' : 's'}`}
          </Button>
          <Button variant={draft ? 'primary' : 'secondary'} icon={ArrowRight} onClick={onDone}>
            Continue
          </Button>
        </div>
      </div>
    </div>
  )
}

function Metric({ label, value }) {
  return (
    <div>
      <p className="text-xs text-gray-500">{label}</p>
      <p className="font-semibold text-gray-900">{value}</p>
    </div>
  )
}
