import { useState, useEffect, useCallback } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { fetchDocument, updateDocument, publishDocument, deleteDocument, translateDocument, regenerateSummary } from '../api'
import type { Document, SSEEvent } from '../types'
import PDFViewer from './PDFViewer'

export default function DocDetail() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const [document, setDocument] = useState<Document | null>(null)
  const [wikiContent, setWikiContent] = useState<string>('')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  // Editable fields
  const [editTitle, setEditTitle] = useState('')
  const [editTags, setEditTags] = useState<string[]>([])
  const [editStatus, setEditStatus] = useState<string>('')
  const [tagInput, setTagInput] = useState('')

  // Translation state
  const [translating, setTranslating] = useState(false)
  const [translationContent, setTranslationContent] = useState('')
  const [translationLang, setTranslationLang] = useState<string>('')

  // View mode - default to PDF, then Wiki if available
  const [viewMode, setViewMode] = useState<'wiki' | 'translation' | 'pdf'>('pdf')

  // Summary regeneration state
  const [regeneratingSummary, setRegeneratingSummary] = useState(false)

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

      // Load wiki content
      if (doc.wikiPath) {
        const wikiRes = await fetch(`/data/${doc.wikiPath}`)
        if (wikiRes.ok) {
          setWikiContent(await wikiRes.text())
        }
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load document')
    } finally {
      setLoading(false)
    }
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
    try {
      await publishDocument(document.id)
      await loadDocument()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to publish')
    }
  }

  const handleDelete = async () => {
    if (!document) return
    if (!confirm('Are you sure you want to delete this document? This action cannot be undone.')) return
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

  const handleTranslate = useCallback(async (targetLang: string) => {
    if (!document) return
    setTranslating(true)
    setTranslationContent('')
    setTranslationLang(targetLang)
    setViewMode('translation')

    try {
      await translateDocument(document.id, targetLang, (event: SSEEvent) => {
        if (event.type === 'assistant') {
          setTranslationContent((prev) => prev + (event.content || ''))
        } else if (event.type === 'error') {
          setError(event.error || 'Translation failed')
          setTranslating(false)
        } else if (event.type === 'complete') {
          setTranslating(false)
        }
      })
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to translate')
      setTranslating(false)
    }
  }, [document])

  const getDisplayContent = () => {
    switch (viewMode) {
      case 'wiki':
        return wikiContent
      case 'translation':
        return translationContent
      case 'pdf':
        return null // PDF is rendered in iframe
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
            Retry
          </button>
        </div>
      </div>
    )
  }

  if (!document) {
    return (
      <div className="p-6">
        <div className="text-gray-500">Document not found</div>
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
            <button
              onClick={() => setViewMode('pdf')}
              className={`px-3 py-1.5 rounded-lg text-sm ${
                viewMode === 'pdf' ? 'bg-blue-100 text-blue-700' : 'text-gray-600 hover:bg-gray-100'
              }`}
            >
              PDF Preview
            </button>
            {wikiContent && (
              <button
                onClick={() => setViewMode('wiki')}
                className={`px-3 py-1.5 rounded-lg text-sm ${
                  viewMode === 'wiki' ? 'bg-blue-100 text-blue-700' : 'text-gray-600 hover:bg-gray-100'
                }`}
              >
                Wiki Page
              </button>
            )}
            {translationContent && (
              <button
                onClick={() => setViewMode('translation')}
                className={`px-3 py-1.5 rounded-lg text-sm ${
                  viewMode === 'translation' ? 'bg-blue-100 text-blue-700' : 'text-gray-600 hover:bg-gray-100'
                }`}
              >
                Translation ({translationLang.toUpperCase()})
              </button>
            )}
          </div>

          {/* Translate buttons */}
          <div className="flex items-center gap-2">
            {document.language === 'en' && (
              <button
                onClick={() => handleTranslate('zh')}
                disabled={translating}
                className="px-3 py-1.5 text-sm bg-gray-100 text-gray-700 rounded-lg hover:bg-gray-200 disabled:opacity-50"
              >
                {translating && translationLang === 'zh' ? 'Translating...' : 'Translate to Chinese'}
              </button>
            )}
            {document.language === 'zh' && (
              <button
                onClick={() => handleTranslate('en')}
                disabled={translating}
                className="px-3 py-1.5 text-sm bg-gray-100 text-gray-700 rounded-lg hover:bg-gray-200 disabled:opacity-50"
              >
                {translating && translationLang === 'en' ? 'Translating...' : 'Translate to English'}
              </button>
            )}
          </div>
        </div>

        {/* Markdown content area */}
        <div className="flex-1 overflow-auto p-6">
          {viewMode === 'pdf' && pdfUrl ? (
            <div className="h-full">
              <PDFViewer url={pdfUrl} />
            </div>
          ) : (
            <div className="max-w-4xl mx-auto prose prose-slate">
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
                {getDisplayContent() || 'No content available'}
              </ReactMarkdown>
            </div>
          )}
        </div>
      </div>

      {/* Right: Metadata panel */}
      <div className="w-80 border-l border-gray-200 bg-gray-50 flex flex-col overflow-hidden">
        <div className="p-4 border-b border-gray-200">
          <h3 className="text-lg font-semibold text-gray-800">Metadata</h3>
        </div>

        <div className="flex-1 overflow-auto p-4 space-y-4">
          {/* Title */}
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">Title</label>
            <input
              type="text"
              value={editTitle}
              onChange={(e) => setEditTitle(e.target.value)}
              className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500"
            />
          </div>

          {/* Source Type */}
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">Source Type</label>
            <div className="px-3 py-2 bg-white border border-gray-200 rounded-lg text-gray-600 capitalize">
              {document.sourceType}
            </div>
          </div>

          {/* Language */}
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">Language</label>
            <div className="px-3 py-2 bg-white border border-gray-200 rounded-lg text-gray-600 uppercase">
              {document.language}
            </div>
          </div>

          {/* Status */}
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">Status</label>
            <select
              value={editStatus}
              onChange={(e) => setEditStatus(e.target.value)}
              className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500"
            >
              <option value="inbox">Inbox</option>
              <option value="published">Published</option>
              <option value="archived">Archived</option>
            </select>
          </div>

          {/* Tags */}
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">Tags</label>
            <div className="flex flex-wrap gap-2 mb-2">
              {editTags.map((tag) => (
                <span
                  key={tag}
                  className="px-2 py-1 text-sm bg-blue-100 text-blue-700 rounded-full flex items-center gap-1"
                >
                  {tag}
                  <button
                    onClick={() => handleRemoveTag(tag)}
                    className="w-4 h-4 text-blue-500 hover:text-blue-700"
                  >
                    x
                  </button>
                </span>
              ))}
            </div>
            <div className="flex gap-2">
              <input
                type="text"
                value={tagInput}
                onChange={(e) => setTagInput(e.target.value)}
                placeholder="Add tag..."
                className="flex-1 px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500"
                onKeyDown={(e) => {
                  if (e.key === 'Enter') handleAddTag()
                }}
              />
              <button
                onClick={handleAddTag}
                className="px-3 py-2 bg-blue-500 text-white rounded-lg hover:bg-blue-600"
              >
                Add
              </button>
            </div>
          </div>

          {/* Summary */}
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">Summary</label>
            <div className="px-3 py-2 bg-white border border-gray-200 rounded-lg text-gray-600 text-sm min-h-[60px]">
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
              className="mt-2 w-full px-3 py-1.5 text-sm bg-gray-100 text-gray-700 rounded-lg hover:bg-gray-200 disabled:opacity-50"
            >
              {regeneratingSummary ? 'Generating...' : 'Regenerate Summary'}
            </button>
          </div>

          {/* Dates */}
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">Created</label>
            <div className="px-3 py-2 bg-white border border-gray-200 rounded-lg text-gray-600">
              {new Date(document.createdAt).toLocaleString('zh-CN')}
            </div>
          </div>

          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">Updated</label>
            <div className="px-3 py-2 bg-white border border-gray-200 rounded-lg text-gray-600">
              {new Date(document.updatedAt).toLocaleString('zh-CN')}
            </div>
          </div>
        </div>

        {/* Action buttons */}
        <div className="p-4 border-t border-gray-200 space-y-2">
          {error && (
            <div className="text-sm text-red-600 mb-2">{error}</div>
          )}
          <button
            onClick={handleSave}
            className="w-full px-4 py-2 bg-blue-500 text-white rounded-lg hover:bg-blue-600 transition-colors"
          >
            Save Changes
          </button>
          {document.status !== 'published' && (
            <button
              onClick={handlePublish}
              className="w-full px-4 py-2 bg-green-500 text-white rounded-lg hover:bg-green-600 transition-colors"
            >
              Publish
            </button>
          )}
          {document.status !== 'archived' && (
            <button
              onClick={() => {
                setEditStatus('archived')
                handleSave()
              }}
              className="w-full px-4 py-2 bg-gray-500 text-white rounded-lg hover:bg-gray-600 transition-colors"
            >
              Archive
            </button>
          )}
          <button
            onClick={handleDelete}
            className="w-full px-4 py-2 bg-red-500 text-white rounded-lg hover:bg-red-600 transition-colors"
          >
            Delete
          </button>
        </div>
      </div>
    </div>
  )
}