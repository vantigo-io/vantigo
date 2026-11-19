import { appUrl } from "@vantigo/frontend-shell";
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

/** The MCP endpoint's address on this installation, as the curl hint builds it (MCP server design M33). */
export const mcpEndpoint = () => `${window.location.origin}${appUrl("/api/mcp")}`;

/**
 * How a client connects (M16): the Claude Code command, the mcpServers block
 * Cursor and most clients read, and the servers block VS Code reads, carrying
 * key — the placeholder vtg_… on the Overview, the new secret in the key
 * dialog, which is the one place it is shown.
 */
export const mcpSnippets = (url: string, name: string, key = "vtg_…") => {
  const server =
    name
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, "-")
      .replace(/^-+|-+$/g, "") || "vantigo";
  const authorization = `Bearer ${key}`;
  return {
    claude: `claude mcp add --transport http ${server} ${url} --header "Authorization: ${authorization}"`,
    json: JSON.stringify(
      { mcpServers: { [server]: { type: "http", url, headers: { Authorization: authorization } } } },
      null,
      2,
    ),
    vscode: JSON.stringify(
      { servers: { [server]: { type: "http", url, headers: { Authorization: authorization } } } },
      null,
      2,
    ),
  };
};
