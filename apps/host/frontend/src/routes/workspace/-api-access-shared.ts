import type { AuthorizationUser } from "../../api/authorization";
import type { ApiKey, Integration } from "../../api/integrations";

/** The colour of an integration's status (API access design K8). */
export const integrationStatusColor: Record<Integration["status"], string> = {
  active: "teal",
  disabled: "orange",
  archived: "gray",
};

/** The colour of a key's status (K12, K13). */
export const keyStatusColor: Record<ApiKey["status"], string> = {
  active: "teal",
  expired: "orange",
  revoked: "red",
};

/** A refusal's field errors, the first message of each, whichever shape the client threw it in. */
export const fieldErrorsOf = (error: unknown): Record<string, string> => {
  const fields = (error as { fields?: Record<string, string[]> } | null)?.fields ?? {};
  return Object.fromEntries(
    Object.entries(fields)
      .filter(([, messages]) => messages.length > 0)
      .map(([field, messages]) => [field, messages[0]]),
  );
};

/** A refusal's code, whichever shape the client threw it in. */
export const codeOf = (error: unknown) => (error as { code?: string } | null)?.code;

/**
 * The people an integration's contact may be (K25 mails them): a person whose
 * account is not disabled, and the contact already chosen, so it still shows.
 */
export const contactOptions = (users: AuthorizationUser[], current: string | null) =>
  users
    .filter((user) => (user.kind === "person" && !user.isDisabled) || user.id === current)
    .map((user) => ({ value: user.id, label: user.displayName || user.email || user.id }));
