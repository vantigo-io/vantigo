/** The last day of a month typed as `2026-03`. */
export const monthEndOf = (month: string): string | null => {
  const match = /^(\d{4})-(\d{2})$/.exec(month);
  if (!match) return null;
  const last = new Date(Date.UTC(Number(match[1]), Number(match[2]), 0));
  return last.toISOString().slice(0, 10);
};
