import type { AgentLanguages, DisclosureWording } from "../api/types";

export type DisclosureLanguage = "nb" | "en";

/**
 * The opening line a customer reads (V11): who they are talking to, that it is
 * an AI, and for which company. Compact is the same Norwegian-first wording in
 * both languages. Names sit in guillemets, as the server words them.
 */
export const disclosureLine = (
  wording: DisclosureWording,
  language: DisclosureLanguage,
  agentName: string,
  companyName: string,
): string => {
  const a = `«${agentName}»`;
  const c = `«${companyName}»`;
  if (wording === "compact") return `${a} (KI) · virtuell assistent, ${c}.`;
  if (language === "nb") {
    return wording === "assistant"
      ? `${a} er ${c} sin virtuelle assistent (KI).`
      : `Du snakker med ${a}, en KI-assistent hos ${c}.`;
  }
  return wording === "assistant"
    ? `${a} is ${c}'s virtual assistant (AI).`
    : `You are talking to ${a}, an AI assistant at ${c}.`;
};

/** The languages the preview shows: the agent's own, or both when it follows the customer. */
export const previewLanguages = (languages: AgentLanguages): readonly DisclosureLanguage[] =>
  languages === "customer" ? ["nb", "en"] : [languages];
