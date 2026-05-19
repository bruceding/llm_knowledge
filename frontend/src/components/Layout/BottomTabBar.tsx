import { Link, useLocation } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useMobileShell } from './MobileShellStore'

export default function BottomTabBar() {
  const { t } = useTranslation()
  const loc = useLocation()
  const { setDrawerOpen } = useMobileShell()
  const path = loc.pathname

  const isInbox = path === '/'
  const isDocs = path === '/documents'
  const isWiki = path === '/wiki' || path.startsWith('/wiki/')
  const isChat = path === '/chat' || path.startsWith('/chat/')

  const itemClass = (active: boolean) =>
    `flex-1 flex flex-col items-center justify-center gap-0.5 text-[11px] ${
      active ? 'text-blue-600' : 'text-gray-500'
    }`

  return (
    <nav
      className="fixed bottom-0 inset-x-0 z-30 h-14 bg-white border-t border-gray-200 flex
                 pb-[env(safe-area-inset-bottom)]"
      aria-label="bottom navigation"
    >
      <Link to="/" className={itemClass(isInbox)} aria-label="inbox">
        <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
          <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2}
            d="M20 13V6a2 2 0 00-2-2H6a2 2 0 00-2 2v7m16 0v5a2 2 0 01-2 2H6a2 2 0 01-2-2v-5m16 0h-2.586a1 1 0 00-.707.293l-2.414 2.414a1 1 0 01-.707.293h-3.172a1 1 0 01-.707-.293l-2.414-2.414A1 1 0 006.586 13H4" />
        </svg>
        <span>{t('mobile.tabs.inbox')}</span>
      </Link>
      <Link to="/documents" className={itemClass(isDocs)} aria-label="documents">
        <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
          <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2}
            d="M9 12h6m-6 4h6m2 5H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z" />
        </svg>
        <span>{t('mobile.tabs.documents')}</span>
      </Link>
      <Link to="/wiki" className={itemClass(isWiki)} aria-label="wiki">
        <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
          <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2}
            d="M4 6h16M4 10h16M4 14h16M4 18h16" />
        </svg>
        <span>{t('mobile.tabs.wiki')}</span>
      </Link>
      <Link to="/chat" className={itemClass(isChat)} aria-label="chat">
        <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
          <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2}
            d="M8 12h.01M12 12h.01M16 12h.01M21 12c0 4.418-4.03 8-9 8a9.863 9.863 0 01-4.255-.949L3 20l1.395-3.72C3.512 15.042 3 13.574 3 12c0-4.418 4.03-8 9-8s9 3.582 9 8z" />
        </svg>
        <span>{t('mobile.tabs.chat')}</span>
      </Link>
      <button onClick={() => setDrawerOpen(true)} className={itemClass(false)} aria-label="more">
        <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
          <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M4 6h16M4 12h16M4 18h16" />
        </svg>
        <span>{t('mobile.tabs.more')}</span>
      </button>
    </nav>
  )
}
