import type { KnowledgeDocument } from "../api/articles";

/**
 * An unsaved draft kept in this browser per article (knowledge design K9), so
 * text typed just before leaving is offered back on the next open. Every
 * access is wrapped: storage can be full, blocked or absent.
 */
export interface LocalDraft {
  title: string;
  content: KnowledgeDocument;
  /** The version the editor had read when the draft was typed; a save of it names this one. */
  baseVersion: number;
  /** When it was typed, in milliseconds. */
  savedAt: number;
}

const key = (articleId: string) => `knowledge.draft.${articleId}`;

export const readDraft = (articleId: string): LocalDraft | null => {
  try {
    const raw = window.localStorage.getItem(key(articleId));
    if (!raw) return null;
    const draft = JSON.parse(raw) as Partial<LocalDraft>;
    if (
      typeof draft.title !== "string" ||
      typeof draft.baseVersion !== "number" ||
      typeof draft.savedAt !== "number" ||
      !draft.content ||
      typeof draft.content !== "object"
    )
      return null;
    return draft as LocalDraft;
  } catch {
    return null;
  }
};

export const writeDraft = (articleId: string, draft: LocalDraft) => {
  try {
    window.localStorage.setItem(key(articleId), JSON.stringify(draft));
  } catch {
    // A draft too large for storage, or storage blocked: the autosave still runs.
  }
};

export const clearDraft = (articleId: string) => {
  try {
    window.localStorage.removeItem(key(articleId));
  } catch {
    // Nothing to do.
  }
};

/**
 * Whether a draft is worth offering: it differs from what the server holds.
 * Every successful save clears it, so one still here is text that was never
 * saved; comparing contents, not clocks, keeps a skewed device clock out of it.
 */
export const isDraftUnsaved = (
  draft: LocalDraft | null,
  saved: { title: string; content: KnowledgeDocument },
): draft is LocalDraft =>
  !!draft && (draft.title !== saved.title || JSON.stringify(draft.content) !== JSON.stringify(saved.content));
