import type { Document, UpdateDocRequest, SSEEvent, UserSettings, Conversation, Message, LoginResponse, RegisterResponse, CaptchaResponse } from './types'

const API_BASE = '/api'

// Auth helper - get headers with authorization token
function getHeaders(): HeadersInit {
  const token = localStorage.getItem('token')
  const headers: HeadersInit = { 'Content-Type': 'application/json' }
  if (token) {
    headers['Authorization'] = `Bearer ${token}`
  }
  return headers
}

// Document API
export async function fetchInbox(): Promise<Document[]> {
  const res = await fetch(`${API_BASE}/documents/inbox`, { headers: getHeaders() })
  if (!res.ok) throw new Error('Failed to fetch inbox')
  return res.json()
}

export async function fetchDocuments(status?: string): Promise<Document[]> {
  const url = status ? `${API_BASE}/documents?status=${status}` : `${API_BASE}/documents`
  const res = await fetch(url, { headers: getHeaders() })
  if (!res.ok) throw new Error('Failed to fetch documents')
  return res.json()
}

export async function fetchDocument(id: number): Promise<Document> {
  const res = await fetch(`${API_BASE}/documents/${id}`, { headers: getHeaders() })
  if (!res.ok) throw new Error('Failed to fetch document')
  return res.json()
}

export async function updateDocument(id: number, data: UpdateDocRequest): Promise<Document> {
  const res = await fetch(`${API_BASE}/documents/${id}`, {
    method: 'PUT',
    headers: getHeaders(),
    body: JSON.stringify(data),
  })
  if (!res.ok) throw new Error('Failed to update document')
  return res.json()
}

export async function publishDocument(id: number): Promise<{ id: number; status: string }> {
  const res = await fetch(`${API_BASE}/documents/${id}/publish`, { method: 'POST', headers: getHeaders() })
  if (!res.ok) throw new Error('Failed to publish document')
  return res.json()
}

export async function deleteDocument(id: number): Promise<{ id: number; message: string }> {
  const res = await fetch(`${API_BASE}/documents/${id}`, { method: 'DELETE', headers: getHeaders() })
  if (!res.ok) throw new Error('Failed to delete document')
  return res.json()
}

export async function regenerateSummary(id: number): Promise<{ summary: string }> {
  const res = await fetch(`${API_BASE}/documents/${id}/regenerate-summary`, { method: 'POST', headers: getHeaders() })
  if (!res.ok) throw new Error('Failed to regenerate summary')
  return res.json()
}

export async function uploadPDF(file: File): Promise<{ id: number; path: string; message: string; pages: number }> {
  const token = localStorage.getItem('token')
  const formData = new FormData()
  formData.append('file', file)
  const headers: HeadersInit = {}
  if (token) {
    headers['Authorization'] = `Bearer ${token}`
  }
  const res = await fetch(`${API_BASE}/raw/pdf`, {
    method: 'POST',
    headers,
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
    headers: getHeaders(),
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
    headers: getHeaders(),
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
    headers: getHeaders(),
    body: JSON.stringify({ name, url, autoSync }),
  })
  const data = await res.json()
  if (!res.ok) throw new Error(data.error)
  return data
}

export async function listRSSFeeds(): Promise<RSSFeed[]> {
  const res = await fetch(`${API_BASE}/rss/feeds`, { headers: getHeaders() })
  return res.json()
}

export async function deleteRSSFeed(id: number): Promise<void> {
  await fetch(`${API_BASE}/rss/feeds/${id}`, { method: 'DELETE', headers: getHeaders() })
}

