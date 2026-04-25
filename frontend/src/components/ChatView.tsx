import { useParams } from 'react-router-dom'

export default function ChatView() {
  const { id } = useParams<{ id?: string }>()

  return (
    <div className="flex flex-col h-full">
      <div className="p-4 border-b border-gray-200">
        <h2 className="text-xl font-semibold text-gray-800">
          Chat {id ? `#${id}` : ''}
        </h2>
      </div>
      <div className="flex-1 overflow-auto p-6">
        <div className="max-w-3xl mx-auto space-y-4">
          <div className="flex gap-3">
            <div className="w-8 h-8 rounded-full bg-blue-500 flex items-center justify-center text-white text-sm">
              U
            </div>
            <div className="flex-1 bg-gray-100 rounded-lg p-3">
              <p className="text-gray-800">Hello! How can I help you today?</p>
            </div>
          </div>
          <div className="flex gap-3 justify-end">
            <div className="flex-1 bg-blue-500 rounded-lg p-3 max-w-[80%]">
              <p className="text-white">Tell me about the knowledge base.</p>
            </div>
            <div className="w-8 h-8 rounded-full bg-gray-300 flex items-center justify-center text-gray-600 text-sm">
              A
            </div>
          </div>
        </div>
      </div>
      <div className="p-4 border-t border-gray-200">
        <div className="max-w-3xl mx-auto">
          <input
            type="text"
            placeholder="Type a message..."
            className="w-full px-4 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500"
          />
        </div>
      </div>
    </div>
  )
}