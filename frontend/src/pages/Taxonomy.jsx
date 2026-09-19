import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  ChevronDown,
  ChevronRight,
  FolderTree,
  Pencil,
  Plus,
  Tags,
  Trash2,
  X,
} from 'lucide-react'
import { api } from '../api/client'
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
  SectionTitle,
  Textarea,
} from '../components/ui'

/**
 * Taxonomy edits the subject catalogue. Aliases are the important part: they are
 * the headings a subject is printed under in real documents, and they are what
 * lets one parser handle papers from any exam without code changes.
 */
export default function Taxonomy() {
  const queryClient = useQueryClient()
  const [editing, setEditing] = useState(null)
  const [creating, setCreating] = useState(false)
  const [expanded, setExpanded] = useState(() => new Set())
  const [topicFor, setTopicFor] = useState(null)

  const { data, isLoading, error } = useQuery({
    queryKey: ['subjects', 'catalogue'],
    queryFn: () => api.getSubjects({ with_topics: true, with_counts: true }),
    select: (payload) => payload?.data ?? [],
  })

  const refresh = () => {
    queryClient.invalidateQueries({ queryKey: ['subjects'] })
    queryClient.invalidateQueries({ queryKey: ['warehouse'] })
  }

  const removeSubject = useMutation({
    mutationFn: (id) => api.deleteSubject(id),
    onSuccess: refresh,
  })
  const removeTopic = useMutation({
    mutationFn: (id) => api.deleteTopic(id),
    onSuccess: refresh,
  })

  const subjects = data ?? []

  const toggle = (id) =>
    setExpanded((current) => {
      const next = new Set(current)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })

  return (
    <div>
      <PageHeader
        title="Taxonomy"
        subtitle="Subjects are shared across every exam, which is what lets two exams reuse the same questions. Aliases teach the parser to recognise a subject's heading."
        actions={
          <Button icon={Plus} onClick={() => setCreating(true)}>
            New subject
          </Button>
        }
      />

      <Note className="mb-5">
        <p className="font-medium">Why aliases matter</p>
        <p>
          Every paper setter names things differently. If a document's section heading does not match
          any alias, its questions land under <strong>Unsorted</strong>. Add the heading as an alias
          here, then re-parse the document from the Documents page and they file themselves correctly.
        </p>
      </Note>

      <ErrorNote error={error || removeSubject.error} className="mb-4" />

      {isLoading ? (
        <Loading label="Loading subjects" />
      ) : subjects.length === 0 ? (
        <EmptyState
          icon={FolderTree}
          title="No subjects"
          message="Create the subjects your exams test. They are shared, so two exams covering arithmetic point at the same subject and share its questions."
          action={
            <Button icon={Plus} onClick={() => setCreating(true)}>
              Create a subject
            </Button>
          }
        />
      ) : (
        <div className="space-y-2">
          {subjects.map((subject) => {
            const open = expanded.has(subject.id)
            const aliases = parseAliases(subject.aliases)
            return (
              <Card key={subject.id} padded={false} className="overflow-hidden">
                <div className="flex flex-wrap items-center gap-3 p-4">
                  <button
                    type="button"
                    onClick={() => toggle(subject.id)}
                    className="flex min-w-0 flex-1 items-center gap-2 text-left"
                  >
                    {open ? (
                      <ChevronDown className="h-4 w-4 flex-shrink-0 text-gray-400" />
                    ) : (
                      <ChevronRight className="h-4 w-4 flex-shrink-0 text-gray-400" />
                    )}
                    <div className="min-w-0">
                      <p className="truncate font-medium text-gray-900">{subject.name}</p>
                      <p className="text-xs text-gray-500">
                        <code>{subject.code}</code> · {aliases.length} alias
                        {aliases.length === 1 ? '' : 'es'} · {subject.topic_count ?? 0} topic
                        {subject.topic_count === 1 ? '' : 's'}
                      </p>
                    </div>
                  </button>

                  <div className="flex items-center gap-2">
                    <Badge tone={subject.question_count > 0 ? 'primary' : 'gray'}>
                      {subject.question_count ?? 0} questions
                    </Badge>
                    {subject.answered_count > 0 && (
                      <Badge tone="green">{subject.answered_count} answered</Badge>
                    )}
                    {subject.exam_count > 0 && (
                      <Badge tone="purple">
                        {subject.exam_count} exam{subject.exam_count === 1 ? '' : 's'}
                      </Badge>
                    )}
                    <IconButton
                      icon={Tags}
                      tone="primary"
                      title="Add a topic"
                      onClick={() => setTopicFor(subject)}
                    />
                    <IconButton
                      icon={Pencil}
                      tone="primary"
                      title="Edit subject"
                      onClick={() => setEditing(subject)}
                    />
                    <IconButton
                      icon={Trash2}
                      tone="red"
                      title="Delete subject"
                      onClick={() => {
                        if (window.confirm(`Delete "${subject.name}"?`)) removeSubject.mutate(subject.id)
                      }}
                    />
                  </div>
                </div>

                {open && (
                  <div className="space-y-4 border-t border-gray-100 bg-gray-50 p-4">
                    <div>
                      <p className="mb-1.5 text-xs font-medium uppercase tracking-wide text-gray-500">
                        Recognised headings
                      </p>
                      {aliases.length === 0 ? (
                        <p className="text-sm text-gray-500">
                          No aliases. Only an exact match on the subject name will be recognised.
                        </p>
                      ) : (
                        <div className="flex flex-wrap gap-1.5">
                          {aliases.map((alias) => (
                            <Badge key={alias} tone="gray">
                              {alias}
                            </Badge>
                          ))}
                        </div>
                      )}
                    </div>

                    <div>
                      <p className="mb-1.5 text-xs font-medium uppercase tracking-wide text-gray-500">
                        Topics
                      </p>
                      {(subject.topics || []).length === 0 ? (
                        <p className="text-sm text-gray-500">
                          No topics yet. Topics let the engine tag questions more finely.
                        </p>
                      ) : (
                        <div className="flex flex-wrap gap-1.5">
                          {subject.topics.map((topic) => (
                            <span
                              key={topic.id}
                              className="inline-flex items-center gap-1 rounded-full border border-purple-200 bg-purple-50 px-2 py-0.5 text-xs text-purple-800"
                            >
                              {topic.name}
                              <button
                                type="button"
                                aria-label={`Delete ${topic.name}`}
                                className="rounded-full p-0.5 hover:bg-purple-200"
                                onClick={() => {
                                  if (window.confirm(`Delete topic "${topic.name}"?`)) {
                                    removeTopic.mutate(topic.id)
                                  }
                                }}
                              >
                                <X className="h-3 w-3" />
                              </button>
                            </span>
                          ))}
                        </div>
                      )}
                    </div>

                    {subject.description && (
                      <p className="text-xs text-gray-500">{subject.description}</p>
                    )}
                  </div>
                )}
              </Card>
            )
          })}
        </div>
      )}

      {(creating || editing) && (
        <SubjectModal
          subject={editing}
          onClose={() => {
            setCreating(false)
            setEditing(null)
          }}
          onSaved={() => {
            setCreating(false)
            setEditing(null)
            refresh()
          }}
        />
      )}

      {topicFor && (
        <TopicModal
          subject={topicFor}
          onClose={() => setTopicFor(null)}
          onSaved={() => {
            setTopicFor(null)
            refresh()
          }}
        />
      )}
    </div>
  )
}

