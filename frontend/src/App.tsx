import { BrowserRouter, Routes, Route } from 'react-router-dom'
import Sidebar from './components/Sidebar'
import Inbox from './components/Inbox'
import DocDetail from './components/DocDetail'
import DocumentsList from './components/DocumentsList'
import WikiView from './components/WikiView'
import ChatView from './components/ChatView'
import ImportView from './components/ImportView'
import TagsView from './components/TagsView'

function App() {
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
          </Routes>
        </main>
      </div>
    </BrowserRouter>
  )
}

export default App