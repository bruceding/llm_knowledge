import { useEffect, useRef, useState } from 'react'
import DocumentChatPanel from './DocumentChatPanel'

interface DocChatBottomSheetProps {
  docId: number
  open: boolean
  onClose: () => void
  onNoteSaved?: () => void
}

export default function DocChatBottomSheet({ docId, open, onClose, onNoteSaved }: DocChatBottomSheetProps) {
  const [dragOffset, setDragOffset] = useState(0)
  const startYRef = useRef<number | null>(null)

  useEffect(() => {
    if (!open) return
    const prev = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    return () => { document.body.style.overflow = prev }
  }, [open])

  const onTouchStart = (e: React.TouchEvent) => {
    startYRef.current = e.touches[0].clientY
  }
  const onTouchMove = (e: React.TouchEvent) => {
    if (startYRef.current == null) return
    const dy = e.touches[0].clientY - startYRef.current
    if (dy > 0) setDragOffset(dy)
  }
  const onTouchEnd = () => {
    if (dragOffset > 120) onClose()
    setDragOffset(0)
    startYRef.current = null
  }

  return (
    <>
      <div
        className={`fixed inset-0 z-40 bg-black/40 transition-opacity ${
          open ? 'opacity-100 pointer-events-auto' : 'opacity-0 pointer-events-none'
        }`}
        onClick={onClose}
        aria-hidden="true"
      />

      <div
        role="dialog"
        aria-label="document chat"
        className={`fixed bottom-0 inset-x-0 z-50 bg-white rounded-t-2xl shadow-2xl
                    flex flex-col transition-transform duration-200
                    ${open ? 'translate-y-0' : 'translate-y-full'}`}
        style={{
          height: '70dvh',
          transform: open && dragOffset > 0 ? `translateY(${dragOffset}px)` : undefined,
        }}
      >
        <div
          className="py-2 flex justify-center cursor-grab touch-pan-y"
          onTouchStart={onTouchStart}
          onTouchMove={onTouchMove}
          onTouchEnd={onTouchEnd}
        >
          <div className="w-10 h-1 rounded-full bg-gray-300" />
        </div>

        <div className="flex-1 overflow-hidden">
          <DocumentChatPanel docId={docId} active={open} onNoteSaved={onNoteSaved} />
        </div>
      </div>
    </>
  )
}
