import { create } from 'zustand'
import type { ReactNode } from 'react'

interface MobileShellState {
  drawerOpen: boolean
  setDrawerOpen: (open: boolean) => void
  title: string
  setTitle: (title: string) => void
  rightSlot: ReactNode | null
  setRightSlot: (slot: ReactNode | null) => void
  leftSlot: ReactNode | null  // 详情页用："← 返回"按钮替代 ☰
  setLeftSlot: (slot: ReactNode | null) => void
}

export const useMobileShell = create<MobileShellState>((set) => ({
  drawerOpen: false,
  setDrawerOpen: (drawerOpen) => set({ drawerOpen }),
  title: '',
  setTitle: (title) => set({ title }),
  rightSlot: null,
  setRightSlot: (rightSlot) => set({ rightSlot }),
  leftSlot: null,
  setLeftSlot: (leftSlot) => set({ leftSlot }),
}))
