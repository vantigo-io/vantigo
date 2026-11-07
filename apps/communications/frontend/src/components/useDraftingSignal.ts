import { type FocusEvent, useEffect, useState } from "react";

/**
 * Tells the conversation when the composer is being used to write: it has focus and text. The
 * thread announces that as presence "replying" (design L19), which guards against two people
 * answering one customer. Spread the returned handlers on the composer's root element.
 */
export function useDraftingSignal(hasText: boolean, onDraftingChange?: (drafting: boolean) => void) {
  const [focused, setFocused] = useState(false);
  const drafting = focused && hasText;
  useEffect(() => {
    onDraftingChange?.(drafting);
  }, [drafting, onDraftingChange]);
  return {
    onFocus: () => setFocused(true),
    onBlur: (event: FocusEvent<HTMLElement>) => {
      if (!event.currentTarget.contains(event.relatedTarget)) setFocused(false);
    },
  };
}
