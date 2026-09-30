import { Loader2, AlertCircle, X, ChevronLeft, ChevronRight } from 'lucide-react'

/** Spinner is the single loading indicator used everywhere. */
export function Spinner({ className = 'w-6 h-6' }) {
  return <Loader2 className={`${className} animate-spin text-primary-600`} />
}

export function Loading({ label = 'Loading', className = 'py-16' }) {
  return (
    <div className={`flex flex-col items-center justify-center gap-3 ${className}`}>
      <Spinner className="w-7 h-7" />
      <p className="text-sm text-gray-500">{label}</p>
    </div>
  )
}

export function PageHeader({ title, subtitle, actions }) {
  return (
    <div className="mb-6 flex flex-wrap items-start justify-between gap-4">
      <div>
        <h1 className="text-2xl font-bold text-gray-900">{title}</h1>
        {subtitle && <p className="mt-1 max-w-3xl text-sm text-gray-600">{subtitle}</p>}
      </div>
      {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
    </div>
  )
}

export function Card({ children, className = '', padded = true }) {
  return (
    <div className={`rounded-xl border border-gray-200 bg-white shadow-sm ${padded ? 'p-5' : ''} ${className}`}>
      {children}
    </div>
  )
}

export function SectionTitle({ children, hint }) {
  return (
    <div className="mb-3">
      <h2 className="text-base font-semibold text-gray-900">{children}</h2>
      {hint && <p className="mt-0.5 text-xs text-gray-500">{hint}</p>}
    </div>
  )
}

const TONES = {
  gray: 'bg-gray-100 text-gray-700 border-gray-200',
  green: 'bg-green-50 text-green-700 border-green-200',
  amber: 'bg-amber-50 text-amber-800 border-amber-200',
  red: 'bg-red-50 text-red-700 border-red-200',
  blue: 'bg-blue-50 text-blue-700 border-blue-200',
  purple: 'bg-purple-50 text-purple-700 border-purple-200',
  primary: 'bg-primary-50 text-primary-700 border-primary-200',
}

export function Badge({ children, tone = 'gray', title, className = '' }) {
  return (
    <span
      title={title}
      className={`inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-xs font-medium ${TONES[tone] || TONES.gray} ${className}`}
    >
      {children}
    </span>
  )
}

/** statusTone keeps status colouring consistent across every view. */
export function statusTone(status) {
  switch (status) {
    case 'completed':
    case 'approved':
    case 'published':
      return 'green'
    case 'processing':
      return 'blue'
    case 'queued':
    case 'pending':
      return 'amber'
    case 'failed':
    case 'rejected':
      return 'red'
    default:
      return 'gray'
  }
}

export function StatusPill({ status, children }) {
  if (!status) return null
  return <Badge tone={statusTone(status)}>{children || status.replace(/_/g, ' ')}</Badge>
}

export function difficultyTone(difficulty) {
  switch (difficulty) {
    case 'easy':
      return 'green'
    case 'hard':
      return 'red'
    default:
      return 'amber'
  }
}

export function EmptyState({ icon: Icon, title, message, action }) {
  return (
    <div className="flex flex-col items-center justify-center rounded-xl border border-dashed border-gray-300 bg-white px-6 py-16 text-center">
      {Icon && <Icon className="mb-3 h-10 w-10 text-gray-300" />}
      <p className="text-base font-medium text-gray-800">{title}</p>
      {message && <p className="mt-1 max-w-md text-sm text-gray-500">{message}</p>}
      {action && <div className="mt-5">{action}</div>}
    </div>
  )
}

export function ErrorNote({ error, className = '' }) {
  if (!error) return null
  return (
    <div className={`flex gap-3 rounded-lg border border-red-200 bg-red-50 p-4 ${className}`}>
      <AlertCircle className="mt-0.5 h-5 w-5 flex-shrink-0 text-red-600" />
      <div className="text-sm text-red-800">{error.message || String(error)}</div>
    </div>
  )
}

export function Note({ children, tone = 'blue', icon: Icon = AlertCircle }) {
  const border = { blue: 'border-blue-200 bg-blue-50 text-blue-900', amber: 'border-amber-200 bg-amber-50 text-amber-900', gray: 'border-gray-200 bg-gray-50 text-gray-700' }
  return (
    <div className={`flex gap-3 rounded-lg border p-4 text-sm ${border[tone] || border.blue}`}>
      <Icon className="mt-0.5 h-4 w-4 flex-shrink-0" />
      <div className="space-y-1">{children}</div>
    </div>
  )
}

export function ProgressBar({ value = 0, tone = 'primary', label }) {
  const pct = Math.max(0, Math.min(100, value))
  const colors = {
    primary: 'bg-primary-600',
    green: 'bg-green-600',
    red: 'bg-red-500',
    amber: 'bg-amber-500',
  }
  return (
    <div>
      {label && (
        <div className="mb-1 flex justify-between text-xs text-gray-500">
          <span>{label}</span>
          <span>{pct}%</span>
        </div>
      )}
      <div className="h-1.5 w-full overflow-hidden rounded-full bg-gray-200">
        <div
          className={`h-full rounded-full transition-all duration-500 ${colors[tone] || colors.primary}`}
          style={{ width: `${pct}%` }}
        />
      </div>
    </div>
  )
}

export function Stat({ label, value, sub, icon: Icon, tone = 'primary' }) {
  const bg = {
    primary: 'bg-primary-500',
    green: 'bg-green-500',
    amber: 'bg-amber-500',
    purple: 'bg-purple-500',
    blue: 'bg-blue-500',
    red: 'bg-red-500',
  }
  return (
    <Card>
      <div className="flex items-start justify-between">
        <div>
          <p className="text-sm text-gray-600">{label}</p>
          <p className="mt-1 text-2xl font-bold text-gray-900">{value}</p>
          {sub && <p className="mt-1 text-xs text-gray-500">{sub}</p>}
        </div>
        {Icon && (
          <div className={`rounded-lg p-2.5 ${bg[tone] || bg.primary}`}>
            <Icon className="h-5 w-5 text-white" />
          </div>
        )}
      </div>
    </Card>
  )
}

// --- form primitives -------------------------------------------------------

const inputClass =
  'w-full rounded-lg border border-gray-300 px-3 py-2 text-sm text-gray-900 placeholder-gray-400 ' +
  'focus:border-primary-500 focus:outline-none focus:ring-2 focus:ring-primary-200 disabled:bg-gray-50'

export function Field({ label, hint, required, htmlFor, children, className = '' }) {
  return (
    <div className={className}>
      {label && (
        <label htmlFor={htmlFor} className="mb-1.5 block text-sm font-medium text-gray-700">
          {label}
          {required && <span className="ml-0.5 text-red-500">*</span>}
        </label>
      )}
      {children}
      {hint && <p className="mt-1 text-xs text-gray-500">{hint}</p>}
    </div>
  )
}

export function Input({ className = '', ...props }) {
  return <input className={`${inputClass} ${className}`} {...props} />
}

export function Textarea({ className = '', rows = 3, ...props }) {
  return <textarea rows={rows} className={`${inputClass} ${className}`} {...props} />
}

export function Select({ className = '', children, ...props }) {
  return (
    <select className={`${inputClass} bg-white ${className}`} {...props}>
      {children}
    </select>
  )
}

export function Checkbox({ label, hint, ...props }) {
  return (
    <label className="flex cursor-pointer items-start gap-2.5">
      <input
        type="checkbox"
        className="mt-0.5 h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-400"
        {...props}
      />
      <span>
        <span className="block text-sm text-gray-800">{label}</span>
        {hint && <span className="block text-xs text-gray-500">{hint}</span>}
      </span>
    </label>
  )
}

const BUTTON_VARIANTS = {
  primary: 'bg-primary-600 text-white hover:bg-primary-700 disabled:bg-primary-300',
  secondary: 'border border-gray-300 bg-white text-gray-700 hover:bg-gray-50',
  danger: 'border border-red-200 bg-white text-red-600 hover:bg-red-50',
  ghost: 'text-gray-600 hover:bg-gray-100',
  subtle: 'bg-primary-50 text-primary-700 hover:bg-primary-100',
}

export function Button({
  children,
  variant = 'primary',
  icon: Icon,
  loading = false,
  className = '',
  type = 'button',
  ...props
}) {
  return (
    <button
      type={type}
      disabled={loading || props.disabled}
      className={`inline-flex items-center justify-center gap-2 rounded-lg px-4 py-2 text-sm font-medium
        transition-colors disabled:cursor-not-allowed disabled:opacity-60
        ${BUTTON_VARIANTS[variant] || BUTTON_VARIANTS.primary} ${className}`}
      {...props}
    >
      {loading ? <Loader2 className="h-4 w-4 animate-spin" /> : Icon && <Icon className="h-4 w-4" />}
      {children}
    </button>
  )
}

export function IconButton({ icon: Icon, title, tone = 'gray', className = '', ...props }) {
  const tones = {
    gray: 'text-gray-500 hover:bg-gray-100 hover:text-gray-700',
    primary: 'text-primary-600 hover:bg-primary-50',
    green: 'text-green-600 hover:bg-green-50',
    red: 'text-red-600 hover:bg-red-50',
  }
  return (
    <button
      type="button"
      title={title}
      aria-label={title}
      className={`rounded-lg p-2 transition-colors disabled:opacity-40 ${tones[tone] || tones.gray} ${className}`}
      {...props}
    >
      <Icon className="h-4 w-4" />
    </button>
  )
}

// --- overlays and lists ----------------------------------------------------

const MODAL_SIZES = {
  sm: 'max-w-md',
  md: 'max-w-2xl',
  lg: 'max-w-4xl',
  xl: 'max-w-6xl',
}

export function Modal({ title, subtitle, onClose, children, footer, size = 'md' }) {
  return (
    <div
      className="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto bg-black/50 p-4 sm:p-8"
      role="dialog"
      aria-modal="true"
      aria-label={title}
    >
      <div className={`w-full ${MODAL_SIZES[size]} rounded-xl bg-white shadow-xl`}>
        <div className="flex items-start justify-between gap-4 border-b border-gray-200 p-5">
          <div>
            <h2 className="text-lg font-semibold text-gray-900">{title}</h2>
            {subtitle && <p className="mt-0.5 text-sm text-gray-500">{subtitle}</p>}
          </div>
          <IconButton icon={X} title="Close" onClick={onClose} />
        </div>
        <div className="max-h-[70vh] overflow-y-auto p-5">{children}</div>
        {footer && <div className="flex justify-end gap-2 border-t border-gray-200 p-4">{footer}</div>}
      </div>
    </div>
  )
}

export function Pagination({ meta, page, onChange }) {
  if (!meta || meta.total_pages <= 1) return null
  const from = (page - 1) * meta.page_size + 1
  const to = Math.min(page * meta.page_size, meta.total)
  return (
    <div className="mt-4 flex items-center justify-between text-sm text-gray-600">
      <span>
        {from}–{to} of {meta.total}
      </span>
      <div className="flex items-center gap-2">
        <IconButton
          icon={ChevronLeft}
          title="Previous page"
          disabled={page <= 1}
          onClick={() => onChange(page - 1)}
        />
        <span className="tabular-nums">
          {page} / {meta.total_pages}
        </span>
        <IconButton
          icon={ChevronRight}
          title="Next page"
          disabled={page >= meta.total_pages}
          onClick={() => onChange(page + 1)}
        />
      </div>
    </div>
  )
}

export function Table({ head, children, className = '' }) {
  return (
    <div className={`overflow-x-auto rounded-xl border border-gray-200 bg-white ${className}`}>
      <table className="w-full text-sm">
        <thead className="border-b border-gray-200 bg-gray-50">
          <tr>
            {head.map((cell, i) => (
              <th
                key={i}
                className={`px-4 py-3 text-xs font-semibold uppercase tracking-wide text-gray-500 ${
                  cell.align === 'right' ? 'text-right' : 'text-left'
                }`}
              >
                {cell.label ?? cell}
              </th>
            ))}
          </tr>
        </thead>
        <tbody className="divide-y divide-gray-100">{children}</tbody>
      </table>
    </div>
  )
}

// --- formatting ------------------------------------------------------------

export function formatBytes(bytes) {
  if (!bytes) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB']
  const index = Math.min(units.length - 1, Math.floor(Math.log(bytes) / Math.log(1024)))
  return `${(bytes / 1024 ** index).toFixed(index === 0 ? 0 : 1)} ${units[index]}`
}

export function formatDuration(ms) {
  if (!ms || ms < 0) return '—'
  if (ms < 1000) return `${ms} ms`
  const seconds = ms / 1000
  if (seconds < 60) return `${seconds.toFixed(1)} s`
  const minutes = Math.floor(seconds / 60)
  return `${minutes}m ${Math.round(seconds % 60)}s`
}

export function formatDate(value) {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '—'
  return date.toLocaleString(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  })
}

export function relativeTime(value) {
  if (!value) return '—'
  const then = new Date(value).getTime()
  if (Number.isNaN(then)) return '—'
  const seconds = Math.round((Date.now() - then) / 1000)
  if (seconds < 60) return 'just now'
  const minutes = Math.round(seconds / 60)
  if (minutes < 60) return `${minutes}m ago`
  const hours = Math.round(minutes / 60)
  if (hours < 24) return `${hours}h ago`
  return `${Math.round(hours / 24)}d ago`
}
