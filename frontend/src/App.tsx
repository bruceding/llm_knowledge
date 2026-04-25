import { useEffect } from 'react'
import { BrowserRouter, Routes, Route } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Sidebar from './components/Sidebar'
import Inbox from './components/Inbox'
import DocDetail from './components/DocDetail'
import DocumentsList from './components/DocumentsList'
import WikiView from './components/WikiView'
import ChatView from './components/ChatView'
import ImportView from './components/ImportView'
import TagsView from './components/TagsView'
import SettingsPage from './components/SettingsPage'
import { fetchSettings } from './api'

function App() {
  const { i18n } = useTranslation()

  useEffect(() => {
    fetchSettings()
      .then((settings) => i18n.changeLanguage(settings.language))
      .catch(() => {}) // Silently fail, use default
  }, [i18n])

  return (
    <BrowserRouter>
      <div className="flex h-screen bg-white">
        <Sidebar />
        <main className="flex-1 overflow-auto">
          <Routes>
            <Route path="/" element={<Inbox />} />
            <Route path="/documents" element={<DocumentsList />} />
            <Route path="/documents/:id" element={<DocDetail />} />
            <Route path="/wiki/*" element={<WikiView />} />
            <Route path="/chat/:id?" element={<ChatView />} />
            <Route path="/import" element={<ImportView />} />
            <Route path="/tags" element={<TagsView />} />
            <Route path="/settings" element={<SettingsPage />} />
          </Routes>
        </main>
      </div>
    </BrowserRouter>
  )
}

export default App