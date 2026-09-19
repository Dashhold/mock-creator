import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { useMutation, useQuery } from '@tanstack/react-query'
import { AlertTriangle, CheckCircle2, ExternalLink, Gauge, Wand2 } from 'lucide-react'
import { api } from '../api/client'
import { useJobTracker } from '../api/hooks'
import {
  Badge,
  Button,
  Card,
  Checkbox,
  ErrorNote,
  Field,
  Input,
  Modal,
  Note,
  ProgressBar,
  Select,
} from './ui'

const PAPER_TYPES = [
  { value: 'balanced', label: 'Balanced', hint: '30% easy, 50% medium, 20% hard' },
  { value: 'easy', label: 'Easy', hint: 'Weighted towards easier questions' },
  { value: 'tough', label: 'Tough', hint: 'Weighted towards harder questions' },
  { value: 'previous_style', label: 'Previous-paper style', hint: 'Mirrors the pattern as-is' },
  { value: 'recent_trend', label: 'Recent trend', hint: 'Prefers newer material' },
  { value: 'speed', label: 'Speed', hint: 'Shorter, easier questions' },
  { value: 'mixed', label: 'Mixed', hint: 'No difficulty preference' },
]

/**
 * GeneratePaperDialog builds a paper from an exam's pattern.
 *
 * It checks supply before generating, because the useful answer to "why is my
 * paper short?" is which subject ran out and what to upload, not a thin paper.
 */
