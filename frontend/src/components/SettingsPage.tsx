import { useState, useEffect } from 'react'
import { useTranslation } from 'react-i18next'
import i18n from '../i18n'
import { fetchSettings, updateSettings, fetchGlobalSettings, updateGlobalSettings, getIMAPConfig, updateIMAPConfig, deleteIMAPConfig, testIMAPConnection, listIMAPFolders } from '../api'
import { useMobileShell } from './Layout/MobileShellStore'

export default function SettingsPage() {
  const { t } = useTranslation()
  const setTitle = useMobileShell((s) => s.setTitle)
  useEffect(() => {
    setTitle(t('sidebar.settings'))
    return () => setTitle('')
  }, [t, setTitle])
  const [currentLang, setCurrentLang] = useState<'en' | 'zh'>('en')
  const [isAdmin, setIsAdmin] = useState(false)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [success, setSuccess] = useState(false)
  const [error, setError] = useState<string | null>(null)

  // Global translation settings (admin only)
  const [translationEnabled, setTranslationEnabled] = useState(false)
  const [apiBase, setApiBase] = useState('https://dashscope.aliyuncs.com/compatible-mode/v1')
  const [apiKey, setApiKey] = useState('')
  const [modelName, setModelName] = useState('deepseek-v4-flash')
  const [globalSaving, setGlobalSaving] = useState(false)
  const [globalSuccess, setGlobalSuccess] = useState(false)
  const [globalError, setGlobalError] = useState<string | null>(null)

  // IMAP state
  const [imapHost, setImapHost] = useState('')
  const [imapPort, setImapPort] = useState(993)
  const [imapUsername, setImapUsername] = useState('')
  const [imapPassword, setImapPassword] = useState('')
  const [imapFolder, setImapFolder] = useState('Newsletter')
  const [imapAutoSync, setImapAutoSync] = useState(false)
  const [imapConfigured, setImapConfigured] = useState(false)
  const [imapSaving, setImapSaving] = useState(false)
  const [imapTesting, setImapTesting] = useState(false)
  const [imapTestResult, setImapTestResult] = useState<string | null>(null)
  const [imapTestSuccess, setImapTestSuccess] = useState(false)
  const [imapSuccess, setImapSuccess] = useState(false)
  const [imapError, setImapError] = useState<string | null>(null)
  const [imapFolders, setImapFolders] = useState<string[]>([])
  const [imapFoldersLoading, setImapFoldersLoading] = useState(false)
  const [showFolderDropdown, setShowFolderDropdown] = useState(false)

  useEffect(() => {
    loadSettings()
  }, [])

  const loadSettings = async () => {
    setLoading(true)
    setError(null)
    try {
      const s = await fetchSettings()
      setCurrentLang(s.language)
      setIsAdmin(s.isAdmin)
      i18n.changeLanguage(s.language)

      // Load global settings if admin
      if (s.isAdmin) {
        try {
          const gs = await fetchGlobalSettings()
          setTranslationEnabled(gs.translationEnabled)
          setApiBase(gs.translationApiBase || 'https://dashscope.aliyuncs.com/compatible-mode/v1')
          setApiKey(gs.translationApiKey || '')
          setModelName(gs.translationModel || 'deepseek-v4-flash')
        } catch (err) {
          console.error('Failed to load global settings:', err)
        }
      }

      // Load IMAP config
      try {
        const imapRes = await getIMAPConfig()
        if (imapRes.configured && imapRes.config) {
          setImapConfigured(true)
          setImapHost(imapRes.config.host)
          setImapPort(imapRes.config.port)
          setImapUsername(imapRes.config.username)
          setImapFolder(imapRes.config.folderName)
          setImapAutoSync(imapRes.config.autoSync)
        }
      } catch (err) {
        console.error('Failed to load IMAP config:', err)
      }
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

  const handleGlobalTranslationSave = async () => {
    setGlobalSaving(true)
    setGlobalSuccess(false)
    setGlobalError(null)
    try {
      await updateGlobalSettings({
        translationEnabled,
        translationApiBase: apiBase,
        // Only send API key if user actually entered a new value (not the mask placeholder)
        ...(apiKey && !apiKey.startsWith('****') ? { translationApiKey: apiKey } : {}),
        translationModel: modelName,
      })
      setGlobalSuccess(true)
      setTimeout(() => setGlobalSuccess(false), 3000)
    } catch (err) {
      setGlobalError(err instanceof Error ? err.message : 'Failed to save global settings')
    } finally {
      setGlobalSaving(false)
    }
  }

  const handleImapSave = async () => {
    setImapSaving(true)
    setImapSuccess(false)
    setImapError(null)
    try {
      await updateIMAPConfig({
        host: imapHost,
        port: imapPort,
        username: imapUsername,
        password: imapPassword,
        folderName: imapFolder,
        autoSync: imapAutoSync,
      })
      setImapConfigured(true)
      setImapPassword('')
      setImapSuccess(true)
      setTimeout(() => setImapSuccess(false), 3000)
    } catch (err) {
      setImapError(err instanceof Error ? err.message : 'Failed to save IMAP config')
    } finally {
      setImapSaving(false)
    }
  }

  const handleImapTest = async () => {
    setImapTesting(true)
    setImapTestResult(null)
    try {
      const result = await testIMAPConnection()
      setImapTestResult(result.message)
      setImapTestSuccess(result.success)
      if (result.availableFolders && result.availableFolders.length > 0) {
        setImapFolders(result.availableFolders)
      }
    } catch (err) {
      setImapTestResult(err instanceof Error ? err.message : 'Test failed')
      setImapTestSuccess(false)
    } finally {
      setImapTesting(false)
    }
  }

  const handleImapDelete = async () => {
    if (!window.confirm(t('import.deleteImapConfirm'))) return
    try {
      await deleteIMAPConfig()
      setImapConfigured(false)
      setImapHost('')
      setImapPort(993)
      setImapUsername('')
      setImapPassword('')
      setImapFolder('Newsletter')
      setImapAutoSync(false)
      setImapFolders([])
    } catch (err) {
      setImapError(err instanceof Error ? err.message : 'Failed to delete config')
    }
  }

  const handleLoadFolders = async () => {
    setImapFoldersLoading(true)
    try {
      const result = await listIMAPFolders()
      setImapFolders(result.folders || [])
      setShowFolderDropdown(true)
    } catch (err) {
      setImapError(err instanceof Error ? err.message : 'Failed to load folders')
    } finally {
      setImapFoldersLoading(false)
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

      {/* Global Translation Section (Admin Only) */}
      {isAdmin && (
        <div className="hidden md:block bg-white border border-gray-200 rounded-lg p-6 mb-6" data-testid="advanced-section-translation">
          <div className="flex items-center gap-2 mb-2">
            <h3 className="text-lg font-medium text-gray-800">{t('settings.pdfTranslation')}</h3>
            <span className="px-2 py-0.5 text-xs font-medium bg-purple-100 text-purple-700 rounded-full">Admin</span>
          </div>
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

              {/* Save button for global settings */}
              <div className="flex gap-3 pt-2">
                <button
                  onClick={handleGlobalTranslationSave}
                  disabled={globalSaving}
                  className="px-4 py-2 bg-purple-500 text-white rounded-lg hover:bg-purple-600 transition-colors disabled:bg-gray-300 disabled:text-gray-500 text-sm"
                >
                  {globalSaving ? t('common.loading') : t('common.save')}
                </button>
              </div>
            </div>
          )}

          {globalSuccess && (
            <div className="mt-4 p-3 bg-green-50 border border-green-200 rounded-lg text-green-700 text-sm">
              {t('settings.saved')}
            </div>
          )}

          {globalError && (
            <div className="mt-4 p-3 bg-red-50 border border-red-200 rounded-lg text-red-700 text-sm">
              {t('common.error')}: {globalError}
            </div>
          )}
        </div>
      )}

      {/* Newsletter IMAP Section */}
      <div className="hidden md:block bg-white border border-gray-200 rounded-lg p-6 mb-6" data-testid="advanced-section-imap">
        <h3 className="text-lg font-medium text-gray-800 mb-2">{t('settings.newsletter')}</h3>
        <p className="text-sm text-gray-600 mb-4">{t('settings.newsletterHint')}</p>

        <div className="space-y-4">
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="block text-xs font-medium text-gray-500 mb-1">{t('settings.imapHost')}</label>
              <input
                type="text"
                value={imapHost}
                onChange={(e) => setImapHost(e.target.value)}
                placeholder="imap.gmail.com"
                className="w-full px-3 py-2 text-sm border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500"
              />
            </div>
            <div>
              <label className="block text-xs font-medium text-gray-500 mb-1">{t('settings.imapPort')}</label>
              <input
                type="number"
                value={imapPort}
                onChange={(e) => setImapPort(parseInt(e.target.value) || 993)}
                className="w-full px-3 py-2 text-sm border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500"
              />
            </div>
          </div>

          <div>
            <label className="block text-xs font-medium text-gray-500 mb-1">{t('settings.imapUsername')}</label>
            <input
              type="text"
              value={imapUsername}
              onChange={(e) => setImapUsername(e.target.value)}
              placeholder="user@gmail.com"
              className="w-full px-3 py-2 text-sm border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500"
            />
          </div>

          <div>
            <label className="block text-xs font-medium text-gray-500 mb-1">{t('settings.imapPassword')}</label>
            <input
              type="password"
              value={imapPassword}
              onChange={(e) => setImapPassword(e.target.value)}
              placeholder={imapConfigured ? '••••••••' : t('settings.imapPassword')}
              className="w-full px-3 py-2 text-sm border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500"
            />
            <div className="flex items-start gap-1.5 mt-1.5 px-2.5 py-2 bg-amber-50 border border-amber-200 rounded-md">
              <span className="text-amber-500 text-sm leading-none mt-0.5">⚠</span>
              <p className="text-xs text-amber-700">
                {t('settings.imapPasswordHint')}{' '}
                <a href="https://support.google.com/accounts/answer/185833" target="_blank" rel="noopener noreferrer" className="underline hover:text-amber-900">{t('settings.imapPasswordLink')}</a>
              </p>
            </div>
          </div>

          <div>
            <label className="block text-xs font-medium text-gray-500 mb-1">{t('settings.imapFolder')}</label>
            <div className="flex gap-2">
              <div className="relative flex-1">
                <input
                  type="text"
                  value={imapFolder}
                  onChange={(e) => { setImapFolder(e.target.value); setShowFolderDropdown(false) }}
                  onFocus={() => { if (imapFolders.length > 0) setShowFolderDropdown(true) }}
                  placeholder="Newsletter"
                  className="w-full px-3 py-2 text-sm border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500"
                />
                {showFolderDropdown && imapFolders.length > 0 && (
                  <div className="absolute z-10 w-full mt-1 max-h-48 overflow-auto bg-white border border-gray-200 rounded-lg shadow-lg">
                    {imapFolders.map((f) => (
                      <button
                        key={f}
                        type="button"
                        className={`w-full text-left px-3 py-1.5 text-sm hover:bg-blue-50 ${f === imapFolder ? 'bg-blue-100 text-blue-700 font-medium' : 'text-gray-700'}`}
                        onClick={() => { setImapFolder(f); setShowFolderDropdown(false) }}
                      >
                        {f}
                      </button>
                    ))}
                  </div>
                )}
              </div>
              <button
                type="button"
                onClick={handleLoadFolders}
                disabled={imapFoldersLoading || !imapConfigured}
                className="px-3 py-2 text-sm border border-gray-300 text-gray-700 rounded-lg hover:bg-gray-50 disabled:text-gray-400 disabled:hover:bg-white whitespace-nowrap"
                title={t('settings.loadFolders')}
              >
                {imapFoldersLoading ? '...' : t('settings.browseFolders')}
              </button>
            </div>
            <p className="text-xs text-gray-500 mt-1">{t('settings.imapFolderHint')}</p>
          </div>

          <div className="flex items-center gap-3">
            <label className="relative inline-flex items-center cursor-pointer">
              <input
                type="checkbox"
                checked={imapAutoSync}
                onChange={(e) => setImapAutoSync(e.target.checked)}
                className="sr-only peer"
              />
              <div className="w-11 h-6 bg-gray-200 peer-focus:outline-none peer-focus:ring-4 peer-focus:ring-blue-300 rounded-full peer peer-checked:after:translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-white after:border-gray-300 after:border after:rounded-full after:h-5 after:w-5 after:transition-all peer-checked:bg-blue-500"></div>
            </label>
            <div>
              <span className="text-sm text-gray-700">{t('settings.autoSync')}</span>
              <p className="text-xs text-gray-500">{t('settings.autoSyncHint')}</p>
            </div>
          </div>

          {imapTestResult && (
            <div className={`p-3 rounded-lg text-sm ${imapTestSuccess ? 'bg-green-50 border border-green-200 text-green-700' : 'bg-red-50 border border-red-200 text-red-700'}`}>
              {imapTestResult}
            </div>
          )}

          {imapSuccess && (
            <div className="p-3 bg-green-50 border border-green-200 rounded-lg text-green-700 text-sm">
              {t('settings.configSaved')}
            </div>
          )}

          {imapError && (
            <div className="p-3 bg-red-50 border border-red-200 rounded-lg text-red-700 text-sm">
              {imapError}
            </div>
          )}

          <div className="flex gap-3">
            <button
              onClick={handleImapSave}
              disabled={imapSaving || !imapHost || !imapUsername}
              className="px-4 py-2 bg-blue-500 text-white rounded-lg hover:bg-blue-600 transition-colors disabled:bg-gray-300 disabled:text-gray-500 text-sm"
            >
              {imapSaving ? t('common.loading') : t('common.save')}
            </button>
            {imapConfigured && (
              <>
                <button
                  onClick={handleImapTest}
                  disabled={imapTesting}
                  className="px-4 py-2 border border-gray-300 text-gray-700 rounded-lg hover:bg-gray-50 transition-colors disabled:text-gray-400 text-sm"
                >
                  {imapTesting ? t('settings.testing') : t('settings.testConnection')}
                </button>
                <button
                  onClick={handleImapDelete}
                  className="px-4 py-2 border border-red-300 text-red-600 rounded-lg hover:bg-red-50 transition-colors text-sm"
                >
                  {t('settings.deleteConfig')}
                </button>
              </>
            )}
          </div>
        </div>
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
