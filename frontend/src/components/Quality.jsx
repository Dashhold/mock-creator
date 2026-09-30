import { useState } from 'react'
import {
  AlertCircle,
  AlertTriangle,
  CheckCircle2,
  ChevronDown,
  ChevronRight,
  Info,
  ShieldAlert,
  ShieldCheck,
  ShieldQuestion,
  Sparkles,
  User,
  Ruler,
  FileSearch,
} from 'lucide-react'
import { Badge, Card } from './ui'

/**
 * The vocabulary of the quality gate, in one place so every view says the same
 * thing about the same state. "unchecked" is deliberately not neutral: content
 * nobody has validated is not known to be safe, and the UI should not imply it
 * is.
 */
export const QUALITY_STATES = {
  pass: {
    label: 'Ready',
    tone: 'green',
    icon: ShieldCheck,
    blurb: 'Passed every check. Cleared for delivery.',
  },
  review: {
    label: 'Needs review',
    tone: 'amber',
    icon: ShieldQuestion,
    blurb: 'Something is uncertain. A person should decide before this is used.',
  },
  failed: {
    label: 'Rejected',
    tone: 'red',
    icon: ShieldAlert,
    blurb: 'A defect was found that would reach a student. Cannot be delivered.',
  },
  unchecked: {
    label: 'Not checked',
    tone: 'gray',
    icon: ShieldQuestion,
    blurb: 'Validation has not run. Not treated as a pass.',
  },
}

export function qualityState(status) {
  return QUALITY_STATES[status] || QUALITY_STATES.unchecked
}

/** QualityBadge is the compact verdict used in lists and tables. */
export function QualityBadge({ status, score, className = '' }) {
  const state = qualityState(status)
  const Icon = state.icon
  const title =
    score !== undefined && score !== null
      ? `${state.blurb} (score ${Number(score).toFixed(2)})`
      : state.blurb
  return (
    <Badge tone={state.tone} title={title} className={className}>
      <Icon className="h-3 w-3" />
      {state.label}
    </Badge>
  )
}

const SEVERITY = {
  critical: {
    label: 'Critical',
    tone: 'red',
    icon: AlertCircle,
    hint: 'Blocks delivery',
  },
  major: {
    label: 'Major',
    tone: 'amber',
    icon: AlertTriangle,
    hint: 'Needs a decision',
  },
  minor: { label: 'Minor', tone: 'gray', icon: Info, hint: 'Noted only' },
}

const SOURCE = {
  rules: { label: 'Checks', icon: Ruler, hint: 'Found by a deterministic check' },
  model: { label: 'Model', icon: Sparkles, hint: 'A language model’s judgement' },
  human: { label: 'Reviewer', icon: User, hint: 'A person’s decision' },
  extraction: { label: 'Extractor', icon: FileSearch, hint: 'Recorded while reading the document' },
}

/**
 * IssueList renders findings grouped by severity.
 *
 * The source of each finding is shown on purpose. A reviewer needs to know
 * whether a defect was measured, judged by a model, or decided by a colleague,
 * because those carry very different weight.
 */
export function IssueList({ issues = [], compact = false, limit }) {
  const [expanded, setExpanded] = useState(false)
  if (!issues.length) {
    return (
      <p className="flex items-center gap-2 text-sm text-green-700">
        <CheckCircle2 className="h-4 w-4" />
        No issues found.
      </p>
    )
  }

  const order = { critical: 0, major: 1, minor: 2 }
  const sorted = [...issues].sort(
    (a, b) => (order[a.severity] ?? 3) - (order[b.severity] ?? 3)
  )
  const cap = limit && !expanded ? limit : sorted.length
  const shown = sorted.slice(0, cap)
  const hidden = sorted.length - shown.length

  return (
    <div className="space-y-2">
      {shown.map((issue, index) => {
        const severity = SEVERITY[issue.severity] || SEVERITY.minor
        const source = SOURCE[issue.source] || SOURCE.rules
        const SeverityIcon = severity.icon
        const SourceIcon = source.icon
        return (
          <div
            key={`${issue.code}-${issue.field}-${index}`}
            className={`rounded-lg border p-3 ${
              issue.severity === 'critical'
                ? 'border-red-200 bg-red-50'
                : issue.severity === 'major'
                ? 'border-amber-200 bg-amber-50'
                : 'border-gray-200 bg-gray-50'
            }`}
          >
            <div className="flex flex-wrap items-center gap-2">
              <SeverityIcon
                className={`h-4 w-4 flex-shrink-0 ${
                  issue.severity === 'critical'
                    ? 'text-red-600'
                    : issue.severity === 'major'
                    ? 'text-amber-600'
                    : 'text-gray-500'
                }`}
              />
              <span className="font-mono text-xs text-gray-700">{issue.code}</span>
              <Badge tone={severity.tone} title={severity.hint}>
                {severity.label}
              </Badge>
              <Badge tone="gray" title={source.hint}>
                <SourceIcon className="h-3 w-3" />
                {source.label}
              </Badge>
              {issue.field && (
                <span className="rounded bg-white px-1.5 py-0.5 text-xs text-gray-600 ring-1 ring-gray-200">
                  {issue.field}
                </span>
              )}
            </div>
            <p className="mt-1.5 text-sm text-gray-800">{issue.message}</p>
            {issue.evidence && !compact && (
              <p className="mt-1.5 break-words rounded bg-white/70 px-2 py-1 font-mono text-xs text-gray-600">
                {issue.evidence}
              </p>
            )}
          </div>
        )
      })}
      {hidden > 0 && (
        <button
          type="button"
          onClick={() => setExpanded(true)}
          className="text-sm text-primary-700 hover:underline"
        >
          Show {hidden} more issue{hidden === 1 ? '' : 's'}
        </button>
      )}
    </div>
  )
}

