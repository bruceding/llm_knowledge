import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'

interface PDFTranslationViewProps {
  rawPath: string
  translatedContent: string
  totalPages: number
  translating: boolean
}

// Parse translated content by page markers
function parseTranslatedPages(content: string): Map<number, string> {
  const pages = new Map<number, string>()
  const pageRegex = /--- Page (\d+) ---\n([\s\S]*?)(?=\n--- Page|$)/g
  let match
  while ((match = pageRegex.exec(content)) !== null) {
    pages.set(parseInt(match[1]), match[2].trim())
  }
  return pages
}

export default function PDFTranslationView({
  rawPath,
  translatedContent,
  totalPages,
  translating,
}: PDFTranslationViewProps) {
  const pageTranslations = parseTranslatedPages(translatedContent)

  // Generate page numbers array
  const pageNumbers = Array.from({ length: totalPages }, (_, i) => i + 1)

  if (translating && translatedContent === '') {
    return (
      <div className="flex items-center justify-center h-full">
        <div className="text-center">
          <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-blue-500 mx-auto mb-4"></div>
          <p className="text-gray-600">正在翻译...</p>
        </div>
      </div>
    )
  }

  return (
    <div className="space-y-8 p-6 max-w-5xl mx-auto">
      {pageNumbers.map((pageNum) => (
        <div key={pageNum} className="border-b border-gray-200 pb-8 last:border-b-0">
          {/* Page header */}
          <div className="flex items-center justify-between mb-4">
            <span className="text-sm font-medium text-gray-500">Page {pageNum}</span>
          </div>

          {/* Original page image */}
          <div className="mb-6">
            <div className="bg-gray-100 rounded-lg p-2">
              <img
                src={`/data/${rawPath}/pages/page_${pageNum}.png`}
                alt={`Page ${pageNum}`}
                className="max-w-full mx-auto rounded shadow-sm"
                loading="lazy"
              />
            </div>
          </div>

          {/* Translated content for this page */}
          {pageTranslations.has(pageNum) ? (
            <div className="prose prose-slate max-w-none">
              <div className="bg-blue-50 rounded-lg p-4">
                <ReactMarkdown remarkPlugins={[remarkGfm]}>
                  {pageTranslations.get(pageNum) || ''}
                </ReactMarkdown>
              </div>
            </div>
          ) : translating && pageNum > pageTranslations.size ? (
            <div className="flex items-center justify-center py-8">
              <div className="animate-spin rounded-full h-6 w-6 border-b-2 border-blue-500"></div>
            </div>
          ) : null}
        </div>
      ))}
    </div>
  )
}