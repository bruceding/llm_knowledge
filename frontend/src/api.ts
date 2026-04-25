import type { Document, UpdateDocRequest, AskRequest, SSEEvent, UserSettings } from './types'

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

// Wiki API - content fetched from /data/wiki/ path
export async function fetchWikiContent(path: string): Promise<string> {
  const res = await fetch(`/data/wiki/${path}.md`)
  if (!res.ok) throw new Error('Failed to fetch wiki content')
  return res.text()
}

// Chat API - SSE streaming
export async function askQuestion(
  request: AskRequest,
  onEvent: (event: SSEEvent) => void
): Promise<void> {
  const res = await fetch(`${API_BASE}/query/ask`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(request),
  })

  if (!res.ok) throw new Error('Failed to start chat')

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

export async function updateSettings(language: 'en' | 'zh'): Promise<UserSettings> {
  const res = await fetch(`${API_BASE}/settings`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ language }),
  })
  if (!res.ok) throw new Error('Failed to update settings')
  return res.json()
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