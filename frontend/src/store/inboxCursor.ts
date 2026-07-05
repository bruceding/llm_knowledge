// One-shot cursor handoff for the Inbox list.
//
// Recorded when the user opens / deletes / defers a document, then consumed the
// next time the Inbox list (re)loads so the selection can be restored to where
// the user was — or to the item now occupying that slot if the original one was
// removed. Module-level (survives Inbox unmount) and single-use by design.

export interface InboxCursor {
  docId: number
  index: number | null
}

let pending: InboxCursor | null = null

export const setInboxCursor = (docId: number, index: number | null) => {
  pending = { docId, index }
}

export const consumeInboxCursor = (): InboxCursor | null => {
  const cursor = pending
  pending = null
  return cursor
}
