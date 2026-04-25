import { useEffect, useRef, useState, useCallback } from 'react'
import * as pdfjsLib from 'pdfjs-dist'

// Set worker path using Vite's URL handling
pdfjsLib.GlobalWorkerOptions.workerSrc = new URL(
  'pdfjs-dist/build/pdf.worker.mjs',
  import.meta.url
).toString()

interface PDFViewerProps {
  url: string
  onPageChange?: (page: number) => void
}

export default function PDFViewer({ url, onPageChange }: PDFViewerProps) {
  const containerRef = useRef<HTMLDivElement>(null)
  const canvasRef = useRef<HTMLCanvasElement>(null)
  const scaleRef = useRef(1.2)
  const renderTaskRef = useRef<pdfjsLib.RenderTask | null>(null)
  const scrollAccumRef = useRef(0)
  const [pdfDoc, setPdfDoc] = useState<pdfjsLib.PDFDocumentProxy | null>(null)
  const [currentPage, setCurrentPage] = useState(1)
  const [totalPages, setTotalPages] = useState(0)
  const [scale, setScale] = useState(1.2)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [searchText, setSearchText] = useState('')
  const [searchResults, setSearchResults] = useState<number[]>([])
  const [currentSearchIndex, setCurrentSearchIndex] = useState(-1)

  // Update ref when scale changes
  useEffect(() => {
    scaleRef.current = scale
  }, [scale])

  // Load PDF document
  useEffect(() => {
    const loadPdf = async () => {
      try {
        setLoading(true)
        setError(null)

        const loadingTask = pdfjsLib.getDocument(url)
        const pdf = await loadingTask.promise
        setPdfDoc(pdf)
        setTotalPages(pdf.numPages)
        setLoading(false)
      } catch (err) {
        setError(err instanceof Error ? err.message : 'Failed to load PDF')
        setLoading(false)
      }
    }

    loadPdf()
  }, [url])

  // Render current page
  const renderPage = useCallback(async (pageNum: number) => {
    if (!pdfDoc || !canvasRef.current) return

    // Cancel any in-progress render to prevent canvas race conditions
    if (renderTaskRef.current) {
      try { renderTaskRef.current.cancel() } catch {}
      renderTaskRef.current = null
    }

    try {
      const page = await pdfDoc.getPage(pageNum)
      const currentScale = scaleRef.current
      const viewport = page.getViewport({ scale: currentScale })

      const canvas = canvasRef.current
      if (!canvas) return
      const context = canvas.getContext('2d')
      if (!context) return

      // Use devicePixelRatio for sharp rendering
      const dpr = window.devicePixelRatio || 1
      canvas.width = viewport.width * dpr
      canvas.height = viewport.height * dpr
      canvas.style.width = `${viewport.width}px`
      canvas.style.height = `${viewport.height}px`
      context.scale(dpr, dpr)

      const renderTask = page.render({
        canvasContext: context,
        viewport: viewport,
        canvas: canvas,
      })
      renderTaskRef.current = renderTask

      await renderTask.promise
      renderTaskRef.current = null

    } catch (err: any) {
      // RenderingCancelledException is expected when zooming quickly
      if (err?.name === 'RenderingCancelledException') return
      console.error('Error rendering page:', err)
    }
  }, [pdfDoc])

  useEffect(() => {
    if (pdfDoc) {
      renderPage(currentPage)
    }
  }, [pdfDoc, currentPage, scale, renderPage]) // Keep scale in dependencies to trigger re-render

  // Wheel-based page flip: accumulate deltaY and flip one page at a time
  useEffect(() => {
    const container = containerRef.current
    if (!container) return

    const handleWheel = (e: WheelEvent) => {
      e.preventDefault()

      scrollAccumRef.current += e.deltaY

      const threshold = 120

      if (scrollAccumRef.current > threshold) {
        scrollAccumRef.current = 0
        if (currentPage < totalPages) {
          setCurrentPage(prev => prev + 1)
          onPageChange?.(currentPage + 1)
        }
      } else if (scrollAccumRef.current < -threshold) {
        scrollAccumRef.current = 0
        if (currentPage > 1) {
          setCurrentPage(prev => prev - 1)
          onPageChange?.(currentPage - 1)
        }
      }
    }

    container.addEventListener('wheel', handleWheel, { passive: false })
    return () => container.removeEventListener('wheel', handleWheel)
  }, [currentPage, totalPages, onPageChange])

  // Page navigation
  const goToPage = (page: number) => {
    if (page >= 1 && page <= totalPages) {
      setCurrentPage(page)
      onPageChange?.(page)
      if (containerRef.current) {
        containerRef.current.scrollTop = 0
      }
    }
  }

  const goToPrevPage = () => goToPage(currentPage - 1)
  const goToNextPage = () => goToPage(currentPage + 1)

  // Zoom controls
  const zoomIn = () => setScale(Math.min(scale + 0.2, 3))
  const zoomOut = () => setScale(Math.max(scale - 0.2, 0.5))
  const resetZoom = () => setScale(1.2)

  // Search functionality
  const handleSearch = async () => {
    if (!searchText.trim() || !pdfDoc) return

    const results: number[] = []

    for (let i = 1; i <= totalPages; i++) {
      const page = await pdfDoc.getPage(i)
      const textContent = await page.getTextContent()
      const pageText = textContent.items
        .map((item: any) => item.str)
        .join(' ')

      if (pageText.toLowerCase().includes(searchText.toLowerCase())) {
        results.push(i)
      }
    }

    setSearchResults(results)
    if (results.length > 0) {
      setCurrentSearchIndex(0)
      goToPage(results[0])
    }
  }

  const goToNextSearchResult = () => {
    if (searchResults.length > 0) {
      const nextIndex = (currentSearchIndex + 1) % searchResults.length
      setCurrentSearchIndex(nextIndex)
      goToPage(searchResults[nextIndex])
    }
  }

  const goToPrevSearchResult = () => {
    if (searchResults.length > 0) {
      const prevIndex = (currentSearchIndex - 1 + searchResults.length) % searchResults.length
      setCurrentSearchIndex(prevIndex)
      goToPage(searchResults[prevIndex])
    }
  }

  if (loading) {
    return (
      <div className="flex items-center justify-center h-full bg-gray-50">
        <div className="text-center">
          <div className="animate-spin rounded-full h-12 w-12 border-b-2 border-blue-500 mx-auto"></div>
          <span className="mt-4 text-gray-600 block">Loading PDF...</span>
        </div>
      </div>
    )
  }

  if (error) {
    return (
      <div className="flex items-center justify-center h-full bg-gray-50">
        <div className="text-red-600 text-center">
          <p className="font-semibold">Error loading PDF</p>
          <p className="text-sm mt-2">{error}</p>
        </div>
      </div>
    )
  }

  return (
    <div className="flex flex-col h-full bg-gray-100">
      {/* Toolbar */}
      <div className="flex flex-wrap items-center gap-3 p-3 bg-white border-b shadow-sm">
        {/* Page navigation */}
        <div className="flex items-center gap-2">
          <button
            onClick={goToPrevPage}
            disabled={currentPage <= 1}
            className="px-3 py-1.5 bg-gray-100 hover:bg-gray-200 disabled:opacity-50 disabled:cursor-not-allowed rounded text-sm font-medium transition-colors"
          >
            ← Prev
          </button>
          <input
            type="number"
            min={1}
            max={totalPages}
            value={currentPage}
            onChange={(e) => goToPage(parseInt(e.target.value) || 1)}
            className="w-14 px-2 py-1 text-sm border rounded text-center"
          />
          <span className="text-sm text-gray-600">
            / {totalPages}
          </span>
          <button
            onClick={goToNextPage}
            disabled={currentPage >= totalPages}
            className="px-3 py-1.5 bg-gray-100 hover:bg-gray-200 disabled:opacity-50 disabled:cursor-not-allowed rounded text-sm font-medium transition-colors"
          >
            Next →
          </button>
        </div>

        <div className="border-l h-6 mx-1"></div>

        {/* Zoom controls */}
        <div className="flex items-center gap-1">
          <button
            onClick={zoomOut}
            disabled={scale <= 0.5}
            className="px-2.5 py-1.5 bg-gray-100 hover:bg-gray-200 disabled:opacity-50 rounded text-sm font-medium transition-colors"
          >
            −
          </button>
          <span className="text-sm text-gray-700 min-w-[50px] text-center font-medium">
            {Math.round(scale * 100)}%
          </span>
          <button
            onClick={zoomIn}
            disabled={scale >= 3}
            className="px-2.5 py-1.5 bg-gray-100 hover:bg-gray-200 disabled:opacity-50 rounded text-sm font-medium transition-colors"
          >
            +
          </button>
          <button
            onClick={resetZoom}
            className="px-2.5 py-1.5 bg-gray-100 hover:bg-gray-200 rounded text-sm font-medium transition-colors"
          >
            Fit
          </button>
        </div>

        <div className="border-l h-6 mx-1"></div>

        {/* Search */}
        <div className="flex items-center gap-2">
          <input
            type="text"
            placeholder="Search in PDF..."
            value={searchText}
            onChange={(e) => setSearchText(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && handleSearch()}
            className="w-40 px-3 py-1.5 text-sm border rounded focus:outline-none focus:ring-2 focus:ring-blue-300"
          />
          <button
            onClick={handleSearch}
            className="px-3 py-1.5 bg-blue-500 hover:bg-blue-600 text-white rounded text-sm font-medium transition-colors"
          >
            Find
          </button>
          {searchResults.length > 0 && (
            <div className="flex items-center gap-1">
              <span className="text-sm text-gray-600">
                {currentSearchIndex + 1}/{searchResults.length}
              </span>
              <button
                onClick={goToPrevSearchResult}
                className="px-2 py-1 bg-gray-100 hover:bg-gray-200 rounded text-sm transition-colors"
              >
                ↑
              </button>
              <button
                onClick={goToNextSearchResult}
                className="px-2 py-1 bg-gray-100 hover:bg-gray-200 rounded text-sm transition-colors"
              >
                ↓
              </button>
            </div>
          )}
        </div>
      </div>

      {/* PDF container */}
      <div
        ref={containerRef}
        className="flex-1 overflow-hidden flex justify-center bg-gray-300 p-6"
      >
        <canvas
          ref={canvasRef}
          className="shadow-xl bg-white"
        />
      </div>
    </div>
  )
}