import { useEffect } from "react";

/**
 * Cmd/Ctrl+Shift+U opens and closes the Conversations panel (Messenger design N24).
 * The Assistant has Cmd/Ctrl+J and Spotlight Cmd/Ctrl+K. Chrome, Safari and
 * Firefox use Cmd/Ctrl+Shift+U for nothing on macOS or Windows; Edge's Read aloud
 * does on Windows (the page asks the browser to leave the key alone, and Edge may
 * still keep it), and the Linux input methods use it to type a Unicode character.
 */
export const isConversationsShortcut = (
  event: Pick<KeyboardEvent, "metaKey" | "ctrlKey" | "shiftKey" | "altKey" | "key">,
) => (event.metaKey || event.ctrlKey) && event.shiftKey && !event.altKey && event.key.toLowerCase() === "u";

/** Elements that keep the key for themselves: code editors, and anything that marks itself `data-conversations-shortcut="ignore"`. */
const EDITOR_SELECTOR = [".cm-editor", ".monaco-editor", '[data-conversations-shortcut="ignore"]'].join(",");

/**
 * Whether the key should be left alone for this keydown: when a handler closer to
 * the focus already took it (`defaultPrevented`), or the focus is in a code editor
 * or in a rich-text editor outside the side panel. A plain input, textarea or the
 * panel's own composer does not use the key, so the shortcut works there.
 */
export const shouldIgnoreConversationsShortcut = (event: Pick<KeyboardEvent, "defaultPrevented" | "target">) => {
  if (event.defaultPrevented) return true;
  const target = event.target;
  if (!(target instanceof Element)) return false;
  if (target.closest("[data-side-panel]")) return false;
  return target.closest(`${EDITOR_SELECTOR},[contenteditable=""],[contenteditable="true"]`) !== null;
};

/** Cmd/Ctrl+Shift+U toggles the panel; listens on the window, in the bubble phase, like the Assistant's. */
export const useConversationsShortcut = (onToggle: () => void, enabled = true) => {
  useEffect(() => {
    if (!enabled) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (!isConversationsShortcut(event) || shouldIgnoreConversationsShortcut(event)) return;
      event.preventDefault();
      onToggle();
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [onToggle, enabled]);
};

export const conversationsShortcutHint = () => {
  try {
    return /Mac|iPhone|iPad/.test(navigator.platform) ? "⌘⇧U" : "Ctrl+Shift+U";
  } catch {
    return "Ctrl+Shift+U";
  }
};
