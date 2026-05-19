// frontend/src/components/Layout/MobileDrawer.tsx
import { useEffect } from 'react'
import { useMobileShell } from './MobileShellStore'
import SidebarContent from './SidebarContent'

export default function MobileDrawer() {
  const { drawerOpen, setDrawerOpen } = useMobileShell()

  useEffect(() => {
    if (!drawerOpen) return
    const prev = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    return () => { document.body.style.overflow = prev }
  }, [drawerOpen])

  return (
    <>
      <div
        className={`fixed inset-0 z-40 bg-black/40 transition-opacity ${
          drawerOpen ? 'opacity-100 pointer-events-auto' : 'opacity-0 pointer-events-none'
        }`}
        onClick={() => setDrawerOpen(false)}
        aria-hidden="true"
      />

      <aside
        className={`fixed top-0 left-0 z-50 h-[100dvh] w-72 bg-gray-50 border-r border-gray-200
                    flex flex-col transition-transform duration-200
                    ${drawerOpen ? 'translate-x-0' : '-translate-x-full'}`}
        aria-label="navigation drawer"
      >
        <SidebarContent
          onNavigate={() => setDrawerOpen(false)}
          hideImport
        />
      </aside>
    </>
  )
}
