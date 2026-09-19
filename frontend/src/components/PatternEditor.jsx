import { useMemo, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Plus, Trash2, Save } from 'lucide-react'
import { api } from '../api/client'
import { useSubjectOptions } from '../api/hooks'
import { Badge, Button, Checkbox, ErrorNote, Field, IconButton, Input, Note, Select } from './ui'

const emptySection = () => ({
  key: Math.random().toString(36).slice(2),
  subject_id: '',
  name: '',
  question_count: 25,
  marks_per_question: '',
  negative_marks: '',
})

/** fromPattern turns a saved pattern into editable rows. */
function fromPattern(pattern) {
  if (!pattern) return null
  return {
    name: pattern.name || '',
    duration_min: pattern.duration_min || '',
    marks_per_question: pattern.marks_per_question || 1,
    negative_marks: pattern.negative_marks || '',
    option_count: pattern.option_count || '',
    activate: Boolean(pattern.is_active),
    sections: (pattern.sections || []).map((section) => ({
      key: `s${section.id}`,
      subject_id: section.subject_id ? String(section.subject_id) : '',
      name: section.name || '',
      question_count: section.question_count || 0,
      marks_per_question: section.marks_per_question || '',
      negative_marks: section.negative_marks || '',
    })),
  }
}

/**
 * PatternEditor edits an exam's blueprint: which subject each section tests and
 * how many questions it carries. Weightage is derived from the counts rather
 * than typed, so the two can never disagree.
 */
