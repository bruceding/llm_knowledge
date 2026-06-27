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
  status: 'inbox' | 'later' | 'published' | 'archived'
  metadata: string
  createdAt: string
  updatedAt: string
  tags: Tag[]
}

export interface PaperSection {
  index: number
  title: string
  slug: string
  hasBody?: boolean
  explanation?: string
  generating?: boolean
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
  type: 'conversation' | 'document' | 'error' | 'complete' | 'progress' | 'session_expired' | 'session_initializing' | 'session_ready' | 'full' | 'delta' | 'done' | 'tool_start' | 'tool_input' | 'tool_end' | 'session'
  conversationId?: number
  content?: string
  text?: string
  error?: string
  message?: SSEMessage | string
  sessionId?: string
  reconnected?: boolean
  toolId?: string
  toolName?: string
  toolInput?: string
  // PDF translation progress
  translatedPdf?: string
  dualPdf?: string
  targetLang?: string
  title?: string
  path?: string
  // PDF translation progress fields
  filePath?: string
}

// User settings types
export interface UserSettings {
  id: number
  userId: number
  language: 'en' | 'zh'
  isAdmin: boolean
  translationEnabled: boolean
  createdAt: string
  updatedAt: string
}

// Global settings types (admin only)
export interface GlobalSettings {
  id: number
  translationEnabled: boolean
  translationApiBase: string
  translationApiKey: string
  translationModel: string
  createdAt: string
  updatedAt: string
}

// Auth types
export interface AuthState {
  isLoggedIn: boolean
  userId: number | null
  username: string | null
  mustChangePassword: boolean
  token: string | null
}

export interface LoginResponse {
  success: boolean
  token: string
  userId: number
  username: string
  mustChangePassword: boolean
  message?: string
}

export interface RegisterResponse {
  success: boolean
  userId: number
}

export interface CaptchaResponse {
  captchaKey: string
  captchaImage: string
}

// Newsletter IMAP types
export interface IMAPConfig {
  id: number
  host: string
  port: number
  username: string
  folderName: string
  autoSync: boolean
  lastSyncAt: string
  createdAt: string
}

export interface IMAPConfigInput {
  host: string
  port: number
  username: string
  password: string
  folderName: string
  autoSync: boolean
}

export interface IMAPConfigResponse {
  configured: boolean
  config?: IMAPConfig
}

export interface IMAPTestResult {
  success: boolean
  folderExists?: boolean
  messageCount?: number
  message: string
  availableFolders?: string[]
}

export interface IMAPFoldersResult {
  folders: string[]
}

export interface NewsletterSyncStatus {
  running: boolean
  result?: NewsletterSyncResult
}

export interface NewsletterSyncResult {
  newArticles: number
  total: number
  downloadErrors: number
  message: string
  error?: string
}