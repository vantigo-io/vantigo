/** The journal entry page's search: `copy` names a posted voucher to start from (R13). */
export interface JournalSearch {
  copy?: number;
}

export const validateJournalSearch = (search: Record<string, unknown>): JournalSearch => {
  const copy = Number(search.copy);
  return Number.isInteger(copy) && copy > 0 ? { copy } : {};
};
