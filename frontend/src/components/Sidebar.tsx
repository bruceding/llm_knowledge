import { useState, useEffect } from 'react'
import { Link, useLocation, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { fetchInbox, fetchDocuments, logout } from '../api'
import { useAuthStore } from '../store/authStore'

export default function Sidebar() {
  const { t } = useTranslation()
  const location = useLocation()
  const navigate = useNavigate()
  const username = useAuthStore((s) => s.username)
  const clearAuth = useAuthStore((s) => s.clearAuth)
  const [searchQuery, setSearchQuery] = useState('')
  const [inboxCount, setInboxCount] = useState(0)
  const [archivedCount, setArchivedCount] = useState(0)
  const [expandedSections, setExpandedSections] = useState({
    navigation: true,
    wiki: true,
    conversations: true,
  })

  async function handleLogout() {
    try {
      await logout()
    } catch (e) {}
    clearAuth()
    navigate('/login')
  }

  useEffect(() => {
    // Fetch inbox and archived count on mount and when location changes
    fetchInbox()
      .then((docs) => setInboxCount(docs.length))
      .catch(() => {})
    fetchDocuments('archived')
      .then((docs) => setArchivedCount(docs.length))
      .catch(() => {})
  }, [location.pathname])

  const toggleSection = (section: keyof typeof expandedSections) => {
    setExpandedSections((prev) => ({ ...prev, [section]: !prev[section] }))
  }

  const handleSearch = (e: React.FormEvent) => {
    e.preventDefault()
    if (searchQuery.trim()) {
      // Navigate to search results or wiki with query
      navigate(`/wiki?search=${encodeURIComponent(searchQuery.trim())}`)
    }
  }

  const isActive = (path: string) => {
    if (path === '/') return location.pathname === '/' && !location.search
    if (path === '/documents?status=archived') {
      return location.pathname === '/documents' && location.search === '?status=archived'
    }
    return location.pathname === path && !location.search
  }

  const navItemClass = (path: string) =>
    `flex items-center gap-2 px-3 py-2 rounded-lg transition-colors ${
      isActive(path)
        ? 'bg-blue-100 text-blue-700 font-medium'
        : 'text-gray-700 hover:bg-gray-200'
    }`

  return (
    <aside className="w-64 bg-gray-50 border-r border-gray-200 flex flex-col h-full">
      {/* Header */}
      <div className="p-4 border-b border-gray-200">
        <h1 className="text-lg font-semibold text-gray-800">{t('sidebar.title')}</h1>
      </div>

      {/* Search */}
      <div className="p-3 border-b border-gray-200">
        <form onSubmit={handleSearch}>
          <div className="relative">
            <input
              type="text"
              placeholder={t('sidebar.searchPlaceholder')}
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
              className="w-full px-3 py-2 pl-9 text-sm border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
            />
            <svg
              className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-gray-400"
              fill="none"
              viewBox="0 0 24 24"
              stroke="currentColor"
            >
              <path
                strokeLinecap="round"
                strokeLinejoin="round"
                strokeWidth={2}
                d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z"
              />
            </svg>
          </div>
        </form>
      </div>

      {/* Navigation */}
      <nav className="flex-1 overflow-y-auto p-2">
        {/* Main Navigation */}
        <div className="mb-4">
          <button
            onClick={() => toggleSection('navigation')}
            className="w-full flex items-center justify-between px-2 py-1 text-xs font-semibold text-gray-500 uppercase tracking-wider hover:text-gray-700"
          >
            <span>{t('sidebar.navigation')}</span>
            <svg
              className={`w-4 h-4 transition-transform ${expandedSections.navigation ? 'rotate-180' : ''}`}
              fill="none"
              viewBox="0 0 24 24"
              stroke="currentColor"
            >
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M19 9l-7 7-7-7" />
            </svg>
          </button>
          {expandedSections.navigation && (
            <ul className="mt-1 space-y-1">
              <li>
                <Link to="/" className={navItemClass('/')}>
                  <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                    <path
                      strokeLinecap="round"
                      strokeLinejoin="round"
                      strokeWidth={2}
                      d="M20 13V6a2 2 0 00-2-2H6a2 2 0 00-2 2v7m16 0v5a2 2 0 01-2 2H6a2 2 0 01-2-2v-5m16 0h-2.586a1 1 0 00-.707.293l-2.414 2.414a1 1 0 01-.707.293h-3.172a1 1 0 01-.707-.293l-2.414-2.414A1 1 0 006.586 13H4"
                    />
                  </svg>
                  <span>{t('sidebar.inbox')}</span>
                  {inboxCount > 0 && (
                    <span className="ml-auto px-2 py-0.5 text-xs bg-blue-500 text-white rounded-full">
                      {inboxCount}
                    </span>
                  )}
                </Link>
              </li>
              <li>
                <Link to="/documents?status=archived" className={navItemClass('/documents?status=archived')}>
                  <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                    <path
                      strokeLinecap="round"
                      strokeLinejoin="round"
                      strokeWidth={2}
                      d="M5 8h14M5 8a2 2 0 110-4h14a2 2 0 110 4M5 8v10a2 2 0 002 2h10a2 2 0 002-2V8m-10 4h4"
                    />
                  </svg>
                  <span>{t('sidebar.archived')}</span>
                  {archivedCount > 0 && (
                    <span className="ml-auto px-2 py-0.5 text-xs bg-gray-500 text-white rounded-full">
                      {archivedCount}
                    </span>
                  )}
                </Link>
              </li>
              <li>
                <Link to="/documents" className={navItemClass('/documents')}>
                  <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                    <path
                      strokeLinecap="round"
                      strokeLinejoin="round"
                      strokeWidth={2}
                      d="M9 12h6m-6 4h6m2 5H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z"
                    />
                  </svg>
                  <span>{t('sidebar.allDocuments')}</span>
                </Link>
              </li>
              <li>
                <Link to="/tags" className={navItemClass('/tags')}>
                  <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                    <path
                      strokeLinecap="round"
                      strokeLinejoin="round"
                      strokeWidth={2}
                      d="M7 7h.01M7 3h5c.512 0 1.024.195 1.414.586l7 7a2 2 0 010 2.828l-7 7a2 2 0 01-2.828 0l-7-7A1.994 1.994 0 013 12V7a4 4 0 014-4z"
                    />
                  </svg>
                  <span>{t('sidebar.tags')}</span>
                </Link>
              </li>
              <li>
                <Link to="/chat" className={navItemClass('/chat')}>
                  <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                    <path
                      strokeLinecap="round"
                      strokeLinejoin="round"
                      strokeWidth={2}
                      d="M8 12h.01M12 12h.01M16 12h.01M21 12c0 4.418-4.03 8-9 8a9.863 9.863 0 01-4.255-.949L3 20l1.395-3.72C3.512 15.042 3 13.574 3 12c0-4.418 4.03-8 9-8s9 3.582 9 8z"
                    />
                  </svg>
                  <span>{t('sidebar.chatHistory')}</span>
                </Link>
              </li>
            </ul>
          )}
        </div>

        {/* Wiki Section */}
        <div className="mb-4">
          <button
            onClick={() => toggleSection('wiki')}
            className="w-full flex items-center justify-between px-2 py-1 text-xs font-semibold text-gray-500 uppercase tracking-wider hover:text-gray-700"
          >
            <span>{t('sidebar.wiki')}</span>
            <svg
              className={`w-4 h-4 transition-transform ${expandedSections.wiki ? 'rotate-180' : ''}`}
              fill="none"
              viewBox="0 0 24 24"
              stroke="currentColor"
            >
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M19 9l-7 7-7-7" />
            </svg>
          </button>
          {expandedSections.wiki && (
            <ul className="mt-1 space-y-1">
              <li>
                <Link to="/wiki" className={navItemClass('/wiki')}>
                  <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                    <path
                      strokeLinecap="round"
                      strokeLinejoin="round"
                      strokeWidth={2}
                      d="M4 6h16M4 10h16M4 14h16M4 18h16"
                    />
                  </svg>
                  <span>{t('sidebar.wikiIndex')}</span>
                </Link>
              </li>
              <li>
                <Link to="/wiki/entities" className={navItemClass('/wiki/entities')}>
                  <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                    <path
                      strokeLinecap="round"
                      strokeLinejoin="round"
                      strokeWidth={2}
                      d="M17 20h5v-2a3 3 0 00-5.356-1.857M17 20H7m10 0v-2c0-.656-.126-1.283-.356-1.857M7 20H2v-2a3 3 0 015.356-1.857M7 20v-2c0-.656.126-1.283.356-1.857m0 0a5.002 5.002 0 019.288 0M15 7a3 3 0 11-6 0 3 3 0 016 0zm6 3a2 2 0 11-4 0 2 2 0 014 0zM7 10a2 2 0 11-4 0 2 2 0 014 0z"
                    />
                  </svg>
                  <span>{t('sidebar.entities')}</span>
                </Link>
              </li>
              <li>
                <Link to="/wiki/topics" className={navItemClass('/wiki/topics')}>
                  <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                    <path
                      strokeLinecap="round"
                      strokeLinejoin="round"
                      strokeWidth={2}
                      d="M7 21a4 4 0 01-4-4V5a2 2 0 012-2h4a2 2 0 012 2v12a4 4 0 01-4 4zm0 0h12a2 2 0 002-2v-4a2 2 0 00-2-2h-2.343M11 7.343l1.657-1.657a2 2 0 012.828 0l2.829 2.829a2 2 0 010 2.828l-8.486 8.485M7 17h.01"
                    />
                  </svg>
                  <span>{t('sidebar.topics')}</span>
                </Link>
              </li>
              <li>
                <Link to="/wiki/sources" className={navItemClass('/wiki/sources')}>
                  <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                    <path
                      strokeLinecap="round"
                      strokeLinejoin="round"
                      strokeWidth={2}
                      d="M12 6.253v13m0-13C10.832 5.477 9.246 5 7.5 5S4.168 5.477 3 6.253v13C4.168 18.477 5.754 18 7.5 18s3.332.477 4.5 1.253m0-13C13.168 5.477 14.754 5 16.5 5c1.747 0 3.332.477 4.5 1.253v13C19.832 18.477 18.247 18 16.5 18c-1.746 0-3.332.477-4.5 1.253"
                    />
                  </svg>
                  <span>{t('sidebar.sources')}</span>
                </Link>
              </li>
            </ul>
          )}
        </div>

        {/* Import Section */}
        <div className="mb-4">
          <Link to="/import" className={navItemClass('/import')}>
            <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path
                strokeLinecap="round"
                strokeLinejoin="round"
                strokeWidth={2}
                d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-8l-4-4m0 0L8 8m4-4v12"
              />
            </svg>
            <span>{t('sidebar.import')}</span>
          </Link>
        </div>

        {/* Settings */}
        <div className="mb-4">
          <Link to="/settings" className={navItemClass('/settings')}>
            <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path
                strokeLinecap="round"
                strokeLinejoin="round"
                strokeWidth={2}
                d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z"
              />
              <path
                strokeLinecap="round"
                strokeLinejoin="round"
                strokeWidth={2}
                d="M15 12a3 3 0 11-6 0 3 3 0 016 0z"
              />
            </svg>
            <span>{t('sidebar.settings')}</span>
          </Link>
        </div>
      </nav>

      {/* Footer */}
      <div className="p-3 border-t border-gray-200 text-xs text-gray-500">
        <div className="flex items-center gap-2">
          <span className="w-2 h-2 bg-green-500 rounded-full"></span>
          <span>{t('sidebar.connected')}</span>
        </div>
      </div>

      {/* User info and logout */}
      <div className="mt-auto p-4 border-t border-gray-200">
        <div className="flex items-center justify-between">
          <span className="text-sm text-gray-600">{username}</span>
          <button
            onClick={handleLogout}
            className="text-sm text-blue-600 hover:underline"
          >
            登出
          </button>
        </div>
      </div>
    </aside>
  )
}