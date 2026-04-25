import { useState, useEffect, useMemo } from 'react'
import { useParams, useNavigate, Link } from 'react-router-dom'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { useTranslation } from 'react-i18next'

export default function WikiView() {
  const params = useParams<{ '*': string }>()
  const navigate = useNavigate()
  const { t } = useTranslation()
  const wikiPath = params['*'] || 'index'
  const [content, setContent] = useState<string>('')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [availablePages, setAvailablePages] = useState<{ name: string; path: string }[]>([])

  useEffect(() => {
    loadWikiContent()
    loadAvailablePages()
  }, [wikiPath])

  const loadWikiContent = async () => {
    setLoading(true)
    setError(null)
    try {
      const res = await fetch(`/data/wiki/${wikiPath}.md`)
      if (!res.ok) {
        if (res.status === 404) {
          setError(t('wikiView.notFound'))
          setContent('')
        } else {
          throw new Error(t('wikiView.error'))
        }
      } else {
        setContent(await res.text())
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : t('wikiView.error'))
    } finally {
      setLoading(false)
    }
  }

  const loadAvailablePages = async () => {
    try {
      // Fetch wiki index to get available pages with their full paths
      const res = await fetch('/data/wiki/index.md')
      if (res.ok) {
        const indexContent = await res.text()
        // Extract full link paths from index
        const links = indexContent.match(/\[([^\]]+)\]\(([^)]+)\)/g) || []
        const pages = links.map((link) => {
          const nameMatch = link.match(/\[([^\]]+)\]/)
          const pathMatch = link.match(/\]\(([^)]+)\)/)
          if (nameMatch && pathMatch) {
            const path = pathMatch[1].replace('.md', '')
            return { name: nameMatch[1], path }
          }
          return null
        }).filter((p): p is { name: string; path: string } => p !== null)
        setAvailablePages(pages)
      }
    } catch {
      // Ignore errors loading index
    }
  }

  // Parse bidirectional links [[link]] and convert to proper links
  const processedContent = useMemo(() => {
    if (!content) return ''
    // Replace [[link]] with [link](wiki://link)
    return content.replace(/\[\[([^\]]+)\]\]/g, (_match, linkText) => {
      const linkName = linkText.trim()
      // Try to find matching page by name
      const matchingPage = availablePages.find(
        (page) => page.name.toLowerCase() === linkName.toLowerCase()
      )
      // Use the full path if found, otherwise just the link name
      const linkPath = matchingPage ? matchingPage.path : linkName
      return `[${linkName}](wiki://${linkPath})`
    })
  }, [content, availablePages])

  // Build breadcrumbs from path
  const breadcrumbs = useMemo(() => {
    const parts = wikiPath.split('/')
    const crumbs: { name: string; path: string }[] = []

    parts.forEach((part, index) => {
      const path = parts.slice(0, index + 1).join('/')
      crumbs.push({
        name: part === 'index' ? t('wikiView.home') : part.charAt(0).toUpperCase() + part.slice(1),
        path: path,
      })
    })

    return crumbs
  }, [wikiPath, t])

  if (loading) {
    return (
      <div className="flex items-center justify-center h-full">
        <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-blue-500"></div>
      </div>
    )
  }

  return (
    <div className="flex h-full">
      {/* Main content area */}
      <div className="flex-1 flex flex-col overflow-hidden">
        {/* Breadcrumb navigation */}
        <div className="p-4 border-b border-gray-200 bg-gray-50">
          <nav className="flex items-center gap-2 text-sm">
            <Link to="/wiki" className="text-blue-600 hover:underline">
              {t('wikiView.title')}
            </Link>
            {breadcrumbs.map((crumb, index) => (
              <span key={crumb.path} className="flex items-center gap-2">
                <span className="text-gray-400">/</span>
                {index === breadcrumbs.length - 1 ? (
                  <span className="text-gray-800 font-medium">{crumb.name}</span>
                ) : (
                  <Link to={`/wiki/${crumb.path}`} className="text-blue-600 hover:underline">
                    {crumb.name}
                  </Link>
                )}
              </span>
            ))}
          </nav>
        </div>

        {/* Content */}
        <div className="flex-1 overflow-auto">
          {error ? (
            <div className="p-6">
              <div className="bg-red-50 border border-red-200 rounded-lg p-4 text-red-700">
                {error}
                {wikiPath !== 'index' && (
                  <Link to="/wiki" className="ml-4 text-red-800 underline">
                    {t('wikiView.goToWikiHome')}
                  </Link>
                )}
              </div>
            </div>
          ) : (
            <div className="p-6 max-w-4xl prose prose-slate">
              <ReactMarkdown
                remarkPlugins={[remarkGfm]}
                components={{
                  // Custom link handling for wiki links
                  a: ({ href, children }) => {
                    if (href?.startsWith('wiki://')) {
                      const linkPath = href.replace('wiki://', '')
                      return (
                        <a
                          href={`/wiki/${linkPath}`}
                          className="text-blue-600 hover:underline cursor-pointer"
                          onClick={(e) => {
                            e.preventDefault()
                            navigate(`/wiki/${linkPath}`)
                          }}
                        >
                          {children}
                        </a>
                      )
                    }
                    // External links
                    if (href?.startsWith('http://') || href?.startsWith('https://')) {
                      return (
                        <a href={href} target="_blank" rel="noopener noreferrer" className="text-blue-600 hover:underline">
                          {children}
                        </a>
                      )
                    }
                    // Relative links within wiki - resolve based on current path
                    if (href && !href.startsWith('#')) {
                      let fullPath = href.replace('.md', '')

                      // Handle relative paths with .. or .
                      if (fullPath.startsWith('../')) {
                        // ../entities/CSA from topics/X becomes entities/CSA
                        fullPath = fullPath.replace('../', '')
                      } else if (fullPath.startsWith('./')) {
                        fullPath = fullPath.replace('./', '')
                      } else if (!fullPath.includes('/')) {
                        // Link without / - resolve relative to current directory
                        const currentDir = wikiPath.includes('/')
                          ? wikiPath.substring(0, wikiPath.lastIndexOf('/'))
                          : wikiPath
                        if (currentDir !== 'index' && currentDir !== '') {
                          fullPath = currentDir + '/' + fullPath
                        }
                      }

                      return (
                        <a
                          href={`/wiki/${fullPath}`}
                          className="text-blue-600 hover:underline cursor-pointer"
                          onClick={(e) => {
                            e.preventDefault()
                            navigate(`/wiki/${fullPath}`)
                          }}
                        >
                          {children}
                        </a>
                      )
                    }
                    return <a href={href} className="text-blue-600 hover:underline">{children}</a>
                  },
                  // Custom heading with anchor links
                  h1: ({ children, id }) => (
                    <h1 id={id} className="text-3xl font-bold text-gray-900 mb-4">
                      {children}
                    </h1>
                  ),
                  h2: ({ children, id }) => (
                    <h2 id={id} className="text-2xl font-semibold text-gray-800 mt-8 mb-4 border-b border-gray-200 pb-2">
                      {children}
                    </h2>
                  ),
                  h3: ({ children, id }) => (
                    <h3 id={id} className="text-xl font-semibold text-gray-800 mt-6 mb-3">
                      {children}
                    </h3>
                  ),
                }}
              >
                {processedContent}
              </ReactMarkdown>
            </div>
          )}
        </div>
      </div>

      {/* Sidebar with quick navigation - only visible on large screens */}
      <aside className="hidden xl:block w-64 border-l border-gray-200 bg-gray-50 overflow-auto">
        <div className="p-4">
          <h3 className="text-sm font-semibold text-gray-500 uppercase mb-3">{t('wikiView.quickLinks')}</h3>
          <ul className="space-y-2">
            <li>
              <Link
                to="/wiki"
                className={`flex items-center gap-2 px-2 py-1.5 text-sm rounded-lg ${
                  wikiPath === 'index'
                    ? 'bg-blue-100 text-blue-700'
                    : 'text-gray-700 hover:bg-gray-200'
                }`}
              >
                <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M3 12l2-2m0 0l7-7 7 7M5 10v10a1 1 0 001 1h3m10-11l2 2m-2-2v10a1 1 0 01-1 1h-3m-6 0a1 1 0 001-1v-4a1 1 0 011-1h2a1 1 0 011 1v4a1 1 0 001 1m-6 0h6" />
                </svg>
                {t('wikiView.index')}
              </Link>
            </li>
            <li>
              <Link
                to="/wiki/entities"
                className={`flex items-center gap-2 px-2 py-1.5 text-sm rounded-lg ${
                  wikiPath === 'entities'
                    ? 'bg-blue-100 text-blue-700'
                    : 'text-gray-700 hover:bg-gray-200'
                }`}
              >
                <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M17 20h5v-2a3 3 0 00-5.356-1.857M17 20H7m10 0v-2c0-.656-.126-1.283-.356-1.857M7 20H2v-2a3 3 0 015.356-1.857M7 20v-2c0-.656.126-1.283.356-1.857m0 0a5.002 5.002 0 019.288 0M15 7a3 3 0 11-6 0 3 3 0 016 0zm6 3a2 2 0 11-4 0 2 2 0 014 0zM7 10a2 2 0 11-4 0 2 2 0 014 0z" />
                </svg>
                {t('wikiView.entities')}
              </Link>
            </li>
            <li>
              <Link
                to="/wiki/topics"
                className={`flex items-center gap-2 px-2 py-1.5 text-sm rounded-lg ${
                  wikiPath === 'topics'
                    ? 'bg-blue-100 text-blue-700'
                    : 'text-gray-700 hover:bg-gray-200'
                }`}
              >
                <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M7 21a4 4 0 01-4-4V5a2 2 0 012-2h4a2 2 0 012 2v12a4 4 0 01-4 4zm0 0h12a2 2 0 002-2v-4a2 2 0 00-2-2h-2.343M11 7.343l1.657-1.657a2 2 0 012.828 0l2.829 2.829a2 2 0 010 2.828l-8.486 8.485M7 17h.01" />
                </svg>
                {t('wikiView.topics')}
              </Link>
            </li>
            <li>
              <Link
                to="/wiki/sources"
                className={`flex items-center gap-2 px-2 py-1.5 text-sm rounded-lg ${
                  wikiPath === 'sources'
                    ? 'bg-blue-100 text-blue-700'
                    : 'text-gray-700 hover:bg-gray-200'
                }`}
              >
                <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 6.253v13m0-13C10.832 5.477 9.246 5 7.5 5S4.168 5.477 3 6.253v13C4.168 18.477 5.754 18 7.5 18s3.332.477 4.5 1.253m0-13C13.168 5.477 14.754 5 16.5 5c1.747 0 3.332.477 4.5 1.253v13C19.832 18.477 18.247 18 16.5 18c-1.746 0-3.332.477-4.5 1.253" />
                </svg>
                {t('wikiView.sources')}
              </Link>
            </li>

            {/* Divider */}
            <li className="border-t border-gray-200 my-2"></li>

            {/* Dynamic pages from wiki content */}
            {availablePages.slice(0, 10).map((page) => (
              <li key={page.name}>
                <Link
                  to={`/wiki/${page.path}`}
                  className={`flex items-center gap-2 px-2 py-1.5 text-sm rounded-lg ${
                    wikiPath.toLowerCase() === page.path.toLowerCase()
                      ? 'bg-blue-100 text-blue-700'
                      : 'text-gray-700 hover:bg-gray-200'
                  }`}
                >
                  <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 12h6m-6 4h6m2 5H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z" />
                  </svg>
                  <span className="truncate">{page.name}</span>
                </Link>
              </li>
            ))}
          </ul>
        </div>
      </aside>
    </div>
  )
}