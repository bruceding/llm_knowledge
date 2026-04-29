import type { Document, UpdateDocRequest, SSEEvent, UserSettings, Conversation, Message } from './types'

const API_BASE = '/api'

// Document API
export async function fetchInbox(): Promise<Document[]> {
  const res = await fetch(`${API_BASE}/documents/inbox`)
  if (!res.ok) throw new Error('Failed to fetch inbox')
  return res.json()
}

export async function fetchDocuments(status?: string): Promise<Document[]> {
  const url = status ? `${API_BASE}/documents?status=${status}` : `${API_BASE}/documents`
  const res = await fetch(url)
  if (!res.ok) throw new Error('Failed to fetch documents')
  return res.json()
}

export async function fetchDocument(id: number): Promise<Document> {
  const res = await fetch(`${API_BASE}/documents/${id}`)
  if (!res.ok) throw new Error('Failed to fetch document')
  return res.json()
}

export async function updateDocument(id: number, data: UpdateDocRequest): Promise<Document> {
  const res = await fetch(`${API_BASE}/documents/${id}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(data),
  })
  if (!res.ok) throw new Error('Failed to update document')
  return res.json()
}

export async function publishDocument(id: number): Promise<{ id: number; status: string }> {
  const res = await fetch(`${API_BASE}/documents/${id}/publish`, { method: 'POST' })
  if (!res.ok) throw new Error('Failed to publish document')
  return res.json()
}

export async function deleteDocument(id: number): Promise<{ id: number; message: string }> {
  const res = await fetch(`${API_BASE}/documents/${id}`, { method: 'DELETE' })
  if (!res.ok) throw new Error('Failed to delete document')
  return res.json()
}

export async function regenerateSummary(id: number): Promise<{ summary: string }> {
  const res = await fetch(`${API_BASE}/documents/${id}/regenerate-summary`, { method: 'POST' })
  if (!res.ok) throw new Error('Failed to regenerate summary')
  return res.json()
}

export async function uploadPDF(file: File): Promise<{ id: number; path: string; message: string; pages: number }> {
  const formData = new FormData()
  formData.append('file', file)
  const res = await fetch(`${API_BASE}/raw/pdf`, {
    method: 'POST',
    body: formData,
  })
  if (!res.ok) {
    const err = await res.json()
    throw new Error(err.error || 'Failed to upload PDF')
  }
  return res.json()
}

export async function uploadPDFUrl(url: string): Promise<{ id: number; path: string; message: string; pages: number }> {
  const res = await fetch(`${API_BASE}/raw/pdf-url`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ url }),
  })
  if (!res.ok) {
    const err = await res.json()
    throw new Error(err.error || 'Failed to upload PDF from URL')
  }
  return res.json()
}

export async function clipWeb(url: string): Promise<{ id: number; title: string; path: string; images: number; message: string }> {
  const res = await fetch(`${API_BASE}/raw/web`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ url }),
  })
  if (!res.ok) {
    const data = await res.json()
    throw new Error(data.error || 'Failed to clip web page')
  }
  return res.json()
}

// RSS API
export async function addRSSFeed(name: string, url: string, autoSync: boolean): Promise<RSSFeed> {
  const res = await fetch(`${API_BASE}/rss/feeds`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name, url, autoSync }),
  })
  const data = await res.json()
  if (!res.ok) throw new Error(data.error)
  return data
}

export async function listRSSFeeds(): Promise<RSSFeed[]> {
  const res = await fetch(`${API_BASE}/rss/feeds`)
  return res.json()
}

export async function deleteRSSFeed(id: number): Promise<void> {
  await fetch(`${API_BASE}/rss/feeds/${id}`, { method: 'DELETE' })
}

export async function syncRSSFeed(id: number): Promise<{ newArticles: number }> {
  const res = await fetch(`${API_BASE}/rss/feeds/${id}/sync`, { method: 'POST' })
  return res.json()
}

interface RSSFeed {
  id: number
  name: string
  url: string
  autoSync: boolean
  lastSyncAt: string
  createdAt: string
  articleCount: number
}

// Wiki API - content fetched from /data/wiki/ path
export async function fetchWikiContent(path: string): Promise<string> {
  const res = await fetch(`/data/wiki/${path}.md`)
  if (!res.ok) throw new Error('Failed to fetch wiki content')
  return res.text()
}

// Conversation API
export async function fetchConversations(): Promise<Conversation[]> {
  const res = await fetch(`${API_BASE}/conversations`)
  if (!res.ok) throw new Error('Failed to fetch conversations')
  return res.json()
}

export async function fetchConversationMessages(conversationId: number): Promise<Message[]> {
  const res = await fetch(`${API_BASE}/conversations/${conversationId}/messages`)
  if (!res.ok) throw new Error('Failed to fetch conversation messages')
  return res.json()
}

// Chat API - New architecture with separate SSE stream and message sending

// Create a new conversation
export async function createConversation(title?: string, docId?: number): Promise<{ conversationId: number; title: string }> {
  const res = await fetch(`${API_BASE}/query/conversation`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ title, docId }),
  })
  if (!res.ok) throw new Error('Failed to create conversation')
  return res.json()
}

// Send a message to an existing conversation
export async function sendQueryMessage(
  conversationId: number,
  message: string,
  images?: string[],
  docId?: number
): Promise<{ status: string; messageId: number; sessionId: string }> {
  const res = await fetch(`${API_BASE}/query/message`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ conversationId, message, images, docId }),
  })
  if (!res.ok) throw new Error('Failed to send message')
  return res.json()
}

// Interrupt the current turn
export async function interruptQuery(conversationId: number): Promise<{ status: string }> {
  const res = await fetch(`${API_BASE}/query/interrupt`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ conversationId }),
  })
  if (!res.ok) throw new Error('Failed to interrupt')
  return res.json()
}

// Delete a conversation
export async function deleteConversation(conversationId: number): Promise<{ status: string; conversationId: number }> {
  const res = await fetch(`${API_BASE}/conversations/${conversationId}`, {
    method: 'DELETE',
  })
  if (!res.ok) throw new Error('Failed to delete conversation')
  return res.json()
}

// Translate API - SSE streaming
export async function translateDocument(
  docId: number,
  targetLang: string,
  onEvent: (event: SSEEvent) => void
): Promise<void> {
  const res = await fetch(`${API_BASE}/translate`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ docId, targetLang }),
  })

  if (!res.ok) throw new Error('Failed to start translation')

  const reader = res.body?.getReader()
  if (!reader) throw new Error('No response body')

  const decoder = new TextDecoder()
  let buffer = ''

  while (true) {
    const { done, value } = await reader.read()
    if (done) break

    buffer += decoder.decode(value, { stream: true })
    const lines = buffer.split('\n')
    buffer = lines.pop() || ''

    for (const line of lines) {
      if (line.startsWith('data: ')) {
        try {
          const data = JSON.parse(line.slice(6))
          onEvent(data)
        } catch {
          // Ignore parse errors
        }
      }
    }
  }
}

// Settings API
export async function fetchSettings(): Promise<UserSettings> {
  const res = await fetch(`${API_BASE}/settings`)
  if (!res.ok) throw new Error('Failed to fetch settings')
  return res.json()
}

export async function updateSettings(settings: Partial<UserSettings>): Promise<UserSettings> {
  const res = await fetch(`${API_BASE}/settings`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(settings),
  })
  if (!res.ok) throw new Error('Failed to update settings')
  return res.json()
}

// PDF Translation API
export async function checkPDFTranslationStatus(docId: number): Promise<{
  exists: boolean
  path?: string
  targetLang?: string
}> {
  const res = await fetch(`${API_BASE}/documents/${docId}/translation-status`)
  if (!res.ok) throw new Error('Failed to check translation status')
  return res.json()
}

export async function translatePDF(
  docId: number,
  onEvent: (event: SSEEvent) => void,
  targetLang?: string
): Promise<void> {
  const res = await fetch(`${API_BASE}/pdf-translate`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ docId, targetLang }),
  })

  if (!res.ok) throw new Error('Failed to start PDF translation')

  const reader = res.body?.getReader()
  if (!reader) throw new Error('No response body')

  const decoder = new TextDecoder()
  let buffer = ''

  while (true) {
    const { done, value } = await reader.read()
    if (done) break

    buffer += decoder.decode(value, { stream: true })
    const lines = buffer.split('\n')
    buffer = lines.pop() || ''

    for (const line of lines) {
      if (line.startsWith('data: ')) {
        try {
          const data = JSON.parse(line.slice(6))
          onEvent(data)
        } catch {
          // Ignore parse errors
        }
      }
    }
  }
}

// Markdown Translation API
export async function checkMarkdownTranslationStatus(docId: number): Promise<{
  exists: boolean
  path: string
  targetLang: string
}> {
  const res = await fetch(`${API_BASE}/documents/${docId}/markdown-translation-status`)
  if (!res.ok) throw new Error('Failed to check markdown translation status')
  return res.json()
}

export async function translateMarkdown(
  docId: number,
  targetLang: string,
  onEvent: (event: SSEEvent) => void
): Promise<void> {
  const res = await fetch(`${API_BASE}/markdown-translate`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ docId, targetLang }),
  })

  if (!res.ok) throw new Error('Failed to start markdown translation')

  const reader = res.body?.getReader()
  if (!reader) throw new Error('No response body')

  const decoder = new TextDecoder()
  let buffer = ''

  while (true) {
    const { done, value } = await reader.read()
    if (done) break

    buffer += decoder.decode(value, { stream: true })
    const lines = buffer.split('\n')
    buffer = lines.pop() || ''

    for (const line of lines) {
      if (line.startsWith('data: ')) {
        try {
          const data = JSON.parse(line.slice(6))
          onEvent(data)
        } catch {
          // Ignore parse errors
        }
      }
    }
  }
}

// Pages API - generate page images for bilingual view
export async function generatePages(docId: number): Promise<{ id: number; total_pages: number; message: string }> {
  const res = await fetch(`${API_BASE}/documents/${docId}/generate-pages`, { method: 'POST' })
  if (!res.ok) throw new Error('Failed to generate page images')
  return res.json()
}

export async function getPagesStatus(docId: number): Promise<{ exists: boolean; page_count: number }> {
  const res = await fetch(`${API_BASE}/documents/${docId}/pages-status`)
  if (!res.ok) throw new Error('Failed to get pages status')
  return res.json()
}

// Image Upload API
export async function uploadImage(data: string, type: string): Promise<{ path: string; filename: string }> {
  const res = await fetch(`${API_BASE}/images/upload`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ data, type }),
  })
  if (!res.ok) {
    const err = await res.json()
    throw new Error(err.error || 'Failed to upload image')
  }
  return res.json()
}