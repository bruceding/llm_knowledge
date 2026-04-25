# Plan: PDFViewer 连续滚动模式

## 背景

当前 PDFViewer 使用单画布、单页渲染模型，通过滚轮累积实现翻页。用户希望实现传统 PDF 阅读器的连续滚动模式，带有可见的滚动条和可选文字功能。

## 推荐方案

使用 PDF.js 内置的 `PDFViewer` 组件（来自 `pdfjs-dist/web/pdf_viewer.mjs`）。它提供：
- 连续滚动 + 内置虚拟化（只渲染可见页面附近的内容）
- **文本层 - 支持文字选择和搜索高亮**
- PDFFindController - 搜索功能
- 滚动时自动检测当前页面
- 渲染队列 + 缓存机制，性能优化

### 文本层功能

PDF.js 会自动在每个页面画布上叠加一个透明的文本层，实现：
- **文字选择** - 点击拖拽选择文字，可复制到剪贴板
- **搜索高亮** - PDFFindController 用黄色背景高亮匹配的文字
- **光标指示** - 鼠标悬停在文字上时显示文本光标

配置方式：
```typescript
const pdfViewer = new pdfjsViewer.PDFViewer({
  container: containerRef.current,
  viewer: viewerRef.current,
  eventBus,
  linkService: pdfLinkService,
  findController: pdfFindController,
  textLayerMode: 1,  // ENABLE - 启用文本层
})
```

需要导入 `pdfjs-dist/web/pdf_viewer.css`，它包含文本层的样式：
- `.textLayer` div 覆盖在画布上
- 默认透明，交互时显示选择/高亮效果

## 实现步骤

### Phase 1: 基础设施搭建

1. **导入 CSS**: `pdfjs-dist/web/pdf_viewer.css`
2. **创建新的 refs**:
   - `viewerRef` - 内部 viewer div
   - `pdfViewerRef` - PDFViewer 实例
   - `eventBusRef` - 事件系统
   - `pdfLinkServiceRef` - 导航服务
   - `pdfFindControllerRef` - 搜索控制器

3. **更新容器结构**: 外层 div 设置 `overflow-auto`，内层 div 设置 class `pdfViewer`

### Phase 2: 初始化 PDFViewer

4. **创建初始化 effect**，设置：
   - EventBus + 事件监听器 (`pagechanging`, `scalechanging`, `updatefindmatchescount`)
   - PDFLinkService
   - PDFFindController
   - PDFViewer 实例（配置 `textLayerMode: 1`）

### Phase 3: 文档加载

5. **修改 PDF 加载逻辑**，调用：
   - `pdfViewer.setDocument(pdf)`
   - `pdfLinkService.setDocument(pdf, null)`
   - `pdfFindController.setDocument(pdf)`

6. **删除** 单页 `renderPage` 回调函数和 `renderTaskRef`

### Phase 4: 移除旧滚动逻辑

7. **删除滚轮累积 useEffect**（原代码 109-137 行）
8. **删除** `scrollAccumRef`

### Phase 5: 更新功能

9. **页面导航**: 使用 `pdfViewer.currentPageNumber` 和 `pdfViewer.previousPage()/nextPage()`
10. **缩放**: 使用 `pdfViewer.increaseScale()/decreaseScale()`，监听 `scalechanging` 事件
11. **搜索**: 使用 EventBus `find` 和 `findagain` dispatch，不再手动提取文本

### Phase 6: 清理

12. **删除** `canvasRef`、`searchResults` state、`currentSearchIndex` state
13. **删除** JSX 中的单个 canvas
14. **删除** 手动搜索文本提取逻辑

## 关键代码变更

### 新导入
```typescript
import * as pdfjsViewer from 'pdfjs-dist/web/pdf_viewer.mjs'
import 'pdfjs-dist/web/pdf_viewer.css'
```

### 新容器 JSX
```typescript
<div ref={containerRef} className="flex-1 overflow-auto bg-gray-300 p-4">
  <div ref={viewerRef} className="pdfViewer" />
</div>
```

### 搜索更新
```typescript
// 使用 EventBus dispatch 替代手动搜索
const handleSearch = () => {
  eventBusRef.current?.dispatch('find', {
    query: searchText,
    caseSensitive: false,
    highlightAll: true,
  })
}
```

## 待修改文件

- [PDFViewer.tsx](frontend/src/components/PDFViewer.tsx) - 主要重构

## 验证步骤

1. 加载 10+ 页 PDF，验证所有页面依次渲染
2. 滚动流畅，滚动条可见
3. 页码输入导航跳转到正确页面
4. 缩放功能正常，可见页面重新渲染
5. **文字选择测试**: 点击拖拽选择文字，验证可复制
6. 搜索功能正常，匹配高亮，可导航
7. 测试 50+ 页 PDF 的性能