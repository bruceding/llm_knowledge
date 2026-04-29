import { useEffect, useRef } from 'react'
import { useTranslation } from 'react-i18next'

interface ConfirmDialogProps {
  open: boolean
  title: string
  message: string
  confirmLabel?: string
  cancelLabel?: string
  variant?: 'danger' | 'warning'
  onConfirm: () => void
  onCancel: () => void
}

export default function ConfirmDialog({
  open,
  title,
  message,
  confirmLabel,
  cancelLabel,
  variant = 'danger',
  onConfirm,
  onCancel,
}: ConfirmDialogProps) {
  const { t } = useTranslation()
  const confirmRef = useRef<HTMLButtonElement>(null)

  useEffect(() => {
    if (open) {
      confirmRef.current?.focus()
    }
  }, [open])

  useEffect(() => {
    if (!open) return
    const handleKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onCancel()
    }
    window.addEventListener('keydown', handleKey)
    return () => window.removeEventListener('keydown', handleKey)
  }, [open, onCancel])

  if (!open) return null

  const btnConfirm = confirmLabel ?? t('common.delete')
  const btnCancel = cancelLabel ?? t('common.cancel')

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      {/* Backdrop */}
      <div
        className="absolute inset-0 bg-black/40 backdrop-blur-sm transition-opacity"
        onClick={onCancel}
      />

      {/* Dialog */}
      <div className="relative w-full max-w-md mx-4 bg-white rounded-xl shadow-2xl border border-gray-200 overflow-hidden animate-in">
        <div className="p-6">
          {/* Icon */}
          <div className={`mx-auto flex h-12 w-12 items-center justify-center rounded-full mb-4 ${
            variant === 'danger' ? 'bg-red-100' : 'bg-amber-100'
          }`}>
            <svg className={`w-6 h-6 ${variant === 'danger' ? 'text-red-600' : 'text-amber-600'}`} fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round">
              <path d="M12 9v3.75m-9.303 3.376c-.866 1.5.217 3.374 1.948 3.374h14.71c1.73 0 2.813-1.874 1.948-3.374L13.949 3.378c-.866-1.5-3.032-1.5-3.898 0L2.697 16.126zM12 15.75h.007v.008H12v-.008z" />
            </svg>
          </div>

          {/* Title */}
          <h3 className="text-lg font-semibold text-gray-900 text-center mb-2">
            {title}
          </h3>

          {/* Message */}
          <p className="text-sm text-gray-500 text-center leading-relaxed">
            {message}
          </p>
        </div>

        {/* Actions */}
        <div className="flex border-t border-gray-100">
          <button
            onClick={onCancel}
            className="flex-1 px-4 py-3 text-sm font-medium text-gray-700 hover:bg-gray-50 transition-colors focus:outline-none"
          >
            {btnCancel}
          </button>
          <div className="w-px bg-gray-100" />
          <button
            ref={confirmRef}
            onClick={onConfirm}
            className={`flex-1 px-4 py-3 text-sm font-medium transition-colors focus:outline-none ${
              variant === 'danger'
                ? 'text-red-600 hover:bg-red-50'
                : 'text-amber-600 hover:bg-amber-50'
            }`}
          >
            {btnConfirm}
          </button>
        </div>
      </div>
    </div>
  )
}
