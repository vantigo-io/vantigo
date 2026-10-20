import { createFileRoute } from "@tanstack/react-router";
import { EntraPage } from "@vantigo/entra-ui";
import { SsoSettingsCard } from "../workspace/-sso-settings";

/**
 * The Entra module's page, with identity's single sign-on card handed in for
 * the wizard's steps 5 and 6 (Entra design D15): the OIDC half pre-filled
 * with the tenant's authority and the client id, then the SCIM half.
 */
const EntraRoute = () => (
  <EntraPage
    signIn={(defaults) => <SsoSettingsCard sections={["oidc"]} defaults={defaults} />}
    provisioning={() => <SsoSettingsCard sections={["scim"]} />}
  />
);

export const Route = createFileRoute("/entra/")({
  component: EntraRoute,
});
