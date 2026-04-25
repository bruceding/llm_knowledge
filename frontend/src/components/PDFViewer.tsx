import { useEffect, useRef, useState, useCallback } from 'react'
import * as pdfjsLib from 'pdfjs-dist'
import * as pdfjsViewer from 'pdfjs-dist/web/pdf_viewer.mjs'
import 'pdfjs-dist/web/pdf_viewer.css'

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
  const viewerRef = useRef<HTMLDivElement>(null)
  const pdfViewerRef = useRef<pdfjsViewer.PDFViewer | null>(null)
  const eventBusRef = useRef<pdfjsViewer.EventBus | null>(null)
  const pdfLinkServiceRef = useRef<pdfjsViewer.PDFLinkService | null>(null)
  const pdfFindControllerRef = useRef<pdfjsViewer.PDFFindController | null>(null)
  const initializedRef = useRef(false)

  const [currentPage, setCurrentPage] = useState(1)
  const [totalPages, setTotalPages] = useState(0)
  const [scale, setScale] = useState(1)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [searchText, setSearchText] = useState('')
  const [searchMatchCount, setSearchMatchCount] = useState(0)
  const [currentMatchIndex, setCurrentMatchIndex] = useState(0)

  // Initialize PDFViewer infrastructure - runs once when refs are ready
  const initializeViewer = useCallback(() => {
    if (!containerRef.current || !viewerRef.current || initializedRef.current) return

    // Create EventBus
    const eventBus = new pdfjsViewer.EventBus()
    eventBusRef.current = eventBus

    // Create PDFLinkService
    const pdfLinkService = new pdfjsViewer.PDFLinkService({
      eventBus,
      externalLinkTarget: pdfjsViewer.LinkTarget.BLANK,
    })
    pdfLinkServiceRef.current = pdfLinkService

    // Create PDFFindController
    const pdfFindController = new pdfjsViewer.PDFFindController({
      eventBus,
      linkService: pdfLinkService,
    })
    pdfFindControllerRef.current = pdfFindController

    // Create PDFViewer with text layer enabled
    const pdfViewer = new pdfjsViewer.PDFViewer({
      container: containerRef.current,
      viewer: viewerRef.current,
      eventBus,
      linkService: pdfLinkService,
      findController: pdfFindController,
      textLayerMode: 1, // ENABLE - for text selection
      removePageBorders: false,
    })
    pdfViewerRef.current = pdfViewer

    // Set viewer reference in linkService
    pdfLinkService.setViewer(pdfViewer)

    // Event listeners
    const onPageChanging = (evt: { pageNumber: number }) => {
      setCurrentPage(evt.pageNumber)
      onPageChange?.(evt.pageNumber)
    }

    const onScaleChanging = (evt: { scale: number }) => {
      setScale(evt.scale)
    }

    const onUpdateFindMatchesCount = (evt: { matchesCount: { total: number; current: number } }) => {
      setSearchMatchCount(evt.matchesCount.total)
      setCurrentMatchIndex(evt.matchesCount.current)
    }

    eventBus.on('pagechanging', onPageChanging)
    eventBus.on('scalechanging', onScaleChanging)
    eventBus.on('updatefindmatchescount', onUpdateFindMatchesCount)
    eventBus.on('pagesinit', () => {
      // Set initial scale when pages are initialized
      if (pdfViewerRef.current) {
        pdfViewerRef.current.currentScaleValue = 'page-width'
      }
    })

    initializedRef.current = true
  }, [onPageChange])

  // Try to initialize on mount and when refs become available
  useEffect(() => {
    // Use a small timeout to ensure refs are attached
    const timer = setTimeout(() => {
      initializeViewer()
    }, 0)
    return () => clearTimeout(timer)
  }, [initializeViewer])

  // Load PDF document
  useEffect(() => {
    const loadPdf = async () => {
      if (!initializedRef.current) {
        // Wait for initialization
        await new Promise(resolve => setTimeout(resolve, 100))
      }

      try {
        setLoading(true)
        setError(null)

        const loadingTask = pdfjsLib.getDocument(url)
        const pdf = await loadingTask.promise
        setTotalPages(pdf.numPages)

        // Set document to viewer
        if (pdfViewerRef.current && pdfLinkServiceRef.current && pdfFindControllerRef.current) {
          pdfViewerRef.current.setDocument(pdf)
          pdfLinkServiceRef.current.setDocument(pdf, null)
          pdfFindControllerRef.current.setDocument(pdf)

          // Set initial scale to fit page width after document is loaded
          pdfViewerRef.current.currentScaleValue = 'page-width'
        }

        setLoading(false)
      } catch (err) {
        setError(err instanceof Error ? err.message : 'Failed to load PDF')
        setLoading(false)
      }
    }

    loadPdf()
  }, [url])

  // Page navigation
  const goToPage = (page: number) => {
    if (pdfViewerRef.current && page >= 1 && page <= totalPages) {
      pdfViewerRef.current.currentPageNumber = page
    }
  }

  const goToPrevPage = () => {
    pdfViewerRef.current?.previousPage()
  }

  const goToNextPage = () => {
    pdfViewerRef.current?.nextPage()
  }

  // Zoom controls
  const zoomIn = () => {
    pdfViewerRef.current?.increaseScale()
  }

  const zoomOut = () => {
    pdfViewerRef.current?.decreaseScale()
  }

  const resetZoom = () => {
    if (pdfViewerRef.current) {
      pdfViewerRef.current.currentScaleValue = 'page-width'
    }
  }

  // Search using EventBus
  const handleSearch = () => {
    if (!searchText.trim() || !eventBusRef.current) return

    eventBusRef.current.dispatch('find', {
      type: '',
      query: searchText,
      caseSensitive: false,
      highlightAll: true,
      findPrevious: false,
    })
  }

  const goToNextSearchResult = () => {
    eventBusRef.current?.dispatch('findagain', {
      type: '',
      query: searchText,
      caseSensitive: false,
      highlightAll: true,
      findPrevious: false,
    })
  }

  const goToPrevSearchResult = () => {
    eventBusRef.current?.dispatch('findagain', {
      type: '',
      query: searchText,
      caseSensitive: false,
      highlightAll: true,
      findPrevious: true,
    })
  }

  return (
    <div className="flex flex-col h-full bg-gray-100 relative">
      {/* Toolbar */}
      <div className="flex flex-wrap items-center gap-3 p-3 bg-white border-b shadow-sm z-20 relative">
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
          {searchMatchCount > 0 && (
            <div className="flex items-center gap-1">
              <span className="text-sm text-gray-600">
                {currentMatchIndex + 1}/{searchMatchCount}
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

      {/* PDF container - always rendered so refs are available */}
      <div
        ref={containerRef}
        className="flex-1 overflow-auto bg-gray-300 absolute inset-0"
        style={{ position: 'absolute' }}
      >
        {loading && (
          <div className="absolute inset-0 flex items-center justify-center bg-gray-50 z-10">
            <div className="text-center">
              <div className="animate-spin rounded-full h-12 w-12 border-b-2 border-blue-500 mx-auto"></div>
              <span className="mt-4 text-gray-600 block">Loading PDF...</span>
            </div>
          </div>
        )}
        {error && (
          <div className="absolute inset-0 flex items-center justify-center bg-gray-50 z-10">
            <div className="text-red-600 text-center">
              <p className="font-semibold">Error loading PDF</p>
              <p className="text-sm mt-2">{error}</p>
            </div>
          </div>
        )}
        <div
          ref={viewerRef}
          className="pdfViewer"
        />
      </div>
    </div>
  )
}