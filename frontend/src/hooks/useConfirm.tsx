import { useCallback, useRef, useState } from 'react'
import ConfirmDialog from '../components/ConfirmDialog'

interface ConfirmOptions {
  title: string
  message: string
  confirmLabel?: string
  cancelLabel?: string
  variant?: 'danger' | 'warning'
}

export function useConfirm() {
  const [state, setState] = useState<{
    open: boolean
    options: ConfirmOptions | null
    resolver: ((value: boolean) => void) | null
  }>({ open: false, options: null, resolver: null })

  const resolverRef = useRef<((value: boolean) => void) | null>(null)
  resolverRef.current = state.resolver

  const confirm = useCallback((options: ConfirmOptions): Promise<boolean> => {
    return new Promise((resolve) => {
      resolverRef.current = resolve
      setState({ open: true, options, resolver: resolve })
    })
  }, [])

  const handleConfirm = useCallback(() => {
    resolverRef.current?.(true)
    setState({ open: false, options: null, resolver: null })
  }, [])

  const handleCancel = useCallback(() => {
    resolverRef.current?.(false)
    setState({ open: false, options: null, resolver: null })
  }, [])

  const dialog = state.options && state.open ? (
    <ConfirmDialog
      open={state.open}
      title={state.options.title}
      message={state.options.message}
      confirmLabel={state.options.confirmLabel}
      cancelLabel={state.options.cancelLabel}
      variant={state.options.variant}
      onConfirm={handleConfirm}
      onCancel={handleCancel}
    />
  ) : null

  return { confirm, dialog }
}
