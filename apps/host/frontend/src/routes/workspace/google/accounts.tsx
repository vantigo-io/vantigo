import { createFileRoute } from "@tanstack/react-router";
import { GoogleAccountsTab } from "@vantigo/google-ui";
import { SsoSettingsCard } from "../-sso-settings";

/**
 * The account rules, the licences, and the users and groups the module keeps for Staff, with
 * identity's single sign-on card handed in for the Sign-in and Provisioning steps (Google Workspace
 * design G24): the OIDC half pre-filled with Google, the Internal web client and the organisation's
 * verified domains, then the SCIM half.
 */
const GoogleAccountsRoute = () => (
  <GoogleAccountsTab
    signIn={(defaults) => <SsoSettingsCard sections={["oidc"]} defaults={defaults} />}
    provisioning={() => <SsoSettingsCard sections={["scim"]} />}
  />
);

export const Route = createFileRoute("/workspace/google/accounts")({
  component: GoogleAccountsRoute,
});