export default function PatternEditor({ examId, pattern, onSaved, onCancel }) {
  const queryClient = useQueryClient()
  const { data: subjects = [] } = useSubjectOptions()
  const isEdit = Boolean(pattern?.id)

  const [form, setForm] = useState(
    () =>
      fromPattern(pattern) || {
        name: '',
        duration_min: 60,
        marks_per_question: 1,
        negative_marks: '',
        option_count: 4,
        activate: true,
        sections: [emptySection()],
      }
  )

  const totals = useMemo(() => {
    const questions = form.sections.reduce(
      (sum, section) => sum + (Number(section.question_count) || 0),
      0
    )
    const marks = form.sections.reduce((sum, section) => {
      const count = Number(section.question_count) || 0
      const per = Number(section.marks_per_question) || Number(form.marks_per_question) || 1
      return sum + count * per
    }, 0)
    return { questions, marks }
  }, [form])

  const unmapped = form.sections.filter((section) => !section.subject_id).length

  const setSection = (key, changes) =>
    setForm((current) => ({
      ...current,
      sections: current.sections.map((section) =>
        section.key === key ? { ...section, ...changes } : section
      ),
    }))

  const save = useMutation({
    mutationFn: () => {
      const body = {
        name: form.name || undefined,
        duration_min: Number(form.duration_min) || 0,
        marks_per_question: Number(form.marks_per_question) || 1,
        negative_marks: Number(form.negative_marks) || 0,
        option_count: Number(form.option_count) || 0,
        activate: form.activate,
        sections: form.sections
          .filter((section) => Number(section.question_count) > 0)
          .map((section, index) => ({
            subject_id: section.subject_id ? Number(section.subject_id) : null,
            name:
              section.name ||
              subjects.find((s) => String(s.id) === section.subject_id)?.name ||
              `Section ${index + 1}`,
            order_index: index,
            question_count: Number(section.question_count),
            marks_per_question: Number(section.marks_per_question) || 0,
            negative_marks: Number(section.negative_marks) || 0,
          })),
      }
      return isEdit
        ? api.updatePattern(examId, pattern.id, body)
        : api.createPattern(examId, body)
    },
    onSuccess: (payload) => {
      queryClient.invalidateQueries({ queryKey: ['exam', String(examId)] })
      queryClient.invalidateQueries({ queryKey: ['patterns', String(examId)] })
      queryClient.invalidateQueries({ queryKey: ['exams'] })
      if (onSaved) onSaved(payload?.data)
    },
  })

  return (
    <div className="space-y-5">
      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-5">
        <Field label="Pattern name" className="lg:col-span-2">
          <Input
            placeholder="Tier 1 pattern"
            value={form.name}
            onChange={(event) => setForm({ ...form, name: event.target.value })}
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
        <Field label="Marks per question">
          <Input
            type="number"
            min="0"
            step="0.25"
            value={form.marks_per_question}
            onChange={(event) => setForm({ ...form, marks_per_question: event.target.value })}
          />
        </Field>
        <Field label="Negative marks" hint="Per wrong answer. 0 for none.">
          <Input
            type="number"
            min="0"
            step="0.25"
            placeholder="0"
            value={form.negative_marks}
            onChange={(event) => setForm({ ...form, negative_marks: event.target.value })}
          />
        </Field>
      </div>

      <div>
        <div className="mb-2 flex items-center justify-between">
          <div>
            <h3 className="text-sm font-semibold text-gray-900">Sections</h3>
            <p className="text-xs text-gray-500">
              One row per section. Weightage is calculated from the question counts.
            </p>
          </div>
          <Field label="Options per question" className="w-40">
            <Input
              type="number"
              min="2"
              max="8"
              value={form.option_count}
              onChange={(event) => setForm({ ...form, option_count: event.target.value })}
            />
          </Field>
        </div>

        <div className="overflow-x-auto rounded-lg border border-gray-200">
          <table className="w-full text-sm">
            <thead className="border-b border-gray-200 bg-gray-50 text-xs uppercase tracking-wide text-gray-500">
              <tr>
                <th className="px-3 py-2 text-left">Subject</th>
                <th className="px-3 py-2 text-left">Printed as</th>
                <th className="px-3 py-2 text-right">Questions</th>
                <th className="px-3 py-2 text-right">Marks each</th>
                <th className="px-3 py-2 text-right">Weightage</th>
                <th className="px-3 py-2" />
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100">
              {form.sections.map((section) => {
                const count = Number(section.question_count) || 0
                const share = totals.questions ? (count / totals.questions) * 100 : 0
                return (
                  <tr key={section.key}>
                    <td className="px-3 py-2">
                      <Select
                        value={section.subject_id}
                        onChange={(event) =>
                          setSection(section.key, { subject_id: event.target.value })
                        }
                      >
                        <option value="">Choose a subject…</option>
                        {subjects.map((subject) => (
                          <option key={subject.id} value={subject.id}>
                            {subject.name}
                            {subject.question_count ? ` (${subject.question_count})` : ''}
                          </option>
                        ))}
                      </Select>
                    </td>
                    <td className="px-3 py-2">
                      <Input
                        placeholder="Section heading as printed"
                        value={section.name}
                        onChange={(event) => setSection(section.key, { name: event.target.value })}
                      />
                    </td>
                    <td className="px-3 py-2">
                      <Input
                        type="number"
                        min="0"
                        className="w-24 text-right"
                        value={section.question_count}
                        onChange={(event) =>
                          setSection(section.key, { question_count: event.target.value })
                        }
                      />
                    </td>
                    <td className="px-3 py-2">
                      <Input
                        type="number"
                        min="0"
                        step="0.25"
                        className="w-24 text-right"
                        placeholder={String(form.marks_per_question || 1)}
                        value={section.marks_per_question}
                        onChange={(event) =>
                          setSection(section.key, { marks_per_question: event.target.value })
                        }
                      />
                    </td>
                    <td className="px-3 py-2 text-right tabular-nums text-gray-600">
                      {share.toFixed(1)}%
                    </td>
                    <td className="px-3 py-2 text-right">
                      <IconButton
                        icon={Trash2}
                        tone="red"
                        title="Remove section"
                        disabled={form.sections.length === 1}
                        onClick={() =>
                          setForm({
                            ...form,
                            sections: form.sections.filter((s) => s.key !== section.key),
                          })
                        }
                      />
                    </td>
                  </tr>
                )
              })}
            </tbody>
            <tfoot className="border-t border-gray-200 bg-gray-50 text-sm font-medium text-gray-700">
              <tr>
                <td className="px-3 py-2" colSpan={2}>
                  Total
                </td>
                <td className="px-3 py-2 text-right tabular-nums">{totals.questions}</td>
                <td className="px-3 py-2 text-right tabular-nums">{totals.marks} marks</td>
                <td className="px-3 py-2 text-right">100%</td>
                <td />
              </tr>
            </tfoot>
          </table>
        </div>

        <Button
          variant="secondary"
          icon={Plus}
          className="mt-3"
          onClick={() => setForm({ ...form, sections: [...form.sections, emptySection()] })}
        >
          Add section
        </Button>
      </div>

      {unmapped > 0 && (
        <Note tone="amber">
          <p>
            {unmapped} section{unmapped === 1 ? '' : 's'} have no subject. Papers skip those
            sections, because there is no pool to draw from.
          </p>
        </Note>
      )}

      <Checkbox
        label="Make this the active pattern"
        hint="Paper generation always uses the active pattern."
        checked={form.activate}
        onChange={(event) => setForm({ ...form, activate: event.target.checked })}
      />

      <ErrorNote error={save.error} />

      <div className="flex items-center gap-2">
        <Button
          icon={Save}
          loading={save.isPending}
          disabled={totals.questions === 0}
          onClick={() => save.mutate()}
        >
          {isEdit ? 'Save pattern' : 'Create pattern'}
        </Button>
        {onCancel && (
          <Button variant="secondary" onClick={onCancel}>
            Cancel
          </Button>
        )}
        {isEdit && pattern?.source === 'derived' && (
          <Badge tone="blue">Editing a derived pattern makes it yours</Badge>
        )}
      </div>
    </div>
  )
}
