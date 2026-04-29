import { useState, useEffect, useRef, useCallback } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { askQuestion, fetchConversations, fetchConversationMessages, uploadImage } from '../api'
import type { SSEEvent, ContentBlock } from '../types'

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
  isThinking: boolean
  toolUse?: string
} {
  let text = ''
  let isThinking = false
  let toolUse: string | undefined

  for (const block of blocks) {
    switch (block.type) {
      case 'text':
        if (block.text) text += block.text
        break
      case 'thinking':
        isThinking = true
        break
      case 'tool_use':
        toolUse = formatToolBlock(block)
        break
    }
  }

  return { text, isThinking, toolUse }
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
  const conversationId = params.id ? parseInt(params.id) : undefined

  const [messages, setMessages] = useState<Message[]>([])
  const [input, setInput] = useState('')
  const [streaming, setStreaming] = useState(false)
  const [currentConversationId, setCurrentConversationId] = useState<number | undefined>(conversationId)
  const [conversations, setConversations] = useState<Conversation[]>([])
  const [showHistory, setShowHistory] = useState(false)

  const [pendingImages, setPendingImages] = useState<string[]>([])
  const [enlargedImage, setEnlargedImage] = useState<string | null>(null)

  const messagesEndRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLInputElement>(null)

  // Track whether the current conversation was just created locally
  // to avoid resetting messages when navigate updates the URL
  const [locallyCreatedId, setLocallyCreatedId] = useState<number | undefined>(undefined)

  // Load conversation list
  const loadConversations = useCallback(async () => {
    try {
      const convs = await fetchConversations()
      setConversations(convs)
    } catch {
      // Silently fail - sidebar will show empty state
    }
  }, [])

  // Load conversation list on mount
  useEffect(() => {
    loadConversations()
  }, [loadConversations])

  // Scroll to bottom when new messages arrive
  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages])

  // Load conversation history on mount (only for conversations not created in current session)
  useEffect(() => {
    if (conversationId && conversationId !== locallyCreatedId) {
      fetchConversationMessages(conversationId).then((dbMessages) => {
        if (dbMessages.length > 0) {
          setMessages(dbMessages.map((m) => ({
            id: m.id,
            role: m.role as 'user' | 'assistant' | 'system',
            content: m.content,
            images: m.images ? JSON.parse(m.images) : undefined,
            timestamp: new Date(m.createdAt),
          })))
        }
      }).catch(() => {
        // Silently fail
      })
    }
  }, [conversationId, locallyCreatedId])

  // Handle sending a message
  const handleSend = useCallback(async () => {
    if ((!input.trim() && pendingImages.length === 0) || streaming) return

    const userMessage: Message = {
      id: messages.length + 1,
      role: 'user',
      content: input.trim(),
      timestamp: new Date(),
      images: pendingImages.length > 0 ? pendingImages : undefined,
    }

    setMessages((prev) => [...prev, userMessage])
    setInput('')
    setStreaming(true)
    const imagesToSend = pendingImages
    setPendingImages([])

    // Add placeholder assistant message
    const assistantMessage: Message = {
      id: messages.length + 2,
      role: 'assistant',
      content: '',
      timestamp: new Date(),
      isStreaming: true,
      isThinking: true,
    }
    setMessages((prev) => [...prev, assistantMessage])

    try {
      await askQuestion(
        {
          conversationId: currentConversationId,
          question: userMessage.content,
          images: imagesToSend.length > 0 ? imagesToSend : undefined,
        },
        (event: SSEEvent) => {
          if (event.type === 'conversation') {
            setCurrentConversationId(event.conversationId)
            // Mark this as locally created so useEffect won't reset messages
            if (!currentConversationId && event.conversationId) {
              setLocallyCreatedId(event.conversationId)
              navigate(`/chat/${event.conversationId}`, { replace: true })
            }
          } else if (event.type === 'assistant') {
            // Parse content blocks from the message (works across different models)
            const msg = event.message
            const blocks = typeof msg === 'object' ? msg?.content : undefined
            if (blocks && blocks.length > 0) {
              const { text, isThinking, toolUse } = extractFromContentBlocks(blocks)
              setMessages((prev) => {
                const last = prev[prev.length - 1]
                if (last.role === 'assistant') {
                  return [...prev.slice(0, -1), {
                    ...last,
                    content: last.content + text,
                    isThinking: isThinking && !text,
                    toolUse: toolUse && !text ? toolUse : undefined,
                  }]
                }
                return prev
              })
            } else if (event.content) {
              // Fallback: models without content blocks (just plain text)
              setMessages((prev) => {
                const last = prev[prev.length - 1]
                if (last.role === 'assistant') {
                  return [...prev.slice(0, -1), { ...last, content: last.content + event.content, isThinking: false, toolUse: undefined }]
                }
                return prev
              })
            }
          } else if (event.type === 'result') {
            // Mark streaming complete
            setMessages((prev) => prev.map(m =>
              m.isStreaming ? { ...m, isStreaming: false, isThinking: false, toolUse: undefined } : m
            ))
          } else if (event.type === 'error') {
            setMessages((prev) => {
              const last = prev[prev.length - 1]
              if (last.role === 'assistant') {
                return [...prev.slice(0, -1), { ...last, content: last.content + '\n\nError: ' + (event.error || 'Unknown error') }]
              }
              return prev
            })
          }
        }
      )
    } catch (err) {
      setMessages((prev) => {
        const last = prev[prev.length - 1]
        if (last.role === 'assistant') {
          return [...prev.slice(0, -1), { ...last, content: t('chatView.connectionError') }]
        }
        return prev
      })
    } finally {
      setStreaming(false)
      inputRef.current?.focus()
      loadConversations()
    }
  }, [input, streaming, currentConversationId, messages.length, navigate, pendingImages, t])

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

  // Handle Enter key
  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      handleSend()
    }
  }

  // Start new conversation
  const handleNewChat = () => {
    setCurrentConversationId(undefined)
    setLocallyCreatedId(undefined)
    setMessages([])
    navigate('/chat')
  }

  return (
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
          <div className="flex-1 overflow-auto p-2">
            {conversations.length === 0 ? (
              <div className="text-center text-gray-500 text-sm p-4">
                {t('chatView.noPreviousConversations')}
              </div>
            ) : (
              <ul className="space-y-1">
                {conversations.map((conv) => (
                  <li key={conv.id}>
                    <button
                      onClick={() => navigate(`/chat/${conv.id}`)}
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
            {messages.length === 0 ? (
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
                disabled={streaming}
                className="flex-1 px-4 py-3 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent disabled:bg-gray-100 disabled:text-gray-500"
              />
              <button
                onClick={() => document.getElementById('image-upload-input')?.click()}
                disabled={streaming}
                className="px-4 py-3 bg-gray-100 text-gray-600 rounded-lg hover:bg-gray-200 transition-colors disabled:bg-gray-300 disabled:text-gray-500"
                title={t('chatView.uploadImage')}
              >
                +
              </button>
              <button
                onClick={handleSend}
                disabled={streaming || (!input.trim() && pendingImages.length === 0)}
                className="px-6 py-3 bg-blue-500 text-white rounded-lg hover:bg-blue-600 transition-colors disabled:bg-gray-300 disabled:text-gray-500 disabled:cursor-not-allowed flex items-center gap-2"
              >
                {streaming ? (
                  <div className="animate-spin w-5 h-5 border-2 border-white border-t-transparent rounded-full"></div>
                ) : (
                  <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 19l9 2-9-18-9 18 9-2zm0 0v-8" />
                  </svg>
                )}
              </button>
            </div>
          </div>
          <div className="mt-2 text-center text-xs text-gray-400">
            {t('chatView.sendHint')}
          </div>
        </div>
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
    </div>
  )
}