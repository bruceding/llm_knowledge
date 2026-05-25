# Code Review: fix/web-clip-enlighter-cleanup

- Date: 2026-05-25
- Branch: `fix/web-clip-enlighter-cleanup`
- Range: `259b279..bb67804`
- Issue: bruceding/llm_knowledge#63
- Verdict: Ready to merge (minor suggestions, no blockers)

## Scope verified

| Plan item | Location | Status |
| --- | --- | --- |
| P0-B JetBrains article-region selector | `backend/api/web.go` (selectors list) | OK |
| P1-A cleanup pass (copy-button / sr-only / aria Copy / enlighter-toolbar-top) | `backend/api/web.go` | OK |
| P0-A Enlighter early-detect handler in `convertNodeToMarkdown` | `backend/api/rss.go` | OK |
| Regression tests | `backend/api/web_test.go` | OK |
| Existing tests still pass | `go test ./api/` 22s | OK |
| Conventional Commit + English comments | commit `bb67804` | OK |

## Applied in this PR

- CRLF safety: `strings.TrimRight(..., "\r\n")` instead of `"\n"` when stripping trailing newline from Enlighter raw text (cheap follow-up to review minor item #2).

## Deferred (tracked as follow-ups, not blocking)

1. **Enlighter language-class hard-coded skip list** (`rss.go` `findLang`). Skips `enlighter-default`, `enlighter`, `enlighter-codebox`. Other Enlighter modifier classes (theme variants `enlighter-t-*`, `enlighter-hl*`, `enlighter-nowrap`, `enlighter-linenumbers`) could win the language slot on non-JetBrains sites. JetBrains itself uses the canonical `enlighter enlighter-default enlighter-{lang}` shape, so the current behavior is correct for the reported bug. Address when a non-JetBrains Enlighter site reports a wrong fence.
2. **`.sr-only` removal is global.** Screen-reader-only text is by design redundant with sighted content, so dropping it from Markdown is usually correct. Flag the cause if a future user reports a missing label.
3. **`button[aria-label*='Copy']` covers any copy-affordance** (GitHub gists, MDN, JetBrains). Intentional; widen the inline comment if a maintainer later narrows the rule.
4. **`[data-clarity-region='article']` ordering is signaling, not functional.** Selector resolution picks the match with most text content, not the first hit. A short clarifying comment would help future readers.
5. **`TestExtractContentJetBrainsBlog`** could additionally assert the heading body text follows immediately (e.g. `## CPU profiles\n\nUse the runtime/pprof package.`) to pin down that copy-button removal didn't swallow the trailing newline.

## Notes

- Bug only reproduces in the post-JS DOM captured by the browser extension. Static HTTP fetch of the JetBrains URL does not contain `EnlighterJSWrapper` / `.copy-button` elements (they are JS-rendered). The hand-crafted fixture in `TestExtractContentJetBrainsBlog` is the canonical reproduction.
- Existing `case "pre"` path (`rss.go:1128`) is untouched and continues to handle plain `<pre><code>` sites.
