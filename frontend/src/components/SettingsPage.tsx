import { useState, useEffect } from 'react'
import { useTranslation } from 'react-i18next'
import i18n from '../i18n'
import { fetchSettings, updateSettings } from '../api'

export default function SettingsPage() {
  const { t } = useTranslation()
  const [currentLang, setCurrentLang] = useState<'en' | 'zh'>('en')
  const [translationEnabled, setTranslationEnabled] = useState(false)
  const [apiBase, setApiBase] = useState('')
  const [apiKey, setApiKey] = useState('')
  const [modelName, setModelName] = useState('')
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [success, setSuccess] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    loadSettings()
  }, [])

  const loadSettings = async () => {
    setLoading(true)
    setError(null)
    try {
      const s = await fetchSettings()
      setCurrentLang(s.language)
      setTranslationEnabled(s.translationEnabled)
      setApiBase(s.translationApiBase || 'https://dashscope.aliyuncs.com/compatible-mode/v1')
      setApiKey(s.translationApiKey || '')
      setModelName(s.translationModel || 'deepseek-v4-flash')
      i18n.changeLanguage(s.language)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load settings')
    } finally {
      setLoading(false)
    }
  }

  const handleLanguageSelect = (lang: 'en' | 'zh') => {
    setCurrentLang(lang)
  }

  const handleSave = async () => {
    setSaving(true)
    setSuccess(false)
    setError(null)
    try {
      await updateSettings({
        language: currentLang,
        translationEnabled,
        translationApiBase: apiBase,
        translationApiKey: apiKey,
        translationModel: modelName,
      })
      i18n.changeLanguage(currentLang)
      setSuccess(true)
      setTimeout(() => setSuccess(false), 3000)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save settings')
    } finally {
      setSaving(false)
    }
  }

  if (loading) {
    return (
      <div className="p-6">
        <h2 className="text-2xl font-bold text-gray-800 mb-4">{t('settings.title')}</h2>
        <div className="flex items-center justify-center py-12">
          <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-blue-500"></div>
        </div>
      </div>
    )
  }

  return (
    <div className="p-6 max-w-2xl">
      <h2 className="text-2xl font-bold text-gray-800 mb-6">{t('settings.title')}</h2>

      {/* Language Section */}
      <div className="bg-white border border-gray-200 rounded-lg p-6 mb-6">
        <h3 className="text-lg font-medium text-gray-800 mb-2">{t('settings.language')}</h3>
        <p className="text-sm text-gray-600 mb-4">{t('settings.languageHint')}</p>

        <div className="flex gap-3">
          <button
            onClick={() => handleLanguageSelect('en')}
            className={`px-4 py-2 rounded-lg border transition-colors ${
              currentLang === 'en'
                ? 'bg-blue-100 border-blue-500 text-blue-700 font-medium'
                : 'border-gray-300 text-gray-700 hover:bg-gray-50'
            }`}
          >
            {t('settings.english')}
          </button>
          <button
            onClick={() => handleLanguageSelect('zh')}
            className={`px-4 py-2 rounded-lg border transition-colors ${
              currentLang === 'zh'
                ? 'bg-blue-100 border-blue-500 text-blue-700 font-medium'
                : 'border-gray-300 text-gray-700 hover:bg-gray-50'
            }`}
          >
            {t('settings.chinese')}
          </button>
        </div>
      </div>

      {/* PDF Translation Section */}
      <div className="bg-white border border-gray-200 rounded-lg p-6 mb-6">
        <h3 className="text-lg font-medium text-gray-800 mb-2">{t('settings.pdfTranslation')}</h3>
        <p className="text-sm text-gray-600 mb-4">{t('settings.pdfTranslationHint')}</p>

        {/* Enable toggle */}
        <div className="flex items-center gap-3 mb-4">
          <label className="relative inline-flex items-center cursor-pointer">
            <input
              type="checkbox"
              checked={translationEnabled}
              onChange={(e) => setTranslationEnabled(e.target.checked)}
              className="sr-only peer"
            />
            <div className="w-11 h-6 bg-gray-200 peer-focus:outline-none peer-focus:ring-4 peer-focus:ring-blue-300 rounded-full peer peer-checked:after:translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-white after:border-gray-300 after:border after:rounded-full after:h-5 after:w-5 after:transition-all peer-checked:bg-blue-500"></div>
          </label>
          <span className="text-sm text-gray-700">{t('settings.enableTranslation')}</span>
        </div>

        {/* API Configuration - only show when enabled */}
        {translationEnabled && (
          <div className="space-y-4 mt-4 p-4 bg-gray-50 rounded-lg">
            {/* API Base URL */}
            <div>
              <label className="block text-xs font-medium text-gray-500 mb-1">
                {t('settings.apiBaseUrl')}
              </label>
              <input
                type="text"
                value={apiBase}
                onChange={(e) => setApiBase(e.target.value)}
                placeholder="https://dashscope.aliyuncs.com/compatible-mode/v1"
                className="w-full px-3 py-2 text-sm border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
              />
            </div>

            {/* API Key */}
            <div>
              <label className="block text-xs font-medium text-gray-500 mb-1">
                {t('settings.apiKey')}
              </label>
              <input
                type="password"
                value={apiKey}
                onChange={(e) => setApiKey(e.target.value)}
                placeholder="sk-..."
                className="w-full px-3 py-2 text-sm border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
              />
              <p className="text-xs text-gray-500 mt-1">{t('settings.apiKeyWarning')}</p>
            </div>

            {/* Model Name */}
            <div>
              <label className="block text-xs font-medium text-gray-500 mb-1">
                {t('settings.modelName')}
              </label>
              <input
                type="text"
                value={modelName}
                onChange={(e) => setModelName(e.target.value)}
                placeholder="deepseek-v4-flash"
                className="w-full px-3 py-2 text-sm border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
              />
            </div>
          </div>
        )}
      </div>

      {/* Messages */}
      {success && (
        <div className="mb-4 p-3 bg-green-50 border border-green-200 rounded-lg text-green-700">
          {t('settings.saved')}
        </div>
      )}

      {error && (
        <div className="mb-4 p-3 bg-red-50 border border-red-200 rounded-lg text-red-700">
          {t('common.error')}: {error}
        </div>
      )}

      {/* Save Button */}
      <button
        onClick={handleSave}
        disabled={saving}
        className={`px-6 py-2 rounded-lg font-medium transition-colors ${
          saving
            ? 'bg-gray-300 text-gray-500 cursor-not-allowed'
            : 'bg-blue-500 text-white hover:bg-blue-600'
        }`}
      >
        {saving ? t('common.loading') : t('common.save')}
      </button>
    </div>
  )
}