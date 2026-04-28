# RSS Feed 自动发现设计

## 背景

用户添加 RSS feed 时，需要手动输入 RSS URL。对于博客网站（如 `https://go.dev/blog/`），用户可能不知道 RSS 地址在哪。

## 目标

用户输入任意 URL：
- 是 RSS URL → 直接添加
- 是博客页面 → 自动发现 RSS → 添加
- 找不到 → 提示尝试过的路径

## 检测流程

```
用户输入 URL
    ↓
尝试 gofeed 解析 → 成功 → 直接添加
    ↓ 失败
解析 HTML <head> 找 link 标签 → 找到 → 用发现的 URL 添加
    ↓ 未找到
探测常见路径 → 找到有效 feed → 用发现的 URL 添加
    ↓ 全失败
返回错误，附带尝试过的路径列表
```

**检测顺序理由：**
- RSS 直接解析优先 - 用户直接输入 RSS URL 时最快路径
- link 标签次之 - 标准方式，覆盖大多数现代博客
- 路径探测兜底 - 兼容老旧站点

## 常见探测路径

- `/feed`
- `/rss`
- `/rss.xml`
- `/atom.xml`
- `/feed.xml`

## 修改范围

只改 `backend/api/rss.go` 的 `AddFeed` 方法，前端不动。

## 返回格式

**成功：** 现有格式不变

```json
{
  "id": 1,
  "name": "Go Blog",
  "url": "https://go.dev/blog/feed.atom",
  "autoSync": false,
  ...
}
```

**失败：**

```json
{
  "error": "未找到 RSS feed。尝试路径：/feed, /rss, /rss.xml, /atom.xml, /feed.xml"
}
```

## 实现要点

1. 用现有 `gofeed` 包解析 RSS
2. 用现有 `goquery` 包解析 HTML
3. HTTP 请求统一超时 15 秒
4. 相对 URL 需转换为绝对 URL（`url.ResolveReference`）