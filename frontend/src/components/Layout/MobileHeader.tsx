// frontend/src/components/Layout/MobileHeader.tsx
import { useMobileShell } from './MobileShellStore'

export default function MobileHeader() {
  const { title, leftSlot, rightSlot, setDrawerOpen } = useMobileShell()

  return (
    <header className="sticky top-0 z-30 h-12 flex items-center px-3 gap-2 bg-white border-b border-gray-200">
      <div className="w-8 flex items-center justify-center">
        {leftSlot ?? (
          <button
            onClick={() => setDrawerOpen(true)}
            aria-label="open menu"
            className="p-1 -ml-1 text-gray-700 hover:bg-gray-100 rounded"
          >
            <svg className="w-6 h-6" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M4 6h16M4 12h16M4 18h16" />
            </svg>
          </button>
        )}
      </div>
      <h1 className="flex-1 text-base font-medium text-gray-800 truncate">{title}</h1>
      <div className="flex items-center gap-1">{rightSlot}</div>
    </header>
  )
}
