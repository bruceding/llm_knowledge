# Code Review — fix/claude-fetcher-content-incomplete

- Date: 2026-05-21
- Branch: `fix/claude-fetcher-content-incomplete`
- Commit: `ae4a359`
- Verdict: **Mergeable with suggested improvements**

## Context

Fixes #61: claude.com articles only kept 1/31 of the body because
`contentNode.First()` discarded sibling `.u-rich-text-blog` nodes, and the
fallback path was unreachable when the selector matched an empty wrapper.

## Strengths

- `extractInnerHTMLAll` uses inner HTML (not OuterHtml) to avoid duplicating
  the wrapping element when concatenating multi-node selections.
- `textLength` relies on goquery's cumulative `Text()`, which correctly sums
  text across multiple matched nodes.
- Secondary-fallback gate (`< 500 chars`) only fires when the primary
  selector produced very little content, so well-configured feeds are
  unaffected.
- `platforms.go` change to `main` only touches the `claude.com` rule and
  does not impact other platforms.
- Tests cover both the multi-node concatenation regression and the
  empty-wrapper fallback path; the existing single-node test guards the
  unchanged code path.

## Suggested improvements (non-blocking)

| # | Severity | Description |
|---|----------|-------------|
| 1 | major (preventive) | `fetcher.go` title extraction uses `contentNode.First().Find("h1")`. Safe today because the claude rule now resolves to a single `main` node, but a future multi-node selector whose `h1` lives in the second node would silently fall through to the page-global `h1` (often a brand wordmark). Recommend `contentNode.Find("h1").First()` — semantically equivalent for single-node, robust for multi-node. |
| 2 | minor | When the secondary fallback picks `main`/`article`, nav/footer text could bleed in. Acceptable trade-off; document it in a comment. |
| 3 | minor | `extractInnerHTMLAll` mutates the underlying DOM via `Find("style, script").Remove()`. Current call sites tolerate this, but the side-effect deserves a comment. |
| 4 | minor | Add a regression test asserting `title` extraction when the `h1` lives in a non-first matched node, to prevent #1 from regressing later. |

## Recommendation

Merge as-is for #61; track items 1 & 4 as follow-up if other platforms
ever adopt a multi-node content selector.
