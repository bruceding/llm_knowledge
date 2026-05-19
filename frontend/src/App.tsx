import { useEffect } from 'react'
import { BrowserRouter, Routes, Route, useLocation, Outlet } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Sidebar from './components/Sidebar'
import { useIsMobile } from './hooks/useIsMobile'
import MobileHeader from './components/Layout/MobileHeader'
import BottomTabBar from './components/Layout/BottomTabBar'
import MobileDrawer from './components/Layout/MobileDrawer'
import Inbox from './components/Inbox'
import DocDetail from './components/DocDetail'
import DocumentsList from './components/DocumentsList'
import WikiView from './components/WikiView'
import ChatView from './components/ChatView'
import ImportView from './components/ImportView'
import TagsView from './components/TagsView'
import SettingsPage from './components/SettingsPage'
import LoginPage from './components/LoginPage'
import RegisterPage from './components/RegisterPage'
import ChangePasswordPage from './components/ChangePasswordPage'
import PrivateRoute from './components/PrivateRoute'
import { fetchSettings } from './api'

// Layout component that decides whether to show sidebar
function Layout() {
  const isMobile = useIsMobile()
  const location = useLocation()
  const hideShellOnDocDetail = !!location.pathname.match(/^\/documents\/\d+$/)

  if (!isMobile) {
    return (
      <div className="flex h-screen bg-white">
        {!hideShellOnDocDetail && <Sidebar />}
        <main className="flex-1 overflow-auto"><Outlet /></main>
      </div>
    )
  }

  return (
    <div className="flex flex-col h-[100dvh] bg-white">
      <MobileHeader />
      <main className={`flex-1 overflow-auto ${hideShellOnDocDetail ? '' : 'pb-14'}`}><Outlet /></main>
      {!hideShellOnDocDetail && <BottomTabBar />}
      <MobileDrawer />
    </div>
  )
}

function App() {
  const { i18n } = useTranslation()

  useEffect(() => {
    fetchSettings()
      .then((settings) => i18n.changeLanguage(settings.language))
      .catch(() => {}) // Silently fail, use default
  }, [i18n])

  return (
    <BrowserRouter>
      <Routes>
        {/* Public routes */}
        <Route path="/login" element={<LoginPage />} />
        <Route path="/register" element={<RegisterPage />} />
        <Route path="/change-password" element={<ChangePasswordPage />} />

        {/* Protected routes */}
        <Route element={<PrivateRoute><Layout /></PrivateRoute>}>
          <Route path="/" element={<Inbox />} />
          <Route path="/documents" element={<DocumentsList />} />
          <Route path="/documents/:id" element={<DocDetail />} />
          <Route path="/wiki/*" element={<WikiView />} />
          <Route path="/chat/:id?" element={<ChatView />} />
          <Route path="/import" element={<ImportView />} />
          <Route path="/tags" element={<TagsView />} />
          <Route path="/settings" element={<SettingsPage />} />
        </Route>
      </Routes>
    </BrowserRouter>
  )
}

export default App