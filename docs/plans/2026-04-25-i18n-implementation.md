# Frontend i18n Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add internationalization support to the frontend, allowing users to switch between English and Chinese UI languages with preferences persisted in the database.

**Architecture:** 
- Backend: Add UserSettings model and API endpoints to store language preference
- Frontend: Use react-i18next with locale JSON files, create Settings page with language selector

**Tech Stack:** Go (Echo), React, react-i18next, TypeScript

---

## Task 1: Backend - Add UserSettings Model

**Files:**
- Modify: `backend/db/models.go`
- Modify: `backend/db/db.go`

**Step 1: Add UserSettings model to models.go**

Add after the ConversationMessage model:

```go
type UserSettings struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Language  string    `gorm:"default:'en'" json:"language"` // 'en' or 'zh'
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}
```

**Step 2: Auto-migrate UserSettings in db.go**

In `Init` function, add UserSettings to auto-migrate:

```go
db.AutoMigrate(&Document{}, &Tag{}, &DocumentTag{}, &Conversation{}, &ConversationMessage{}, &UserSettings{})
```

**Step 3: Test by starting backend**

Run: `cd backend && go run main.go`
Expected: Server starts without errors, database creates user_settings table

**Step 4: Commit**

```bash
git add backend/db/models.go backend/db/db.go
git commit -m "feat: add UserSettings model for storing language preference"
```

---

## Task 2: Backend - Add Settings API

**Files:**
- Create: `backend/api/settings.go`
- Modify: `backend/main.go`

**Step 1: Create settings.go with Get and Update handlers**

```go
package api

import (
	"llm-knowledge/db"
	"net/http"

	"github.com/labstack/echo/v4"
)

type SettingsHandler struct{}

func (h *SettingsHandler) GetSettings(c echo.Context) error {
	var settings db.UserSettings
	result := db.DB.First(&settings)
	if result.Error != nil {
		// Create default settings if not exists
		settings = db.UserSettings{Language: "en"}
		db.DB.Create(&settings)
	}
	return c.JSON(http.StatusOK, settings)
}

func (h *SettingsHandler) UpdateSettings(c echo.Context) error {
	var settings db.UserSettings
	result := db.DB.First(&settings)
	if result.Error != nil {
		// Create if not exists
		settings = db.UserSettings{Language: "en"}
	}

	var input struct {
		Language string `json:"language"`
	}
	if err := c.Bind(&input); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid input"})
	}

	if input.Language != "en" && input.Language != "zh" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "language must be 'en' or 'zh'"})
	}

	settings.Language = input.Language
	db.DB.Save(&settings)

	return c.JSON(http.StatusOK, settings)
}
```

**Step 2: Register routes in main.go**

Add after the translate routes:

```go
// Settings API
settingsH := &api.SettingsHandler{}
e.GET("/api/settings", settingsH.GetSettings)
e.PUT("/api/settings", settingsH.UpdateSettings)
```

**Step 3: Test API manually**

Run backend, then test:
```bash
curl http://localhost:8080/api/settings
# Expected: {"id":1,"language":"en","createdAt":"...","updatedAt":"..."}

curl -X PUT http://localhost:8080/api/settings -H "Content-Type: application/json" -d '{"language":"zh"}'
# Expected: {"id":1,"language":"zh",...}
```

**Step 4: Commit**

```bash
git add backend/api/settings.go backend/main.go
git commit -m "feat: add Settings API endpoints for language preference"
```

---

## Task 3: Frontend - Install i18next Dependencies

**Files:**
- Modify: `frontend/package.json`

**Step 1: Install dependencies**

```bash
cd frontend && npm install i18next react-i18next
```

**Step 2: Verify installation**

Check package.json has:
```json
"i18next": "^23.x",
"react-i18next": "^14.x"
```

**Step 3: Commit**

```bash
git add frontend/package.json frontend/package-lock.json
git commit -m "chore: add i18next dependencies for internationalization"
```

---

## Task 4: Frontend - Setup i18n Configuration

