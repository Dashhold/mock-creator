import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link2, Trash2, Plus, Power } from 'lucide-react'
import { api } from '../api/client'
import { useExamOptions, useSubjectOptions } from '../api/hooks'
import {
  Badge,
  Button,
  EmptyState,
  ErrorNote,
  Field,
  IconButton,
  Input,
  Loading,
  Note,
  Select,
} from './ui'

/**
 * AssociationEditor manages which other exams an exam may borrow questions from.
 *
 * This is the answer to "my new exam has no content yet": point it at an exam
 * that already does, optionally for one subject only, and its questions become
 * available immediately.
 */
export default function AssociationEditor({ examId, examName }) {
  const queryClient = useQueryClient()
  const { data: exams = [] } = useExamOptions()
  const { data: subjects = [] } = useSubjectOptions()
  const [adding, setAdding] = useState(false)
  const [form, setForm] = useState({ source_exam_id: '', subject_id: '', similarity: 0.8, note: '' })

  const { data, isLoading, error } = useQuery({
    queryKey: ['associations', String(examId)],
    queryFn: () => api.getAssociations(examId),
    select: (payload) => payload?.data ?? [],
  })

  const refresh = () => {
    queryClient.invalidateQueries({ queryKey: ['associations', String(examId)] })
    queryClient.invalidateQueries({ queryKey: ['exam', String(examId)] })
    queryClient.invalidateQueries({ queryKey: ['warehouse'] })
    queryClient.invalidateQueries({ queryKey: ['questions'] })
  }

  const create = useMutation({
    mutationFn: () =>
      api.createAssociation(examId, {
        source_exam_id: Number(form.source_exam_id),
        subject_id: form.subject_id ? Number(form.subject_id) : null,
        similarity: Number(form.similarity),
        note: form.note || undefined,
      }),
    onSuccess: () => {
      setAdding(false)
      setForm({ source_exam_id: '', subject_id: '', similarity: 0.8, note: '' })
      refresh()
    },
  })

  const toggle = useMutation({
    mutationFn: ({ id, enabled }) => api.updateAssociation(examId, id, { enabled }),
    onSuccess: refresh,
  })

  const remove = useMutation({
    mutationFn: (id) => api.deleteAssociation(examId, id),
    onSuccess: refresh,
  })

  const associations = data ?? []
  const candidates = exams.filter((exam) => exam.id !== Number(examId))

  if (isLoading) return <Loading label="Loading associations" className="py-10" />

  return (
    <div className="space-y-4">
      <ErrorNote error={error} />

      {associations.length === 0 && !adding ? (
        <EmptyState
          icon={Link2}
          title="No associations"
          message={`${
            examName || 'This exam'
          } only uses its own material. Associate another exam to borrow its questions, which is the fastest way to get a new exam producing papers.`}
          action={
            <Button icon={Plus} onClick={() => setAdding(true)} disabled={candidates.length === 0}>
              Associate an exam
            </Button>
          }
        />
      ) : (
        <div className="space-y-2">
          {associations.map((assoc) => (
            <div
              key={assoc.id}
              className={`flex flex-wrap items-center justify-between gap-3 rounded-lg border p-3 ${
                assoc.enabled ? 'border-gray-200 bg-white' : 'border-gray-200 bg-gray-50 opacity-70'
              }`}
            >
              <div className="min-w-0">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="font-medium text-gray-900">
                    {assoc.source_exam?.name || `Exam ${assoc.source_exam_id}`}
                  </span>
                  <Badge tone={assoc.subject ? 'purple' : 'blue'}>
                    {assoc.subject ? assoc.subject.name : 'All subjects'}
                  </Badge>
                  <Badge tone="gray">similarity {Number(assoc.similarity).toFixed(2)}</Badge>
                  {!assoc.enabled && <Badge tone="amber">disabled</Badge>}
                </div>
                {assoc.note && <p className="mt-1 text-xs text-gray-500">{assoc.note}</p>}
              </div>
              <div className="flex items-center gap-1">
                <IconButton
                  icon={Power}
                  title={assoc.enabled ? 'Disable' : 'Enable'}
                  tone={assoc.enabled ? 'gray' : 'green'}
                  onClick={() => toggle.mutate({ id: assoc.id, enabled: !assoc.enabled })}
                />
                <IconButton
                  icon={Trash2}
                  title="Remove association"
                  tone="red"
                  onClick={() => {
                    if (window.confirm('Remove this association? Borrowed questions stop being available to this exam.')) {
                      remove.mutate(assoc.id)
                    }
                  }}
                />
              </div>
            </div>
          ))}

          {!adding && (
            <Button
              variant="secondary"
              icon={Plus}
              onClick={() => setAdding(true)}
              disabled={candidates.length === 0}
            >
              Associate another exam
            </Button>
          )}
        </div>
      )}

      {candidates.length === 0 && !associations.length && (
        <Note tone="gray">
          <p>There is only one exam so far. Create a second exam to link them.</p>
        </Note>
      )}

      {adding && (
        <div className="space-y-3 rounded-lg border border-primary-200 bg-primary-50/40 p-4">
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label="Borrow from" required>
              <Select
                value={form.source_exam_id}
                onChange={(event) => setForm({ ...form, source_exam_id: event.target.value })}
              >
                <option value="">Choose an exam…</option>
                {candidates.map((exam) => (
                  <option key={exam.id} value={exam.id}>
                    {exam.name}
                    {exam.summary?.questions ? ` — ${exam.summary.questions} questions` : ''}
                  </option>
                ))}
              </Select>
            </Field>
            <Field
              label="Limit to one subject"
              hint="Leave blank to borrow across every subject that exam covers."
            >
              <Select
                value={form.subject_id}
                onChange={(event) => setForm({ ...form, subject_id: event.target.value })}
              >
                <option value="">All subjects</option>
                {subjects.map((subject) => (
                  <option key={subject.id} value={subject.id}>
                    {subject.name}
                  </option>
                ))}
              </Select>
            </Field>
            <Field
              label="How comparable are they?"
              hint="Higher values are preferred when the generator has a choice."
            >
              <Input
                type="range"
                min="0.1"
                max="1"
                step="0.05"
                value={form.similarity}
                onChange={(event) => setForm({ ...form, similarity: event.target.value })}
              />
              <p className="mt-1 text-xs text-gray-600">{Number(form.similarity).toFixed(2)}</p>
            </Field>
            <Field label="Note" hint="Why these two are comparable.">
              <Input
                placeholder="Same arithmetic level"
                value={form.note}
                onChange={(event) => setForm({ ...form, note: event.target.value })}
              />
            </Field>
          </div>

          <ErrorNote error={create.error} />

          <div className="flex gap-2">
            <Button
              icon={Link2}
              loading={create.isPending}
              disabled={!form.source_exam_id}
              onClick={() => create.mutate()}
            >
              Add association
            </Button>
            <Button variant="secondary" onClick={() => setAdding(false)}>
              Cancel
            </Button>
          </div>
        </div>
      )}
    </div>
  )
}
