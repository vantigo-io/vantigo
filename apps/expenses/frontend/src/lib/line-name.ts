import { useI18n } from "@vantigo/frontend-shell";
import type { Expense } from "../api/entries";
import "../i18n";
import { perDiemTypeLabelKey } from "./per-diem";

/**
 * What one line of a travel claim is called.
 *
 * A per diem day carries no description of its own — the server stores the
 * empty string and the contract says the client names it — so what the day
 * *is* names it, in the reader's own language. Every other line is called
 * what its owner wrote.
 */
export const useLineName = () => {
  const { t } = useI18n("expenses");
  return (line: Expense): string =>
    line.kind === "per_diem" && line.perDiem ? t(perDiemTypeLabelKey(line.perDiem.type)) : line.description;
};