/** ConfidenceMeter shows an extraction or quality score as a labelled bar. */
export function ConfidenceMeter({ value, label = 'Extraction confidence', hint }) {
  if (value === undefined || value === null) return null
  const pct = Math.round(Math.max(0, Math.min(1, value)) * 100)
  const tone = pct >= 85 ? 'bg-green-500' : pct >= 65 ? 'bg-amber-500' : 'bg-red-500'
  return (
    <div>
      <div className="mb-1 flex items-baseline justify-between gap-2">
        <span className="text-xs text-gray-600">{label}</span>
        <span className="text-xs font-semibold text-gray-800">{pct}%</span>
      </div>
      <div className="h-1.5 w-full overflow-hidden rounded-full bg-gray-200">
        <div className={`h-full rounded-full ${tone}`} style={{ width: `${pct}%` }} />
      </div>
      {hint && <p className="mt-1 text-xs text-gray-500">{hint}</p>}
    </div>
  )
}

/**
 * QualityGatePanel is the headline verdict for a paper: whether it may be
 * delivered, and if not, exactly what is stopping it.
 */
export function QualityGatePanel({ qa, onRecheck, rechecking, modelEnabled }) {
  if (!qa) return null
  const state = qualityState(qa.quality_status || qa.status)
  const Icon = state.icon
  const counts = qa.counts || {}
  const issues = qa.issues || []
  const blocking = qa.blocking || []

  return (
    <Card className="space-y-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="flex items-start gap-3">
          <div
            className={`rounded-lg p-2 ${
              state.tone === 'green'
                ? 'bg-green-100'
                : state.tone === 'red'
                ? 'bg-red-100'
                : state.tone === 'amber'
                ? 'bg-amber-100'
                : 'bg-gray-100'
            }`}
          >
            <Icon
              className={`h-5 w-5 ${
                state.tone === 'green'
                  ? 'text-green-700'
                  : state.tone === 'red'
                  ? 'text-red-700'
                  : state.tone === 'amber'
                  ? 'text-amber-700'
                  : 'text-gray-600'
              }`}
            />
          </div>
          <div>
            <p className="text-base font-semibold text-gray-900">
              Quality gate: {state.label}
            </p>
            <p className="mt-0.5 max-w-xl text-sm text-gray-600">
              {qa.explanation || state.blurb}
            </p>
          </div>
        </div>
        {onRecheck && (
          <button
            type="button"
            onClick={onRecheck}
            disabled={rechecking}
            className="rounded-lg border border-gray-300 px-3 py-1.5 text-sm font-medium text-gray-700 hover:bg-gray-50 disabled:opacity-50"
          >
            {rechecking ? 'Re-checking…' : 'Re-run checks'}
          </button>
        )}
      </div>

      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <Metric label="Critical" value={counts.critical ?? 0} tone="red" />
        <Metric label="Major" value={counts.major ?? 0} tone="amber" />
        <Metric label="Minor" value={counts.minor ?? 0} tone="gray" />
        <Metric
          label="Score"
          value={qa.quality_score !== undefined ? Number(qa.quality_score).toFixed(2) : '—'}
          tone="gray"
        />
      </div>

      {/* Whether a model reviewed this is stated either way. A paper that only
          passed the structural checks must not look like one a model also read. */}
      <div className="flex flex-wrap items-center gap-2 border-t border-gray-100 pt-3">
        {qa.model_checked ? (
          <Badge tone="purple" title={`Reviewed by ${qa.model_name}`}>
            <Sparkles className="h-3 w-3" /> Model reviewed
          </Badge>
        ) : (
          <Badge
            tone="gray"
            title={
              modelEnabled
                ? 'A model is configured but has not reviewed this paper'
                : 'No model is configured, so only the deterministic checks ran'
            }
          >
            <Sparkles className="h-3 w-3" />
            {modelEnabled ? 'Model review pending' : 'Checks only, no model'}
          </Badge>
        )}
        {qa.rules_version ? (
          <Badge tone="gray" title="Which generation of the checks produced this verdict">
            Rules v{qa.rules_version}
          </Badge>
        ) : null}
        {qa.checked_at && (
          <span className="text-xs text-gray-500">
            Checked {new Date(qa.checked_at).toLocaleString()}
          </span>
        )}
      </div>

      {blocking.length > 0 && (
        <div>
          <p className="mb-2 text-sm font-semibold text-red-800">
            Blocking publication ({blocking.length})
          </p>
          <IssueList issues={blocking} />
        </div>
      )}

      {issues.length > blocking.length && (
        <Collapsible title={`All findings (${issues.length})`}>
          <IssueList issues={issues} limit={12} />
        </Collapsible>
      )}

      {qa.model_summary && (
        <div className="rounded-lg border border-purple-200 bg-purple-50 p-3">
          <p className="mb-1 flex items-center gap-1.5 text-xs font-semibold text-purple-900">
            <Sparkles className="h-3.5 w-3.5" /> Final model review
          </p>
          <p className="text-sm text-purple-900">{qa.model_summary}</p>
          {qa.model_grounded === false && (
            <p className="mt-1.5 text-xs text-purple-800">
              This review quoted text that is not in the paper, so its findings were
              discarded and a person should read the paper.
            </p>
          )}
        </div>
      )}

      {qa.warnings?.length > 0 && (
        <ul className="list-disc space-y-1 pl-5 text-xs text-gray-600">
          {qa.warnings.map((warning, index) => (
            <li key={index}>{warning}</li>
          ))}
        </ul>
      )}
    </Card>
  )
}