export default function GeneratePaperDialog({ exam, pattern, onClose }) {
  const [jobId, setJobId] = useState(null)
  const [form, setForm] = useState({
    title: '',
    type: 'balanced',
    total_questions: '',
    duration_min: pattern?.duration_min || '',
    include_borrowed: true,
    only_approved: false,
    require_answer: true,
    year_from: '',
    year_to: '',
    avoid_previous: true,
  })

  const { data: previousPapers = [] } = useQuery({
    queryKey: ['papers', 'exam', String(exam.id)],
    queryFn: () => api.getPapers({ exam_id: exam.id, page_size: 50 }),
    select: (payload) => payload?.data ?? [],
  })

  const buildBody = () => ({
    title: form.title || undefined,
    type: form.type,
    total_questions: Number(form.total_questions) || 0,
    duration_min: Number(form.duration_min) || 0,
    include_borrowed: form.include_borrowed,
    only_approved: form.only_approved,
    require_answer: form.require_answer,
    year_from: form.year_from ? Number(form.year_from) : null,
    year_to: form.year_to ? Number(form.year_to) : null,
    exclude_paper_ids:
      form.avoid_previous && previousPapers.length > 0 ? previousPapers.map((p) => p.id) : undefined,
  })

  const availability = useMutation({
    mutationFn: () => api.paperAvailability(exam.id, buildBody()),
    select: (payload) => payload?.data,
  })

  const generate = useMutation({
    mutationFn: () => api.generatePaper(exam.id, buildBody()),
    onSuccess: (payload) => setJobId(payload?.data?.job?.id),
  })

  const { job } = useJobTracker(jobId, { invalidate: [['papers'], ['exam', String(exam.id)]] })

  // Check supply as soon as the dialog opens so the numbers are there to read.
  useEffect(() => {
    availability.mutate()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const report = availability.data?.data
  const running = job && !['completed', 'failed', 'cancelled'].includes(job.status)
  const built = job?.status === 'completed' ? job.result : null

  return (
    <Modal
      title={`Generate a paper for ${exam.name}`}
      subtitle={
        pattern
          ? `Using pattern v${pattern.version}: ${pattern.total_questions} questions across ${
              pattern.sections?.length || 0
            } sections`
          : 'This exam has no active pattern'
      }
      size="lg"
      onClose={onClose}
      footer={
        built ? (
          <>
            <Button variant="secondary" onClick={onClose}>
              Close
            </Button>
            <Link to={`/papers/${built.paper_id}`}>
              <Button icon={ExternalLink}>Open the paper</Button>
            </Link>
          </>
        ) : (
          <>
            <Button variant="secondary" onClick={onClose}>
              Cancel
            </Button>
            <Button
              icon={Gauge}
              variant="secondary"
              loading={availability.isPending}
              onClick={() => availability.mutate()}
            >
              Re-check supply
            </Button>
            <Button icon={Wand2} loading={generate.isPending || running} onClick={() => generate.mutate()}>
              Generate
            </Button>
          </>
        )
      }
    >
      <div className="space-y-5">
        {!pattern && (
          <Note tone="amber">
            <p>
              Set an active pattern first. Without one there is no blueprint describing what a paper
              for this exam should contain.
            </p>
          </Note>
        )}

        {built ? (
          <Card>
            <div className="mb-3 flex items-center gap-2">
              <CheckCircle2 className="h-5 w-5 text-green-600" />
              <p className="font-semibold text-gray-900">{built.title}</p>
            </div>
            <div className="grid grid-cols-2 gap-3 text-sm sm:grid-cols-4">
              <Metric label="Questions" value={built.total_questions} />
              <Metric label="Marks" value={built.total_marks} />
              <Metric label="Borrowed" value={built.analytics?.borrowed_count ?? 0} />
              <Metric label="Seed" value={built.seed} />
            </div>
            {built.warnings?.length > 0 && (
              <ul className="mt-3 space-y-1 text-xs text-amber-800">
                {built.warnings.map((warning, index) => (
                  <li key={index}>• {warning}</li>
                ))}
              </ul>
            )}
            {built.analytics?.shortfalls?.length > 0 && (
              <div className="mt-3 space-y-1">
                {built.analytics.shortfalls.map((shortfall, index) => (
                  <p key={index} className="text-xs text-amber-800">
                    <strong>{shortfall.section}</strong>: {shortfall.reason}
                  </p>
                ))}
              </div>
            )}
          </Card>
        ) : (
          <>
            <div className="grid gap-3 sm:grid-cols-2">
              <Field label="Title" hint="Left blank, the exam name is used.">
                <Input
                  placeholder={`${exam.name} mock paper`}
                  value={form.title}
                  onChange={(event) => setForm({ ...form, title: event.target.value })}
                />
              </Field>
              <Field
                label="Flavour"
                hint={PAPER_TYPES.find((t) => t.value === form.type)?.hint}
              >
                <Select
                  value={form.type}
                  onChange={(event) => setForm({ ...form, type: event.target.value })}
                >
                  {PAPER_TYPES.map((type) => (
                    <option key={type.value} value={type.value}>
                      {type.label}
                    </option>
                  ))}
                </Select>
              </Field>
              <Field
                label="Question count"
                hint={`Blank uses the pattern's ${pattern?.total_questions ?? '—'}. A different number scales every section.`}
              >
                <Input
                  type="number"
                  min="1"
                  placeholder={String(pattern?.total_questions ?? '')}
                  value={form.total_questions}
                  onChange={(event) => setForm({ ...form, total_questions: event.target.value })}
                />
              </Field>
              <Field label="Duration (minutes)">
                <Input
                  type="number"
                  min="0"
                  value={form.duration_min}
                  onChange={(event) => setForm({ ...form, duration_min: event.target.value })}
                />
              </Field>
              <Field label="From year" hint="Optional. Limits how old the questions may be.">
                <Input
                  type="number"
                  placeholder="any"
                  value={form.year_from}
                  onChange={(event) => setForm({ ...form, year_from: event.target.value })}
                />
              </Field>
              <Field label="To year">
                <Input
                  type="number"
                  placeholder="any"
                  value={form.year_to}
                  onChange={(event) => setForm({ ...form, year_to: event.target.value })}
                />
              </Field>
            </div>

            <div className="space-y-2.5 rounded-lg border border-gray-200 bg-gray-50 p-4">
              <Checkbox
                label="Only questions with a known answer"
                hint="Strongly recommended. A question with no answer key cannot be marked."
                checked={form.require_answer}
                onChange={(event) => setForm({ ...form, require_answer: event.target.checked })}
              />
              <Checkbox
                label="Allow borrowed questions"
                hint="Include questions reached through this exam's associations."
                checked={form.include_borrowed}
                onChange={(event) => setForm({ ...form, include_borrowed: event.target.checked })}
              />
              <Checkbox
                label="Only reviewed questions"
                hint="Restrict to questions marked approved."
                checked={form.only_approved}
                onChange={(event) => setForm({ ...form, only_approved: event.target.checked })}
              />
              <Checkbox
                label={`Avoid questions used in the ${previousPapers.length} earlier paper${
                  previousPapers.length === 1 ? '' : 's'
                }`}
                hint="Keeps a series of mock papers from repeating itself."
                disabled={previousPapers.length === 0}
                checked={form.avoid_previous && previousPapers.length > 0}
                onChange={(event) => setForm({ ...form, avoid_previous: event.target.checked })}
              />
            </div>

            {report && <AvailabilityReport report={report} />}
          </>
        )}

        {running && (
          <Card>
            <p className="mb-2 text-sm font-medium text-gray-900">Building the paper</p>
            <p className="mb-2 text-xs text-gray-500">{job.stage}</p>
            <ProgressBar value={job.progress} />
          </Card>
        )}

        {job?.status === 'failed' && <ErrorNote error={{ message: job.error }} />}
        <ErrorNote error={generate.error} />
        <ErrorNote error={availability.error} />
      </div>
    </Modal>
  )
}

function AvailabilityReport({ report }) {
  return (
    <div>
      <div className="mb-2 flex flex-wrap items-center gap-2">
        <p className="text-sm font-semibold text-gray-900">Supply check</p>
        {report.buildable ? (
          <Badge tone="green">
            <CheckCircle2 className="h-3 w-3" /> Every section can be filled
          </Badge>
        ) : (
          <Badge tone="amber">
            <AlertTriangle className="h-3 w-3" /> Some sections will be short
          </Badge>
        )}
        <span className="text-xs text-gray-500">
          {report.total_available} available against {report.total_requested} requested
        </span>
      </div>

      <div className="overflow-hidden rounded-lg border border-gray-200">
        <table className="w-full text-sm">
          <thead className="bg-gray-50 text-xs uppercase tracking-wide text-gray-500">
            <tr>
              <th className="px-3 py-2 text-left">Section</th>
              <th className="px-3 py-2 text-right">Needed</th>
              <th className="px-3 py-2 text-right" title="Passed every quality check">
                Ready
              </th>
              <th className="px-3 py-2 text-right" title="Held for a reviewer's decision">
                Held
              </th>
              <th className="px-3 py-2 text-right">Own</th>
              <th className="px-3 py-2 text-right">Borrowed</th>
              <th className="px-3 py-2 text-right">Status</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-gray-100">
            {(report.sections || []).map((section, index) => (
              <tr key={index}>
                <td className="px-3 py-2 text-gray-900">{section.section}</td>
                <td className="px-3 py-2 text-right tabular-nums">{section.requested}</td>
                {/* "Ready" is the pool the generator will actually draw from.
                    Showing the raw total instead is how a paper gets planned
                    around questions that will be refused. */}
                <td
                  className={`px-3 py-2 text-right font-semibold tabular-nums ${
                    section.deliverable >= section.requested ? 'text-green-700' : 'text-amber-700'
                  }`}
                >
                  {section.deliverable ?? 0}
                </td>
                <td className="px-3 py-2 text-right tabular-nums text-gray-500">
                  {section.held_for_review ?? 0}
                </td>
                <td className="px-3 py-2 text-right tabular-nums">{section.own}</td>
                <td className="px-3 py-2 text-right tabular-nums">{section.borrowed}</td>
                <td className="px-3 py-2 text-right">
                  {section.sufficient ? (
                    <Badge tone="green">ok</Badge>
                  ) : (
                    <Badge tone="amber">short</Badge>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <p className="mt-2 text-xs text-gray-500">
        Only questions marked ready can be selected. A section that is short with
        questions held for review is cleared by resolving them, not by uploading more.
      </p>

      {report.warnings?.length > 0 && (
        <ul className="mt-2 space-y-1 text-xs text-amber-800">
          {report.warnings.map((warning, index) => (
            <li key={index}>• {warning}</li>
          ))}
        </ul>
      )}
    </div>
  )
}

function Metric({ label, value }) {
  return (
    <div>
      <p className="text-xs text-gray-500">{label}</p>
      <p className="font-semibold tabular-nums text-gray-900">{value}</p>
    </div>
  )
}