**Files:**
- Create: `frontend/src/i18n/index.ts`
- Create: `frontend/src/i18n/locales/en.json`
- Create: `frontend/src/i18n/locales/zh.json`
- Modify: `frontend/src/main.tsx`

**Step 1: Create i18n/index.ts**

```ts
import i18n from 'i18next'
import { initReactI18next } from 'react-i18next'
import en from './locales/en.json'
import zh from './locales/zh.json'

i18n
  .use(initReactI18next)
  .init({
    resources: {
      en: { translation: en },
      zh: { translation: zh },
    },
    lng: 'en', // default language
    fallbackLng: 'en',
    interpolation: {
      escapeValue: false,
    },
  })

export default i18n
```

**Step 2: Create en.json with sidebar translations**

```json
{
  "sidebar": {
    "title": "LLM Knowledge",
    "searchPlaceholder": "Search...",
    "navigation": "Navigation",
    "inbox": "Inbox",
    "allDocuments": "All Documents",
    "tags": "Tags",
    "chatHistory": "Chat History",
    "wiki": "Wiki",
    "wikiIndex": "Index",
    "entities": "Entities",
    "topics": "Topics",
    "sources": "Sources",
    "import": "Import",
    "settings": "Settings",
    "connected": "Connected"
  },
  "inbox": {
    "title": "Inbox",
    "documentsPending": "documents pending review",
    "noDocuments": "No documents to review",
    "importHint": "Import new documents to start building your knowledge base",
    "pendingReview": "Pending Review",
    "published": "Published",
    "archived": "Archived",
    "retry": "Retry"
  },
  "import": {
    "title": "Import",
    "description": "Upload and import new documents into your knowledge base.",
    "uploadPdf": "Upload PDF",
    "dragDropHint": "Drag and drop PDF files here, or click to browse",
    "pdfSizeLimit": "Supports PDF files up to 50MB",
    "selectPdf": "Select PDF",
    "webClipping": "Web Clipping",
    "webClipHint": "Paste a URL to clip web content and save it to your knowledge base.",
    "clip": "Clip",
    "clipping": "Clipping...",
    "rssFeeds": "RSS Feeds",
    "rssHint": "Subscribe to RSS feeds to automatically import new articles.",
    "addFeed": "Add Feed",
    "adding": "Adding...",
    "activeFeeds": "Active Feeds",
    "viewDocument": "View Document"
  },
  "settings": {
    "title": "Settings",
    "language": "Language",
    "languageHint": "Select your preferred display language for the interface.",
    "english": "English",
    "chinese": "中文"
  },
  "common": {
    "loading": "Loading...",
    "error": "Error",
    "save": "Save",
    "cancel": "Cancel",
    "delete": "Delete",
    "edit": "Edit"
  }
}
```

**Step 3: Create zh.json**

```json
{
  "sidebar": {
    "title": "LLM 知识库",
    "searchPlaceholder": "搜索...",
    "navigation": "导航",
    "inbox": "收件箱",
    "allDocuments": "所有文档",
    "tags": "标签",
    "chatHistory": "对话历史",
    "wiki": "Wiki",
    "wikiIndex": "索引",
    "entities": "实体",
    "topics": "主题",
    "sources": "来源",
    "import": "导入",
    "settings": "设置",
    "connected": "已连接"
  },
  "inbox": {
    "title": "收件箱",
    "documentsPending": "个文档待审核",
    "noDocuments": "暂无待审核文档",
    "importHint": "导入新文档开始构建您的知识库",
    "pendingReview": "待审核",
    "published": "已发布",
    "archived": "已归档",
    "retry": "重试"
  },
  "import": {
    "title": "导入",
    "description": "上传并导入新文档到您的知识库。",
    "uploadPdf": "上传 PDF",
    "dragDropHint": "拖拽 PDF 文件到此处，或点击选择文件",
    "pdfSizeLimit": "支持最大 50MB 的 PDF 文件",
    "selectPdf": "选择 PDF",
    "webClipping": "网页裁剪",
    "webClipHint": "粘贴网址以裁剪网页内容并保存到知识库。",
    "clip": "裁剪",
    "clipping": "裁剪中...",
    "rssFeeds": "RSS 订阅",
    "rssHint": "订阅 RSS 源以自动导入新文章。",
    "addFeed": "添加源",
    "adding": "添加中...",
    "activeFeeds": "已订阅源",
    "viewDocument": "查看文档"
  },
  "settings": {
    "title": "设置",
    "language": "语言",
    "languageHint": "选择界面的首选显示语言。",
    "english": "English",
    "chinese": "中文"
  },
  "common": {
    "loading": "加载中...",
    "error": "错误",
    "save": "保存",
    "cancel": "取消",
    "delete": "删除",
    "edit": "编辑"
  }
}
```

