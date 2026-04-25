import { useState, useEffect, useRef, useCallback } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { askQuestion } from '../api'
import type { SSEEvent } from '../types'

interface Message {
  id: number
  role: 'user' | 'assistant' | 'system'
  content: string
  timestamp: Date
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
  const [toolUseStatus, setToolUseStatus] = useState<string | null>(null)
  const [conversations, _setConversations] = useState<Conversation[]>([])
  const [showHistory, setShowHistory] = useState(false)

  const messagesEndRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLInputElement>(null)

  // Scroll to bottom when new messages arrive
  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages])

  // Load conversation history on mount
  useEffect(() => {
    // Placeholder: would load conversation messages from backend
    // Backend doesn't have list conversations endpoint yet, so we show placeholder
    if (conversationId) {
      // Would fetch messages for this conversation
      setMessages([
        {
          id: 1,
          role: 'system',
          content: 'Welcome to the knowledge base chat. Ask questions about your documents.',
          timestamp: new Date(),
        },
      ])
    }
  }, [conversationId])

  // Handle sending a message
  const handleSend = useCallback(async () => {
    if (!input.trim() || streaming) return

    const userMessage: Message = {
      id: messages.length + 1,
      role: 'user',
      content: input.trim(),
      timestamp: new Date(),
    }

    setMessages((prev) => [...prev, userMessage])
    setInput('')
    setStreaming(true)
    setToolUseStatus(null)

    // Add placeholder assistant message
    const assistantMessage: Message = {
      id: messages.length + 2,
      role: 'assistant',
      content: '',
      timestamp: new Date(),
    }
    setMessages((prev) => [...prev, assistantMessage])

    try {
      await askQuestion(
        {
          conversationId: currentConversationId,
          question: userMessage.content,
        },
        (event: SSEEvent) => {
          if (event.type === 'conversation') {
            setCurrentConversationId(event.conversationId)
            // Update URL if new conversation
            if (!currentConversationId && event.conversationId) {
              navigate(`/chat/${event.conversationId}`, { replace: true })
            }
          } else if (event.type === 'assistant') {
            // Append content to assistant message
            setMessages((prev) => {
              const last = prev[prev.length - 1]
              if (last.role === 'assistant') {
                return [...prev.slice(0, -1), { ...last, content: last.content + (event.content || '') }]
              }
              return prev
            })
          } else if (event.type === 'tool_use') {
            setToolUseStatus(event.toolName || 'Processing...')
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
      setToolUseStatus(null)
      inputRef.current?.focus()
    }
  }, [input, streaming, currentConversationId, messages.length, navigate])

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
                    <div className="whitespace-pre-wrap">
                      {msg.content || (streaming && msg.role === 'assistant' ? t('chatView.thinking') : '')}
                    </div>
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

            {/* Tool use status */}
            {toolUseStatus && (
              <div className="flex items-center gap-2 px-4 py-2 bg-gray-50 border border-gray-200 rounded-lg">
                <div className="animate-spin w-4 h-4 border-2 border-gray-300 border-t-blue-500 rounded-full"></div>
                <span className="text-sm text-gray-600">{t('chatView.lookingUp')} {toolUseStatus}</span>
              </div>
            )}

            <div ref={messagesEndRef} />
          </div>
        </div>

        {/* Input */}
        <div className="p-4 border-t border-gray-200">
          <div className="max-w-3xl mx-auto flex gap-2">
            <input
              ref={inputRef}
              type="text"
              value={input}
              onChange={(e) => setInput(e.target.value)}
              onKeyDown={handleKeyDown}
              placeholder={t('chatView.placeholder')}
              disabled={streaming}
              className="flex-1 px-4 py-3 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent disabled:bg-gray-100 disabled:text-gray-500"
            />
            <button
              onClick={handleSend}
              disabled={streaming || !input.trim()}
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
          <div className="mt-2 text-center text-xs text-gray-400">
            {t('chatView.sendHint')}
          </div>
        </div>
      </div>
    </div>
  )
}