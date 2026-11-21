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

/** The environment variable the snippets read the key from (S4). */
export const mcpKeyVariable = "VANTIGO_API_KEY";

/** The id of VS Code's prompt for the key. */
const vscodeKeyInput = "vantigo-key";

/**
 * How a client connects (M16, S4): the Claude Code command, the Cursor block
 * and the VS Code block. None carries a key, so none can be committed with
 * one: the command reads $VANTIGO_API_KEY from the shell when it is run (and
 * Claude Code keeps it in the user's own ~/.claude.json), Cursor reads
 * ${env:VANTIGO_API_KEY} when it starts the server, and VS Code asks for the
 * key once (${input:…}, a password prompt) and keeps it in its secret
 * storage. The key itself is shown only in the new key's Copy field.
 */
export const mcpSnippets = (url: string, name: string) => {
  const server =
    name
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, "-")
      .replace(/^-+|-+$/g, "") || "vantigo";
  const reference = (expression: string) => `Bearer ${"$"}{${expression}}`;
  return {
    claude: `claude mcp add --transport http ${server} ${url} --header "Authorization: Bearer $${mcpKeyVariable}"`,
    // With no key: Claude Code finds the sign-in itself (OAuth connections design C22).
    claudeSignIn: `claude mcp add --transport http ${server} ${url}`,
    json: JSON.stringify(
      { mcpServers: { [server]: { url, headers: { Authorization: reference(`env:${mcpKeyVariable}`) } } } },
      null,
      2,
    ),
    vscode: JSON.stringify(
      {
        inputs: [{ type: "promptString", id: vscodeKeyInput, description: "Vantigo API key", password: true }],
        servers: {
          [server]: { type: "http", url, headers: { Authorization: reference(`input:${vscodeKeyInput}`) } },
        },
      },
      null,
      2,
    ),
  };
};
