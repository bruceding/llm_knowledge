import { useState, useCallback } from 'react'
import PDFViewer from './PDFViewer'

interface DualPDFViewerProps {
  originalUrl: string
  translatedUrl: string
}

export default function DualPDFViewer({ originalUrl, translatedUrl }: DualPDFViewerProps) {
  const [scrollPosition, setScrollPosition] = useState<number | undefined>()
  const [scale, setScale] = useState<number | undefined>()

  // Handle scroll from either viewer
  const handleScrollChange = useCallback((position: number) => {
    setScrollPosition(position)
  }, [])

  // Handle scale change from either viewer
  const handleScaleChange = useCallback((newScale: number) => {
    setScale(newScale)
  }, [])

  return (
    <div className="flex h-full gap-1">
      {/* Original PDF */}
      <div className="flex-1 flex flex-col border-r border-gray-200">
        <div className="p-2 bg-gray-100 text-sm font-medium text-gray-700 border-b border-gray-200">
          Original
        </div>
        <div className="flex-1 overflow-hidden">
          <PDFViewer
            url={originalUrl}
            syncEnabled={true}
            onScrollPositionChange={handleScrollChange}
            scrollPosition={scrollPosition}
            onScaleChange={handleScaleChange}
            externalScale={scale}
          />
        </div>
      </div>

      {/* Translated PDF */}
      <div className="flex-1 flex flex-col">
        <div className="p-2 bg-purple-100 text-sm font-medium text-purple-700 border-b border-purple-200">
          Translation
        </div>
        <div className="flex-1 overflow-hidden">
          <PDFViewer
            url={translatedUrl}
            syncEnabled={true}
            onScrollPositionChange={handleScrollChange}
            scrollPosition={scrollPosition}
            onScaleChange={handleScaleChange}
            externalScale={scale}
          />
        </div>
      </div>
    </div>
  )
}