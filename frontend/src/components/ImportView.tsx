import { useState, useRef, useCallback, useEffect } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router-dom'
import { uploadPDF, uploadPDFUrl, clipWeb, addRSSFeed, listRSSFeeds, deleteRSSFeed, syncRSSFeed, getIMAPConfig, syncNewsletter, getNewsletterSyncStatus, addBlogFeed, listBlogFeeds, configBlogFeed, syncBlogFeed, deleteBlogFeed, type BlogFeed, type AddBlogFeedResult } from '../api'
import { useConfirm } from '../hooks/useConfirm'
import { useIsMobile } from '../hooks/useIsMobile'

type ImportTab = 'pdf' | 'web' | 'rss' | 'blog' | 'newsletter'

const tabConfig: { key: ImportTab; icon: React.ReactNode; color: string; activeColor: string }[] = [
  {
    key: 'pdf',
    color: 'text-blue-500',
    activeColor: 'text-blue-600 bg-blue-50 border-blue-500',

    icon: (
      <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M7 21h10a2 2 0 002-2V9.414a1 1 0 00-.293-.707l-5.414-5.414A1 1 0 0012.586 3H7a2 2 0 00-2 2v14a2 2 0 002 2z" />
      </svg>
    ),
  },
  {
    key: 'web',
    color: 'text-green-500',
    activeColor: 'text-green-600 bg-green-50 border-green-500',

    icon: (
      <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M21 12a9 9 0 01-9 9m9-9a9 9 0 00-9-9m9 9H3m9 9a9 9 0 01-9-9m9 9c1.657 0 3-4.03 3-9s-1.343-9-3-9m0 18c-1.657 0-3-4.03-3-9s1.343-9 3-9m-9 9a9 9 0 019-9" />
      </svg>
    ),
  },
  {
    key: 'rss',
    color: 'text-orange-500',
    activeColor: 'text-orange-600 bg-orange-50 border-orange-500',

    icon: (
      <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 5c7.18 0 13 5.82 13 13M6 11a7 7 0 017 7m-6 0a1 1 0 11-2 0 1 1 0 012 0z" />
      </svg>
    ),
  },
  {
    key: 'blog',
    color: 'text-teal-500',
    activeColor: 'text-teal-600 bg-teal-50 border-teal-500',

    icon: (
      <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M19 20H5a2 2 0 01-2-2V6a2 2 0 012-2h10a2 2 0 012 2v1m2 13a2 2 0 01-2-2V7m2 13a2 2 0 002-2V9.5a2 2 0 00-2-2h-2m-4-3H9M7 16h6M7 8h6v4H7V8z" />
      </svg>
    ),
  },
  {
    key: 'newsletter',
    color: 'text-purple-500',
    activeColor: 'text-purple-600 bg-purple-50 border-purple-500',

    icon: (
      <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M3 8l7.89 5.26a2 2 0 002.22 0L21 8M5 19h14a2 2 0 002-2V7a2 2 0 00-2-2H5a2 2 0 00-2 2v10a2 2 0 002 2z" />
      </svg>
    ),
  },
]

