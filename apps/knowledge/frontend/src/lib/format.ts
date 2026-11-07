import { useI18n } from "@vantigo/frontend-shell";
import type { KnowledgeActor } from "../api/articles";

/** The package's words and its date formatting, with a former user named as such (K17). */
export const useKnowledgeFormat = () => {
  const { t, formatters } = useI18n("knowledge");
  return {
    t,
    dateTime: (instant: string) =>
      formatters.formatDate(new Date(instant), { dateStyle: "medium", timeStyle: "short" }),
    date: (instant: string) => formatters.formatDate(new Date(instant), { dateStyle: "medium" }),
    actor: (actor: KnowledgeActor | null | undefined) => actor?.displayName ?? t("formerUser"),
  };
};