function parseAliases(raw) {
  if (!raw) return []
  if (Array.isArray(raw)) return raw
  try {
    const parsed = JSON.parse(raw)
    return Array.isArray(parsed) ? parsed : []
  } catch {
    return []
  }
}

function SubjectModal({ subject, onClose, onSaved }) {
  const isEdit = Boolean(subject?.id)
  const [form, setForm] = useState({
    name: subject?.name || '',
    code: subject?.code || '',
    description: subject?.description || '',
    aliases: parseAliases(subject?.aliases).join('\n'),
  })

  const save = useMutation({
    mutationFn: () => {
      const body = {
        name: form.name,
        code: form.code || undefined,
        description: form.description,
        aliases: form.aliases
          .split('\n')
          .map((line) => line.trim())
          .filter(Boolean),
      }
      return isEdit ? api.updateSubject(subject.id, body) : api.createSubject(body)
    },
    onSuccess: onSaved,
  })

  return (
    <Modal
      title={isEdit ? `Edit ${subject.name}` : 'New subject'}
      subtitle="Aliases are matched against section headings in uploaded documents."
      onClose={onClose}
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            Cancel
          </Button>
          <Button loading={save.isPending} disabled={!form.name.trim()} onClick={() => save.mutate()}>
            {isEdit ? 'Save' : 'Create'}
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <Field label="Name" required>
          <Input
            autoFocus
            placeholder="Quantitative Aptitude"
            value={form.name}
            onChange={(event) => setForm({ ...form, name: event.target.value })}
          />
        </Field>
        <Field label="Code" hint="Derived from the name if left blank.">
          <Input
            placeholder="quant"
            value={form.code}
            onChange={(event) => setForm({ ...form, code: event.target.value })}
          />
        </Field>
        <Field
          label="Aliases"
          hint="One per line. Include every heading you have seen this subject printed under. Case and punctuation do not matter."
        >
          <Textarea
            rows={8}
            className="font-mono text-xs"
            placeholder={'numerical ability\nmathematics\narithmetic\nquantitative techniques'}
            value={form.aliases}
            onChange={(event) => setForm({ ...form, aliases: event.target.value })}
          />
        </Field>
        <Field label="Description">
          <Textarea
            rows={2}
            value={form.description}
            onChange={(event) => setForm({ ...form, description: event.target.value })}
          />
        </Field>
        <ErrorNote error={save.error} />
      </div>
    </Modal>
  )
}

function TopicModal({ subject, onClose, onSaved }) {
  const [form, setForm] = useState({ name: '', code: '', aliases: '' })

  const save = useMutation({
    mutationFn: () =>
      api.createTopic({
        subject_id: subject.id,
        name: form.name,
        code: form.code || undefined,
        aliases: form.aliases
          .split('\n')
          .map((line) => line.trim())
          .filter(Boolean),
      }),
    onSuccess: onSaved,
  })

  return (
    <Modal
      title={`New topic in ${subject.name}`}
      subtitle="Topics tag questions more finely than subjects do."
      size="sm"
      onClose={onClose}
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            Cancel
          </Button>
          <Button loading={save.isPending} disabled={!form.name.trim()} onClick={() => save.mutate()}>
            Create
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <SectionTitle hint="Questions mentioning these terms are tagged automatically during extraction.">
          Topic
        </SectionTitle>
        <Field label="Name" required>
          <Input
            autoFocus
            placeholder="Percentages"
            value={form.name}
            onChange={(event) => setForm({ ...form, name: event.target.value })}
          />
        </Field>
        <Field label="Aliases" hint="One per line.">
          <Textarea
            rows={4}
            className="font-mono text-xs"
            placeholder={'percentage\npercent change'}
            value={form.aliases}
            onChange={(event) => setForm({ ...form, aliases: event.target.value })}
          />
        </Field>
        <ErrorNote error={save.error} />
      </div>
    </Modal>
  )
}
