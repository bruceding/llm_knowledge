import { useState, useEffect, useRef } from 'react'
import { Link, useLocation, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { fetchInbox, updateDocument, deleteDocument } from '../api'
import { useConfirm } from '../hooks/useConfirm'
import { useIsMobile } from '../hooks/useIsMobile'
import { useMobileShell } from './Layout/MobileShellStore'
import { setInboxCursor, consumeInboxCursor } from '../store/inboxCursor'
import type { Document } from '../types'

export default function Inbox() {
  const location = useLocation()
  const navigate = useNavigate()
  const { t, i18n } = useTranslation()
  const { confirm, dialog: confirmDialog } = useConfirm()
  const isMobile = useIsMobile()
  const setTitle = useMobileShell((s) => s.setTitle)
  const setRightSlot = useMobileShell((s) => s.setRightSlot)
  const [documents, setDocuments] = useState<Document[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [activeDocId, setActiveDocId] = useState<number | null>(null)
  const itemRefs = useRef<Map<number, HTMLElement>>(new Map())
  const confirmingRef = useRef(false)

  useEffect(() => {
    setTitle(t('sidebar.inbox'))
    setRightSlot(null)
    return () => setTitle('')
  }, [t, setTitle, setRightSlot])

  useEffect(() => {
    loadDocuments()
  }, [location.key])

  // Scroll the active item into view when selection changes (keyboard nav)
  useEffect(() => {
    if (activeDocId === null) return
    itemRefs.current.get(activeDocId)?.scrollIntoView({ block: 'nearest', behavior: 'smooth' })
  }, [activeDocId])

  // Restore the cursor after the list (re)loads: select the remembered doc, or —
  // if it's gone (deleted / moved) — the item now occupying its slot. One-shot.
  useEffect(() => {
    if (documents.length === 0) return
    const cursor = consumeInboxCursor()
    if (!cursor) return
    const existing = documents.find((d) => d.id === cursor.docId)
    const targetId = existing
      ? existing.id
      : cursor.index != null
        ? documents[Math.min(cursor.index, documents.length - 1)]?.id
        : undefined
    if (targetId != null) setActiveDocId(targetId)
  }, [documents])

  // Keyboard navigation & actions (desktop only)
  useEffect(() => {
    if (isMobile) return
    const handleKeyDown = async (e: KeyboardEvent) => {
      if (confirmingRef.current) return
      if (e.target instanceof HTMLInputElement || e.target instanceof HTMLTextAreaElement || e.target instanceof HTMLSelectElement) return
      if (e.metaKey || e.ctrlKey || e.altKey) return
      if (documents.length === 0) return

      const currentIndex = documents.findIndex((d) => d.id === activeDocId)

      if (e.key === 'j' || e.key === 'ArrowDown') {
        e.preventDefault()
        const next = currentIndex < 0 ? 0 : Math.min(currentIndex + 1, documents.length - 1)
        setActiveDocId(documents[next].id)
        return
      }
      if (e.key === 'k' || e.key === 'ArrowUp') {
        e.preventDefault()
        const prev = currentIndex < 0 ? documents.length - 1 : Math.max(currentIndex - 1, 0)
        setActiveDocId(documents[prev].id)
        return
      }

      if (activeDocId === null) return
      const activeDoc = documents.find((d) => d.id === activeDocId)
      if (!activeDoc) return

      if (e.key === 'Enter') {
        e.preventDefault()
        setInboxCursor(activeDocId, currentIndex)
        navigate(`/documents/${activeDocId}`)
        return
      }
      if (e.key === 'd') {
        e.preventDefault()
        confirmingRef.current = true
        const confirmed = await confirm({
          title: t('common.delete'),
          message: t('docDetail.deleteConfirm'),
        })
        confirmingRef.current = false
        if (!confirmed) return
        setInboxCursor(activeDocId, currentIndex)
        deleteDocument(activeDocId)
          .then(() => loadDocuments())
          .catch((err) => setError(err instanceof Error ? err.message : 'Failed to delete'))
        return
      }
      if (e.key === 'l') {
        if (activeDoc.status !== 'inbox') return
        e.preventDefault()
        setInboxCursor(activeDocId, currentIndex)
        updateDocument(activeDocId, { status: 'later' })
          .then(() => loadDocuments())
          .catch((err) => setError(err instanceof Error ? err.message : 'Failed to move to later'))
        return
      }
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [documents, activeDocId, isMobile, t, confirm, navigate])

  const loadDocuments = async () => {
    setLoading(true)
    setError(null)
    try {
      const docs = await fetchInbox()
      setDocuments(docs)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load documents')
    } finally {
      setLoading(false)
    }
  }

  const getSourceIcon = (sourceType: string) => {
    switch (sourceType) {
      case 'pdf':
        return (
          <svg className="w-5 h-5 text-red-500" fill="currentColor" viewBox="0 0 24 24">
            <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8l-6-6zm-1 2l5 5h-5V4zM6 20V4h6v6h6v10H6z" />
            <path d="M8 12h8v2H8zm0 4h8v2H8z" />
          </svg>
        )
      case 'rss':
        return (
          <svg className="w-5 h-5 text-orange-500" fill="currentColor" viewBox="0 0 24 24">
            <path d="M6.18 15.64a2.18 2.18 0 0 1 2.18 2.18C8.36 19 7.36 20 6.18 20C5 20 4 19 4 17.82a2.18 2.18 0 0 1 2.18-2.18M4 4.44A15.56 15.56 0 0 1 19.56 20h-2.83A12.73 12.73 0 0 0 4 7.27V4.44m0 5.66a9.9 9.9 0 0 1 9.9 9.9h-2.83A7.07 7.07 0 0 0 4 12.93V10.1z" />
          </svg>
        )
      case 'web':
        return (
          <svg className="w-5 h-5 text-blue-500" fill="currentColor" viewBox="0 0 24 24">
            <path d="M12 2C6.48 2 2 6.48 2 12s4.48 10 10 10 10-4.48 10-10S17.52 2 12 2zm-1 17.93c-3.95-.49-7-3.85-7-7.93 0-.62.08-1.21.21-1.79L9 15v1c0 1.1.9 2 2 2v1.93zm6.9-2.54c-.26-.81-1-1.39-1.9-1.39h-1v-3c0-.55-.45-1-1-1H8v-2h2c.55 0 1-.45 1-1V7h2c1.1 0 2-.9 2-2v-.41c2.93 1.19 5 4.06 5 7.41 0 2.08-.8 3.97-2.1 5.39z" />
          </svg>
        )
      default:
        return (
          <svg className="w-5 h-5 text-gray-500" fill="currentColor" viewBox="0 0 24 24">
            <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8l-6-6zm-1 2l5 5h-5V4zM6 20V4h6v6h6v10H6z" />
          </svg>
        )
    }
  }

  const formatDate = (dateStr: string) => {
    const date = new Date(dateStr)
    const locale = i18n.language === 'zh' ? 'zh-CN' : 'en-US'
    return date.toLocaleDateString(locale, {
      year: 'numeric',
      month: 'short',
      day: 'numeric',
    })
  }

  const getStatusBadge = (status: string) => {
    switch (status) {
      case 'inbox':
        return (
          <span className="px-2 py-1 text-xs bg-yellow-100 text-yellow-800 rounded-full">
            {t('inbox.pendingReview')}
          </span>
        )
      case 'published':
        return (
          <span className="px-2 py-1 text-xs bg-green-100 text-green-800 rounded-full">
            {t('inbox.published')}
          </span>
        )
      case 'archived':
        return (
          <span className="px-2 py-1 text-xs bg-gray-100 text-gray-800 rounded-full">
            {t('inbox.archived')}
          </span>
        )
      case 'later':
        return (
          <span className="px-2 py-1 text-xs bg-blue-100 text-blue-800 rounded-full">
            {t('inbox.later')}
          </span>
        )
      default:
        return null
    }
  }

  if (loading) {
    return (
      <div className="p-3 md:p-6">
        <h2 className="text-2xl font-bold text-gray-800 mb-4">{t('inbox.title')}</h2>
        <div className="flex items-center justify-center py-12">
          <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-blue-500"></div>
        </div>
      </div>
    )
  }

  if (error) {
    return (
      <div className="p-3 md:p-6">
        <h2 className="text-2xl font-bold text-gray-800 mb-4">{t('inbox.title')}</h2>
        <div className="bg-red-50 border border-red-200 rounded-lg p-4 text-red-700">
          {error}
          <button onClick={loadDocuments} className="ml-4 text-red-800 underline">
            {t('inbox.retry')}
          </button>
        </div>
      </div>
    )
  }

  return (
    <>
    <div className="p-3 md:p-6">
      <div className="flex items-center justify-between mb-6">
        <h2 className="text-2xl font-bold text-gray-800">{t('inbox.title')}</h2>
        <div className="flex items-center gap-2 text-sm text-gray-600">
          <span>{documents.length} {t('inbox.documentsPending')}</span>
          <button onClick={loadDocuments} className="p-2 hover:bg-gray-100 rounded-lg">
            <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path
                strokeLinecap="round"
                strokeLinejoin="round"
                strokeWidth={2}
                d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15"
              />
            </svg>
          </button>
        </div>
      </div>

      {documents.length === 0 ? (
        <div className="border-2 border-dashed border-gray-300 rounded-lg p-12 text-center text-gray-500">
          <svg className="w-12 h-12 mx-auto mb-4 text-gray-400" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path
              strokeLinecap="round"
              strokeLinejoin="round"
              strokeWidth={1.5}
              d="M20 13V6a2 2 0 00-2-2H6a2 2 0 00-2 2v7m16 0v5a2 2 0 01-2 2H6a2 2 0 01-2-2v-5m16 0h-2.586a1 1 0 00-.707.293l-2.414 2.414a1 1 0 01-.707.293h-3.172a1 1 0 01-.707-.293l-2.414-2.414A1 1 0 006.586 13H4"
            />
          </svg>
          <p className="text-lg font-medium mb-2">{t('inbox.noDocuments')}</p>
          <p className="text-sm">{t('inbox.importHint')}</p>
        </div>
      ) : (
        <div className="space-y-4">
          {documents.map((doc, index) => (
            <Link
              key={doc.id}
              ref={(el) => {
                if (el) itemRefs.current.set(doc.id, el)
                else itemRefs.current.delete(doc.id)
              }}
              data-testid={`inbox-item-${doc.id}`}
              data-active={doc.id === activeDocId ? 'true' : undefined}
              to={`/documents/${doc.id}`}
              onMouseEnter={() => setActiveDocId(doc.id)}
              onClick={() => setInboxCursor(doc.id, index)}
              className={`block bg-white border rounded-lg p-4 transition-all cursor-pointer group ${
                doc.id === activeDocId ? 'shadow-md border-blue-300' : 'border-gray-200 hover:shadow-md hover:border-blue-300'
              }`}
            >
              <div className="flex items-start justify-between mb-2">
                <div className="flex items-center gap-2">
                  {getSourceIcon(doc.sourceType)}
                  <h3 className={`font-medium transition-colors line-clamp-1 ${
                    doc.id === activeDocId ? 'text-blue-600' : 'text-gray-800 group-hover:text-blue-600'
                  }`}>
                    {doc.title}
                  </h3>
                </div>
              </div>

              {doc.summary && (
                <p className="text-sm text-gray-600 mb-3 line-clamp-2">
                  {doc.summary}
                </p>
              )}

              <div className="flex items-center gap-2 mb-3 text-sm text-gray-500">
                <span>{formatDate(doc.createdAt)}</span>
                <span className="text-gray-300">|</span>
                <span className="uppercase">{doc.language}</span>
                {doc.sourceType === 'rss' && doc.metadata && (() => {
                  try {
                    const meta = JSON.parse(doc.metadata)
                    return meta.feedName ? (
                      <>
                        <span className="text-gray-300">|</span>
                        <span className="text-orange-600">{meta.feedName}</span>
                      </>
                    ) : null
                  } catch { return null }
                })()}
              </div>

              <div className="flex items-center gap-2">
                {getStatusBadge(doc.status)}
                {doc.tags && doc.tags.length > 0 && (
                  <div className="flex items-center gap-1 flex-wrap">
                    {doc.tags.slice(0, 3).map((tag) => (
                      <span
                        key={tag.id}
                        className="px-2 py-0.5 text-xs rounded-full"
                        style={{
                          backgroundColor: tag.color + '20',
                          color: tag.color,
                        }}
                      >
                        {tag.name}
                      </span>
                    ))}
                    {doc.tags.length > 3 && (
                      <span className="text-xs text-gray-400">+{doc.tags.length - 3}</span>
                    )}
                  </div>
                )}
              </div>

              <div className="mt-3 pt-3 border-t border-gray-100 flex items-center justify-between text-xs text-gray-400">
                <div className="flex items-center gap-2">
                  <span className="capitalize">{doc.sourceType}</span>
                  {doc.status === 'inbox' && (
                    <button
                      onClick={(e) => {
                        e.preventDefault()
                        e.stopPropagation()
                        updateDocument(doc.id, { status: 'later' })
                          .then(() => loadDocuments())
                          .catch((err) => setError(err instanceof Error ? err.message : 'Failed to move to later'))
                      }}
                      className="px-2 py-1 bg-blue-500 text-white rounded hover:bg-blue-600 transition-colors"
                    >
                      {t('inbox.later')}
                    </button>
                  )}
                </div>
                <svg
                  className={`w-4 h-4 transition-colors ${doc.id === activeDocId ? 'text-blue-500' : 'group-hover:text-blue-500'}`}
                  fill="none"
                  viewBox="0 0 24 24"
                  stroke="currentColor"
                >
                  <path
                    strokeLinecap="round"
                    strokeLinejoin="round"
                    strokeWidth={2}
                    d="M9 5l7 7-7 7"
                  />
                </svg>
              </div>
            </Link>
          ))}
        </div>
      )}
    </div>
    {confirmDialog}
    </>
  )
}