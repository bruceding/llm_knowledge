// Document types
export interface Tag {
  id: number
  name: string
  color: string
  createdAt: string
}

export interface Document {
  id: number
  title: string
  sourceType: string
  sourceUrl?: string
  rawPath: string
  wikiPath: string
  summary: string
  language: string
  status: 'inbox' | 'published' | 'archived'
  metadata: string
  createdAt: string
  updatedAt: string
  tags: Tag[]
}

// Conversation types
export interface Conversation {
  id: number
  title: string
  createdAt: string
  updatedAt: string
}

export interface Message {
  id: number
  conversationId: number
  role: 'user' | 'assistant' | 'system'
  content: string
  images?: string[]
  contextDocIds: string
  createdAt: string
}

// API request types
export interface UpdateDocRequest {
  title?: string
  status?: string
  tagNames?: string[]
}

export interface AskRequest {
  conversationId?: number
  question: string
  docId?: number
  images?: string[]
}

export interface ImageUploadResponse {
  path: string
  filename: string
}

export interface TranslateRequest {
  docId: number
  targetLang: string
}

// SSE event types from backend
export interface ContentBlock {
  type: 'text' | 'thinking' | 'tool_use'
  text?: string
  id?: string
  name?: string
  input?: Record<string, unknown>
}

export interface SSEMessage {
  role: string
  content: ContentBlock[]
}

export interface SSEEvent {
  type: 'conversation' | 'document' | 'assistant' | 'result' | 'error' | 'complete' | 'progress'
  conversationId?: number
  docId?: number
  title?: string
  targetLang?: string
  content?: string
  error?: string
  filePath?: string
  message?: SSEMessage | string  // SSEMessage for chat events, string for progress events
  // PDF translation progress
  translatedPdf?: string
  dualPdf?: string
  // Markdown translation result
  path?: string
}

// User settings types
export interface UserSettings {
  id: number
  language: 'en' | 'zh'
  translationEnabled: boolean
  translationApiBase: string
  translationApiKey: string
  translationModel: string
  createdAt: string
  updatedAt: string
}