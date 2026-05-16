# Blog Feed Import 设计文档

日期：2026-05-15

## 背景

目前 Import 支持的源类型：RSS / Newsletter / Web(单页) / PDF / Wiki。

像 claude.com/blog 这种 blog 索引页形态目前不支持：
- 入口是列表页，本体是 N 篇文章 —— 不能复用 Web 单页 import
- 没有官方 RSS feed —— 不能复用 RSS handler

## 目标

新增 Blog Feed 类型，支持：
1. **自动检测平台**：内置常见 blog 平台规则（claude.com, Webflow, WordPress, Ghost, Medium）
2. **手动配置 fallback**：检测失败时，用户手动配置 selector
3. **增量同步**：只抓取发布日期 > 已抓取最大日期的新文章
4. **首次同步限制**：首次只抓 5 篇

## 数据模型

### BlogFeed 表

```go
type BlogFeed struct {
    ID              uint      `gorm:"primaryKey"`
    UserID          uint      `gorm:"index;not null"`
    Name            string    `json:"name"`
    IndexURL        string    `json:"indexUrl"`        // 索引页 URL
    PlatformType    string    `json:"platformType"`    // claude, webflow, wordpress, ghost, medium, custom
    LinkSelector    string    `json:"linkSelector"`    // 文章链接 CSS selector
    ContentSelector string    `json:"contentSelector"` // 正文 CSS selector
    LinkExclude     string    `json:"linkExclude"`     // 排除规则（可选）
    AutoSync        bool      `gorm:"default:false"`
    LastArticleDate time.Time `json:"lastArticleDate"` // 已抓取文章的最大日期
    LastSyncAt      time.Time `json:"lastSyncAt"`
    CreatedAt       time.Time `json:"createdAt"`
}
```

### Document 表扩展

新增字段：`BlogFeedID uint`（关联 BlogFeed）

---

## 平台规则库

```go
type PlatformRule struct {
    Name            string
    URLPatterns     []string // URL 匹配优先
    DetectPatterns  []string // HTML 特征匹配
    LinkSelector    string
    ContentSelector string
    LinkExclude     string
}

var PlatformRules = []PlatformRule{
    {
        Name:           "claude",
        URLPatterns:    []string{"claude.com"},
        LinkSelector:   "a[href^='/blog/']",
        LinkExclude:    "a[href*='/category/']",
        ContentSelector: ".u-rich-text-blog",
    },
    {
        Name:           "webflow",
        DetectPatterns: []string{"data-wf-domain", "data-wf-page"},
        LinkSelector:   "a[href^='/blog/']",
        ContentSelector: ".w-richtext, .rich-text",
    },
    {
        Name:           "wordpress",
        DetectPatterns: []string{"class=\"wp-", "WordPress"},
        LinkSelector:   "article a, .post-title a",
        ContentSelector: ".entry-content, .post-content",
    },
    {
        Name:           "ghost",
        DetectPatterns: []string{"class=\"gh-", "Ghost"},
        LinkSelector:   "a[href^='/post/']",
        ContentSelector: ".gh-content, .post-content",
    },
    {
        Name:           "medium",
        URLPatterns:    []string{"medium.com"},
        LinkSelector:   "article a[href^='/p/']",
        ContentSelector: "article section",
    },
}
```

**匹配优先级**：
1. URLPatterns 匹配（如 claude.com）
2. DetectPatterns HTML 特征匹配
3. 都不匹配 → 返回 `nil`，触发手动配置

---

## API 设计

### AddFeed
```
POST /api/blog-feeds
Body: { name, indexUrl, autoSync }

Response:
  成功检测: { feed, platformType: "claude", detected: true }
  检测失败: { error: "无法识别站点，请手动配置", needConfig: true }
```

流程：
1. 抓取索引页 HTML
2. 调用 `DetectPlatform(html, url)` 尝试识别
3. 成功 → 创建 BlogFeed（保存 selector）
4. 失败 → 返回 `needConfig: true`

### ConfigFeed
```
POST /api/blog-feeds/:id/config
Body: { linkSelector, contentSelector, linkExclude }

Response: { feed, platformType: "custom" }
```

### SyncFeed
```
POST /api/blog-feeds/:id/sync
Response: { newArticles, total, downloadErrors }
```

流程：
1. 抓取索引页，用 selector 提取链接列表
2. 首次同步：取前 5 个链接
3. 遍历链接：
   - 抓正文 + 提取日期（复用 `extractPublishedTime`）
   - 判断：日期 > LastArticleDate 才入库
   - 更新 LastArticleDate
4. 创建 Document（SourceType: "blog"，BlogFeedID 关联）
5. 更新 BlogFeed.LastSyncAt

### ListFeeds / DeleteFeed
复用 RSSHandler 结构。

---

## 增量同步逻辑

```go
func syncFeedInternal(feed *db.BlogFeed) SyncResult {
    links := extractArticleLinks(feed.IndexURL, feed.LinkSelector, feed.LinkExclude)

    // 首次同步：取前 5 个
    if feed.LastSyncAt.IsZero() {
        links = links[:min(5, len(links))]
    }

    newArticles := 0
    maxDate := feed.LastArticleDate

    for _, link := range links {
        content, date := fetchArticleWithDate(link.URL, feed.ContentSelector)

        // 后续同步：只抓日期 > LastArticleDate
        if !feed.LastSyncAt.IsZero() && date <= feed.LastArticleDate {
            continue
        }

        saveDocument(link, content, date, feed.ID)
        newArticles++

        if date > maxDate {
            maxDate = date
        }
    }

    feed.LastArticleDate = maxDate
    feed.LastSyncAt = time.Now()
    db.DB.Save(feed)
}
```

**日期提取**：复用 `web.go` 的 `extractPublishedTime`，从 meta tags 或 `<time>` 元素提取。

---

## 文件结构

新增：
```
backend/db/blog_feed.go          # BlogFeed 数据模型
backend/api/blog.go              # BlogHandler + API
backend/blog/platforms.go        # 平台规则库
backend/blog/detect.go           # DetectPlatform 函数
backend/blog/extract.go          # extractArticleLinks 函数
```

修改：
```
backend/db/db.go                 # AutoMigrate 增加 BlogFeed
backend/api/routes.go            # 注册 /api/blog-feeds routes
frontend/src/pages/BlogFeeds.tsx # 新页面
```

---

## 前端改动

新增：
- **BlogFeeds.tsx**：blog feed 管理页面（参考 RSSFeeds.tsx）
- **AddBlogFeed**：添加对话框，支持检测失败后弹出配置
- **ConfigBlogFeed**：手动配置 selector 对话框

---

## 错误处理

| 场景 | 处理 |
|------|------|
| 索引页抓取失败 | 返回错误，提示检查 URL |
| 平台检测失败 | 返回 `needConfig: true` |
| 正文抓取失败 | 记录错误，继续下一篇 |
| 日期提取失败 | 日期为空，仍入库，不参与增量判断 |
| selector 配置错误 | 抓取空内容，提示调整 |

---

## 实现优先级

1. 后端：数据模型 + 平台规则库 + API
2. 前端：BlogFeeds 页面
3. 测试：claude.com/blog 实际同步