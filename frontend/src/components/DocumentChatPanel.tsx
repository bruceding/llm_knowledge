import { useState, useEffect, useRef, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { createDocNote, getAuthHeaders } from '../api'

// Format tool name + JSON input string into a human-readable description
function formatToolName(name: string, inputJson: string): string {
  try {
    const input = JSON.parse(inputJson) as Record<string, string>
    switch (name) {
      case 'Read':
        return `Reading ${input.file_path || input.path || 'file'}`
      case 'Glob':
        return `Searching ${input.pattern || 'files'}`
      case 'Grep':
        return `Searching for "${input.pattern || ''}" in ${input.path || 'files'}`
      case 'LS':
        return `Listing ${input.path || 'directory'}`
      default:
        return `Using ${name}`
    }
  } catch {
    return inputJson ? `Using ${name}` : name
  }
}

interface DocumentChatPanelProps {
  docId: number
  active: boolean // Only connect SSE when active (chat tab is visible)
  onNoteSaved?: () => void // Notify parent when a note is saved
}

interface ChatMessage {
  id: string
  role: 'user' | 'assistant'
  content: string
  timestamp: Date
  isStreaming?: boolean
  isThinking?: boolean
  isToolUse?: boolean
  toolDesc?: string
}

// Persist messages and sessionId across component remounts (module-level)
// Keep only last 5 documents to prevent memory leak
const MAX_STORED_DOCS = 5
const chatStore = new Map<number, { messages: ChatMessage[]; sessionId: string }>()

function cleanupChatStore(currentDocId: number) {
  // Remove entries for documents other than current, keeping only last N
  if (chatStore.size > MAX_STORED_DOCS) {
    const keys = [...chatStore.keys()].filter(k => k !== currentDocId)
    // Remove oldest entries first
    while (chatStore.size > MAX_STORED_DOCS && keys.length > 0) {
      chatStore.delete(keys.shift()!)
    }
  }
}

export default function DocumentChatPanel({ docId, active, onNoteSaved }: DocumentChatPanelProps) {
  const { t } = useTranslation()

  // Restore from store on mount
  const stored = chatStore.get(docId)
  const [messages, setMessages] = useState<ChatMessage[]>(stored?.messages || [])
  const [sessionId, setSessionId] = useState<string>(stored?.sessionId || '')
  const [input, setInput] = useState('')
  const [connecting, setConnecting] = useState(false)
  const [error, setError] = useState<string | null>(null)

  // Note saving state
  const [savedMsgIds, setSavedMsgIds] = useState<Set<string>>(new Set())
  const [noteModalOpen, setNoteModalOpen] = useState(false)
  const [noteModalMsg, setNoteModalMsg] = useState<ChatMessage | null>(null)
  const [noteContent, setNoteContent] = useState('')
  const [savingNote, setSavingNote] = useState(false)

  const abortRef = useRef<AbortController | null>(null)
  const messagesEndRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLInputElement>(null)
  const sessionIdRef = useRef(sessionId)
  // Message that POST /message failed to deliver (in-memory session expired);
  // re-sent automatically once /stream reconnects with a resumed Claude session.
  const pendingResendRef = useRef<string | null>(null)
  // Track whether the panel is active so the SSE stream loop can decide whether
  // to silently auto-reconnect after an unexpected drop.
  const activeRef = useRef(active)
  useEffect(() => { activeRef.current = active }, [active])
  // Reconnect attempt counter; reset to 0 on successful `session` event.
  // Bounded retries with exponential backoff prevent a stale ChatSessionID
  // (or other persistent failure) from spawning Claude processes in a tight loop.
  const reconnectAttemptRef = useRef(0)
  const MAX_RECONNECT_ATTEMPTS = 8
  // Forward ref to startNewSession; lets handleSSEEvent re-trigger a fresh
  // /stream call (e.g. when a resend hits session_expired again) without
  // participating in the callback dependency chain.
  const startNewSessionRef = useRef<(fresh?: boolean) => void>(() => {})

  // Keep ref and store in sync
  useEffect(() => {
    sessionIdRef.current = sessionId
  }, [sessionId])

  useEffect(() => {
    chatStore.set(docId, { messages, sessionId })
    cleanupChatStore(docId)
  }, [docId, messages, sessionId])

  // Auto-scroll to bottom
  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages])

  // Process a single SSE event object
  const handleSSEEvent = useCallback((event: Record<string, unknown>) => {
    if (event.type === 'session') {
      const newSid = event.sessionId as string
      setSessionId(newSid)
      setConnecting(false)
      // SSE is alive — reset the reconnect backoff so transient drops later
      // don't immediately hit the retry cap.
      reconnectAttemptRef.current = 0
      // If a message was pending due to session_expired, resend it now that the
      // backend has a live (resumed) session bound to this sessionId. If the
      // resend itself returns session_expired (HTTP 200, not a fetch error),
      // re-queue the message and trigger another reconnect — otherwise the
      // user's input would be silently dropped.
      if (pendingResendRef.current) {
        const msg = pendingResendRef.current
        pendingResendRef.current = null
        fetch('/api/doc-chat/message', {
          method: 'POST',
          headers: getAuthHeaders(),
          body: JSON.stringify({ sessionId: newSid, message: msg })
        }).then(res => {
          if (!res.ok) {
            // HTTP error (401/500/etc) — re-queue and surface so the user
            // sees the failure instead of staring at a stuck spinner.
            pendingResendRef.current = msg
            setError(`Resend failed: HTTP ${res.status}`)
            setMessages(prev => prev.filter(m => !m.isThinking))
            return null
          }
          return res.json()
        }).then(data => {
          if (!data) return
          if (data.isNewSession || data.status === 'session_expired') {
            // Session expired again during resend (rare but possible if the
            // backend session was cleaned up between connect and resend).
            // Re-queue and trigger another /stream so the message is not lost.
            pendingResendRef.current = msg
            sessionIdRef.current = ''
            setSessionId('')
            startNewSessionRef.current()
          }
        }).catch(err => {
          pendingResendRef.current = msg
          setError(err instanceof Error ? err.message : 'Failed to resend message')
          setMessages(prev => prev.filter(m => !m.isThinking))
        })
      }
    } else if (event.type === 'delta') {
      setMessages(prev => {
        const lastMsg = prev[prev.length - 1]
        if (lastMsg?.role === 'assistant' && lastMsg.isStreaming) {
          return prev.map((m, i) =>
            i === prev.length - 1
              ? { ...m, content: m.content + (event.text as string || ''), isThinking: false }
              : m
          )
        } else {
          return [...prev, {
            id: Date.now().toString(),
            role: 'assistant',
            content: (event.text as string) || '',
            timestamp: new Date(),
            isStreaming: true,
          }]
        }
      })
    } else if (event.type === 'full') {
      setMessages(prev => {
        const lastMsg = prev[prev.length - 1]
        if (lastMsg?.role === 'assistant' && lastMsg.isStreaming) {
          return prev.map((m, i) =>
            i === prev.length - 1
              ? { ...m, content: (event.content as string) || '', isThinking: false, isToolUse: false }
              : m
          )
        } else {
          return [...prev, {
            id: Date.now().toString(),
            role: 'assistant',
            content: (event.content as string) || '',
            timestamp: new Date(),
            isStreaming: true,
          }]
        }
      })
    } else if (event.type === 'tool_start') {
      const toolDesc = formatToolName((event.toolName as string) || 'Tool', (event.toolInput as string) || '')
      setMessages(prev => {
        const lastMsg = prev[prev.length - 1]
        if (lastMsg?.role === 'assistant' && lastMsg.isStreaming) {
          return prev.map((m, i) =>
            i === prev.length - 1
              ? { ...m, isToolUse: true, toolDesc }
              : m
          )
        }
        return prev
      })
    } else if (event.type === 'tool_input') {
      const toolDesc = formatToolName((event.toolName as string) || 'Tool', (event.toolInput as string) || '')
      setMessages(prev => {
        const lastMsg = prev[prev.length - 1]
        if (lastMsg?.role === 'assistant' && lastMsg.isStreaming && lastMsg.isToolUse) {
          return prev.map((m, i) =>
            i === prev.length - 1
              ? { ...m, toolDesc }
              : m
          )
        }
        return prev
      })
    } else if (event.type === 'tool_end') {
      setMessages(prev => {
        const lastMsg = prev[prev.length - 1]
        if (lastMsg?.role === 'assistant' && lastMsg.isStreaming) {
          return prev.map((m, i) =>
            i === prev.length - 1
              ? { ...m, isToolUse: false, toolDesc: undefined }
              : m
          )
        }
        return prev
      })
    } else if (event.type === 'done') {
      setMessages(prev => prev.map(m =>
        m.isStreaming ? { ...m, isStreaming: false, isThinking: false, isToolUse: false, toolDesc: undefined } : m
      ))
      setTimeout(() => inputRef.current?.focus(), 0)
    } else if (event.type === 'error') {
      setError((event.error as string) || 'An error occurred')
      setMessages(prev => prev.map(m =>
        m.isStreaming ? { ...m, content: m.content || '[已停止]', isStreaming: false, isThinking: false, isToolUse: false, toolDesc: undefined } : m
      ))
    }
  }, [])

  // Forward ref to connectSSE, set after connectSSE is defined below. Lets
  // processSSEStream trigger auto-reconnect without creating a callback cycle.
  const connectSSERef = useRef<() => void>(() => {})

  // Schedule an auto-reconnect with exponential backoff + retry cap.
  // Caller is responsible for guarding `activeRef.current` itself; this just
  // limits the loop. Delay grows 500ms → 1s → 2s ... capped at 30s.
  const scheduleReconnect = useCallback(() => {
    if (!activeRef.current) return
    if (reconnectAttemptRef.current >= MAX_RECONNECT_ATTEMPTS) {
      setError('Reconnect failed after multiple attempts')
      return
    }
    const attempt = reconnectAttemptRef.current
    reconnectAttemptRef.current++
    const delay = Math.min(500 * (2 ** attempt), 30_000)
    setTimeout(() => { if (activeRef.current) connectSSERef.current() }, delay)
  }, [])

  // Shared SSE stream processor
  const processSSEStream = useCallback((res: Response, controller: AbortController) => {
    const reader = res.body?.getReader()
    if (!reader) throw new Error('No response body')

    const decoder = new TextDecoder()
    let buffer = ''

    const pump = (): Promise<void> => reader.read().then(({ done, value }) => {
      if (controller.signal.aborted) return
      if (done) {
        setConnecting(false)
        abortRef.current = null
        // Server closed the stream (session cleaned up, process exit, restart).
        // If the panel is still visible, silently reconnect — scheduleReconnect
        // applies exponential backoff and a retry cap so a persistent failure
        // (e.g. stale ChatSessionID) can't loop on the backend.
        scheduleReconnect()
        return
      }
      buffer += decoder.decode(value, { stream: true })
      const lines = buffer.split(/\r?\n/)
      buffer = lines.pop() || ''
      for (const line of lines) {
        if (line.startsWith('data: ')) {
          const payload = line.slice(6)
          try {
            const data = JSON.parse(payload)
            handleSSEEvent(data)
          } catch { /* ignore parse errors */ }
        }
      }
      return pump()
    })

    return pump()
  }, [handleSSEEvent, scheduleReconnect])

  // Start a brand new session.
  // fresh=true tells the backend to drop any stored ChatSessionID before
  // calling Claude, so "Clear Chat" actually starts a new conversation
  // instead of resuming the old one via --resume.
  const startNewSession = useCallback((fresh: boolean = false) => {
    if (abortRef.current) {
      abortRef.current.abort()
    }

    const controller = new AbortController()
    abortRef.current = controller
    setConnecting(true)
    setError(null)

    const headers = { ...getAuthHeaders(), 'Accept': 'text/event-stream' } as Record<string, string>
    const url = fresh
      ? `/api/doc-chat/stream?docId=${docId}&fresh=1`
      : `/api/doc-chat/stream?docId=${docId}`

    fetch(url, { headers, signal: controller.signal })
      .then(res => {
        if (!res.ok) throw new Error(`SSE request failed: ${res.status}`)
        return processSSEStream(res, controller)
      })
      .catch(err => {
        if (err.name !== 'AbortError') {
          setConnecting(false)
          scheduleReconnect()
        }
      })
  }, [docId, processSSEStream, scheduleReconnect])

  // Connect: try reconnecting to existing session, fall back to new
  const connectSSE = useCallback(() => {
    if (abortRef.current) {
      abortRef.current.abort()
    }

    const sid = sessionIdRef.current
    if (!sid) {
      startNewSession()
      return
    }

    const controller = new AbortController()
    abortRef.current = controller
    setConnecting(true)
    setError(null)

    const headers = { ...getAuthHeaders(), 'Accept': 'text/event-stream' } as Record<string, string>

    fetch(`/api/doc-chat/reconnect?sessionId=${sid}`, { headers, signal: controller.signal })
      .then(res => {
        if (!res.ok) {
          // Session not found — start new session but preserve messages
          // so the user doesn't lose visible chat history.
          startNewSession()
          return
        }
        return processSSEStream(res, controller)
      })
      .catch(err => {
        if (err.name !== 'AbortError') {
          setConnecting(false)
          startNewSession()
        }
      })
  }, [startNewSession, processSSEStream])

  // Keep refs pointed at the latest callbacks so handlers above can trigger
  // them without participating in the useCallback dependency chain.
  useEffect(() => { connectSSERef.current = connectSSE }, [connectSSE])
  useEffect(() => { startNewSessionRef.current = startNewSession }, [startNewSession])

  // Connect when active, disconnect when inactive.
  // Use a small delay to survive React StrictMode's unmount/remount cycle.
  useEffect(() => {
    if (!active) {
      if (abortRef.current) {
        abortRef.current.abort()
        abortRef.current = null
      }
      return
    }

    const timer = setTimeout(() => connectSSE(), 50)
    return () => {
      clearTimeout(timer)
      if (abortRef.current) {
        abortRef.current.abort()
      }
    }
  }, [active, connectSSE])

  // Send message
  const handleSend = async () => {
    if (!input.trim()) return
    const messageContent = input.trim()

    const userMessage: ChatMessage = {
      id: Date.now().toString(),
      role: 'user',
      content: messageContent,
      timestamp: new Date()
    }

    // Add thinking placeholder for assistant
    const thinkingMessage: ChatMessage = {
      id: (Date.now() + 1).toString(),
      role: 'assistant',
      content: '',
      timestamp: new Date(),
      isStreaming: true,
      isThinking: true
    }

    setMessages(prev => [...prev, userMessage, thinkingMessage])
    setInput('')
    setError(null)

    try {
      const res = await fetch('/api/doc-chat/message', {
        method: 'POST',
        headers: getAuthHeaders(),
        body: JSON.stringify({ sessionId, message: messageContent })
      })

      const data = await res.json()

      if (data.isNewSession || data.status === 'session_expired') {
        // In-memory session was cleaned up but the DB still has the Claude
        // session_id. Trigger a fresh /stream call which will --resume from DB,
        // and re-send the message once the new sessionId arrives via SSE.
        // Drop the thinking placeholder so we don't leave a stale spinner if
        // the reconnect hits its retry cap; the resend handler will add a
        // fresh one when it actually reaches Claude.
        pendingResendRef.current = messageContent
        setMessages(prev => prev.filter(m => !m.isThinking))
        sessionIdRef.current = ''
        setSessionId('')
        startNewSession()
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to send message')
      // Remove thinking placeholder on error
      setMessages(prev => prev.filter(m => !m.isThinking))
    }
  }

  // Clear conversation. fresh=true tells the backend to wipe the stored
  // ChatSessionID before launching Claude, otherwise /stream would silently
  // --resume the conversation we just cleared.
  const handleClear = () => {
    setMessages([])
    setSessionId('')
    sessionIdRef.current = ''
    pendingResendRef.current = null
    reconnectAttemptRef.current = 0
    setError(null)
    startNewSession(true)
  }

  // Open note save modal
  const handleOpenSaveNote = (msg: ChatMessage) => {
    setNoteModalMsg(msg)
    setNoteContent(msg.content)
    setNoteModalOpen(true)
  }

  // Save note
  const handleSaveNote = async () => {
    if (!noteModalMsg || !noteContent.trim()) return
    setSavingNote(true)
    try {
      await createDocNote(docId, noteContent.trim(), noteModalMsg.id)
      setSavedMsgIds(prev => new Set(prev).add(noteModalMsg.id))
      setNoteModalOpen(false)
      setNoteModalMsg(null)
      onNoteSaved?.()
    } catch {
      // Show error but don't crash
    } finally {
      setSavingNote(false)
    }
  }

  const isAnyStreaming = messages.some(m => m.isStreaming)

  return (
    <div className="flex flex-col h-full">
      {/* Messages area */}
      <div className="flex-1 overflow-auto p-2 space-y-2">
        {messages.length === 0 && !connecting && (
          <div className="text-center text-gray-400 text-xs py-8">
            {t('docDetail.chatPlaceholder')}
          </div>
        )}

        {messages.map((msg) => (
          <div key={msg.id} className={`flex gap-2 ${msg.role === 'user' ? 'justify-end' : 'justify-start'}`}>
            {msg.role === 'assistant' && (
              <div className="w-5 h-5 rounded-full bg-blue-500 flex items-center justify-center text-white text-xs shrink-0">
                AI
              </div>
            )}
            <div className={`max-w-[85%] rounded px-2 py-1.5 ${
              msg.role === 'user'
                ? 'bg-blue-500 text-white text-xs'
                : 'bg-gray-100 text-gray-800'
            }`}>
              {msg.role === 'user' ? (
                <div className="whitespace-pre-wrap break-words text-xs">{msg.content}</div>
              ) : msg.isThinking || (msg.isStreaming && !msg.content && !msg.isToolUse) ? (
                <div className="flex items-center gap-1 text-xs text-gray-500">
                  <div className="animate-spin w-3 h-3 border border-gray-300 border-t-blue-500 rounded-full"></div>
                  <span>{t('chatView.thinking')}</span>
                </div>
              ) : msg.isToolUse && !msg.content ? (
                <div className="flex items-center gap-1 text-xs text-gray-500">
                  <div className="animate-spin w-3 h-3 border border-gray-300 border-t-blue-500 rounded-full"></div>
                  <span>{msg.toolDesc || t('chatView.thinking')}</span>
                </div>
              ) : (
                <div>
                  <div className="prose prose-sm prose-slate max-w-none text-xs [&_p]:my-1 [&_h1]:text-sm [&_h2]:text-sm [&_h3]:text-xs [&_ul]:my-1 [&_ol]:my-1 [&_li]:my-0 [&_code]:text-xs [&_pre]:my-1 [&_table]:my-1 [&_table]:border [&_table]:border-collapse [&_table]:w-full [&_table]:overflow-x-auto [&_th]:border [&_th]:border-gray-300 [&_th]:bg-gray-50 [&_th]:px-2 [&_th]:py-1 [&_th]:font-medium [&_th]:text-left [&_td]:border [&_td]:border-gray-300 [&_td]:px-2 [&_td]:py-1 [&_tr:nth-child(even)_td]:bg-gray-50">
                    <ReactMarkdown remarkPlugins={[remarkGfm]}>
                      {msg.content}
                    </ReactMarkdown>
                  </div>
                  {!msg.isStreaming && (
                    <div className="mt-1 flex items-center gap-1">
                      {savedMsgIds.has(msg.id) ? (
                        <span className="text-[10px] text-green-600 flex items-center gap-0.5">
                          <svg className="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 13l4 4L19 7" />
                          </svg>
                          Saved
                        </span>
                      ) : (
                        <button
                          onClick={() => handleOpenSaveNote(msg)}
                          className="text-[10px] text-gray-400 hover:text-blue-500 flex items-center gap-0.5"
                          title="Save as note"
                        >
                          <svg className="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 5a2 2 0 012-2h10a2 2 0 012 2v16l-7-3.5L5 21V5z" />
                          </svg>
                          Save
                        </button>
                      )}
                    </div>
                  )}
                </div>
              )}
            </div>
          </div>
        ))}

        {/* Show connecting indicator regardless of message count */}
        {connecting && (
          <div className="text-center text-gray-400 text-xs py-4">
            <div className="animate-spin inline-block w-4 h-4 border border-gray-300 border-t-blue-500 rounded-full"></div>
            <div className="mt-1">{messages.length > 0 ? (t('docDetail.reconnecting') || 'Reconnecting...') : (t('docDetail.connecting') || 'Connecting...')}</div>
          </div>
        )}

        {error && (
          <div className="text-xs text-red-500 p-2 bg-red-50 rounded">
            {error}
          </div>
        )}

        <div ref={messagesEndRef} />
      </div>

      {/* Input area */}
      <div className="border-t border-gray-200 p-2">
        <div className="flex gap-2">
          <input
            ref={inputRef}
            type="text"
            value={input}
            onChange={(e) => setInput(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && !e.shiftKey) {
                e.preventDefault()
                handleSend()
              }
            }}
            placeholder={t('docDetail.chatPlaceholder')}
            className="flex-1 px-2 py-1.5 border border-gray-300 rounded text-xs focus:outline-none focus:ring-1 focus:ring-blue-500"
            disabled={connecting || isAnyStreaming}
          />
          <button
            onClick={handleSend}
            disabled={!input.trim() || connecting || isAnyStreaming}
            className="px-3 py-1.5 bg-blue-500 text-white rounded text-xs disabled:opacity-50 hover:bg-blue-600"
          >
            {t('docDetail.send')}
          </button>
          <button
            onClick={handleClear}
            className="px-2 py-1.5 bg-gray-100 text-gray-600 rounded text-xs hover:bg-gray-200"
          >
            {t('docDetail.clearChat')}
          </button>
        </div>
      </div>

      {/* Note save modal */}
      {noteModalOpen && noteModalMsg && (
        <div className="fixed inset-0 bg-black bg-opacity-40 flex items-center justify-center z-50">
          <div className="bg-white rounded-lg shadow-xl w-[500px] max-w-[90vw] max-h-[80vh] flex flex-col">
            <div className="flex items-center justify-between p-4 border-b border-gray-200">
              <h3 className="text-sm font-semibold text-gray-800">Save as Note</h3>
              <button
                onClick={() => setNoteModalOpen(false)}
                className="p-1 text-gray-400 hover:text-gray-600 hover:bg-gray-100 rounded"
              >
                <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
                </svg>
              </button>
            </div>
            <div className="flex-1 overflow-auto p-4">
              <p className="text-xs text-gray-500 mb-2">Edit the content before saving:</p>
              <textarea
                value={noteContent}
                onChange={(e) => setNoteContent(e.target.value)}
                className="w-full h-48 px-3 py-2 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-blue-500 resize-none"
              />
            </div>
            <div className="flex justify-end gap-2 p-4 border-t border-gray-200">
              <button
                onClick={() => setNoteModalOpen(false)}
                className="px-4 py-1.5 text-xs bg-gray-100 text-gray-600 rounded hover:bg-gray-200"
              >
                Cancel
              </button>
              <button
                onClick={handleSaveNote}
                disabled={savingNote || !noteContent.trim()}
                className="px-4 py-1.5 text-xs bg-blue-500 text-white rounded hover:bg-blue-600 disabled:opacity-50"
              >
                {savingNote ? 'Saving...' : 'Save'}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
