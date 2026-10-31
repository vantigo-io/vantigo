import { screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { renderWithProviders } from "../test/render";
import { connected, stubEntraApi } from "../test/server";
import { EntraPage, type SignInDefaults } from "./entra";

// The four tabs have a test file each (status-tab, connection-tab, accounts-tab,
// outlook-tab). EntraPage is their deprecated composition, kept for one release.
describe("EntraPage — the deprecated composition of the four tabs", () => {
  it("names the module and draws every tab's cards, handing identity's forms to step 5 and 6", async () => {
    stubEntraApi({ connection: connected() });
    const signIn = vi.fn((defaults: SignInDefaults) => <p>sign-in form for {defaults.clientId}</p>);
    const provisioning = vi.fn(() => <p>provisioning form</p>);
    renderWithProviders(<EntraPage signIn={signIn} provisioning={provisioning} />);

    expect(await screen.findByRole("heading", { name: "Microsoft Entra ID" })).toBeInTheDocument();
    for (const name of [
      "Status",
      "Register the application",
      "Add a client secret",
      "Grant the permissions and the role",
      "Set the account rules",
      "Sign-in",
      "Provisioning",
      "Outlook and mail",
    ])
      expect(await screen.findByRole("region", { name })).toBeInTheDocument();
    expect(
      within(screen.getByRole("region", { name: "Provisioning" })).getByText("provisioning form"),
    ).toBeInTheDocument();
    expect(signIn).toHaveBeenCalled();
  });
});
