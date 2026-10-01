import { useEffect, useRef, useState } from 'react'
import { ChevronDown, Download, FileText, FileType2, FileCode2 } from 'lucide-react'
import { api } from '../api/client'
import { Button } from './ui'

const FORMATS = [
  { value: 'pdf', label: 'PDF', hint: 'Print-ready booklet', icon: FileText },
  { value: 'docx', label: 'Word', hint: 'Editable .docx', icon: FileType2 },
  { value: 'md', label: 'Markdown', hint: 'Plain text', icon: FileCode2 },
]

const PARTS = [
  { value: 'both', label: 'Question paper + answer key' },
  { value: 'paper', label: 'Question paper only' },
  { value: 'key', label: 'Answer key & solutions only' },
]

/**
 * ExportMenu downloads a paper as a Dashhold-EdTech branded PDF or Word
 * booklet, or as Markdown. A paper that has not passed quality review can only
 * be downloaded as a draft, which the server stamps DRAFT on every page.
 */
export default function ExportMenu({ paperId, publishable, compact = false }) {
  const [open, setOpen] = useState(false)
  const [format, setFormat] = useState('pdf')
  const [part, setPart] = useState('both')
  const [explanations, setExplanations] = useState(true)
  const [watermark, setWatermark] = useState(true)
  const ref = useRef(null)

  useEffect(() => {
    if (!open) return undefined
    const onDown = (event) => {
      if (ref.current && !ref.current.contains(event.target)) setOpen(false)
    }
    const onKey = (event) => event.key === 'Escape' && setOpen(false)
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [open])

  const booklet = format === 'pdf' || format === 'docx'
  const href = api.exportUrl(paperId, {
    // Markdown keeps its old meaning of "answers": include the key or not.
    answers: booklet ? part !== 'paper' : part !== 'paper',
    explanations,
    draft: !publishable,
    format,
    part: booklet ? part : undefined,
    watermark: booklet ? watermark : undefined,
  })

  return (
    <div className="relative" ref={ref}>
      <Button
        variant="secondary"
        icon={Download}
        onClick={() => setOpen((value) => !value)}
        aria-haspopup="dialog"
        aria-expanded={open}
      >
        {compact ? '' : publishable ? 'Export' : 'Export draft'}
        <ChevronDown className="ml-1 h-3.5 w-3.5" aria-hidden="true" />
      </Button>

      {open && (
        <div
          role="dialog"
          aria-label="Export paper"
          className="absolute right-0 z-20 mt-2 w-80 rounded-lg border border-gray-200 bg-white p-4 text-left shadow-lg"
        >
          <p className="mb-2 text-xs font-semibold uppercase tracking-wide text-gray-500">Format</p>
          <div className="mb-4 grid grid-cols-3 gap-2">
            {FORMATS.map(({ value, label, hint, icon: Icon }) => (
              <button
                key={value}
                type="button"
                onClick={() => setFormat(value)}
                aria-pressed={format === value}
                title={hint}
                className={`flex flex-col items-center gap-1 rounded-md border px-2 py-2 text-xs font-medium transition-colors ${
                  format === value
                    ? 'border-primary-600 bg-primary-50 text-primary-700'
                    : 'border-gray-200 text-gray-700 hover:bg-gray-50'
                }`}
              >
                <Icon className="h-4 w-4" aria-hidden="true" />
                {label}
              </button>
            ))}
          </div>

          <p className="mb-2 text-xs font-semibold uppercase tracking-wide text-gray-500">Contents</p>
          <div className="mb-4 space-y-1.5">
            {PARTS.map(({ value, label }) => (
              <label key={value} className="flex cursor-pointer items-center gap-2 text-sm text-gray-700">
                <input
                  type="radio"
                  name={`export-part-${paperId}`}
                  value={value}
                  checked={part === value}
                  onChange={() => setPart(value)}
                />
                {label}
              </label>
            ))}
          </div>

          <div className="mb-4 space-y-1.5">
            <label className="flex cursor-pointer items-center gap-2 text-sm text-gray-700">
              <input
                type="checkbox"
                checked={explanations}
                disabled={part === 'paper'}
                onChange={(event) => setExplanations(event.target.checked)}
              />
              Include worked solutions
            </label>
            {booklet && (
              <label className="flex cursor-pointer items-center gap-2 text-sm text-gray-700">
                <input type="checkbox" checked={watermark} onChange={(event) => setWatermark(event.target.checked)} />
                Dashhold-EdTech watermark
              </label>
            )}
          </div>

          {!publishable && (
            <p className="mb-3 rounded-md bg-amber-50 px-3 py-2 text-xs text-amber-800">
              This paper has not passed quality review, so the download is stamped DRAFT.
            </p>
          )}

          <a href={href} download onClick={() => setOpen(false)} className="block">
            <Button icon={Download} className="w-full justify-center">
              {publishable ? 'Download' : 'Download draft'}
            </Button>
          </a>
        </div>
      )}
    </div>
  )
}
