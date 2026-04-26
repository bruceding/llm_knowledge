import { useState, useRef, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { uploadPDF, clipWeb } from '../api'

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

  // RSS state
  const [rssUrl, setRssUrl] = useState('')
  const [rssFeeds, setRssFeeds] = useState<{ name: string; url: string; count?: number }[]>([])
  const [addingRss, setAddingRss] = useState(false)

  const fileInputRef = useRef<HTMLInputElement>(null)

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

  // Handle adding RSS feed (placeholder)
  const handleAddRss = async () => {
    if (!rssUrl.trim()) return

    setAddingRss(true)
    setError(null)

    try {
      // Placeholder: would call backend to add RSS feed
      // Backend doesn't have RSS endpoint yet
      const feedName = rssUrl.split('/').pop() || 'RSS Feed'
      setRssFeeds((prev) => [...prev, { name: feedName, url: rssUrl }])
      setRssUrl('')
      setError(t('import.errorRssNotImplemented'))
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to add RSS feed')
    } finally {
      setAddingRss(false)
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
            <div className="flex gap-2 mb-4">
              <input
                type="url"
                value={rssUrl}
                onChange={(e) => setRssUrl(e.target.value)}
                placeholder="https://example.com/rss"
                disabled={addingRss}
                className="flex-1 px-4 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 disabled:bg-gray-100"
              />
              <button
                onClick={handleAddRss}
                disabled={addingRss || !rssUrl.trim()}
                className="px-4 py-2 bg-orange-500 text-white rounded-lg hover:bg-orange-600 transition-colors disabled:bg-gray-300 disabled:text-gray-500"
              >
                {addingRss ? t('import.adding') : t('import.addFeed')}
              </button>
            </div>

            {rssFeeds.length > 0 && (
              <div className="space-y-2">
                <h4 className="text-sm font-medium text-gray-700">{t('import.activeFeeds')}</h4>
                <ul className="space-y-2">
                  {rssFeeds.map((feed) => (
                    <li
                      key={feed.url}
                      className="flex items-center justify-between px-3 py-2 bg-gray-50 rounded-lg"
                    >
                      <div>
                        <div className="text-sm font-medium text-gray-800">{feed.name}</div>
                        <div className="text-xs text-gray-500 truncate max-w-xs">{feed.url}</div>
                      </div>
                      <button
                        onClick={() => setRssFeeds((prev) => prev.filter((f) => f.url !== feed.url))}
                        className="text-gray-400 hover:text-red-500"
                      >
                        <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                          <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
                        </svg>
                      </button>
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