export async function syncRSSFeed(id: number): Promise<{ newArticles: number }> {
  const res = await fetch(`${API_BASE}/rss/feeds/${id}/sync`, { method: 'POST', headers: getHeaders() })
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

// Wiki API - content fetched from /data/wiki/ path (no auth needed for static files)
export async function fetchWikiContent(path: string): Promise<string> {
  const res = await fetch(`/data/wiki/${path}.md`)
  if (!res.ok) throw new Error('Failed to fetch wiki content')
  return res.text()
}

// Conversation API
export async function fetchConversations(): Promise<Conversation[]> {
  const res = await fetch(`${API_BASE}/conversations`, { headers: getHeaders() })
  if (!res.ok) throw new Error('Failed to fetch conversations')
  return res.json()
}

export async function fetchConversationMessages(conversationId: number): Promise<Message[]> {
  const res = await fetch(`${API_BASE}/conversations/${conversationId}/messages`, { headers: getHeaders() })
  if (!res.ok) throw new Error('Failed to fetch conversation messages')
  return res.json()
}

// Chat API - New architecture with separate SSE stream and message sending

// Create a new conversation
export async function createConversation(title?: string, docId?: number): Promise<{ conversationId: number; title: string }> {
  const res = await fetch(`${API_BASE}/query/conversation`, {
    method: 'POST',
    headers: getHeaders(),
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
    headers: getHeaders(),
    body: JSON.stringify({ conversationId, message, images, docId }),
  })
  if (!res.ok) throw new Error('Failed to send message')
  return res.json()
}

// Interrupt the current turn
export async function interruptQuery(conversationId: number): Promise<{ status: string }> {
  const res = await fetch(`${API_BASE}/query/interrupt`, {
    method: 'POST',
    headers: getHeaders(),
    body: JSON.stringify({ conversationId }),
  })
  if (!res.ok) throw new Error('Failed to interrupt')
  return res.json()
}

// Delete a conversation
export async function deleteConversation(conversationId: number): Promise<{ status: string; conversationId: number }> {
  const res = await fetch(`${API_BASE}/conversations/${conversationId}`, {
    method: 'DELETE',
    headers: getHeaders(),
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
    headers: getHeaders(),
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
  const res = await fetch(`${API_BASE}/settings`, { headers: getHeaders() })
  if (!res.ok) throw new Error('Failed to fetch settings')
  return res.json()
}

export async function updateSettings(settings: Partial<UserSettings>): Promise<UserSettings> {
  const res = await fetch(`${API_BASE}/settings`, {
    method: 'PUT',
    headers: getHeaders(),
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
  const res = await fetch(`${API_BASE}/documents/${docId}/translation-status`, { headers: getHeaders() })
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
    headers: getHeaders(),
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
  const res = await fetch(`${API_BASE}/documents/${docId}/markdown-translation-status`, { headers: getHeaders() })
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
    headers: getHeaders(),
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
  const res = await fetch(`${API_BASE}/documents/${docId}/generate-pages`, { method: 'POST', headers: getHeaders() })
  if (!res.ok) throw new Error('Failed to generate page images')
  return res.json()
}

export async function getPagesStatus(docId: number): Promise<{ exists: boolean; page_count: number }> {
  const res = await fetch(`${API_BASE}/documents/${docId}/pages-status`, { headers: getHeaders() })
  if (!res.ok) throw new Error('Failed to get pages status')
  return res.json()
}

// Image Upload API
export async function uploadImage(data: string, type: string): Promise<{ path: string; filename: string }> {
  const res = await fetch(`${API_BASE}/images/upload`, {
    method: 'POST',
    headers: getHeaders(),
    body: JSON.stringify({ data, type }),
  })
  if (!res.ok) {
    const err = await res.json()
    throw new Error(err.error || 'Failed to upload image')
  }
  return res.json()
}

// Doc Notes API
export interface DocNote {
  id: number
  documentId: number
  content: string
  sourceMsgId: string
  wikiPushed: boolean
  wikiPushedAt?: string
  createdAt: string
  updatedAt: string
}

export async function fetchDocNotes(docId: number): Promise<DocNote[]> {
  const res = await fetch(`${API_BASE}/documents/${docId}/notes`, { headers: getHeaders() })
  if (!res.ok) throw new Error('Failed to fetch notes')
  return res.json()
}

export async function createDocNote(docId: number, content: string, sourceMsgId?: string): Promise<DocNote> {
  const res = await fetch(`${API_BASE}/documents/${docId}/notes`, {
    method: 'POST',
    headers: getHeaders(),
    body: JSON.stringify({ content, sourceMsgId }),
  })
  if (!res.ok) {
    const err = await res.json()
    throw new Error(err.error || 'Failed to create note')
  }
  return res.json()
}

export async function updateDocNote(docId: number, noteId: number, content: string): Promise<DocNote> {
  const res = await fetch(`${API_BASE}/documents/${docId}/notes/${noteId}`, {
    method: 'PUT',
    headers: getHeaders(),
    body: JSON.stringify({ content }),
  })
  if (!res.ok) throw new Error('Failed to update note')
  return res.json()
}

export async function deleteDocNote(docId: number, noteId: number): Promise<void> {
  const res = await fetch(`${API_BASE}/documents/${docId}/notes/${noteId}`, { method: 'DELETE', headers: getHeaders() })
  if (!res.ok) throw new Error('Failed to delete note')
}

export async function pushNoteToWiki(docId: number, noteId: number): Promise<{ message: string; wikiPath: string }> {
  const res = await fetch(`${API_BASE}/documents/${docId}/notes/${noteId}/wiki-push`, { method: 'POST', headers: getHeaders() })
  if (!res.ok) {
    const err = await res.json()
    throw new Error(err.error || 'Failed to push to wiki')
  }
  return res.json()
}

// Auth helper (for external use)
export function getAuthHeaders(): HeadersInit {
  return getHeaders()
}

// Auth API (public routes - no token needed)
export async function getCaptcha(): Promise<CaptchaResponse> {
  const res = await fetch(`${API_BASE}/auth/captcha`)
  if (!res.ok) throw new Error('Failed to get captcha')
  return res.json()
}

export async function register(
  username: string,
  password: string,
  email: string,
  captchaKey: string,
  captchaAnswer: string
): Promise<RegisterResponse> {
  const res = await fetch(`${API_BASE}/auth/register`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, password, email, captchaKey, captchaAnswer }),
  })
  const data = await res.json()
  if (!res.ok) throw new Error(data.error || 'Registration failed')
  return data
}

export async function login(
  username: string,
  password: string,
  captchaKey: string,
  captchaAnswer: string
): Promise<LoginResponse> {
  const res = await fetch(`${API_BASE}/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, password, captchaKey, captchaAnswer }),
  })
  const data = await res.json()
  if (!res.ok) throw new Error(data.error || 'Login failed')
  return data
}

export async function logout(): Promise<void> {
  const res = await fetch(`${API_BASE}/auth/logout`, {
    method: 'POST',
    headers: getHeaders(),
  })
  if (!res.ok) throw new Error('Logout failed')
}

export async function checkAuthStatus(): Promise<{ loggedIn: boolean; userId?: number; username?: string }> {
  const token = localStorage.getItem('token')
  if (!token) return { loggedIn: false }

  const res = await fetch(`${API_BASE}/auth/status`, {
    headers: { 'Authorization': `Bearer ${token}` },
  })
  return res.json()
}

export async function changePassword(currentPassword: string, newPassword: string): Promise<void> {
  const res = await fetch(`${API_BASE}/auth/password`, {
    method: 'PUT',
    headers: getHeaders(),
    body: JSON.stringify({ currentPassword, newPassword }),
  })
  const data = await res.json()
  if (!res.ok) throw new Error(data.error || 'Password change failed')
}