**Step 4: Import i18n in main.tsx**

Add at the top:

```ts
import './i18n'
```

**Step 5: Verify i18n works**

Run: `npm run dev`
Expected: App starts without errors

**Step 6: Commit**

```bash
git add frontend/src/i18n/ frontend/src/main.tsx
git commit -m "feat: setup i18next configuration with English and Chinese locales"
```

---

## Task 5: Frontend - Add Settings API Client

**Files:**
- Modify: `frontend/src/api.ts`
- Modify: `frontend/src/types.ts`

**Step 1: Add UserSettings type to types.ts**

```ts
export interface UserSettings {
  id: number
  language: 'en' | 'zh'
  createdAt: string
  updatedAt: string
}
```

**Step 2: Add settings API functions to api.ts**

```ts
import type { Document, UpdateDocRequest, AskRequest, SSEEvent, UserSettings } from './types'

// ... existing code ...

// Settings API
export async function fetchSettings(): Promise<UserSettings> {
  const res = await fetch(`${API_BASE}/settings`)
  if (!res.ok) throw new Error('Failed to fetch settings')
  return res.json()
}

export async function updateSettings(language: 'en' | 'zh'): Promise<UserSettings> {
  const res = await fetch(`${API_BASE}/settings`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ language }),
  })
  if (!res.ok) throw new Error('Failed to update settings')
  return res.json()
}
```

**Step 3: Commit**

```bash
git add frontend/src/api.ts frontend/src/types.ts
git commit -m "feat: add Settings API client functions"
```

---

## Task 6: Frontend - Create Settings Page

**Files:**
- Create: `frontend/src/components/SettingsPage.tsx`
- Modify: `frontend/src/App.tsx`

**Step 1: Create SettingsPage.tsx**

```tsx
import { useState, useEffect } from 'react'
import { useTranslation } from 'react-i18next'
import { fetchSettings, updateSettings } from '../api'

export default function SettingsPage() {
  const { t, i18n } = useTranslation()
  const [language, setLanguage] = useState<'en' | 'zh'>('en')
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [saved, setSaved] = useState(false)

  useEffect(() => {
    loadSettings()
  }, [])

  const loadSettings = async () => {
    try {
      const settings = await fetchSettings()
      setLanguage(settings.language)
      i18n.changeLanguage(settings.language)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load settings')
    } finally {
      setLoading(false)
    }
  }

  const handleSave = async () => {
    setSaving(true)
    setError(null)
    setSaved(false)

    try {
      await updateSettings(language)
      i18n.changeLanguage(language)
      setSaved(true)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save settings')
    } finally {
      setSaving(false)
    }
  }

  if (loading) {
    return (
      <div className="p-6">
        <div className="flex items-center justify-center py-12">
          <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-blue-500"></div>
        </div>
      </div>
    )
  }

  return (
    <div className="p-6">
      <h2 className="text-2xl font-bold text-gray-800 mb-6">{t('settings.title')}</h2>

      {error && (
        <div className="mb-4 bg-red-50 border border-red-200 rounded-lg p-4 text-red-700">
          {error}
        </div>
      )}

      {saved && (
        <div className="mb-4 bg-green-50 border border-green-200 rounded-lg p-4 text-green-700">
          Settings saved successfully
        </div>
      )}

      <div className="bg-white border border-gray-200 rounded-lg p-6">
        <div className="mb-6">
          <label className="block text-lg font-semibold text-gray-700 mb-2">
            {t('settings.language')}
          </label>
          <p className="text-sm text-gray-500 mb-4">{t('settings.languageHint')}</p>
          
          <div className="flex gap-4">
            <button
              onClick={() => setLanguage('en')}
              className={`px-4 py-2 rounded-lg border transition-colors ${
                language === 'en'
                  ? 'bg-blue-100 border-blue-500 text-blue-700'
                  : 'border-gray-300 text-gray-700 hover:bg-gray-50'
              }`}
            >
              {t('settings.english')}
            </button>
            <button
              onClick={() => setLanguage('zh')}
              className={`px-4 py-2 rounded-lg border transition-colors ${
                language === 'zh'
                  ? 'bg-blue-100 border-blue-500 text-blue-700'
                  : 'border-gray-300 text-gray-700 hover:bg-gray-50'
              }`}
            >
              {t('settings.chinese')}
            </button>
          </div>
        </div>

        <button
          onClick={handleSave}
          disabled={saving}
          className="px-4 py-2 bg-blue-500 text-white rounded-lg hover:bg-blue-600 disabled:bg-gray-300 disabled:text-gray-500 transition-colors"
        >
          {saving ? t('common.loading') : t('common.save')}
        </button>
      </div>
    </div>
  )
}
```

