import { useState, useEffect, useRef, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { createDocNote } from '../api'
import type { ContentBlock } from '../types'

// Format a tool_use content block into a human-readable description
function formatToolBlock(block: ContentBlock): string {
  const name = block.name || 'Tool'
  try {
    switch (name) {
      case 'Read':
        return `Reading ${(block.input as Record<string, string>)?.file_path || (block.input as Record<string, string>)?.path || 'file'}`
      case 'Glob':
        return `Searching ${(block.input as Record<string, string>)?.pattern || 'files'}`
      case 'Grep':
        return `Searching for "${(block.input as Record<string, string>)?.pattern || ''}" in ${(block.input as Record<string, string>)?.path || 'files'}`
      case 'LS':
        return `Listing ${(block.input as Record<string, string>)?.path || 'directory'}`
      default:
        return `Using ${name}`
    }
  } catch {
    return `Using ${name}`
  }
}

// Extract display info from message content blocks (handles different model outputs)
function extractFromContentBlocks(blocks: ContentBlock[]): {
  text: string
  hasThinking: boolean
  hasToolUse: boolean
  toolDesc?: string
} {
  let text = ''
  let hasThinking = false
  let hasToolUse = false
  let toolDesc: string | undefined

  for (const block of blocks) {
    switch (block.type) {
      case 'text':
        if (block.text) text += block.text
        break
      case 'thinking':
        hasThinking = true
        break
      case 'tool_use':
        hasToolUse = true
        toolDesc = formatToolBlock(block)
        break
    }
  }

  return { text, hasThinking, hasToolUse, toolDesc }
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

export default function DocumentChatPanel({ docId, active, onNoteSaved }: DocumentChatPanelProps) {
  const { t } = useTranslation()
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [sessionId, setSessionId] = useState<string>('')
  const [input, setInput] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  // Note saving state
  const [savedMsgIds, setSavedMsgIds] = useState<Set<string>>(new Set())
  const [noteModalOpen, setNoteModalOpen] = useState(false)
  const [noteModalMsg, setNoteModalMsg] = useState<ChatMessage | null>(null)
  const [noteContent, setNoteContent] = useState('')
  const [savingNote, setSavingNote] = useState(false)

  const eventSourceRef = useRef<EventSource | null>(null)
  const messagesEndRef = useRef<HTMLDivElement>(null)

  // Auto-scroll to bottom
  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages])

  // Start SSE connection
  const startSSE = useCallback(() => {
    if (eventSourceRef.current) {
      eventSourceRef.current.close()
    }

    const es = new EventSource(`/api/doc-chat/stream?docId=${docId}`)
    eventSourceRef.current = es
    setLoading(true)

    es.onmessage = (e) => {
      const event = JSON.parse(e.data)

      if (event.type === 'session') {
        setSessionId(event.sessionId)
        setLoading(false)
      } else if (event.type === 'assistant') {
        // Parse content blocks from the message (works across different models)
        const msg = event.message
        const blocks = typeof msg === 'object' ? msg?.content : undefined
        if (blocks && blocks.length > 0) {
          const { text, hasThinking, hasToolUse, toolDesc } = extractFromContentBlocks(blocks)
          setMessages(prev => {
            const lastMsg = prev[prev.length - 1]
            if (lastMsg?.role === 'assistant' && lastMsg.isStreaming) {
              return prev.map((m, i) =>
                i === prev.length - 1
                  ? { ...m, content: m.content + text, isThinking: hasThinking && !text, isToolUse: hasToolUse && !text, toolDesc }
                  : m
              )
            } else {
              return [...prev, {
                id: Date.now().toString(),
                role: 'assistant',
                content: text,
                timestamp: new Date(),
                isStreaming: true,
                isThinking: hasThinking && !text,
                isToolUse: hasToolUse && !text,
                toolDesc,
              }]
            }
          })
        } else if (event.content) {
          // Fallback: models without content blocks
          setMessages(prev => {
            const lastMsg = prev[prev.length - 1]
            if (lastMsg?.role === 'assistant' && lastMsg.isStreaming) {
              return prev.map((m, i) =>
                i === prev.length - 1
                  ? { ...m, content: m.content + event.content }
                  : m
              )
            } else {
              return [...prev, {
                id: Date.now().toString(),
                role: 'assistant',
                content: event.content || '',
                timestamp: new Date(),
                isStreaming: true
              }]
            }
          })
        }
      } else if (event.type === 'result') {
        // Mark streaming complete
        setMessages(prev => prev.map(m =>
          m.isStreaming ? { ...m, isStreaming: false, isThinking: false } : m
        ))
      } else if (event.type === 'error') {
        setError(event.error || 'An error occurred')
        setMessages(prev => prev.map(m =>
          m.isStreaming ? { ...m, isStreaming: false, isThinking: false } : m
        ))
      }
    }

    es.onerror = () => {
      es.close()
      eventSourceRef.current = null
      setLoading(false)
    }
  }, [docId])

  // Start SSE when active becomes true
  useEffect(() => {
    if (!active) {
      // Close connection when inactive
      if (eventSourceRef.current) {
        eventSourceRef.current.close()
        eventSourceRef.current = null
      }
      return
    }

    startSSE()
    return () => {
      if (eventSourceRef.current) {
        eventSourceRef.current.close()
      }
    }
  }, [active, startSSE])

  // Send message
  const handleSend = async () => {
    if (!input.trim()) return

    const userMessage: ChatMessage = {
      id: Date.now().toString(),
      role: 'user',
      content: input.trim(),
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
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ sessionId, message: input.trim() })
      })

      const data = await res.json()

      if (data.isNewSession || data.status === 'session_expired') {
        // Session expired - clear and restart
        setMessages([])
        setSessionId('')
        setError(t('docDetail.sessionExpired'))
        startSSE()
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to send message')
      // Remove thinking placeholder on error
      setMessages(prev => prev.filter(m => !m.isThinking))
    }
  }

  // Clear conversation
  const handleClear = () => {
    setMessages([])
    setSessionId('')
    setError(null)
    startSSE()
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

  return (
    <div className="flex flex-col h-full">
      {/* Messages area */}
      <div className="flex-1 overflow-auto p-2 space-y-2">
        {messages.length === 0 && !loading && (
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

        {loading && messages.length === 0 && (
          <div className="text-center text-gray-400 text-xs py-4">
            <div className="animate-spin inline-block w-4 h-4 border border-gray-300 border-t-blue-500 rounded-full"></div>
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
            disabled={loading}
          />
          <button
            onClick={handleSend}
            disabled={!input.trim() || loading}
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
