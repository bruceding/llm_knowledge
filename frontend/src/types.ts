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
  rawPath: string
  wikiPath: string
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
}

export interface TranslateRequest {
  docId: number
  targetLang: string
}

// SSE event types from backend
export interface SSEEvent {
  type: 'conversation' | 'document' | 'assistant' | 'tool_use' | 'error' | 'complete'
  conversationId?: number
  docId?: number
  title?: string
  targetLang?: string
  content?: string
  toolName?: string
  error?: string
  filePath?: string
}

// User settings types
export interface UserSettings {
  id: number
  language: 'en' | 'zh'
  createdAt: string
  updatedAt: string
}