function Metric({ label, value, tone }) {
  const colors = {
    red: 'text-red-700',
    amber: 'text-amber-700',
    gray: 'text-gray-800',
  }
  return (
    <div className="rounded-lg bg-gray-50 px-3 py-2">
      <p className="text-xs text-gray-600">{label}</p>
      <p className={`text-lg font-bold ${colors[tone] || colors.gray}`}>{value}</p>
    </div>
  )
}

/** Collapsible hides detail until asked for, without hiding that it exists. */
export function Collapsible({ title, children, defaultOpen = false }) {
  const [open, setOpen] = useState(defaultOpen)
  return (
    <div className="rounded-lg border border-gray-200">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-2 px-3 py-2 text-left text-sm font-medium text-gray-800 hover:bg-gray-50"
      >
        {open ? (
          <ChevronDown className="h-4 w-4 text-gray-500" />
        ) : (
          <ChevronRight className="h-4 w-4 text-gray-500" />
        )}
        {title}
      </button>
      {open && <div className="border-t border-gray-200 p-3">{children}</div>}
    </div>
  )
}

/**
 * SourceWindow shows the exact document lines a question was read from.
 *
 * This is what lets a reviewer tell an extraction fault from a bad source. The
 * question's own lines are highlighted; the surrounding lines are context.
 */
export function SourceWindow({ window: sourceWindow }) {
  if (!sourceWindow?.available) {
    return (
      <p className="text-sm text-gray-500">
        The converted text for this document is no longer stored, so the original lines
        cannot be shown.
      </p>
    )
  }
  const lines = String(sourceWindow.text || '').split('\n')
  const first = sourceWindow.first_line ?? 0
  const qFrom = sourceWindow.question_at?.first ?? -1
  const qTo = sourceWindow.question_at?.last ?? -1

  return (
    <div className="overflow-x-auto rounded-lg bg-gray-900 p-3">
      <pre className="text-xs leading-relaxed">
        {lines.map((line, index) => {
          const lineNo = first + index
          const inQuestion = lineNo >= qFrom && lineNo <= qTo
          return (
            <div
              key={lineNo}
              className={inQuestion ? 'bg-primary-900/50 text-primary-100' : 'text-gray-400'}
            >
              <span className="mr-3 inline-block w-10 select-none text-right text-gray-600">
                {lineNo}
              </span>
              {line || ' '}
            </div>
          )
        })}
      </pre>
    </div>
  )
}