**Step 2: Add route in App.tsx**

Import and add route:

```tsx
import SettingsPage from './components/SettingsPage'

// In Routes:
<Route path="/settings" element={<SettingsPage />} />
```

**Step 3: Test Settings page**

Run frontend, navigate to `/settings`
Expected: Settings page loads with language selector

**Step 4: Commit**

```bash
git add frontend/src/components/SettingsPage.tsx frontend/src/App.tsx
git commit -m "feat: create Settings page with language selector"
```

---

## Task 7: Frontend - Add Settings Link to Sidebar

**Files:**
- Modify: `frontend/src/components/Sidebar.tsx`

**Step 1: Add Settings link in Sidebar**

Add after Import section, before Footer:

```tsx
{/* Settings */}
<div className="mb-4">
  <Link to="/settings" className={navItemClass('/settings')}>
    <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
      <path
        strokeLinecap="round"
        strokeLinejoin="round"
        strokeWidth={2}
        d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z"
      />
      <path
        strokeLinecap="round"
        strokeLinejoin="round"
        strokeWidth={2}
        d="M15 12a3 3 0 11-6 0 3 3 0 016 0z"
      />
    </svg>
    <span>{t('sidebar.settings')}</span>
  </Link>
</div>
```

**Step 2: Add useTranslation hook in Sidebar**

```tsx
import { useTranslation } from 'react-i18next'

// In component:
const { t } = useTranslation()
```

**Step 3: Replace hardcoded text with t() calls**

Replace all hardcoded UI text with translation keys:
- "LLM Knowledge" → `{t('sidebar.title')}`
- "Search..." → `{t('sidebar.searchPlaceholder')}`
- "Navigation" → `{t('sidebar.navigation')}`
- "Inbox" → `{t('sidebar.inbox')}`
- etc.

**Step 4: Test Sidebar translations**

Run frontend, verify sidebar shows correct language
Test: Change language in Settings, verify sidebar updates

**Step 5: Commit**

```bash
git add frontend/src/components/Sidebar.tsx
git commit -m "feat: add Settings link and translations to Sidebar"
```

---

## Task 8: Frontend - Translate Inbox Component

**Files:**
- Modify: `frontend/src/components/Inbox.tsx`

**Step 1: Add useTranslation hook**

```tsx
import { useTranslation } from 'react-i18next'

const { t } = useTranslation()
```

**Step 2: Replace hardcoded text with t() calls**

Key replacements:
- "Inbox" → `{t('inbox.title')}`
- "documents pending review" → `{t('inbox.documentsPending')}`
- "No documents to review" → `{t('inbox.noDocuments')}`
- "Import new documents..." → `{t('inbox.importHint')}`
- "Pending Review" → `{t('inbox.pendingReview')}`
- "Published" → `{t('inbox.published')}`
- "Archived" → `{t('inbox.archived')}`
- "Retry" → `{t('inbox.retry')}`

**Step 3: Commit**

