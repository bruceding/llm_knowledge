import { useState, useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { getCaptcha, login } from '../api'
import { useAuthStore } from '../store/authStore'

export default function LoginPage() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const setAuth = useAuthStore((s) => s.setAuth)

  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [captchaKey, setCaptchaKey] = useState('')
  const [captchaAnswer, setCaptchaAnswer] = useState('')
  const [captchaImage, setCaptchaImage] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    refreshCaptcha()
  }, [])

  async function refreshCaptcha() {
    try {
      const data = await getCaptcha()
      setCaptchaKey(data.captchaKey)
      setCaptchaImage(data.captchaImage)
    } catch (e) {
      setError(t('auth.captchaError'))
    }
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    setError('')
    setLoading(true)

    try {
      const data = await login(username, password, captchaKey, captchaAnswer)
      setAuth(data.token, data.userId, data.username, data.mustChangePassword)

      if (data.mustChangePassword) {
        navigate('/change-password')
      } else {
        navigate('/')
      }
    } catch (e: any) {
      setError(e.message)
      refreshCaptcha()
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="min-h-screen flex items-center justify-center bg-gray-50">
      <div className="max-w-md w-full p-8 bg-white rounded-lg shadow-md">
        <h2 className="text-2xl font-bold text-center mb-6">{t('auth.login')}</h2>

        {error && (
          <div className="mb-4 p-3 bg-red-100 text-red-700 rounded">{error}</div>
        )}

        <form onSubmit={handleSubmit}>
          <div className="mb-4">
            <label className="block text-sm font-medium mb-2">{t('auth.username')}</label>
            <input
              type="text"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              placeholder={t('auth.usernamePlaceholder')}
              className="w-full p-2 border rounded focus:outline-none focus:ring-2 focus:ring-blue-500"
              required
            />
          </div>

          <div className="mb-4">
            <label className="block text-sm font-medium mb-2">{t('auth.password')}</label>
            <input
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder={t('auth.passwordPlaceholder')}
              className="w-full p-2 border rounded focus:outline-none focus:ring-2 focus:ring-blue-500"
              required
            />
          </div>

          <div className="mb-4">
            <label className="block text-sm font-medium mb-2">{t('auth.captcha')}</label>
            <div className="flex gap-2">
              <input
                type="text"
                value={captchaAnswer}
                onChange={(e) => setCaptchaAnswer(e.target.value.toUpperCase())}
                placeholder={t('auth.captchaPlaceholder')}
                className="flex-1 p-2 border rounded focus:outline-none focus:ring-2 focus:ring-blue-500"
                maxLength={4}
                required
              />
              <img
                src={captchaImage}
                alt="captcha"
                onClick={refreshCaptcha}
                className="h-10 cursor-pointer border rounded"
                title={t('auth.refreshCaptcha')}
              />
            </div>
          </div>

          <button
            type="submit"
            disabled={loading}
            className="w-full py-2 bg-blue-600 text-white rounded hover:bg-blue-700 disabled:opacity-50"
          >
            {loading ? t('auth.loggingIn') : t('auth.loginButton')}
          </button>
        </form>

        <p className="mt-4 text-center text-sm">
          <a href="/register" className="text-blue-600 hover:underline">
            {t('auth.noAccount')} {t('auth.registerLink')}
          </a>
        </p>
      </div>
    </div>
  )
}