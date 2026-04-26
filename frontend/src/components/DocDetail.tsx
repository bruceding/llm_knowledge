import { useState, useEffect, useCallback } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { useTranslation } from 'react-i18next'
import { fetchDocument, updateDocument, publishDocument, deleteDocument, regenerateSummary, getPagesStatus, fetchSettings, checkPDFTranslationStatus, translatePDF } from '../api'
import type { Document, SSEEvent, UserSettings } from '../types'
import PDFViewer from './PDFViewer'
import PDFTranslationView from './PDFTranslationView'
import DocumentChatPanel from './DocumentChatPanel'
import DualPDFViewer from './DualPDFViewer'

export default function DocDetail() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const { t, i18n } = useTranslation()
  const [document, setDocument] = useState<Document | null>(null)
  const [wikiContent, setWikiContent] = useState<string>('')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  // Editable fields
  const [editTitle, setEditTitle] = useState('')
  const [editTags, setEditTags] = useState<string[]>([])
  const [editStatus, setEditStatus] = useState<string>('')
  const [tagInput, setTagInput] = useState('')

  // Translation state (for existing translations)
  const [translationContent, setTranslationContent] = useState('')
  const [translationLang, setTranslationLang] = useState<string>('')
  const [totalPages, setTotalPages] = useState(0)

  // PDF Translation state
  const [settings, setSettings] = useState<UserSettings | null>(null)
  const [pdfTranslationStatus, setPdfTranslationStatus] = useState<{ exists: boolean; path?: string; targetLang?: string } | null>(null)
  const [pdfTranslating, setPdfTranslating] = useState(false)
  const [pdfTranslationProgress, setPdfTranslationProgress] = useState('')
  const [translatedPdfPath, setTranslatedPdfPath] = useState<string | null>(null)

  // View mode - default to PDF, then Wiki if available
  const [viewMode, setViewMode] = useState<'wiki' | 'translation' | 'bilingual' | 'pdf' | 'dual-pdf'>('pdf')

  // Summary regeneration state
  const [regeneratingSummary, setRegeneratingSummary] = useState(false)

  // Metadata panel tab state
  const [metadataTab, setMetadataTab] = useState<'metadata' | 'chat'>('metadata')

  // Panel width state - responsive default based on screen size
  const [panelWidth, setPanelWidth] = useState(() => {
    const width = window.innerWidth
    if (width >= 1920) return 480
    if (width >= 1440) return 400
    return 320
  })
  const [isResizing, setIsResizing] = useState(false)
  const [panelHidden, setPanelHidden] = useState(false)

  // Publish state
  const [publishing, setPublishing] = useState(false)

  // Load document and content
  useEffect(() => {
    if (!id) return
    loadDocument()
  }, [id])

  const loadDocument = async () => {
    setLoading(true)
    setError(null)
    try {
      const doc = await fetchDocument(parseInt(id!))
      setDocument(doc)
      setEditTitle(doc.title)
      setEditTags(doc.tags.map((t) => t.name))
      setEditStatus(doc.status)

      // Load settings
      try {
        const s = await fetchSettings()
        setSettings(s)
      } catch (err) {
        console.error('Failed to load settings:', err)
      }

      // Load wiki content
      if (doc.wikiPath) {
        const wikiRes = await fetch(`/data/${doc.wikiPath}`)
        if (wikiRes.ok) {
          setWikiContent(await wikiRes.text())
        }
      }

      // Load existing translation if available
      if (doc.rawPath) {
        // Check for Chinese translation
        const zhRes = await fetch(`/data/${doc.rawPath}/paper_zh.md`)
        if (zhRes.ok) {
          const zhContent = await zhRes.text()
          if (zhContent.trim()) {
            setTranslationContent(zhContent)
            setTranslationLang('zh')
          }
        }

        // Check for English translation (if original is Chinese)
        if (!translationContent && doc.language === 'zh') {
          const enRes = await fetch(`/data/${doc.rawPath}/paper_en.md`)
          if (enRes.ok) {
            const enContent = await enRes.text()
            if (enContent.trim()) {
              setTranslationContent(enContent)
              setTranslationLang('en')
            }
          }
        }

        // Load page count for PDF documents
        if (doc.sourceType === 'pdf') {
          try {
            const pagesStatus = await getPagesStatus(doc.id)
            if (pagesStatus.exists) {
              setTotalPages(pagesStatus.page_count)
            }
          } catch (err) {
            console.error('Failed to get pages status:', err)
          }

          // Check PDF translation status
          try {
            const pdfStatus = await checkPDFTranslationStatus(doc.id)
            setPdfTranslationStatus(pdfStatus)
            if (pdfStatus.exists && pdfStatus.path) {
              setTranslatedPdfPath(pdfStatus.path)
            }
          } catch (err) {
            console.error('Failed to check PDF translation status:', err)
          }
        }
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load document')
    } finally {
      setLoading(false)
    }
  }

  // Handle panel resize
  useEffect(() => {
    const handleMouseMove = (e: MouseEvent) => {
      if (!isResizing) return
      const newWidth = window.innerWidth - e.clientX
      // Clamp width between 240 and 600
      setPanelWidth(Math.max(240, Math.min(600, newWidth)))
    }

    const handleMouseUp = () => {
      setIsResizing(false)
    }

    if (isResizing) {
      window.document.addEventListener('mousemove', handleMouseMove)
      window.document.addEventListener('mouseup', handleMouseUp)
    }

    return () => {
      window.document.removeEventListener('mousemove', handleMouseMove)
      window.document.removeEventListener('mouseup', handleMouseUp)
    }
  }, [isResizing])

  const startResizing = () => {
    setIsResizing(true)
  }

  const handleSave = async () => {
    if (!document) return
    try {
      const updated = await updateDocument(document.id, {
        title: editTitle,
        status: editStatus,
        tagNames: editTags,
      })
      setDocument(updated)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save')
    }
  }

  const handlePublish = async () => {
    if (!document) return
    setPublishing(true)
    try {
      await publishDocument(document.id)
      await loadDocument() // Refresh to get wikiPath
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to publish')
    } finally {
      setPublishing(false)
    }
  }

  const handleDelete = async () => {
    if (!document) return
    if (!confirm(t('docDetail.deleteConfirm'))) return
    try {
      await deleteDocument(document.id)
      navigate('/')
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to delete')
    }
  }

  const handleAddTag = () => {
    const tag = tagInput.trim()
    if (tag && !editTags.includes(tag)) {
      setEditTags([...editTags, tag])
      setTagInput('')
    }
  }

  const handleRemoveTag = (tag: string) => {
    setEditTags(editTags.filter((t) => t !== tag))
  }

  // PDF Translation handler (layout-preserving)
  const handlePDFTranslate = useCallback(async (targetLang?: string) => {
    if (!document || !settings?.translationEnabled) return

    const lang = targetLang || (document.language === 'en' ? 'zh' : 'en')
    setPdfTranslating(true)
    setPdfTranslationProgress(t('docDetail.translationProgress'))

    try {
      await translatePDF(document.id, (event: SSEEvent) => {
        if (event.type === 'progress') {
          setPdfTranslationProgress(event.message || 'Processing...')
        } else if (event.type === 'error') {
          setError(event.error || 'Translation failed')
          setPdfTranslating(false)
        } else if (event.type === 'complete') {
          setPdfTranslating(false)
          if (event.translatedPdf) {
            setTranslatedPdfPath(event.translatedPdf)
            setPdfTranslationStatus({ exists: true, path: event.translatedPdf, targetLang: lang })
            setViewMode('dual-pdf')
          }
        }
      }, lang)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to translate PDF')
      setPdfTranslating(false)
    }
  }, [document, settings, t])

  const getDisplayContent = () => {
    switch (viewMode) {
      case 'wiki':
        return wikiContent
      case 'translation':
        return translationContent
      case 'bilingual':
        return null // Bilingual view renders separately
      case 'pdf':
        return null // PDF is rendered in iframe
      case 'dual-pdf':
        return null // Dual PDF view renders separately
      default:
        return wikiContent
    }
  }

  // Check if PDF file exists
  const pdfUrl = document?.rawPath ? `/data/${document.rawPath}/paper.pdf` : null

  if (loading) {
    return (
      <div className="flex items-center justify-center h-full">
        <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-blue-500"></div>
      </div>
    )
  }

  if (error && !document) {
    return (
      <div className="p-6">
        <div className="bg-red-50 border border-red-200 rounded-lg p-4 text-red-700">
          {error}
          <button onClick={loadDocument} className="ml-4 text-red-800 underline">
            {t('docDetail.retry')}
          </button>
        </div>
      </div>
    )
  }

  if (!document) {
    return (
      <div className="p-6">
        <div className="text-gray-500">{t('docDetail.documentNotFound')}</div>
      </div>
    )
  }

  return (
    <div className="flex h-full">
      {/* Left: Markdown content */}
      <div className="flex-1 flex flex-col overflow-hidden">
        {/* Header with view mode toggle */}
        <div className="p-4 border-b border-gray-200 flex items-center justify-between">
          <div className="flex items-center gap-4">
            {/* Back button */}
            <button
              onClick={() => navigate('/documents')}
              className="p-2 text-gray-600 hover:bg-gray-100 rounded-lg transition-colors"
              title="Back to documents list"
            >
              <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M15 19l-7-7 7-7" />
              </svg>
            </button>

            <button
              onClick={() => setViewMode('pdf')}
              className={`px-3 py-1.5 rounded-lg text-sm ${
                viewMode === 'pdf' ? 'bg-blue-100 text-blue-700' : 'text-gray-600 hover:bg-gray-100'
              }`}
            >
              {t('docDetail.rawContent')}
            </button>
            {wikiContent && (
              <button
                onClick={() => setViewMode('wiki')}
                className={`px-3 py-1.5 rounded-lg text-sm ${
                  viewMode === 'wiki' ? 'bg-blue-100 text-blue-700' : 'text-gray-600 hover:bg-gray-100'
                }`}
              >
                {t('docDetail.wikiContent')}
              </button>
            )}
            {translationContent && (
              <button
                onClick={() => setViewMode(document.sourceType === 'pdf' ? 'bilingual' : 'translation')}
                className={`px-3 py-1.5 rounded-lg text-sm ${
                  (viewMode === 'bilingual' || viewMode === 'translation') ? 'bg-blue-100 text-blue-700' : 'text-gray-600 hover:bg-gray-100'
                }`}
              >
                {t('docDetail.translation')} ({translationLang.toUpperCase()})
              </button>
            )}
            {pdfTranslationStatus?.exists && (
              <button
                onClick={() => setViewMode('dual-pdf')}
                className={`px-3 py-1.5 rounded-lg text-sm ${
                  viewMode === 'dual-pdf' ? 'bg-purple-100 text-purple-700' : 'text-gray-600 hover:bg-gray-100'
                }`}
              >
                {t('docDetail.dualPdfView')}
              </button>
            )}
          </div>

          {/* Translate buttons */}
          <div className="flex items-center gap-2">
            {/* PDF Translation button - only when settings enabled and no translation yet */}
            {document.sourceType === 'pdf' && settings?.translationEnabled && !pdfTranslationStatus?.exists && !pdfTranslating && (
              <button
                onClick={() => handlePDFTranslate()}
                className="px-3 py-1.5 text-sm bg-purple-100 text-purple-700 rounded-lg hover:bg-purple-200"
              >
                {t('docDetail.translatePdf')}
              </button>
            )}
            {/* PDF Translation progress */}
            {pdfTranslating && (
              <div className="flex items-center gap-2 px-3 py-1.5 bg-purple-50 rounded-lg">
                <div className="animate-spin h-4 w-4 border-2 border-purple-500 rounded-full border-t-transparent"></div>
                <span className="text-sm text-purple-700">{pdfTranslationProgress}</span>
              </div>
            )}
          </div>
        </div>

        {/* Markdown content area */}
        <div className="flex-1 overflow-auto">
          {viewMode === 'pdf' && pdfUrl ? (
            <div className="h-full">
              <PDFViewer url={pdfUrl} />
            </div>
          ) : viewMode === 'dual-pdf' && pdfUrl && translatedPdfPath ? (
            <DualPDFViewer originalUrl={pdfUrl} translatedUrl={translatedPdfPath} />
          ) : viewMode === 'bilingual' && document?.rawPath ? (
            <PDFTranslationView
              rawPath={document.rawPath}
              translatedContent={translationContent}
              totalPages={totalPages}
            />
          ) : (
            <div className="p-6 max-w-4xl mx-auto prose prose-slate">
              <ReactMarkdown
                remarkPlugins={[remarkGfm]}
                components={{
                  // Custom link handling for wiki links
                  a: ({ href, children }) => {
                    if (href?.startsWith('wiki://')) {
                      const wikiPath = href.replace('wiki://', '')
                      return (
                        <a
                          href={`/wiki/${wikiPath}`}
                          className="text-blue-600 hover:underline"
                          onClick={(e) => {
                            e.preventDefault()
                            navigate(`/wiki/${wikiPath}`)
                          }}
                        >
                          {children}
                        </a>
                      )
                    }
                    return <a href={href} className="text-blue-600 hover:underline">{children}</a>
                  },
                  // Handle image paths - convert relative to absolute
                  img: ({ src, alt }) => {
                    if (src && document?.rawPath) {
                      // Convert relative path to absolute /data/ path
                      if (!src.startsWith('/') && !src.startsWith('http')) {
                        src = `/data/${document.rawPath}/${src}`
                      }
                    }
                    return <img src={src} alt={alt} className="max-w-full h-auto rounded-lg shadow-sm" />
                  },
                }}
              >
                {getDisplayContent() || t('docDetail.noContent')}
              </ReactMarkdown>
            </div>
          )}
        </div>
      </div>

      {/* Show panel button when hidden */}
      {panelHidden && (
        <button
          onClick={() => setPanelHidden(false)}
          className="fixed right-4 top-20 z-50 p-2 bg-white border border-gray-300 rounded-lg shadow-md hover:bg-gray-100 transition-colors"
          title="Show panel"
        >
          <svg className="w-5 h-5 text-gray-600" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M4 6h16M4 12h16M4 18h7" />
          </svg>
        </button>
      )}

      {/* Right: Metadata panel */}
      {!panelHidden && (
      <div
        style={{ width: panelWidth }}
        className="border-l border-gray-200 bg-gray-50 flex flex-col overflow-hidden relative"
      >
        {/* Resize handle */}
        <div
          onMouseDown={startResizing}
          className="absolute left-0 top-0 bottom-0 w-1 cursor-col-resize hover:bg-blue-400 transition-colors"
          style={{ backgroundColor: isResizing ? '#60a5fa' : 'transparent' }}
        />

        {/* Tab bar with hide button */}
        <div className="flex border-b border-gray-200 bg-white items-center">
          <button
            onClick={() => setMetadataTab('metadata')}
            className={`flex-1 px-3 py-1.5 text-xs font-medium ${
              metadataTab === 'metadata'
                ? 'text-blue-600 border-b-2 border-blue-600'
                : 'text-gray-500 hover:text-gray-700'
            }`}
          >
            {t('docDetail.metadataTab')}
          </button>
          <button
            onClick={() => setMetadataTab('chat')}
            className={`flex-1 px-3 py-1.5 text-xs font-medium ${
              metadataTab === 'chat'
                ? 'text-blue-600 border-b-2 border-blue-600'
                : 'text-gray-500 hover:text-gray-700'
            }`}
          >
            {t('docDetail.chatTab')}
          </button>
          <button
            onClick={() => setPanelHidden(true)}
            className="p-1 text-gray-500 hover:text-gray-700 hover:bg-gray-100 rounded"
            title="Hide panel"
          >
            <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
            </svg>
          </button>
        </div>

        {/* Metadata tab content - CSS hidden/show */}
        <div className={`flex-1 overflow-auto ${metadataTab === 'metadata' ? '' : 'hidden'}`}>
          <div className="p-3 space-y-2">
          {/* Title */}
          <div>
            <label className="block text-xs font-medium text-gray-500 mb-0.5">{t('documentsList.titleColumn')}</label>
            <input
              type="text"
              value={editTitle}
              onChange={(e) => setEditTitle(e.target.value)}
              className="w-full px-2 py-1 text-sm border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>

          {/* Summary */}
          <div>
            <label className="block text-xs font-medium text-gray-500 mb-0.5">Summary</label>
            <div className="px-2 py-1 bg-white border border-gray-200 rounded text-gray-600 text-xs min-h-[40px]">
              {document.summary || 'No summary generated'}
            </div>
            <button
              onClick={async () => {
                if (!document) return
                setRegeneratingSummary(true)
                try {
                  const result = await regenerateSummary(document.id)
                  setDocument({ ...document, summary: result.summary })
                } catch (err) {
                  setError(err instanceof Error ? err.message : 'Failed to regenerate summary')
                } finally {
                  setRegeneratingSummary(false)
                }
              }}
              disabled={regeneratingSummary}
              className="mt-1 w-full px-2 py-1 text-xs bg-gray-100 text-gray-600 rounded hover:bg-gray-200 disabled:opacity-50"
            >
              {regeneratingSummary ? 'Generating...' : 'Regenerate'}
            </button>
          </div>

          {/* Status */}
          <div>
            <label className="block text-xs font-medium text-gray-500 mb-0.5">{t('docDetail.status')}</label>
            <select
              value={editStatus}
              onChange={(e) => setEditStatus(e.target.value)}
              className="w-full px-2 py-1 text-sm border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-blue-500"
            >
              <option value="inbox">{t('docDetail.inbox')}</option>
              <option value="published">{t('documentsList.published')}</option>
              <option value="archived">{t('docDetail.archivedStatus')}</option>
            </select>
          </div>

          {/* Tags */}
          <div>
            <label className="block text-xs font-medium text-gray-500 mb-0.5">{t('docDetail.tags')}</label>
            <div className="flex flex-wrap gap-1 mb-1">
              {editTags.map((tag) => (
                <span
                  key={tag}
                  className="px-1.5 py-0.5 text-xs bg-blue-100 text-blue-700 rounded-full flex items-center gap-0.5"
                >
                  {tag}
                  <button
                    onClick={() => handleRemoveTag(tag)}
                    className="w-3 h-3 text-blue-500 hover:text-blue-700"
                  >
                    ×
                  </button>
                </span>
              ))}
            </div>
            <div className="flex gap-1">
              <input
                type="text"
                value={tagInput}
                onChange={(e) => setTagInput(e.target.value)}
                placeholder={t('docDetail.addTagPlaceholder')}
                className="flex-1 px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-blue-500"
                onKeyDown={(e) => {
                  if (e.key === 'Enter') handleAddTag()
                }}
              />
              <button
                onClick={handleAddTag}
                className="px-2 py-1 text-xs bg-blue-500 text-white rounded hover:bg-blue-600"
              >
                +
              </button>
            </div>
          </div>

          {/* Dates */}
          <div className="text-xs text-gray-500 pt-2 border-t border-gray-200">
            <div className="flex justify-between">
              <span>Created:</span>
              <span>{new Date(document.createdAt).toLocaleDateString(i18n.language === 'zh' ? 'zh-CN' : 'en-US')}</span>
            </div>
            <div className="flex justify-between mt-0.5">
              <span>Updated:</span>
              <span>{new Date(document.updatedAt).toLocaleDateString(i18n.language === 'zh' ? 'zh-CN' : 'en-US')}</span>
            </div>
          </div>
          </div>
        </div>

        {/* Chat tab content - CSS hidden/show */}
        <div className={`flex-1 overflow-hidden ${metadataTab === 'chat' ? '' : 'hidden'}`}>
          <DocumentChatPanel docId={document.id} active={metadataTab === 'chat'} />
        </div>

        {/* Action buttons - only show in metadata tab */}
        <div className={`p-2 border-t border-gray-200 space-y-1 ${metadataTab === 'metadata' ? '' : 'hidden'}`}>
          {error && (
            <div className="text-xs text-red-600 mb-1">{error}</div>
          )}
          <button
            onClick={handleSave}
            className="w-full px-3 py-1.5 text-sm bg-blue-500 text-white rounded hover:bg-blue-600 transition-colors"
          >
            {t('docDetail.saveChanges')}
          </button>
          {document.status !== 'published' && (
            <button
              onClick={handlePublish}
              disabled={publishing}
              className="w-full px-3 py-1.5 text-sm bg-green-500 text-white rounded hover:bg-green-600 disabled:opacity-50 transition-colors"
            >
              {publishing ? 'Publishing...' : t('docDetail.publish')}
            </button>
          )}
          {document.status !== 'archived' && (
            <button
              onClick={() => {
                setEditStatus('archived')
                handleSave()
              }}
              className="w-full px-3 py-1.5 text-sm bg-gray-500 text-white rounded hover:bg-gray-600 transition-colors"
            >
              {t('docDetail.archive')}
            </button>
          )}
          <button
            onClick={handleDelete}
            className="w-full px-3 py-1.5 text-sm bg-red-500 text-white rounded hover:bg-red-600 transition-colors"
          >
            {t('docDetail.delete')}
          </button>
        </div>
      </div>
      )}
    </div>
  )
}