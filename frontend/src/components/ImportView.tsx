import { useState, useRef, useCallback, useEffect } from 'react'
import { useTranslation } from 'react-i18next'
import { uploadPDF, uploadPDFUrl, clipWeb, addRSSFeed, listRSSFeeds, deleteRSSFeed, syncRSSFeed } from '../api'

export default function ImportView() {
  const { t } = useTranslation()
  const [dragActive, setDragActive] = useState(false)
  const [uploading, setUploading] = useState(false)
  const [uploadProgress, setUploadProgress] = useState<string | null>(null)
  const [uploadResult, setUploadResult] = useState<{ id: number; path: string; message: string; pages: number } | null>(null)
  const [error, setError] = useState<string | null>(null)

  // URL clipping state
  const [urlInput, setUrlInput] = useState('')
  const [clippingUrl, setClippingUrl] = useState(false)

  // PDF URL state
  const [pdfUrl, setPdfUrl] = useState('')
  const [uploadingFromUrl, setUploadingFromUrl] = useState(false)

  // RSS state
  const [rssUrl, setRssUrl] = useState('')
  const [rssName, setRssName] = useState('')
  const [rssAutoSync, setRssAutoSync] = useState(false)
  const [rssFeeds, setRssFeeds] = useState<any[]>([])
  const [addingRss, setAddingRss] = useState(false)
  const [syncingFeedId, setSyncingFeedId] = useState<number | null>(null)

  const fileInputRef = useRef<HTMLInputElement>(null)

  // Load RSS feeds on mount
  useEffect(() => {
    loadRSSFeeds()
  }, [])

  const loadRSSFeeds = async () => {
    try {
      const feeds = await listRSSFeeds()
      setRssFeeds(feeds)
    } catch (err) {
      console.error('Failed to load RSS feeds:', err)
    }
  }

  // Handle drag events
  const handleDrag = useCallback((e: React.DragEvent) => {
    e.preventDefault()
    e.stopPropagation()
    if (e.type === 'dragenter' || e.type === 'dragover') {
      setDragActive(true)
    } else if (e.type === 'dragleave') {
      setDragActive(false)
    }
  }, [])

  // Handle drop
  const handleDrop = useCallback(async (e: React.DragEvent) => {
    e.preventDefault()
    e.stopPropagation()
    setDragActive(false)

    const files = e.dataTransfer.files
    if (files && files.length > 0) {
      const file = files[0]
      if (file.type === 'application/pdf') {
        await handleUpload(file)
      } else {
        setError(t('import.errorOnlyPdf'))
      }
    }
  }, [])

  // Handle file upload
  const handleUpload = async (file: File) => {
    setUploading(true)
    setError(null)
    setUploadResult(null)
    setUploadProgress(`Uploading ${file.name}...`)

    try {
      const result = await uploadPDF(file)
      setUploadResult(result)
      setUploadProgress(`Successfully processed ${file.name} (${result.pages} pages)`)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Upload failed')
      setUploadProgress(null)
    } finally {
      setUploading(false)
    }
  }

  // Handle file input change
  const handleFileChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const files = e.target.files
    if (files && files.length > 0) {
      const file = files[0]
      handleUpload(file)
    }
  }

  // Handle PDF upload from URL
  const handleUploadFromUrl = async () => {
    if (!pdfUrl.trim()) return

    setUploadingFromUrl(true)
    setError(null)
    setUploadResult(null)
    setUploadProgress(`Downloading PDF from URL...`)

    try {
      const result = await uploadPDFUrl(pdfUrl)
      setUploadResult(result)
      setUploadProgress(`Successfully processed PDF (${result.pages} pages)`)
      setPdfUrl('')
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to upload PDF from URL')
      setUploadProgress(null)
    } finally {
      setUploadingFromUrl(false)
    }
  }

  // Handle URL clipping
  const handleClipUrl = async () => {
    if (!urlInput.trim()) return

    setClippingUrl(true)
    setError(null)
    setUploadResult(null)

    try {
      const result = await clipWeb(urlInput)
      setUploadResult({
        id: result.id,
        path: result.path,
        message: result.message,
        pages: result.images, // Reuse pages field for image count
      })
      setUrlInput('')
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to clip URL')
    } finally {
      setClippingUrl(false)
    }
  }

  // Handle adding RSS feed
  const handleAddRss = async () => {
    if (!rssUrl.trim()) return

    setAddingRss(true)
    setError(null)

    try {
      // Backend will parse RSS feed title if name is empty
      await addRSSFeed(rssName.trim(), rssUrl, rssAutoSync)
      setRssUrl('')
      setRssName('')
      setRssAutoSync(false)
      await loadRSSFeeds()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to add RSS feed')
    } finally {
      setAddingRss(false)
    }
  }

  // Handle syncing RSS feed
  const handleSyncFeed = async (feedId: number) => {
    setSyncingFeedId(feedId)
    setError(null)

    try {
      const result = await syncRSSFeed(feedId)
      if (result.newArticles > 0) {
        setUploadResult({
          id: 0,
          path: '',
          message: `Synced ${result.newArticles} new articles`,
          pages: result.newArticles,
        })
      }
      await loadRSSFeeds()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to sync RSS feed')
    } finally {
      setSyncingFeedId(null)
    }
  }

  // Handle deleting RSS feed
  const handleDeleteFeed = async (feedId: number) => {
    try {
      await deleteRSSFeed(feedId)
      await loadRSSFeeds()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to delete RSS feed')
    }
  }

  return (
    <div className="p-6">
      <h2 className="text-2xl font-bold text-gray-800 mb-4">{t('import.title')}</h2>
      <p className="text-gray-600 mb-6">{t('import.description')}</p>

      {error && (
        <div className="mb-4 bg-red-50 border border-red-200 rounded-lg p-4 text-red-700 flex items-center justify-between">
          <span>{error}</span>
          <button onClick={() => setError(null)} className="text-red-800 hover:text-red-900">
            <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
            </svg>
          </button>
        </div>
      )}

      {uploadResult && (
        <div className="mb-4 bg-green-50 border border-green-200 rounded-lg p-4 text-green-700">
          <div className="font-medium">{uploadResult.message}</div>
          <div className="text-sm mt-1">
            Document ID: {uploadResult.id}, Pages: {uploadResult.pages}
          </div>
          <a
            href={`/documents/${uploadResult.id}`}
            className="inline-block mt-2 text-green-800 underline hover:text-green-900"
          >
            {t('import.viewDocument')}
          </a>
        </div>
      )}

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
        {/* PDF Upload */}
        <div className="space-y-4">
          <h3 className="text-lg font-semibold text-gray-700">{t('import.uploadPdf')}</h3>
          <div
            className={`border-2 border-dashed rounded-lg p-12 text-center transition-colors ${
              dragActive ? 'border-blue-500 bg-blue-50' : 'border-gray-300'
            }`}
            onDragEnter={handleDrag}
            onDragLeave={handleDrag}
            onDragOver={handleDrag}
            onDrop={handleDrop}
          >
            {uploading ? (
              <div className="flex flex-col items-center">
                <div className="animate-spin rounded-full h-12 w-12 border-b-2 border-blue-500 mb-4"></div>
                <p className="text-gray-600">{uploadProgress}</p>
              </div>
            ) : (
              <>
                <div className="text-gray-400 mb-4">
                  <svg
                    className="mx-auto h-12 w-12"
                    fill="none"
                    viewBox="0 0 24 24"
                    stroke="currentColor"
                  >
                    <path
                      strokeLinecap="round"
                      strokeLinejoin="round"
                      strokeWidth={1.5}
                      d="M7 16a4 4 0 01-.88-7.903A5 5 0 1115.9 6L16 6a5 5 0 011 9.9M15 13l-3-3m0 0l-3 3m3-3v12"
                    />
                  </svg>
                </div>
                <p className="text-gray-600 mb-2">{t('import.dragDropHint')}</p>
                <p className="text-sm text-gray-400">{t('import.pdfSizeLimit')}</p>
                <button
                  onClick={() => fileInputRef.current?.click()}
                  className="mt-4 px-4 py-2 bg-blue-500 text-white rounded-lg hover:bg-blue-600 transition-colors"
                >
                  {t('import.selectPdf')}
                </button>
                <input
                  ref={fileInputRef}
                  type="file"
                  accept=".pdf"
                  onChange={handleFileChange}
                  className="hidden"
                />
              </>
            )}
          </div>

          {/* PDF URL input */}
          <div className="border border-gray-200 rounded-lg p-4">
            <p className="text-gray-600 mb-3 text-sm">{t('import.pdfUrlHint')}</p>
            <div className="flex gap-2">
              <input
                type="url"
                value={pdfUrl}
                onChange={(e) => setPdfUrl(e.target.value)}
                placeholder="https://arxiv.org/pdf/xxxx.pdf"
                disabled={uploadingFromUrl}
                className="flex-1 px-4 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 disabled:bg-gray-100"
              />
              <button
                onClick={handleUploadFromUrl}
                disabled={uploadingFromUrl || !pdfUrl.trim()}
                className="px-4 py-2 bg-blue-500 text-white rounded-lg hover:bg-blue-600 transition-colors disabled:bg-gray-300 disabled:text-gray-500"
              >
                {uploadingFromUrl ? t('import.uploading') : t('import.importFromUrl')}
              </button>
            </div>
          </div>
        </div>

        {/* Web Clipping */}
        <div className="space-y-4">
          <h3 className="text-lg font-semibold text-gray-700">{t('import.webClipping')}</h3>
          <div className="border border-gray-200 rounded-lg p-6">
            <p className="text-gray-600 mb-4 text-sm">
              {t('import.webClipHint')}
            </p>
            <div className="flex gap-2">
              <input
                type="url"
                value={urlInput}
                onChange={(e) => setUrlInput(e.target.value)}
                placeholder="https://example.com/article"
                disabled={clippingUrl}
                className="flex-1 px-4 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 disabled:bg-gray-100"
              />
              <button
                onClick={handleClipUrl}
                disabled={clippingUrl || !urlInput.trim()}
                className="px-4 py-2 bg-green-500 text-white rounded-lg hover:bg-green-600 transition-colors disabled:bg-gray-300 disabled:text-gray-500"
              >
                {clippingUrl ? t('import.clipping') : t('import.clip')}
              </button>
            </div>
          </div>

          {/* RSS Feeds */}
          <h3 className="text-lg font-semibold text-gray-700 mt-6">{t('import.rssFeeds')}</h3>
          <div className="border border-gray-200 rounded-lg p-6">
            <p className="text-gray-600 mb-4 text-sm">
              {t('import.rssHint')}
            </p>
            <div className="space-y-3">
              <input
                type="text"
                value={rssName}
                onChange={(e) => setRssName(e.target.value)}
                placeholder="Feed name (optional)"
                disabled={addingRss}
                className="w-full px-4 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 disabled:bg-gray-100"
              />
              <input
                type="url"
                value={rssUrl}
                onChange={(e) => setRssUrl(e.target.value)}
                placeholder="https://example.com/rss"
                disabled={addingRss}
                className="w-full px-4 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 disabled:bg-gray-100"
              />
              <div className="flex items-center gap-2">
                <input
                  type="checkbox"
                  id="autoSync"
                  checked={rssAutoSync}
                  onChange={(e) => setRssAutoSync(e.target.checked)}
                  disabled={addingRss}
                  className="w-4 h-4"
                />
                <label htmlFor="autoSync" className="text-sm text-gray-600">
                  Auto sync (sync automatically in background)
                </label>
              </div>
              <button
                onClick={handleAddRss}
                disabled={addingRss || !rssUrl.trim()}
                className="w-full px-4 py-2 bg-orange-500 text-white rounded-lg hover:bg-orange-600 transition-colors disabled:bg-gray-300 disabled:text-gray-500"
              >
                {addingRss ? t('import.adding') : t('import.addFeed')}
              </button>
            </div>

            {rssFeeds.length > 0 && (
              <div className="mt-6 space-y-2">
                <h4 className="text-sm font-medium text-gray-700">{t('import.activeFeeds')}</h4>
                <ul className="space-y-2">
                  {rssFeeds.map((feed) => (
                    <li
                      key={feed.id}
                      className="flex items-center justify-between px-3 py-2 bg-gray-50 rounded-lg"
                    >
                      <div>
                        <div className="text-sm font-medium text-gray-800">{feed.name}</div>
                        <div className="text-xs text-gray-500 truncate max-w-xs">{feed.url}</div>
                        <div className="text-xs text-gray-400">
                          {feed.articleCount} articles • Last sync: {feed.lastSyncAt ? new Date(feed.lastSyncAt).toLocaleDateString() : 'Never'}
                        </div>
                      </div>
                      <div className="flex items-center gap-2">
                        <button
                          onClick={() => handleSyncFeed(feed.id)}
                          disabled={syncingFeedId === feed.id}
                          className="text-blue-500 hover:text-blue-700 disabled:text-gray-400"
                          title="Sync now"
                        >
                          {syncingFeedId === feed.id ? (
                            <div className="animate-spin rounded-full h-5 w-5 border-b-2 border-blue-500"></div>
                          ) : (
                            <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
                            </svg>
                          )}
                        </button>
                        <button
                          onClick={() => handleDeleteFeed(feed.id)}
                          className="text-gray-400 hover:text-red-500"
                          title="Delete feed"
                        >
                          <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
                          </svg>
                        </button>
                      </div>
                    </li>
                  ))}
                </ul>
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  )
}