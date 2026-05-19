import { useSyncExternalStore } from 'react'

const QUERY = '(max-width: 767px)' // 与 Tailwind md (>=768) 对齐：< md = mobile

function subscribe(callback: () => void) {
  const mql = window.matchMedia(QUERY)
  mql.addEventListener('change', callback)
  return () => mql.removeEventListener('change', callback)
}

function getSnapshot(): boolean {
  return window.matchMedia(QUERY).matches
}

function getServerSnapshot(): boolean {
  return false // 默认按桌面渲染（项目无 SSR，但保留兜底）
}

export function useIsMobile(): boolean {
  return useSyncExternalStore(subscribe, getSnapshot, getServerSnapshot)
}
