import { useState, useEffect, useCallback, useRef } from 'react'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { useTranslation } from 'react-i18next'
import { fetchPaperSections, sectionizePaper, generatePaperSection, type PaperSection } from '../api'

// PaperSectionsView: left chapter list + right per-chapter explanation.
// Explanations are lazy-generated on click (blocking -p on the backend),
// then cached to disk so re-opening the view is instant. See
// docs/plans/2026-06-27-paper-section-explain-design.md for the full layout.
export default function PaperSectionsView({ docId, summary, onAskPaper }: { docId: number; summary: string; onAskPaper: () => void }) {
  const { t } = useTranslation()
  const [sections, setSections] = useState<PaperSection[]>([])
  const [paperMdExists, setPaperMdExists] = useState(true)
  const [sectionizing, setSectionizing] = useState(false)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [generatingIndex, setGeneratingIndex] = useState<number | null>(null)
  const [genError, setGenError] = useState<string | null>(null)
  const [genErrorIndex, setGenErrorIndex] = useState<number | null>(null)
  // Guards against React StrictMode double-invoking the effect (dev) and
  // rapid re-mounts firing two POST /sectionize — the second would queue
  // behind summarySem and false-error after 30s.
  const sectionizeInflight = useRef(false)

  const doSectionize = useCallback(async () => {
    if (sectionizeInflight.current) return
    sectionizeInflight.current = true
    setSectionizing(true)
    setError(null)
    try {
      const { sections } = await sectionizePaper(docId)
      setSections(sections)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to identify sections')
    } finally {
      setSectionizing(false)
      sectionizeInflight.current = false
    }
  }, [docId])

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    setGenError(null)
    setGenErrorIndex(null)
    try {
      const { sections, paperMdExists, sectionized } = await fetchPaperSections(docId)
      setSections(sections)
      setPaperMdExists(paperMdExists)
      // `sectionized` (from the API response) drives whether to auto-fire
      // sectionize; no separate state needed — the response is fresh per load.
      if (paperMdExists && !sectionized) {
        // Fire sectionize; its own `sectionizing` state drives the
        // "识别章节中…" UI (one Claude call can take a minute+).
        doSectionize()
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to load sections')
    } finally {
      setLoading(false)
    }
  }, [docId, doSectionize])

  useEffect(() => { load() }, [load])

  const handleGenerate = async (index: number) => {
    setGeneratingIndex(index)
    setGenError(null)
    setGenErrorIndex(null)
    try {
      const updated = await generatePaperSection(docId, index)
      setSections(prev => prev.map(s => (s.index === index ? updated : s)))
    } catch (e) {
      // Inline per-section error — do NOT setError(), which would early-return
      // and wipe the whole chapter list + scroll position on one failure.
      setGenErrorIndex(index)
      setGenError(e instanceof Error ? e.message : 'Failed to generate')
    } finally {
      setGeneratingIndex(null)
    }
  }

  if (loading) {
    return (
      <div className="flex items-center justify-center h-full">
        <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-blue-500" />
      </div>
    )
  }
  if (error) {
    return <div data-testid="paper-sections-error" className="p-6 text-red-600">{error}</div>
  }
  if (!paperMdExists) {
    return (
      <div data-testid="paper-sections-empty" className="p-6 text-gray-500">
        {t('paperSections.noPaperMd')}
      </div>
    )
  }
  if (sectionizing) {
    return (
      <div data-testid="paper-sections-sectionizing" className="flex flex-col items-center justify-center h-full gap-3">
        <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-blue-500" />
        <div className="text-sm text-gray-600">{t('paperSections.sectionizing')}</div>
      </div>
    )
  }
  if (sections.length === 0) {
    return (
      <div data-testid="paper-sections-empty" className="p-6 text-gray-500">
        {t('paperSections.noSections')}
      </div>
    )
  }

  return (
    <div className="flex h-full">
      <nav className="w-56 shrink-0 border-r border-gray-200 overflow-auto p-2 space-y-1">
        {sections.map(s => (
          <button
            key={s.index}
            onClick={() => document.getElementById(`section-${s.index}`)?.scrollIntoView({ behavior: 'smooth' })}
            className={`block w-full text-left px-2 py-1.5 rounded text-sm ${s.explanation ? 'text-blue-700' : 'text-gray-600'} hover:bg-gray-100`}
          >
            {s.title}
          </button>
        ))}
      </nav>
      <div className="flex-1 flex flex-col overflow-hidden">
        {summary && (
          <div className="px-6 py-2 bg-gray-50 border-b border-gray-200 text-sm text-gray-600">
            📄 {summary}
          </div>
        )}
        <div className="flex-1 overflow-auto p-6 max-w-3xl prose prose-slate" data-testid="paper-sections-content">
          {sections.map(s => (
            <section key={s.index} id={`section-${s.index}`} className="mb-8">
              <h2 className="text-xl font-semibold mb-2">{s.title}</h2>
              {s.explanation ? (
                <>
                  <ReactMarkdown remarkPlugins={[remarkGfm]}>{s.explanation}</ReactMarkdown>
                  {s.hasBody !== false && (
                    <button
                      onClick={() => handleGenerate(s.index)}
                      disabled={generatingIndex !== null}
                      className="mt-2 px-2 py-1 text-xs text-gray-500 border border-gray-300 rounded hover:bg-gray-100 disabled:opacity-50"
                    >
                      {generatingIndex === s.index ? t('paperSections.generating') : t('paperSections.regenerate')}
                    </button>
                  )}
                </>
              ) : s.hasBody === false ? (
                // Parent section whose content lives in sub-sections — no body
                // to explain. Don't offer a generate button that would 4xx.
                <div className="text-sm text-gray-400 italic">{t('paperSections.noBody')}</div>
              ) : (
                <div>
                  <button
                    onClick={() => handleGenerate(s.index)}
                    disabled={generatingIndex !== null}
                    className="px-3 py-1.5 text-sm bg-blue-100 text-blue-700 rounded hover:bg-blue-200 disabled:opacity-50"
                  >
                    {generatingIndex === s.index ? t('paperSections.generating') : t('paperSections.generate')}
                  </button>
                  {genErrorIndex === s.index && genError && (
                    <div className="mt-2 text-sm text-red-600">{genError}</div>
                  )}
                </div>
              )}
            </section>
          ))}
          <div className="mt-8">
            <button
              onClick={onAskPaper}
              className="px-3 py-1.5 text-sm bg-blue-100 text-blue-700 rounded hover:bg-blue-200"
            >
              💬 {t('paperSections.askPaper')}
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}
