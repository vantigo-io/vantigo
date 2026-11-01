import { useEffect } from "react";

/**
 * Elements that keep Cmd/Ctrl+J for themselves: rich-text editors
 * (`contenteditable`), the code editors the host may embed (CodeMirror and
 * Monaco) and anything that marks itself with
 * `data-assistant-shortcut="ignore"`.
 */
const EDITOR_SELECTOR = [
  '[contenteditable=""]',
  '[contenteditable="true"]',
  ".cm-editor",
  ".monaco-editor",
  '[data-assistant-shortcut="ignore"]',
].join(",");

/**
 * Whether Cmd/Ctrl+J should be left alone for this keydown. It is when the
 * focus is in an editor that uses the key (see `EDITOR_SELECTOR`), and when a
 * handler closer to the focus already took the key (`defaultPrevented`: the
 * rule for a textarea with its own handler). A plain input or textarea does
 * not use the key, so the shortcut works there, the assistant's own composer
 * included.
 */
export const shouldIgnoreAssistantShortcut = (event: Pick<KeyboardEvent, "defaultPrevented" | "target">) => {
  if (event.defaultPrevented) return true;
  const target = event.target;
  if (!(target instanceof Element)) return false;
  if (target.closest("[data-assistant-panel]")) return false;
  return target.closest(EDITOR_SELECTOR) !== null;
};

export const isAssistantShortcut = (event: KeyboardEvent) =>
  (event.metaKey || event.ctrlKey) && !event.shiftKey && !event.altKey && event.key.toLowerCase() === "j";

/**
 * Cmd/Ctrl+J toggles the panel. The listener is on the window in the bubble
 * phase, so a handler on the focused element runs first and may keep the key
 * by calling `preventDefault`. When it does act it prevents the browser's own
 * use of the key (Chrome opens its downloads).
 */
export const useAssistantShortcut = (onToggle: () => void, enabled = true) => {
  useEffect(() => {
    if (!enabled) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (!isAssistantShortcut(event) || shouldIgnoreAssistantShortcut(event)) return;
      event.preventDefault();
      onToggle();
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [onToggle, enabled]);
};
