const REVIEW_LINK = /\/agents\/review\/([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})/i;

/** The review item an agent's note points at (virtual employees design V20), or null for any other text. */
export const reviewItemIn = (text: string): { before: string; id: string } | null => {
  const match = REVIEW_LINK.exec(text);
  return match && match.index !== undefined
    ? { before: text.slice(0, match.index).replace(/[\s:]+$/, ""), id: match[1] }
    : null;
};