export default function ImportView() {
  const { t } = useTranslation()
  const isMobile = useIsMobile()
  const { confirm, dialog: confirmDialog } = useConfirm()
  const [activeTab, setActiveTab] = useState<ImportTab>('pdf')

  // PDF state
  const [dragActive, setDragActive] = useState(false)
  const [uploading, setUploading] = useState(false)
  const [uploadProgress, setUploadProgress] = useState<string | null>(null)
  const [uploadResult, setUploadResult] = useState<{ id: number; path: string; message: string; pages: number } | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [pdfUrl, setPdfUrl] = useState('')
  const [uploadingFromUrl, setUploadingFromUrl] = useState(false)

  // Web clipping state
  const [urlInput, setUrlInput] = useState('')
  const [clippingUrl, setClippingUrl] = useState(false)

  // RSS state
  const [rssUrl, setRssUrl] = useState('')
  const [rssName, setRssName] = useState('')
  const [rssAutoSync, setRssAutoSync] = useState(false)
  const [rssFeeds, setRssFeeds] = useState<any[]>([])
  const [addingRss, setAddingRss] = useState(false)
  const [syncingFeedId, setSyncingFeedId] = useState<number | null>(null)

  // Blog state
  const [blogUrl, setBlogUrl] = useState('')
  const [blogName, setBlogName] = useState('')
  const [blogAutoSync, setBlogAutoSync] = useState(false)
  const [blogFeeds, setBlogFeeds] = useState<BlogFeed[]>([])
  const [addingBlog, setAddingBlog] = useState(false)
  const [syncingBlogFeedId, setSyncingBlogFeedId] = useState<number | null>(null)
  const [showBlogConfig, setShowBlogConfig] = useState(false)
  const [configBlogFeedId, setConfigBlogFeedId] = useState<number | null>(null)
  const [configLinkSelector, setConfigLinkSelector] = useState('')
  const [configContentSelector, setConfigContentSelector] = useState('')
  const [configLinkExclude, setConfigLinkExclude] = useState('')
  const [configuringBlog, setConfiguringBlog] = useState(false)

  // Newsletter state
  const [newsletterConfigured, setNewsletterConfigured] = useState(false)
  const [newsletterLastSync, setNewsletterLastSync] = useState<string | null>(null)
  const [syncingNewsletter, setSyncingNewsletter] = useState(false)

  const fileInputRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    loadRSSFeeds()
    loadBlogFeeds()
    loadNewsletterConfig()
  }, [])

  const loadRSSFeeds = async () => {
    try {
      const feeds = await listRSSFeeds()
      setRssFeeds(feeds)
    } catch (err) {
      console.error('Failed to load RSS feeds:', err)
    }
  }

  const loadBlogFeeds = async () => {
    try {
      const feeds = await listBlogFeeds()
      setBlogFeeds(feeds)
    } catch (err) {
      console.error('Failed to load blog feeds:', err)
    }
  }

  const loadNewsletterConfig = async () => {
    try {
      const res = await getIMAPConfig()
      setNewsletterConfigured(res.configured)
      if (res.configured && res.config) {
        setNewsletterLastSync(res.config.lastSyncAt)
      }
    } catch (err) {
      console.error('Failed to load newsletter config:', err)
    }
  }

  const handleSyncNewsletter = async () => {
    setSyncingNewsletter(true)
    setError(null)
    try {
      await syncNewsletter()
      // Poll for completion
      const poll = async () => {
        try {
          const status = await getNewsletterSyncStatus()
          if (status.running) {
            setTimeout(poll, 2000)
          } else {
            setSyncingNewsletter(false)
            if (status.result) {
              if (status.result.error) {
                setError(status.result.error)
              } else {
                setUploadResult({
                  id: 0,
                  path: '',
                  message: status.result.message,
                  pages: status.result.newArticles,
                })
              }
            }
            await loadNewsletterConfig()
          }
        } catch {
          setSyncingNewsletter(false)
          setError('Failed to check sync status')
        }
      }
      setTimeout(poll, 2000)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to start sync')
      setSyncingNewsletter(false)
    }
  }

  const handleDrag = useCallback((e: React.DragEvent) => {
    e.preventDefault()
    e.stopPropagation()
    if (e.type === 'dragenter' || e.type === 'dragover') {
      setDragActive(true)
    } else if (e.type === 'dragleave') {
      setDragActive(false)
    }
  }, [])

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

  const handleFileChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const files = e.target.files
    if (files && files.length > 0) {
      handleUpload(files[0])
    }
  }

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

  const handleClipUrl = async () => {
    if (!urlInput.trim()) return
    setClippingUrl(true)
    setError(null)
    setUploadResult(null)
    try {
      const result = await clipWeb(urlInput.trim())
      setUploadResult({
        id: result.id,
        path: result.path,
        message: result.message,
        pages: result.images,
      })
      setUrlInput('')
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to clip URL')
    } finally {
      setClippingUrl(false)
    }
  }

  const handleAddRss = async () => {
    if (!rssUrl.trim()) return
    setAddingRss(true)
    setError(null)
    try {
      await addRSSFeed(rssName.trim(), rssUrl.trim(), rssAutoSync)
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

  const handleSyncFeed = async (feedId: number) => {
    setSyncingFeedId(feedId)
    setError(null)
    try {
      const result = await syncRSSFeed(feedId)
      if (result.newArticles > 0) {
        setUploadResult({
          id: 0,
          path: '',
          message: t('import.syncCompleted', { count: result.newArticles }),
          pages: result.newArticles,
        })
      } else {
        setUploadResult({
          id: 0,
          path: '',
          message: t('import.syncNoNew'),
          pages: 0,
        })
      }
      await loadRSSFeeds()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to sync RSS feed')
    } finally {
      setSyncingFeedId(null)
    }
  }

  const handleDeleteFeed = async (feedId: number) => {
    const confirmed = await confirm({
      title: t('common.delete'),
      message: t('import.deleteFeedConfirm'),
    })
    if (!confirmed) return
    try {
      await deleteRSSFeed(feedId)
      await loadRSSFeeds()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to delete RSS feed')
    }
  }

  const handleAddBlog = async () => {
    if (!blogUrl.trim()) return
    setAddingBlog(true)
    setError(null)
    try {
      const result: AddBlogFeedResult = await addBlogFeed(blogName.trim(), blogUrl.trim(), blogAutoSync)
      if (result.needConfig) {
        // Show config dialog
        setConfigBlogFeedId(result.feed.id)
        setShowBlogConfig(true)
      } else {
        setBlogUrl('')
        setBlogName('')
        setBlogAutoSync(false)
        await loadBlogFeeds()
        if (result.syncResult) {
          const message = result.syncResult.newArticles > 0
            ? t('import.syncCompleted', { count: result.syncResult.newArticles })
            : t('import.syncNoNew')
          setUploadResult({
            id: 0,
            path: '',
            message,
            pages: result.syncResult.newArticles,
          })
        } else if (result.detected) {
          setUploadResult({
            id: 0,
            path: '',
            message: t('import.platformDetected', { platform: result.platformType }),
            pages: 0,
          })
        }
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to add blog feed')
    } finally {
      setAddingBlog(false)
    }
  }

  const handleConfigBlog = async () => {
    if (!configLinkSelector.trim() || !configContentSelector.trim()) return
    if (!configBlogFeedId) return
    setConfiguringBlog(true)
    setError(null)
    try {
      await configBlogFeed(configBlogFeedId, configLinkSelector, configContentSelector, configLinkExclude)
      setShowBlogConfig(false)
      setConfigBlogFeedId(null)
      setConfigLinkSelector('')
      setConfigContentSelector('')
      setConfigLinkExclude('')
      await loadBlogFeeds()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to configure blog feed')
    } finally {
      setConfiguringBlog(false)
    }
  }

  const handleSyncBlogFeed = async (feedId: number) => {
    setSyncingBlogFeedId(feedId)
    setError(null)
    try {
      const result = await syncBlogFeed(feedId)
      if (result.newArticles > 0) {
        setUploadResult({
          id: 0,
          path: '',
          message: t('import.syncCompleted', { count: result.newArticles }),
          pages: result.newArticles,
        })
      } else {
        setUploadResult({
          id: 0,
          path: '',
          message: t('import.syncNoNew'),
          pages: 0,
        })
      }
      await loadBlogFeeds()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to sync blog feed')
    } finally {
      setSyncingBlogFeedId(null)
    }
  }

  const handleDeleteBlogFeed = async (feedId: number) => {
    const confirmed = await confirm({
      title: t('common.delete'),
      message: t('import.deleteFeedConfirm'),
    })
    if (!confirmed) return
    try {
      await deleteBlogFeed(feedId)
      await loadBlogFeeds()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to delete blog feed')
    }
  }

  if (isMobile) {
    return (
      <div className="flex flex-col items-center justify-center h-full p-8 text-center text-gray-700">
        <svg className="w-16 h-16 text-gray-400 mb-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
          <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2}
            d="M12 18h.01M8 21h8a2 2 0 002-2V5a2 2 0 00-2-2H8a2 2 0 00-2 2v14a2 2 0 002 2z" />
        </svg>
        <h2 className="text-lg font-semibold mb-2">{t('mobile.import.desktopOnly')}</h2>
        <p className="text-sm text-gray-500 mb-6">{t('mobile.import.desktopOnlyHint')}</p>
        <Link to="/" className="px-4 py-2 bg-blue-500 text-white rounded text-sm">
          {t('mobile.import.backToInbox')}
        </Link>
      </div>
    )
  }

  const tabLabel: Record<ImportTab, string> = {
    pdf: t('import.uploadPdf'),
    web: t('import.webClipping'),
    rss: t('import.rssFeeds'),
    blog: t('import.blogFeeds'),
    newsletter: t('import.newsletter'),
  }

  return (
    <>
    <div className="p-6">
      <h2 className="text-2xl font-bold text-gray-800 mb-2">{t('import.title')}</h2>
      <p className="text-gray-500 mb-6">{t('import.description')}</p>

      {/* Global notifications */}
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
          {uploadResult.id > 0 && (
            <div className="text-sm mt-1">
              Document ID: {uploadResult.id}, Pages: {uploadResult.pages}
            </div>
          )}
          {uploadResult.id > 0 && (
            <a
              href={`/documents/${uploadResult.id}`}
              className="inline-block mt-2 text-green-800 underline hover:text-green-900"
            >
              {t('import.viewDocument')}
            </a>
          )}
        </div>
      )}

      {/* Tab bar */}
      <div className="flex gap-1 border-b border-gray-200 mb-6">
        {tabConfig.map(({ key, icon, color, activeColor }) => (
          <button
            key={key}
            onClick={() => setActiveTab(key)}
            className={`flex items-center gap-2 px-4 py-2.5 text-sm font-medium border-b-2 -mb-px transition-colors rounded-t-lg ${
              activeTab === key
                ? `${activeColor} border-b-2`
                : `text-gray-500 border-transparent hover:text-gray-700 hover:bg-gray-50`
            }`}
          >
            <span className={activeTab === key ? '' : color}>{icon}</span>
            {tabLabel[key]}
          </button>
        ))}
      </div>

      {/* Tab content */}
      <div className="max-w-2xl">
        {activeTab === 'pdf' && (
          <div className="space-y-4">
            <div
              className={`border-2 border-dashed rounded-xl p-12 text-center transition-colors ${
                dragActive ? 'border-blue-500 bg-blue-50' : 'border-gray-300 hover:border-gray-400'
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
                    <svg className="mx-auto h-12 w-12" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                      <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M7 16a4 4 0 01-.88-7.903A5 5 0 1115.9 6L16 6a5 5 0 011 9.9M15 13l-3-3m0 0l-3 3m3-3v12" />
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

            <div className="border border-gray-200 rounded-xl p-5">
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
        )}

        {activeTab === 'web' && (
          <div className="border border-gray-200 rounded-xl p-6">
            <p className="text-gray-600 mb-4 text-sm">{t('import.webClipHint')}</p>
            <div className="flex gap-2">
              <input
                type="url"
                value={urlInput}
                onChange={(e) => setUrlInput(e.target.value)}
                placeholder="https://example.com/article"
                disabled={clippingUrl}
                className="flex-1 px-4 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-green-500 disabled:bg-gray-100"
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
        )}

        {activeTab === 'rss' && (
          <div className="space-y-4">
            <div className="border border-gray-200 rounded-xl p-6">
              <p className="text-gray-600 mb-4 text-sm">{t('import.rssHint')}</p>
              <div className="space-y-3">
                <input
                  type="text"
                  value={rssName}
                  onChange={(e) => setRssName(e.target.value)}
                  placeholder="Feed name (optional)"
                  disabled={addingRss}
                  className="w-full px-4 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-orange-500 disabled:bg-gray-100"
                />
                <input
                  type="url"
                  value={rssUrl}
                  onChange={(e) => setRssUrl(e.target.value)}
                  placeholder="https://example.com/rss"
                  disabled={addingRss}
                  className="w-full px-4 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-orange-500 disabled:bg-gray-100"
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
            </div>

            {rssFeeds.length > 0 && (
              <div className="border border-gray-200 rounded-xl p-6">
                <h4 className="text-sm font-medium text-gray-700 mb-3">{t('import.activeFeeds')}</h4>
                <ul className="space-y-2">
                  {rssFeeds.map((feed) => (
                    <li
                      key={feed.id}
                      className="flex items-center justify-between px-3 py-2.5 bg-gray-50 rounded-lg"
                    >
                      <div className="min-w-0 flex-1">
                        <div className="text-sm font-medium text-gray-800">{feed.name}</div>
                        <div className="text-xs text-gray-500 truncate">{feed.url}</div>
                        <div className="text-xs text-gray-400">
                          {feed.articleCount} articles · Last sync: {feed.lastSyncAt && feed.lastSyncAt !== '0001-01-01T00:00:00Z' ? new Date(feed.lastSyncAt).toLocaleDateString() : 'Never'}
                        </div>
                      </div>
                      <div className="flex items-center gap-2 ml-3 shrink-0">
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
        )}

        {activeTab === 'blog' && (
          <div className="space-y-4">
            <div className="border border-gray-200 rounded-xl p-6">
              <p className="text-gray-600 mb-4 text-sm">{t('import.blogHint')}</p>
              <div className="space-y-3">
                <input
                  type="text"
                  value={blogName}
                  onChange={(e) => setBlogName(e.target.value)}
                  placeholder="Feed name (optional)"
                  disabled={addingBlog}
                  className="w-full px-4 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-teal-500 disabled:bg-gray-100"
                />
                <input
                  type="url"
                  value={blogUrl}
                  onChange={(e) => setBlogUrl(e.target.value)}
                  placeholder="https://claude.com/blog"
                  disabled={addingBlog}
                  className="w-full px-4 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-teal-500 disabled:bg-gray-100"
                />
                <div className="flex items-center gap-2">
                  <input
                    type="checkbox"
                    id="blogAutoSync"
                    checked={blogAutoSync}
                    onChange={(e) => setBlogAutoSync(e.target.checked)}
                    disabled={addingBlog}
                    className="w-4 h-4"
                  />
                  <label htmlFor="blogAutoSync" className="text-sm text-gray-600">
                    Auto sync (sync automatically in background)
                  </label>
                </div>
                <button
                  onClick={handleAddBlog}
                  disabled={addingBlog || !blogUrl.trim()}
                  className="w-full px-4 py-2 bg-teal-500 text-white rounded-lg hover:bg-teal-600 transition-colors disabled:bg-gray-300 disabled:text-gray-500"
                >
                  {addingBlog ? t('import.adding') : t('import.addFeed')}
                </button>
              </div>
            </div>

            {blogFeeds.length > 0 && (
              <div className="border border-gray-200 rounded-xl p-6">
                <h4 className="text-sm font-medium text-gray-700 mb-3">{t('import.activeFeeds')}</h4>
                <ul className="space-y-2">
                  {blogFeeds.map((feed) => (
                    <li
                      key={feed.id}
                      className="flex items-center justify-between px-3 py-2.5 bg-gray-50 rounded-lg"
                    >
                      <div className="min-w-0 flex-1">
                        <div className="text-sm font-medium text-gray-800">{feed.name}</div>
                        <div className="text-xs text-gray-500 truncate">{feed.indexUrl}</div>
                        <div className="text-xs text-gray-400">
                          {feed.platformType} · {feed.articleCount} articles · Last sync: {feed.lastSyncAt && feed.lastSyncAt !== '0001-01-01T00:00:00Z' ? new Date(feed.lastSyncAt).toLocaleDateString() : 'Never'}
                        </div>
                      </div>
                      <div className="flex items-center gap-2 ml-3 shrink-0">
                        <button
                          onClick={() => handleSyncBlogFeed(feed.id)}
                          disabled={syncingBlogFeedId === feed.id}
                          className="text-teal-500 hover:text-teal-700 disabled:text-gray-400"
                          title="Sync now"
                        >
                          {syncingBlogFeedId === feed.id ? (
                            <div className="animate-spin rounded-full h-5 w-5 border-b-2 border-teal-500"></div>
                          ) : (
                            <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
                            </svg>
                          )}
                        </button>
                        <button
                          onClick={() => handleDeleteBlogFeed(feed.id)}
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
        )}

        {activeTab === 'newsletter' && (
          <div className="border border-gray-200 rounded-xl p-6">
            <p className="text-gray-600 mb-4 text-sm">{t('import.newsletterHint')}</p>
            {!newsletterConfigured ? (
              <div className="text-center py-6">
                <div className="text-gray-400 mb-3">
                  <svg className="mx-auto h-10 w-10" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M3 8l7.89 5.26a2 2 0 002.22 0L21 8M5 19h14a2 2 0 002-2V7a2 2 0 00-2-2H5a2 2 0 00-2 2v10a2 2 0 002 2z" />
                  </svg>
                </div>
                <p className="text-gray-500 mb-3 text-sm">{t('import.newsletterNotConfigured')}</p>
                <a
                  href="/settings"
                  className="inline-block px-4 py-2 bg-purple-500 text-white rounded-lg hover:bg-purple-600 transition-colors text-sm"
                >
                  {t('import.goToSettings')}
                </a>
              </div>
            ) : (
              <div className="flex items-center justify-between">
                <div className="text-sm text-gray-600">
                  {t('import.lastSync')}: {newsletterLastSync && newsletterLastSync !== '0001-01-01T00:00:00Z'
                    ? new Date(newsletterLastSync).toLocaleString()
                    : t('import.never')}
                </div>
                <button
                  onClick={handleSyncNewsletter}
                  disabled={syncingNewsletter}
                  className="px-4 py-2 bg-purple-500 text-white rounded-lg hover:bg-purple-600 transition-colors disabled:bg-gray-300 disabled:text-gray-500 text-sm"
                >
                  {syncingNewsletter ? t('import.syncing') : t('import.syncNewsletter')}
                </button>
              </div>
            )}
          </div>
        )}
      </div>
    </div>
    {confirmDialog}

    {/* Blog config dialog */}
    {showBlogConfig && (
      <div
        className="fixed inset-0 bg-black/50 flex items-center justify-center z-50"
        onClick={() => setShowBlogConfig(false)}
        onKeyDown={(e) => e.key === 'Escape' && setShowBlogConfig(false)}
      >
        <div
          className="bg-white rounded-xl shadow-xl max-w-md w-full mx-4"
          onClick={(e) => e.stopPropagation()}
        >
          <div className="p-6">
            <div className="flex items-center gap-3 mb-4">
              <div className="w-10 h-10 rounded-full bg-teal-100 flex items-center justify-center">
                <svg className="w-5 h-5 text-teal-600" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.066 2.573c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.573 1.066c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.066-2.573c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z" />
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
                </svg>
              </div>
              <div>
                <h3 className="text-lg font-semibold text-gray-900">{t('import.blogConfigTitle')}</h3>
                <p className="text-sm text-gray-500">{t('import.blogConfigHint')}</p>
              </div>
            </div>
            <div className="space-y-3">
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">
                  {t('import.linkSelector')} <span className="text-red-500">*</span>
                </label>
                <input
                  type="text"
                  value={configLinkSelector}
                  onChange={(e) => setConfigLinkSelector(e.target.value)}
                  placeholder="a[href^='/blog/']"
                  disabled={configuringBlog}
                  className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-teal-500 disabled:bg-gray-100 text-sm"
                />
              </div>
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">
                  {t('import.contentSelector')} <span className="text-red-500">*</span>
                </label>
                <input
                  type="text"
                  value={configContentSelector}
                  onChange={(e) => setConfigContentSelector(e.target.value)}
                  placeholder=".post-content"
                  disabled={configuringBlog}
                  className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-teal-500 disabled:bg-gray-100 text-sm"
                />
              </div>
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">
                  {t('import.linkExclude')}
                </label>
                <input
                  type="text"
                  value={configLinkExclude}
                  onChange={(e) => setConfigLinkExclude(e.target.value)}
                  placeholder=".sidebar a, .footer a"
                  disabled={configuringBlog}
                  className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-teal-500 disabled:bg-gray-100 text-sm"
                />
              </div>
            </div>
          </div>
          <div className="flex justify-end gap-3 px-6 py-4 border-t border-gray-100">
            <button
              onClick={() => {
                setShowBlogConfig(false)
                setConfigBlogFeedId(null)
                setConfigLinkSelector('')
                setConfigContentSelector('')
                setConfigLinkExclude('')
              }}
              disabled={configuringBlog}
              className="px-4 py-2 text-sm text-gray-700 bg-gray-100 rounded-lg hover:bg-gray-200 disabled:opacity-50"
            >
              {t('common.cancel')}
            </button>
            <button
              onClick={handleConfigBlog}
              disabled={configuringBlog || !configLinkSelector.trim() || !configContentSelector.trim()}
              className="px-4 py-2 text-sm text-white bg-teal-500 rounded-lg hover:bg-teal-600 disabled:bg-gray-300 disabled:text-gray-500"
            >
              {configuringBlog ? t('import.adding') : t('import.saveConfig')}
            </button>
          </div>
        </div>
      </div>
    )}
    </>
  )
}
