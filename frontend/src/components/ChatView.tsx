import { useState, useEffect, useRef, useCallback } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { createConversation, sendQueryMessage, interruptQuery, fetchConversations, fetchConversationMessages, fetchQueryStatus, deleteConversation, uploadImage, getAuthHeaders } from '../api'
import { useConfirm } from '../hooks/useConfirm'
import { useIsMobile } from '../hooks/useIsMobile'
import { useMobileShell } from './Layout/MobileShellStore'
import type { SSEEvent } from '../types'

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
    // inputJson may be partial (streaming), show raw
    return inputJson ? `Using ${name}` : name
  }
}

interface Message {
  id: number
  role: 'user' | 'assistant' | 'system'
  content: string
  images?: string[]
  timestamp: Date
  isStreaming?: boolean
  isThinking?: boolean
  toolUse?: string
}

interface Conversation {
  id: number
  title: string
  createdAt: string
}

export default function ChatView() {
  const params = useParams<{ id?: string }>()
  const navigate = useNavigate()
  const { t, i18n } = useTranslation()
  // 'new' is a mobile-only sentinel for the compose view (no real conversation yet)
  const isMobileComposeMode = params.id === 'new'
  const urlConversationId = params.id && params.id !== 'new' ? parseInt(params.id) : undefined

  const [messages, setMessages] = useState<Message[]>([])
  const [input, setInput] = useState('')
  const [isStreaming, setIsStreaming] = useState(false)
  const [currentConversationId, setCurrentConversationId] = useState<number | undefined>(undefined)
  const [conversations, setConversations] = useState<Conversation[]>([])
  const [showHistory, setShowHistory] = useState(false)

  const [pendingImages, setPendingImages] = useState<string[]>([])
  const [enlargedImage, setEnlargedImage] = useState<string | null>(null)
  const [connectionError, setConnectionError] = useState<string | null>(null)

  const messagesEndRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLInputElement>(null)
  const abortRef = useRef<AbortController | null>(null)
  const isStreamingRef = useRef(false)
  const sseReadyRef = useRef(false)
  const sseFailedRef = useRef(false)
  const [forceRefreshKey, setForceRefreshKey] = useState(0)
  const forceRefreshKeyRef = useRef(0)

  const isMobile = useIsMobile()
  const setTitle = useMobileShell((s) => s.setTitle)
  const setLeftSlot = useMobileShell((s) => s.setLeftSlot)

  useEffect(() => {
    if (!isMobile) return
    setTitle(t('sidebar.chatHistory'))
    if (urlConversationId || isMobileComposeMode) {
      setLeftSlot(
        <button onClick={() => navigate('/chat')} aria-label="back to chat list" className="p-1 -ml-1 text-gray-700">
          <svg className="w-6 h-6" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M15 19l-7-7 7-7" />
          </svg>
        </button>
      )
    } else {
      setLeftSlot(null)
    }
    return () => { setTitle(''); setLeftSlot(null) }
  }, [isMobile, urlConversationId, isMobileComposeMode, t, navigate, setTitle, setLeftSlot])

  // Load conversation list
  const loadConversations = useCallback(async () => {
    try {
      const convs = await fetchConversations()
      setConversations(convs)
    } catch {
      // Silently fail
    }
  }, [])

  useEffect(() => {
    loadConversations()
  }, [loadConversations])

  // Scroll to bottom when new messages arrive
  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages])

  // Load conversation history when switching
  useEffect(() => {
    // Run when: URL differs from currentConversationId (normal case),
    // OR forceRefreshKey was incremented (sidebar click while async pending)
    if (urlConversationId && (urlConversationId !== currentConversationId || forceRefreshKey > forceRefreshKeyRef.current)) {
      forceRefreshKeyRef.current = forceRefreshKey // Mark this key as processed

      // IMPORTANT: Abort existing SSE connection first to prevent old messages leaking
      if (abortRef.current) {
        abortRef.current.abort()
        abortRef.current = null
        sseReadyRef.current = false
        sseFailedRef.current = false
      }

      // Reset streaming state before loading new conversation
      isStreamingRef.current = false
      setIsStreaming(false)
      setPendingImages([])

      // Stale guard: prevent async results from overriding state after user navigates away
      let stale = false

      // Load history and check backend session status in parallel
      Promise.all([
        fetchConversationMessages(urlConversationId).catch(() => []),
        fetchQueryStatus(urlConversationId),
      ]).then(([dbMessages, queryStatus]) => {
        if (stale) return

        const loadedMessages: Message[] = (dbMessages as any[]).length > 0
          ? (dbMessages as any[]).map((m) => ({
              id: m.id,
              role: m.role as 'user' | 'assistant' | 'system',
              content: m.content,
              images: typeof m.images === 'string' && m.images ? JSON.parse(m.images) : (m.images || []),
              timestamp: new Date(m.createdAt),
            }))
          : []

        // Restore streaming state based on backend session status
        const status = queryStatus.status || 'idle'
        if (status === 'streaming') {
          // Session is actively streaming text — restore content
          isStreamingRef.current = true
          setIsStreaming(true)
          loadedMessages.push({
            id: Date.now(),
            role: 'assistant',
            content: queryStatus.streamingContent || '',
            timestamp: new Date(),
            isStreaming: true,
            isThinking: false,
          })
        } else if (status === 'thinking') {
          // Session is processing but no text output yet
          isStreamingRef.current = true
          setIsStreaming(true)
          loadedMessages.push({
            id: Date.now(),
            role: 'assistant',
            content: '',
            timestamp: new Date(),
            isStreaming: true,
            isThinking: true,
          })
        } else {
          // Idle — no active processing
          isStreamingRef.current = false
          setIsStreaming(false)
        }

        setMessages(loadedMessages)
        // Set conversation ID after messages are loaded to avoid SSE race condition
        setCurrentConversationId(urlConversationId)
      })

      return () => { stale = true }
    }
  }, [urlConversationId, currentConversationId, forceRefreshKey])

  // Handle SSE events
  const handleSSEEvent = useCallback((event: SSEEvent) => {
    if (event.type === 'session_expired') {
      isStreamingRef.current = false
      setIsStreaming(false)
      return
    }

    if (event.type === 'session_initializing') {
      setConnectionError(null)
      return
    }

    if (event.type === 'session_ready') {
      sseReadyRef.current = true
      return
    }

    // Streaming text delta (Claude/Qwen models)
    if (event.type === 'delta') {
      setMessages((prev) => {
        const last = prev[prev.length - 1]
        if (last.role === 'assistant' && last.isStreaming) {
          return [...prev.slice(0, -1), { ...last, content: last.content + (event.text || ''), isThinking: false }]
        }
        return [...prev, {
          id: Date.now(),
          role: 'assistant' as const,
          content: event.text || '',
          timestamp: new Date(),
          isStreaming: true,
        }]
      })
      return
    }

    // Full content replacement (GLM non-streaming / SSE reconnect extension)
    if (event.type === 'full') {
      setMessages((prev) => {
        const last = prev[prev.length - 1]
        if (last.role === 'assistant' && last.isStreaming) {
          return [...prev.slice(0, -1), { ...last, content: event.content || '', isThinking: false, toolUse: undefined }]
        }
        return prev
      })
      return
    }

    // Tool use events
    if (event.type === 'tool_start') {
      const toolDesc = formatToolName(event.toolName || 'Tool', event.toolInput || '')
      setMessages((prev) => {
        const last = prev[prev.length - 1]
        if (last.role === 'assistant' && last.isStreaming) {
          return [...prev.slice(0, -1), { ...last, toolUse: toolDesc }]
        }
        return prev
      })
      return
    }

    if (event.type === 'tool_input') {
      const toolDesc = formatToolName(event.toolName || 'Tool', event.toolInput || '')
      setMessages((prev) => {
        const last = prev[prev.length - 1]
        if (last.role === 'assistant' && last.isStreaming && last.toolUse) {
          return [...prev.slice(0, -1), { ...last, toolUse: toolDesc }]
        }
        return prev
      })
      return
    }

    if (event.type === 'tool_end') {
      setMessages((prev) => {
        const last = prev[prev.length - 1]
        if (last.role === 'assistant' && last.isStreaming) {
          return [...prev.slice(0, -1), { ...last, toolUse: undefined }]
        }
        return prev
      })
      return
    }

    // Turn end signal — replaces [DONE] hack and result event
    if (event.type === 'done') {
      setMessages((prev) => prev.map(m =>
        m.isStreaming ? { ...m, isStreaming: false, isThinking: false, toolUse: undefined } : m
      ))
      isStreamingRef.current = false
      setIsStreaming(false)
      loadConversations()
      setTimeout(() => inputRef.current?.focus(), 0)
      return
    }

    // Error
    if (event.type === 'error') {
      setMessages((prev) => {
        const last = prev[prev.length - 1]
        if (last.role === 'assistant' && last.isStreaming) {
          const displayContent = last.content || '[已停止]'
          return [...prev.slice(0, -1), { ...last, content: displayContent, isStreaming: false, isThinking: false }]
        }
        return prev
      })
      isStreamingRef.current = false
      setIsStreaming(false)
      setTimeout(() => inputRef.current?.focus(), 0)
    }
  }, [loadConversations])

  // Connect SSE stream when conversationId changes
  useEffect(() => {
    if (!currentConversationId) {
      // No active conversation — ensure SSE state is clean
      sseReadyRef.current = false
      sseFailedRef.current = false
      return
    }

    if (abortRef.current) {
      abortRef.current.abort()
      sseReadyRef.current = false
    }

    sseReadyRef.current = false
    sseFailedRef.current = false
    setConnectionError(null)

    const controller = new AbortController()
    abortRef.current = controller

    // Capture conversationId for this SSE connection (for defensive checks)
    const sseConversationId = currentConversationId

    const headers = { ...getAuthHeaders(), 'Accept': 'text/event-stream' } as Record<string, string>

    let cancelled = false

    fetch(`/api/query/stream?conversationId=${currentConversationId}`, { headers, signal: controller.signal })
      .then(res => {
        if (!res.ok) {
          // Backend returned error before SSE could start — session creation likely failed
          sseFailedRef.current = true
          sseReadyRef.current = false
          setConnectionError(t('chatView.connectionError'))
          return
        }
        // SSE connection is open, but session may still be initializing.
        // Wait for 'session_ready' event to set sseReadyRef = true.
        const reader = res.body?.getReader()
        if (!reader) throw new Error('No response body')

        const decoder = new TextDecoder()
        let buffer = ''
        let idleTimer: ReturnType<typeof setTimeout> | null = null
        const IDLE_TIMEOUT = 90_000 // 90s no data → disconnect and reset

        const resetIdleTimer = () => {
          if (idleTimer) clearTimeout(idleTimer)
          idleTimer = setTimeout(() => {
            console.warn('[SSE] Idle timeout (90s), disconnecting')
            controller.abort()
            sseReadyRef.current = false
            sseFailedRef.current = true
            abortRef.current = null
            if (isStreamingRef.current) {
              setMessages((prev) => {
                const last = prev[prev.length - 1]
                if (last.role === 'assistant' && last.isStreaming) {
                  return [...prev.slice(0, -1), { ...last, content: last.content || t('chatView.connectionError'), isStreaming: false, isThinking: false, toolUse: undefined }]
                }
                return prev
              })
              isStreamingRef.current = false
              setIsStreaming(false)
            } else {
              setConnectionError(t('chatView.connectionError'))
            }
          }, IDLE_TIMEOUT)
        }
        resetIdleTimer()

        const pump = (): Promise<void> => reader.read().then(({ done, value }) => {
          if (controller.signal.aborted) {
            if (idleTimer) clearTimeout(idleTimer)
            return
          }
          if (done) {
            if (idleTimer) clearTimeout(idleTimer)
            sseReadyRef.current = false
            sseFailedRef.current = true
            if (abortRef.current === controller) abortRef.current = null
            if (isStreamingRef.current) {
              isStreamingRef.current = false
              setIsStreaming(false)
            } else if (!cancelled) {
              // Stream ended unexpectedly while idle — backend session died
              setConnectionError(t('chatView.connectionError'))
            }
            return
          }
          resetIdleTimer()
          buffer += decoder.decode(value, { stream: true })
          const lines = buffer.split(/\r?\n/)
          buffer = lines.pop() || ''
          for (const line of lines) {
            if (line.startsWith('data: ')) {
              const payload = line.slice(6)
              try {
                const event: SSEEvent = JSON.parse(payload)
                // Defensive check: skip events from wrong conversation
                if (event.conversationId && event.conversationId !== sseConversationId) {
                  console.warn(`[SSE] Received event from wrong conversation ${event.conversationId}, expected ${sseConversationId}`)
                  continue
                }
                handleSSEEvent(event)
              } catch { /* ignore parse errors */ }
            }
          }
          return pump()
        })

        return pump()
      })
      .catch(() => {
        if (!cancelled) {
          sseReadyRef.current = false
          sseFailedRef.current = true
          isStreamingRef.current = false
          setIsStreaming(false)
          setConnectionError(t('chatView.connectionError'))
        }
      })

    return () => {
      cancelled = true
      controller.abort()
      sseReadyRef.current = false
    }
  }, [currentConversationId, handleSSEEvent, t])

  // Handle image upload
  const handleImageUpload = useCallback(async (file: File) => {
    const allowedTypes = ['image/png', 'image/jpeg', 'image/gif', 'image/webp']
    if (!allowedTypes.includes(file.type)) {
      alert(t('chatView.imageTypeError'))
      return
    }
    if (file.size > 10 * 1024 * 1024) {
      alert(t('chatView.imageSizeError'))
      return
    }
    const reader = new FileReader()
    reader.onload = async (e) => {
      const base64 = e.target?.result as string
      try {
        const type = file.type.split('/')[1]
        const result = await uploadImage(base64, type)
        setPendingImages(prev => [...prev, result.path])
      } catch (err) {
        console.error('Failed to upload image:', err)
        alert(t('chatView.imageUploadError'))
      }
    }
    reader.readAsDataURL(file)
  }, [t])

  const handlePaste = useCallback((e: React.ClipboardEvent) => {
    const items = e.clipboardData.items
    for (const item of items) {
      if (item.type.startsWith('image/')) {
        const file = item.getAsFile()
        if (file) handleImageUpload(file)
      }
    }
  }, [handleImageUpload])

  const handleFileInputChange = useCallback((e: React.ChangeEvent<HTMLInputElement>) => {
    const files = e.target.files
    if (files) {
      for (const file of files) handleImageUpload(file)
    }
    e.target.value = ''
  }, [handleImageUpload])

  const handleRemoveImage = useCallback((index: number) => {
    setPendingImages(prev => prev.filter((_, i) => i !== index))
  }, [])

  // Handle sending a message
  const handleSend = useCallback(async () => {
    if ((!input.trim() && pendingImages.length === 0) || isStreamingRef.current) return

    const userContent = input.trim()
    setInput('')

    // Lock immediately so stop button appears and re-entry is blocked
    isStreamingRef.current = true
    setIsStreaming(true)

    // Create conversation if needed
    let convId = currentConversationId
    if (!convId) {
      try {
        const result = await createConversation(userContent)
        convId = result.conversationId
        setCurrentConversationId(convId)
        navigate(`/chat/${convId}`, { replace: true })
        loadConversations()
      } catch {
        isStreamingRef.current = false
        setIsStreaming(false)
        return
      }
    }

    // Add user message to UI
    const userMessage: Message = {
      id: Date.now(),
      role: 'user',
      content: userContent,
      timestamp: new Date(),
      images: pendingImages.length > 0 ? pendingImages : undefined,
    }
    setMessages((prev) => [...prev, userMessage])

    const imagesToSend = pendingImages
    setPendingImages([])

    // Add placeholder assistant message
    const assistantMessage: Message = {
      id: Date.now() + 1,
      role: 'assistant',
      content: '',
      timestamp: new Date(),
      isStreaming: true,
      isThinking: true,
    }
    setMessages((prev) => [...prev, assistantMessage])

    // Send message to backend
    try {
      // If SSE has already failed, don't wait — show error immediately
      if (sseFailedRef.current) {
        throw new Error('SSE connection failed')
      }

      // Wait for SSE connection to be ready before sending (timeout after 30s)
      // Claude Code startup can be slow, especially on first run
      if (!sseReadyRef.current) {
        await new Promise<void>((resolve, reject) => {
          const timeout = setTimeout(() => reject(new Error('SSE connection timeout')), 30000)
          const check = () => {
            if (sseFailedRef.current) {
              clearTimeout(timeout)
              reject(new Error('SSE connection failed'))
            } else if (sseReadyRef.current) {
              clearTimeout(timeout)
              resolve()
            } else {
              setTimeout(check, 50)
            }
          }
          check()
        })
      }
      const result = await sendQueryMessage(convId, userContent, imagesToSend.length > 0 ? imagesToSend : undefined)
      if (result.contextLost) {
        setMessages((prev) => {
          const assistantIdx = prev.length - 1
          return [...prev.slice(0, assistantIdx), { id: Date.now() - 1, role: 'system', content: t('chatView.contextLostWarning'), timestamp: new Date() }, prev[assistantIdx]]
        })
      }
    } catch (err) {
      setMessages((prev) => {
        const last = prev[prev.length - 1]
        if (last.role === 'assistant') {
          return [...prev.slice(0, -1), { ...last, content: t('chatView.connectionError'), isStreaming: false, isThinking: false }]
        }
        return prev
      })
      isStreamingRef.current = false
      setIsStreaming(false)
    }
  }, [input, currentConversationId, navigate, t, pendingImages])

  // Handle stopping the stream
  const handleStop = useCallback(async () => {
    if (currentConversationId && isStreamingRef.current) {
      isStreamingRef.current = false
      setIsStreaming(false)
      setMessages((prev) => prev.map(m =>
        m.isStreaming ? { ...m, isStreaming: false, isThinking: false, toolUse: undefined, content: m.content || '[已停止]' } : m
      ))
      try {
        await interruptQuery(currentConversationId)
      } catch {
        // Ignore errors
      }
    }
  }, [currentConversationId])

  // Handle Enter key
  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      handleSend()
    }
  }

  // Start new conversation
  const handleNewChat = useCallback(() => {
    // IMPORTANT: Abort existing SSE connection first to prevent old messages leaking
    if (abortRef.current) {
      abortRef.current.abort()
      abortRef.current = null
    }

    // Clear all state
    setCurrentConversationId(undefined)
    setMessages([])
    isStreamingRef.current = false
    setIsStreaming(false)
    setPendingImages([])
    setShowHistory(false)
    sseReadyRef.current = false
    sseFailedRef.current = false
    setConnectionError(null)

    // Navigate to new chat route. On mobile, use a sentinel '/chat/new' to switch
    // to the compose view (input area). Desktop stays at '/chat' since its layout
    // already shows the input panel inline.
    navigate(isMobile ? '/chat/new' : '/chat')
  }, [navigate, isMobile])

  // Switch to a different conversation
  const handleSwitchConversation = (convId: number) => {
    setForceRefreshKey(k => k + 1) // Force effect re-run to handle race condition when switching back
    navigate(`/chat/${convId}`)
  }

  // Delete a conversation
  const { confirm, dialog: confirmDialog } = useConfirm()

  const handleDeleteConversation = async (convId: number, e: React.MouseEvent) => {
    e.stopPropagation()
    const confirmed = await confirm({
      title: t('chatView.delete'),
      message: t('chatView.deleteConfirm'),
    })
    if (!confirmed) return

    try {
      await deleteConversation(convId)
      if (currentConversationId === convId) {
        handleNewChat()
      }
      await loadConversations()
    } catch {
      // Still try to refresh the list even if delete may have partially succeeded
      loadConversations()
    }
  }

  // --- Mobile: single-column routing ---
  if (isMobile) {
    if (!urlConversationId && !isMobileComposeMode) {
      // Mobile: session list only
      return (
        <>
        <div className="flex flex-col h-full">
          <div className="p-4 border-b border-gray-200">
            <button
              onClick={handleNewChat}
              className="w-full px-4 py-2 bg-blue-500 text-white rounded-lg hover:bg-blue-600 transition-colors flex items-center justify-center gap-2"
            >
              <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
              </svg>
              {t('chatView.newConversation')}
            </button>
          </div>
          <div className="flex-1 overflow-y-auto overflow-x-hidden p-2">
            {conversations.length === 0 ? (
              <div className="text-center text-gray-500 text-sm p-4">
                {t('chatView.noPreviousConversations')}
              </div>
            ) : (
              <ul className="space-y-1">
                {conversations.map((conv) => (
                  <li key={conv.id}>
                    <button
                      data-testid="chat-session-item"
                      onClick={() => handleSwitchConversation(conv.id)}
                      className={`w-full px-3 py-2 text-left rounded-lg text-sm ${
                        currentConversationId === conv.id
                          ? 'bg-blue-100 text-blue-700'
                          : 'text-gray-700 hover:bg-gray-200'
                      }`}
                    >
                      <div className="truncate">{conv.title}</div>
                      <div className="text-xs text-gray-500 mt-1">
                        {new Date(conv.createdAt).toLocaleDateString(i18n.language === 'zh' ? 'zh-CN' : 'en-US')}
                      </div>
                    </button>
                  </li>
                ))}
              </ul>
            )}
          </div>
        </div>
        {confirmDialog}
        </>
      )
    }

    // Mobile: conversation stream + input only
    return (
      <>
      <div className="flex flex-col h-full">
        {/* Messages */}
        <div className="flex-1 overflow-auto p-4">
          <div className="space-y-4">
            {connectionError && (
              <div className="text-center py-4">
                <div className="inline-flex items-center gap-2 px-4 py-2 bg-red-50 text-red-700 rounded-lg text-sm">
                  <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-2.5L13.732 4c-.77-.833-1.964-.833-2.732 0L4.082 16.5c-.77.833.192 2.5 1.732 2.5z" />
                  </svg>
                  {connectionError}
                  <button
                    onClick={() => {
                      setConnectionError(null)
                      sseFailedRef.current = false
                      setForceRefreshKey(k => k + 1)
                    }}
                    className="text-red-600 hover:text-red-800 underline ml-2"
                  >
                    {t('chatView.retry') || 'Retry'}
                  </button>
                </div>
              </div>
            )}
            {messages.length === 0 && !connectionError ? (
              <div className="text-center text-gray-500 py-12">
                <svg className="w-16 h-16 mx-auto mb-4 text-gray-400" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M8 12h.01M12 12h.01M16 12h.01M21 12c0 4.418-4.03 8-9 8a9.863 9.863 0 01-4.255-.949L3 20l1.395-3.72C3.512 15.042 3 13.574 3 12c0-4.418 4.03-8 9-8s9 3.582 9 8z" />
                </svg>
                <p className="text-lg font-medium mb-2">{t('chatView.startConversation')}</p>
                <p className="text-sm">{t('chatView.askAboutKnowledge')}</p>
              </div>
            ) : (
              messages.map((msg) => (
                <div
                  key={msg.id}
                  className={`flex gap-3 ${msg.role === 'user' ? 'justify-end' : ''}`}
                >
                  {msg.role !== 'user' && (
                    <div className="w-8 h-8 rounded-full bg-blue-500 flex items-center justify-center text-white text-sm shrink-0">
                      AI
                    </div>
                  )}
                  <div
                    className={`flex-1 rounded-lg p-3 max-w-[85%] ${
                      msg.role === 'user'
                        ? 'bg-blue-500 text-white'
                        : msg.role === 'system'
                        ? 'bg-yellow-100 text-yellow-800'
                        : 'bg-gray-100 text-gray-800'
                    }`}
                  >
                    {msg.role === 'system' && (
                      <div className="text-xs font-medium mb-1">{t('chatView.system')}</div>
                    )}
                    {msg.role === 'user' && msg.images && msg.images.length > 0 && (
                      <div className="flex gap-2 mb-2">
                        {msg.images.map((imgPath: string, idx: number) => (
                          <img
                            key={idx}
                            src={imgPath}
                            alt={`image-${idx}`}
                            className="w-20 h-20 object-cover rounded cursor-pointer hover:opacity-80"
                            onClick={() => setEnlargedImage(imgPath)}
                          />
                        ))}
                      </div>
                    )}
                    {msg.role === 'assistant' && (msg.isThinking || (msg.isStreaming && !msg.content && !msg.toolUse)) ? (
                      <div className="flex items-center gap-2">
                        <div className="animate-spin w-4 h-4 border-2 border-gray-300 border-t-blue-500 rounded-full"></div>
                        <span className="text-gray-500">{t('chatView.thinking')}</span>
                      </div>
                    ) : msg.role === 'assistant' && msg.toolUse && !msg.content ? (
                      <div className="flex items-center gap-2">
                        <div className="animate-spin w-4 h-4 border-2 border-gray-300 border-t-blue-500 rounded-full"></div>
                        <span className="text-gray-500 text-sm">{msg.toolUse}</span>
                      </div>
                    ) : msg.role === 'assistant' && msg.isStreaming && msg.content ? (
                      <div>
                        <div className="prose prose-sm prose-slate max-w-none text-sm [&_p]:my-1 [&_h1]:text-base [&_h2]:text-base [&_h3]:text-sm [&_ul]:my-1 [&_ol]:my-1 [&_li]:my-0.5 [&_code]:text-xs [&_pre]:my-1 [&_pre]:bg-gray-800 [&_pre]:text-gray-100 [&_pre]:rounded [&_pre]:p-3 [&_table]:my-1 [&_table]:border [&_table]:border-collapse [&_table]:w-full [&_table]:overflow-x-auto [&_th]:border [&_th]:border-gray-300 [&_th]:bg-gray-50 [&_th]:px-2 [&_th]:py-1 [&_th]:font-medium [&_th]:text-left [&_td]:border [&_td]:border-gray-300 [&_td]:px-2 [&_td]:py-1 [&_tr:nth-child(even)_td]:bg-gray-50 [&_blockquote]:border-l-3 [&_blockquote]:border-blue-400 [&_blockquote]:pl-3 [&_blockquote]:text-gray-600 [&_strong]:text-gray-900 [&_a]:text-blue-500 [&_a]:underline">
                          <ReactMarkdown remarkPlugins={[remarkGfm]}>
                            {msg.content}
                          </ReactMarkdown>
                        </div>
                        {msg.toolUse ? (
                          <div className="flex items-center gap-2 mt-2 pt-2 border-t border-gray-200">
                            <div className="animate-spin w-3 h-3 border-2 border-gray-300 border-t-blue-500 rounded-full"></div>
                            <span className="text-gray-400 text-xs">{msg.toolUse}</span>
                          </div>
                        ) : (
                          <div className="flex items-center gap-2 mt-2 pt-2 border-t border-gray-200">
                            <div className="animate-spin w-3 h-3 border-2 border-gray-300 border-t-blue-500 rounded-full"></div>
                            <span className="text-gray-400 text-xs">{t('chatView.thinking')}</span>
                          </div>
                        )}
                      </div>
                    ) : msg.role === 'assistant' ? (
                      <div className="prose prose-sm prose-slate max-w-none text-sm [&_p]:my-1 [&_h1]:text-base [&_h2]:text-base [&_h3]:text-sm [&_ul]:my-1 [&_ol]:my-1 [&_li]:my-0.5 [&_code]:text-xs [&_pre]:my-1 [&_pre]:bg-gray-800 [&_pre]:text-gray-100 [&_pre]:rounded [&_pre]:p-3 [&_table]:my-1 [&_table]:border [&_table]:border-collapse [&_table]:w-full [&_table]:overflow-x-auto [&_th]:border [&_th]:border-gray-300 [&_th]:bg-gray-50 [&_th]:px-2 [&_th]:py-1 [&_th]:font-medium [&_th]:text-left [&_td]:border [&_td]:border-gray-300 [&_td]:px-2 [&_td]:py-1 [&_tr:nth-child(even)_td]:bg-gray-50 [&_blockquote]:border-l-3 [&_blockquote]:border-blue-400 [&_blockquote]:pl-3 [&_blockquote]:text-gray-600 [&_strong]:text-gray-900 [&_a]:text-blue-500 [&_a]:underline">
                        <ReactMarkdown remarkPlugins={[remarkGfm]}>
                          {msg.content}
                        </ReactMarkdown>
                      </div>
                    ) : (
                      <div className="whitespace-pre-wrap">
                        {msg.content}
                      </div>
                    )}
                    <div className={`text-xs mt-2 ${msg.role === 'user' ? 'text-blue-200' : 'text-gray-500'}`}>
                      {msg.timestamp.toLocaleTimeString(i18n.language === 'zh' ? 'zh-CN' : 'en-US')}
                    </div>
                  </div>
                  {msg.role === 'user' && (
                    <div className="w-8 h-8 rounded-full bg-gray-300 flex items-center justify-center text-gray-600 text-sm shrink-0">
                      U
                    </div>
                  )}
                </div>
              ))
            )}
            <div ref={messagesEndRef} />
          </div>
        </div>

        {/* Input */}
        <div className="p-3 border-t border-gray-200">
          {pendingImages.length > 0 && (
            <div className="flex gap-2 p-2 bg-gray-50 rounded-lg mb-2">
              {pendingImages.map((path, index) => (
                <div key={path} className="relative">
                  <img
                    src={path}
                    alt={`pending-${index}`}
                    className="w-16 h-16 object-cover rounded cursor-pointer hover:opacity-80"
                    onClick={() => setEnlargedImage(path)}
                  />
                  <button
                    onClick={() => handleRemoveImage(index)}
                    className="absolute -top-1 -right-1 w-5 h-5 bg-red-500 text-white rounded-full text-xs hover:bg-red-600"
                  >
                    ×
                  </button>
                </div>
              ))}
            </div>
          )}
          <div className="flex gap-2">
            <input
              type="file"
              accept="image/png,image/jpeg,image/gif,image/webp"
              onChange={handleFileInputChange}
              className="hidden"
              id="mobile-image-upload-input"
            />
            <input
              ref={inputRef}
              type="text"
              value={input}
              onChange={(e) => setInput(e.target.value)}
              onKeyDown={handleKeyDown}
              onPaste={handlePaste}
              placeholder={t('chatView.placeholder')}
              disabled={isStreaming}
              aria-label="message input"
              className="flex-1 px-4 py-3 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent disabled:bg-gray-100 disabled:text-gray-500"
            />
            <button
              onClick={() => document.getElementById('mobile-image-upload-input')?.click()}
              disabled={isStreaming}
              className="px-3 py-3 bg-gray-100 text-gray-600 rounded-lg hover:bg-gray-200 transition-colors disabled:bg-gray-300 disabled:text-gray-500"
              title={t('chatView.uploadImage')}
            >
              +
            </button>
            {isStreaming ? (
              <button
                onClick={handleStop}
                className="px-4 py-3 bg-red-500 text-white rounded-lg hover:bg-red-600 transition-colors flex items-center gap-2"
              >
                <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                  <rect x="6" y="6" width="12" height="12" strokeWidth={2} />
                </svg>
              </button>
            ) : (
              <button
                onClick={handleSend}
                disabled={!input.trim() && pendingImages.length === 0}
                className="px-4 py-3 bg-blue-500 text-white rounded-lg hover:bg-blue-600 transition-colors disabled:bg-gray-300 disabled:text-gray-500 disabled:cursor-not-allowed flex items-center gap-2"
              >
                <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 19l9 2-9-18-9 18 9-2zm0 0v-8" />
                </svg>
              </button>
            )}
          </div>
        </div>

        {/* Image enlargement overlay */}
        {enlargedImage && (
          <div
            className="fixed inset-0 bg-black bg-opacity-75 flex items-center justify-center z-50"
            onClick={() => setEnlargedImage(null)}
          >
            <button
              className="absolute top-4 right-4 w-8 h-8 bg-white text-black rounded-full text-lg hover:bg-gray-200"
              onClick={() => setEnlargedImage(null)}
            >
              ×
            </button>
            <img
              src={enlargedImage}
              alt="enlarged"
              className="max-w-[90%] max-h-[90%] object-contain"
            />
          </div>
        )}
      </div>
      {confirmDialog}
      </>
    )
  }

  // --- Desktop branch (unchanged) ---
  return (
    <>
    <div className="flex h-full">
      {/* Conversation history sidebar */}
      {showHistory && (
        <div className="w-64 border-r border-gray-200 bg-gray-50 flex flex-col">
          <div className="p-4 border-b border-gray-200">
            <button
              onClick={handleNewChat}
              className="w-full px-4 py-2 bg-blue-500 text-white rounded-lg hover:bg-blue-600 transition-colors flex items-center justify-center gap-2"
            >
              <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
              </svg>
              {t('chatView.newConversation')}
            </button>
          </div>
          <div className="flex-1 overflow-y-auto overflow-x-hidden p-2">
            {conversations.length === 0 ? (
              <div className="text-center text-gray-500 text-sm p-4">
                {t('chatView.noPreviousConversations')}
              </div>
            ) : (
              <ul className="space-y-1">
                {conversations.map((conv) => (
                  <li key={conv.id} className="flex items-center gap-1 min-w-0">
                    <button
                      onClick={() => handleSwitchConversation(conv.id)}
                      className={`flex-1 min-w-0 px-3 py-2 text-left rounded-lg text-sm ${
                        currentConversationId === conv.id
                          ? 'bg-blue-100 text-blue-700'
                          : 'text-gray-700 hover:bg-gray-200'
                      }`}
                    >
                      <div className="truncate">{conv.title}</div>
                      <div className="text-xs text-gray-500 mt-1">
                        {new Date(conv.createdAt).toLocaleDateString(i18n.language === 'zh' ? 'zh-CN' : 'en-US')}
                      </div>
                    </button>
                    <button
                      onClick={(e) => handleDeleteConversation(conv.id, e)}
                      className="p-1 text-gray-400 hover:text-red-500 hover:bg-red-50 rounded"
                      title={t('chatView.delete')}
                    >
                      <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" />
                      </svg>
                    </button>
                  </li>
                ))}
              </ul>
            )}
          </div>
        </div>
      )}

      {/* Main chat area */}
      <div className="flex-1 flex flex-col">
        {/* Header */}
        <div className="p-4 border-b border-gray-200 flex items-center justify-between">
          <div className="flex items-center gap-4">
            <button
              onClick={() => setShowHistory(!showHistory)}
              className="p-2 text-gray-600 hover:bg-gray-100 rounded-lg"
            >
              <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M4 6h16M4 12h16M4 18h16" />
              </svg>
            </button>
            <h2 className="text-xl font-semibold text-gray-800">
              {currentConversationId ? `${t('chatView.title')} #${currentConversationId}` : t('chatView.newConversation')}
            </h2>
          </div>
          {!currentConversationId && (
            <button
              onClick={handleNewChat}
              className="px-3 py-1.5 text-sm bg-blue-100 text-blue-700 rounded-lg hover:bg-blue-200"
            >
              {t('chatView.clearChat')}
            </button>
          )}
        </div>

        {/* Messages */}
        <div className="flex-1 overflow-auto p-6">
          <div className="max-w-3xl mx-auto space-y-4">
            {connectionError && (
              <div className="text-center py-4">
                <div className="inline-flex items-center gap-2 px-4 py-2 bg-red-50 text-red-700 rounded-lg text-sm">
                  <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-2.5L13.732 4c-.77-.833-1.964-.833-2.732 0L4.082 16.5c-.77.833.192 2.5 1.732 2.5z" />
                  </svg>
                  {connectionError}
                  <button
                    onClick={() => {
                      setConnectionError(null)
                      sseFailedRef.current = false
                      setForceRefreshKey(k => k + 1)
                    }}
                    className="text-red-600 hover:text-red-800 underline ml-2"
                  >
                    {t('chatView.retry') || 'Retry'}
                  </button>
                </div>
              </div>
            )}
            {messages.length === 0 && !connectionError ? (
              <div className="text-center text-gray-500 py-12">
                <svg className="w-16 h-16 mx-auto mb-4 text-gray-400" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M8 12h.01M12 12h.01M16 12h.01M21 12c0 4.418-4.03 8-9 8a9.863 9.863 0 01-4.255-.949L3 20l1.395-3.72C3.512 15.042 3 13.574 3 12c0-4.418 4.03-8 9-8s9 3.582 9 8z" />
                </svg>
                <p className="text-lg font-medium mb-2">{t('chatView.startConversation')}</p>
                <p className="text-sm">{t('chatView.askAboutKnowledge')}</p>
              </div>
            ) : (
              messages.map((msg) => (
                <div
                  key={msg.id}
                  className={`flex gap-3 ${msg.role === 'user' ? 'justify-end' : ''}`}
                >
                  {msg.role !== 'user' && (
                    <div className="w-8 h-8 rounded-full bg-blue-500 flex items-center justify-center text-white text-sm shrink-0">
                      AI
                    </div>
                  )}
                  <div
                    className={`flex-1 rounded-lg p-3 max-w-[85%] ${
                      msg.role === 'user'
                        ? 'bg-blue-500 text-white'
                        : msg.role === 'system'
                        ? 'bg-yellow-100 text-yellow-800'
                        : 'bg-gray-100 text-gray-800'
                    }`}
                  >
                    {msg.role === 'system' && (
                      <div className="text-xs font-medium mb-1">{t('chatView.system')}</div>
                    )}
                    {msg.role === 'user' && msg.images && msg.images.length > 0 && (
                      <div className="flex gap-2 mb-2">
                        {msg.images.map((imgPath: string, idx: number) => (
                          <img
                            key={idx}
                            src={imgPath}
                            alt={`image-${idx}`}
                            className="w-20 h-20 object-cover rounded cursor-pointer hover:opacity-80"
                            onClick={() => setEnlargedImage(imgPath)}
                          />
                        ))}
                      </div>
                    )}
                    {msg.role === 'assistant' && (msg.isThinking || (msg.isStreaming && !msg.content && !msg.toolUse)) ? (
                      <div className="flex items-center gap-2">
                        <div className="animate-spin w-4 h-4 border-2 border-gray-300 border-t-blue-500 rounded-full"></div>
                        <span className="text-gray-500">{t('chatView.thinking')}</span>
                      </div>
                    ) : msg.role === 'assistant' && msg.toolUse && !msg.content ? (
                      <div className="flex items-center gap-2">
                        <div className="animate-spin w-4 h-4 border-2 border-gray-300 border-t-blue-500 rounded-full"></div>
                        <span className="text-gray-500 text-sm">{msg.toolUse}</span>
                      </div>
                    ) : msg.role === 'assistant' && msg.isStreaming && msg.content ? (
                      <div>
                        <div className="prose prose-sm prose-slate max-w-none text-sm [&_p]:my-1 [&_h1]:text-base [&_h2]:text-base [&_h3]:text-sm [&_ul]:my-1 [&_ol]:my-1 [&_li]:my-0.5 [&_code]:text-xs [&_pre]:my-1 [&_pre]:bg-gray-800 [&_pre]:text-gray-100 [&_pre]:rounded [&_pre]:p-3 [&_table]:my-1 [&_table]:border [&_table]:border-collapse [&_table]:w-full [&_table]:overflow-x-auto [&_th]:border [&_th]:border-gray-300 [&_th]:bg-gray-50 [&_th]:px-2 [&_th]:py-1 [&_th]:font-medium [&_th]:text-left [&_td]:border [&_td]:border-gray-300 [&_td]:px-2 [&_td]:py-1 [&_tr:nth-child(even)_td]:bg-gray-50 [&_blockquote]:border-l-3 [&_blockquote]:border-blue-400 [&_blockquote]:pl-3 [&_blockquote]:text-gray-600 [&_strong]:text-gray-900 [&_a]:text-blue-500 [&_a]:underline">
                          <ReactMarkdown remarkPlugins={[remarkGfm]}>
                            {msg.content}
                          </ReactMarkdown>
                        </div>
                        {msg.toolUse ? (
                          <div className="flex items-center gap-2 mt-2 pt-2 border-t border-gray-200">
                            <div className="animate-spin w-3 h-3 border-2 border-gray-300 border-t-blue-500 rounded-full"></div>
                            <span className="text-gray-400 text-xs">{msg.toolUse}</span>
                          </div>
                        ) : (
                          <div className="flex items-center gap-2 mt-2 pt-2 border-t border-gray-200">
                            <div className="animate-spin w-3 h-3 border-2 border-gray-300 border-t-blue-500 rounded-full"></div>
                            <span className="text-gray-400 text-xs">{t('chatView.thinking')}</span>
                          </div>
                        )}
                      </div>
                    ) : msg.role === 'assistant' ? (
                      <div className="prose prose-sm prose-slate max-w-none text-sm [&_p]:my-1 [&_h1]:text-base [&_h2]:text-base [&_h3]:text-sm [&_ul]:my-1 [&_ol]:my-1 [&_li]:my-0.5 [&_code]:text-xs [&_pre]:my-1 [&_pre]:bg-gray-800 [&_pre]:text-gray-100 [&_pre]:rounded [&_pre]:p-3 [&_table]:my-1 [&_table]:border [&_table]:border-collapse [&_table]:w-full [&_table]:overflow-x-auto [&_th]:border [&_th]:border-gray-300 [&_th]:bg-gray-50 [&_th]:px-2 [&_th]:py-1 [&_th]:font-medium [&_th]:text-left [&_td]:border [&_td]:border-gray-300 [&_td]:px-2 [&_td]:py-1 [&_tr:nth-child(even)_td]:bg-gray-50 [&_blockquote]:border-l-3 [&_blockquote]:border-blue-400 [&_blockquote]:pl-3 [&_blockquote]:text-gray-600 [&_strong]:text-gray-900 [&_a]:text-blue-500 [&_a]:underline">
                        <ReactMarkdown remarkPlugins={[remarkGfm]}>
                          {msg.content}
                        </ReactMarkdown>
                      </div>
                    ) : (
                      <div className="whitespace-pre-wrap">
                        {msg.content}
                      </div>
                    )}
                    <div className={`text-xs mt-2 ${msg.role === 'user' ? 'text-blue-200' : 'text-gray-500'}`}>
                      {msg.timestamp.toLocaleTimeString(i18n.language === 'zh' ? 'zh-CN' : 'en-US')}
                    </div>
                  </div>
                  {msg.role === 'user' && (
                    <div className="w-8 h-8 rounded-full bg-gray-300 flex items-center justify-center text-gray-600 text-sm shrink-0">
                      U
                    </div>
                  )}
                </div>
              ))
            )}

            <div ref={messagesEndRef} />
          </div>
        </div>

        {/* Input */}
        <div className="p-4 border-t border-gray-200">
          <div className="max-w-3xl mx-auto">
            {pendingImages.length > 0 && (
              <div className="flex gap-2 p-2 bg-gray-50 rounded-lg mb-2">
                {pendingImages.map((path, index) => (
                  <div key={path} className="relative">
                    <img
                      src={path}
                      alt={`pending-${index}`}
                      className="w-16 h-16 object-cover rounded cursor-pointer hover:opacity-80"
                      onClick={() => setEnlargedImage(path)}
                    />
                    <button
                      onClick={() => handleRemoveImage(index)}
                      className="absolute -top-1 -right-1 w-5 h-5 bg-red-500 text-white rounded-full text-xs hover:bg-red-600"
                    >
                      ×
                    </button>
                  </div>
                ))}
              </div>
            )}
            <div className="flex gap-2">
              <input
                type="file"
                accept="image/png,image/jpeg,image/gif,image/webp"
                onChange={handleFileInputChange}
                className="hidden"
                id="image-upload-input"
              />
              <input
                ref={inputRef}
                type="text"
                value={input}
                onChange={(e) => setInput(e.target.value)}
                onKeyDown={handleKeyDown}
                onPaste={handlePaste}
                placeholder={t('chatView.placeholder')}
                disabled={isStreaming}
                aria-label="message input"
                className="flex-1 px-4 py-3 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent disabled:bg-gray-100 disabled:text-gray-500"
              />
              <button
                onClick={() => document.getElementById('image-upload-input')?.click()}
                disabled={isStreaming}
                className="px-4 py-3 bg-gray-100 text-gray-600 rounded-lg hover:bg-gray-200 transition-colors disabled:bg-gray-300 disabled:text-gray-500"
                title={t('chatView.uploadImage')}
              >
                +
              </button>
              {isStreaming ? (
                <button
                  onClick={handleStop}
                  className="px-6 py-3 bg-red-500 text-white rounded-lg hover:bg-red-600 transition-colors flex items-center gap-2"
                >
                  <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                    <rect x="6" y="6" width="12" height="12" strokeWidth={2} />
                  </svg>
                </button>
              ) : (
                <button
                  onClick={handleSend}
                  disabled={!input.trim() && pendingImages.length === 0}
                  className="px-6 py-3 bg-blue-500 text-white rounded-lg hover:bg-blue-600 transition-colors disabled:bg-gray-300 disabled:text-gray-500 disabled:cursor-not-allowed flex items-center gap-2"
                >
                  <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 19l9 2-9-18-9 18 9-2zm0 0v-8" />
                  </svg>
                </button>
              )}
            </div>
          </div>
          <div className="mt-2 text-center text-xs text-gray-400">
            {t('chatView.sendHint')}
          </div>
        </div>

        {/* Image enlargement overlay */}
        {enlargedImage && (
          <div
            className="fixed inset-0 bg-black bg-opacity-75 flex items-center justify-center z-50"
            onClick={() => setEnlargedImage(null)}
          >
            <button
              className="absolute top-4 right-4 w-8 h-8 bg-white text-black rounded-full text-lg hover:bg-gray-200"
              onClick={() => setEnlargedImage(null)}
            >
              ×
            </button>
            <img
              src={enlargedImage}
              alt="enlarged"
              className="max-w-[90%] max-h-[90%] object-contain"
            />
          </div>
        )}
      </div>
      {confirmDialog}
    </div>
    </>
  )
}
