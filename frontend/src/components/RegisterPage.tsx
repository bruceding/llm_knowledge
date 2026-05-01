import { useState, useEffect } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { getCaptcha, register } from '../api'
import type { CaptchaResponse } from '../types'

export default function RegisterPage() {
  const { t } = useTranslation()
  const navigate = useNavigate()

  const [username, setUsername] = useState('')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [captchaAnswer, setCaptchaAnswer] = useState('')
  const [captcha, setCaptcha] = useState<CaptchaResponse | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    refreshCaptcha()
  }, [])

  const refreshCaptcha = async () => {
    try {
      const data = await getCaptcha()
      setCaptcha(data)
    } catch {
      setError(t('auth.captchaError'))
    }
  }

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setError('')
    setLoading(true)

    // Client-side validation
    if (username.length < 3 || username.length > 20) {
      setError(t('auth.usernameHint'))
      setLoading(false)
      return
    }

    if (password.length < 6 || password.length > 32) {
      setError(t('auth.passwordLengthHint'))
      setLoading(false)
      return
    }

    if (!/[a-zA-Z]/.test(password)) {
      setError(t('auth.passwordLetterHint'))
      setLoading(false)
      return
    }

    if (!/[0-9]/.test(password)) {
      setError(t('auth.passwordDigitHint'))
      setLoading(false)
      return
    }

    if (password !== confirmPassword) {
      setError(t('auth.passwordMismatch'))
      setLoading(false)
      return
    }

    if (!captcha) {
      setError(t('auth.captchaRequired'))
      setLoading(false)
      return
    }

    if (!captchaAnswer.trim()) {
      setError(t('auth.captchaRequired'))
      setLoading(false)
      return
    }

    try {
      await register(username, password, email, captcha.captchaKey, captchaAnswer)
      navigate('/login', { state: { registered: true } })
    } catch (err) {
      const message = err instanceof Error ? err.message : t('auth.registerFailed')
      setError(message)
      refreshCaptcha()
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="min-h-screen flex items-center justify-center bg-gray-50">
      <div className="max-w-md w-full bg-white rounded-lg shadow-md p-8">
        <h1 className="text-2xl font-bold text-center text-gray-900 mb-6">
          {t('auth.register')}
        </h1>

        {error && (
          <div className="mb-4 p-3 bg-red-50 border border-red-200 text-red-700 rounded-md text-sm">
            {error}
          </div>
        )}

        <form onSubmit={handleSubmit} className="space-y-4">
          {/* Username */}
          <div>
            <label htmlFor="username" className="block text-sm font-medium text-gray-700 mb-1">
              {t('auth.username')}
            </label>
            <input
              id="username"
              type="text"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              placeholder={t('auth.usernamePlaceholder')}
              className="w-full px-3 py-2 border border-gray-300 rounded-md focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
              required
            />
            <p className="mt-1 text-xs text-gray-500">{t('auth.usernameHint')}</p>
          </div>

          {/* Email */}
          <div>
            <label htmlFor="email" className="block text-sm font-medium text-gray-700 mb-1">
              {t('auth.email')}
            </label>
            <input
              id="email"
              type="email"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              placeholder={t('auth.emailPlaceholder')}
              className="w-full px-3 py-2 border border-gray-300 rounded-md focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
              required
            />
          </div>

          {/* Password */}
          <div>
            <label htmlFor="password" className="block text-sm font-medium text-gray-700 mb-1">
              {t('auth.password')}
            </label>
            <input
              id="password"
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder={t('auth.passwordPlaceholder')}
              className="w-full px-3 py-2 border border-gray-300 rounded-md focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
              required
            />
            <p className="mt-1 text-xs text-gray-500">{t('auth.passwordHint')}</p>
          </div>

          {/* Confirm Password */}
          <div>
            <label htmlFor="confirmPassword" className="block text-sm font-medium text-gray-700 mb-1">
              {t('auth.confirmPassword')}
            </label>
            <input
              id="confirmPassword"
              type="password"
              value={confirmPassword}
              onChange={(e) => setConfirmPassword(e.target.value)}
              placeholder={t('auth.confirmPasswordPlaceholder')}
              className="w-full px-3 py-2 border border-gray-300 rounded-md focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
              required
            />
          </div>

          {/* Captcha */}
          <div>
            <label htmlFor="captcha" className="block text-sm font-medium text-gray-700 mb-1">
              {t('auth.captcha')}
            </label>
            <div className="flex items-center gap-3">
              {captcha && (
                <img
                  src={captcha.captchaImage}
                  alt="Captcha"
                  className="h-10 rounded border border-gray-200"
                />
              )}
              <button
                type="button"
                onClick={refreshCaptcha}
                className="text-sm text-blue-600 hover:text-blue-800"
              >
                {t('auth.refreshCaptcha')}
              </button>
            </div>
            <input
              id="captcha"
              type="text"
              value={captchaAnswer}
              onChange={(e) => setCaptchaAnswer(e.target.value)}
              placeholder={t('auth.captchaPlaceholder')}
              className="w-full px-3 py-2 border border-gray-300 rounded-md focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
              required
            />
          </div>

          {/* Submit Button */}
          <button
            type="submit"
            disabled={loading}
            className="w-full py-2 px-4 bg-blue-600 text-white rounded-md hover:bg-blue-700 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:ring-offset-2 disabled:opacity-50 disabled:cursor-not-allowed"
          >
            {loading ? t('auth.registering') : t('auth.registerButton')}
          </button>
        </form>

        {/* Login Link */}
        <p className="mt-4 text-center text-sm text-gray-600">
          {t('auth.haveAccount')}{' '}
          <Link to="/login" className="text-blue-600 hover:text-blue-800 font-medium">
            {t('auth.loginLink')}
          </Link>
        </p>
      </div>
    </div>
  )
}