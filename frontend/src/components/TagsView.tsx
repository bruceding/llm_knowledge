import { useState, useEffect } from 'react'
import { Link } from 'react-router-dom'
import { fetchDocuments } from '../api'
import type { Document, Tag } from '../types'

export default function TagsView() {
  const [documents, setDocuments] = useState<Document[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [selectedTag, setSelectedTag] = useState<string | null>(null)

  useEffect(() => {
    loadDocuments()
  }, [])

  const loadDocuments = async () => {
    setLoading(true)
    setError(null)
    try {
      const docs = await fetchDocuments()
      setDocuments(docs)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load documents')
    } finally {
      setLoading(false)
    }
  }

  // Aggregate tags from all documents
  const tagsMap = documents.reduce((acc, doc) => {
    for (const tag of doc.tags) {
      if (!acc.has(tag.name)) {
        acc.set(tag.name, { tag, count: 0, documents: [] })
      }
      const entry = acc.get(tag.name)!
      entry.count++
      entry.documents.push(doc)
    }
    return acc
  }, new Map<string, { tag: Tag; count: number; documents: Document[] }>())

  const tags = Array.from(tagsMap.values()).sort((a, b) => b.count - a.count)

  const filteredDocuments = selectedTag
    ? tagsMap.get(selectedTag)?.documents || []
    : documents

  if (loading) {
    return (
      <div className="p-6">
        <h2 className="text-2xl font-bold text-gray-800 mb-4">Tags</h2>
        <div className="flex items-center justify-center py-12">
          <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-blue-500"></div>
        </div>
      </div>
    )
  }

  if (error) {
    return (
      <div className="p-6">
        <h2 className="text-2xl font-bold text-gray-800 mb-4">Tags</h2>
        <div className="bg-red-50 border border-red-200 rounded-lg p-4 text-red-700">
          {error}
          <button onClick={loadDocuments} className="ml-4 text-red-800 underline">
            Retry
          </button>
        </div>
      </div>
    )
  }

  return (
    <div className="p-6">
      <h2 className="text-2xl font-bold text-gray-800 mb-4">Tags</h2>
      <p className="text-gray-600 mb-6">Browse documents by tag</p>

      {tags.length === 0 ? (
        <div className="border-2 border-dashed border-gray-300 rounded-lg p-12 text-center text-gray-500">
          <svg className="w-12 h-12 mx-auto mb-4 text-gray-400" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M7 7h.01M7 3h5c.512 0 1.024.195 1.414.586l7 7a2 2 0 010 2.828l-7 7a2 2 0 01-2.828 0l-7-7A1.994 1.994 0 013 12V7a4 4 0 014-4z" />
          </svg>
          <p className="text-lg font-medium mb-2">No tags found</p>
          <p className="text-sm">Add tags to documents to organize your knowledge base</p>
        </div>
      ) : (
        <div className="grid grid-cols-1 lg:grid-cols-3 gap-6">
          {/* Tags list */}
          <div className="lg:col-span-1">
            <div className="bg-white border border-gray-200 rounded-lg p-4">
              <h3 className="text-lg font-semibold text-gray-700 mb-4">All Tags ({tags.length})</h3>
              <div className="space-y-2">
                {tags.map(({ tag, count }) => (
                  <button
                    key={tag.name}
                    onClick={() => setSelectedTag(selectedTag === tag.name ? null : tag.name)}
                    className={`w-full flex items-center justify-between px-3 py-2 rounded-lg transition-colors ${
                      selectedTag === tag.name
                        ? 'bg-blue-100 text-blue-700'
                        : 'hover:bg-gray-100 text-gray-700'
                    }`}
                  >
                    <div className="flex items-center gap-2">
                      <span
                        className="w-3 h-3 rounded-full"
                        style={{ backgroundColor: tag.color }}
                      />
                      <span className="text-sm">{tag.name}</span>
                    </div>
                    <span className="text-xs text-gray-500">{count} docs</span>
                  </button>
                ))}
              </div>
            </div>
          </div>

          {/* Documents with selected tag */}
          <div className="lg:col-span-2">
            <div className="bg-white border border-gray-200 rounded-lg p-4">
              <h3 className="text-lg font-semibold text-gray-700 mb-4">
                {selectedTag ? `Documents tagged "${selectedTag}"` : 'Recent Documents'}
              </h3>
              {filteredDocuments.length === 0 ? (
                <div className="text-center text-gray-500 py-8">
                  No documents found
                </div>
              ) : (
                <div className="space-y-2">
                  {filteredDocuments.slice(0, 20).map((doc) => (
                    <Link
                      key={doc.id}
                      to={`/documents/${doc.id}`}
                      className="block flex items-center justify-between px-3 py-3 hover:bg-gray-50 rounded-lg transition-colors"
                    >
                      <div>
                        <div className="text-sm font-medium text-gray-800">{doc.title}</div>
                        <div className="flex items-center gap-2 mt-1">
                          <span className="text-xs text-gray-500 capitalize">{doc.sourceType}</span>
                          <span className="text-xs text-gray-400">
                            {new Date(doc.createdAt).toLocaleDateString()}
                          </span>
                        </div>
                      </div>
                      <div className="flex items-center gap-2">
                        {doc.tags.slice(0, 3).map((t) => (
                          <span
                            key={t.id}
                            className="px-2 py-0.5 text-xs rounded-full"
                            style={{ backgroundColor: t.color + '20', color: t.color }}
                          >
                            {t.name}
                          </span>
                        ))}
                      </div>
                    </Link>
                  ))}
                </div>
              )}
            </div>
          </div>
        </div>
      )}
    </div>
  )
}