```bash
git add frontend/src/components/Inbox.tsx
git commit -m "feat: translate Inbox component"
```

---

## Task 9: Frontend - Translate ImportView Component

**Files:**
- Modify: `frontend/src/components/ImportView.tsx`

**Step 1: Add useTranslation hook**

```tsx
import { useTranslation } from 'react-i18next'

const { t } = useTranslation()
```

**Step 2: Replace hardcoded text with t() calls**

Key replacements:
- "Import" → `{t('import.title')}`
- "Upload and import..." → `{t('import.description')}`
- "Upload PDF" → `{t('import.uploadPdf')}`
- "Drag and drop..." → `{t('import.dragDropHint')}`
- "Supports PDF files..." → `{t('import.pdfSizeLimit')}`
- "Select PDF" → `{t('import.selectPdf')}`
- "Web Clipping" → `{t('import.webClipping')}`
- "Clip" / "Clipping..." → `{t('import.clip')}` / `{t('import.clipping')}`
- etc.

**Step 3: Commit**

```bash
git add frontend/src/components/ImportView.tsx
git commit -m "feat: translate ImportView component"
```

---

## Task 10: Frontend - Load Language on App Start

**Files:**
- Modify: `frontend/src/App.tsx`

**Step 1: Load user settings on app mount**

Add useEffect to fetch and apply saved language:

```tsx
import { useEffect } from 'react'
import { useTranslation } from 'react-i18next'
import { fetchSettings } from './api'

function App() {
  const { i18n } = useTranslation()

  useEffect(() => {
    fetchSettings()
      .then((settings) => i18n.changeLanguage(settings.language))
      .catch(() => {}) // Silently fail, use default
  }, [i18n])

  // ... rest of component
}
```

**Step 2: Test full flow**

1. Start app fresh → should show English (default)
2. Go to Settings → change to Chinese → Save
3. Refresh page → should show Chinese

**Step 3: Commit**

```bash
git add frontend/src/App.tsx
git commit -m "feat: load and apply saved language preference on app start"
```

---

## Task 11: Frontend - Translate Remaining Components

**Files:**
- Modify: `frontend/src/components/DocumentsList.tsx`
- Modify: `frontend/src/components/DocDetail.tsx`
- Modify: `frontend/src/components/WikiView.tsx`
- Modify: `frontend/src/components/ChatView.tsx`
- Modify: `frontend/src/components/TagsView.tsx`

**Step 1: Add translations for each component**

Add useTranslation hook and replace hardcoded text in each component.
Add new translation keys to en.json and zh.json as needed.

**Step 2: Commit each component separately**

```bash
git add frontend/src/components/DocumentsList.tsx frontend/src/i18n/locales/*.json
git commit -m "feat: translate DocumentsList component"

# Repeat for each component
```

---

## Task 12: Final Integration Test

**Step 1: Run backend**

```bash
cd backend && go run main.go
```

**Step 2: Run frontend**

```bash
cd frontend && npm run dev
```

**Step 3: Test complete flow**

1. Open app → default English UI
2. Navigate to Settings → change to Chinese
3. All UI elements should show Chinese
4. Refresh → still Chinese (persisted)
5. Check document content → stays original language
6. Change back to English → verify all updates

**Step 4: Final commit if needed**

```bash
git add -A
git commit -m "feat: complete i18n implementation with full UI translations"
```

---

## Translation Keys Summary

| Category | Keys Added |
|----------|------------|
| sidebar | title, searchPlaceholder, navigation, inbox, allDocuments, tags, chatHistory, wiki, wikiIndex, entities, topics, sources, import, settings, connected |
| inbox | title, documentsPending, noDocuments, importHint, pendingReview, published, archived, retry |
| import | title, description, uploadPdf, dragDropHint, pdfSizeLimit, selectPdf, webClipping, webClipHint, clip, clipping, rssFeeds, rssHint, addFeed, adding, activeFeeds, viewDocument |
| settings | title, language, languageHint, english, chinese |
| common | loading, error, save, cancel, delete, edit |

Additional keys will be added as needed for DocumentsList, DocDetail, WikiView, ChatView, TagsView.