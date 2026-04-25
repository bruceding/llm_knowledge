import { Link } from 'react-router-dom'

export default function Sidebar() {
  return (
    <aside className="w-64 bg-gray-50 border-r border-gray-200 flex flex-col">
      <div className="p-4 border-b border-gray-200">
        <h1 className="text-lg font-semibold text-gray-800">LLM Knowledge</h1>
      </div>
      <nav className="flex-1 p-2">
        <ul className="space-y-1">
          <li>
            <Link
              to="/"
              className="flex items-center gap-2 px-3 py-2 rounded-lg text-gray-700 hover:bg-gray-200 transition-colors"
            >
              <span>Inbox</span>
            </Link>
          </li>
          <li>
            <Link
              to="/wiki"
              className="flex items-center gap-2 px-3 py-2 rounded-lg text-gray-700 hover:bg-gray-200 transition-colors"
            >
              <span>Wiki</span>
            </Link>
          </li>
          <li>
            <Link
              to="/chat"
              className="flex items-center gap-2 px-3 py-2 rounded-lg text-gray-700 hover:bg-gray-200 transition-colors"
            >
              <span>Chat</span>
            </Link>
          </li>
          <li>
            <Link
              to="/import"
              className="flex items-center gap-2 px-3 py-2 rounded-lg text-gray-700 hover:bg-gray-200 transition-colors"
            >
              <span>Import</span>
            </Link>
          </li>
        </ul>
      </nav>
    </aside>
  )
}