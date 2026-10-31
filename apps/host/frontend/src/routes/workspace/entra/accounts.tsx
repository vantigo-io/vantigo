import { createFileRoute } from "@tanstack/react-router";
import { EntraAccountsTab } from "@vantigo/entra-ui";
import { SsoSettingsCard } from "../-sso-settings";

/**
 * Steps 4 to 6 and the Groups table, with identity's single sign-on card handed
 * in for steps 5 and 6 (Entra design D15): the OIDC half pre-filled with the
 * tenant's authority and the client id, then the SCIM half.
 */
const EntraAccountsRoute = () => (
  <EntraAccountsTab
    signIn={(defaults) => <SsoSettingsCard sections={["oidc"]} defaults={defaults} />}
    provisioning={() => <SsoSettingsCard sections={["scim"]} />}
  />
);

export const Route = createFileRoute("/workspace/entra/accounts")({
  component: EntraAccountsRoute,
});
