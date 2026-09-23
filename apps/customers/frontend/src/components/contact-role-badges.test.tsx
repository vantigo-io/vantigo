import { MantineProvider } from "@mantine/core";
import { cleanup, render, screen } from "@testing-library/react";
import { setLanguagePreference } from "@vantigo/frontend-shell";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { ContactRoleBadges } from "./contact-role-badges";
import "../i18n";

const renderBadges = (roles: { role: string; primary: boolean }[]) =>
  render(
    <MantineProvider>
      <ContactRoleBadges roles={roles} />
    </MantineProvider>,
  );

describe("ContactRoleBadges", () => {
  // The language is a module-level preference, so it is reset between cases the
  // way legal-badges.test.tsx resets it — otherwise the nb case below leaks into
  // whatever runs after it.
  beforeEach(() => setLanguagePreference("auto"));
  afterEach(cleanup);

  it("renders one badge per role with its catalog label", () => {
    renderBadges([
      { role: "billing", primary: true },
      { role: "decision_maker", primary: false },
    ]);

    expect(screen.getByText("Billing")).toBeInTheDocument();
    expect(screen.getByText("Decision maker")).toBeInTheDocument();
  });

  it("marks the primary role and says so where a reader can find it", () => {
    renderBadges([
      { role: "billing", primary: true },
      { role: "project", primary: false },
    ]);

    // The star is decoration; the sentence is the accessible answer, so it is
    // the one asserted. Two badges, one of them labelled.
    expect(screen.getByLabelText("Primary billing contact")).toBeInTheDocument();
    expect(screen.queryByLabelText("Primary project contact")).not.toBeInTheDocument();
  });

  it("localizes the labels and the primary sentence", async () => {
    // The nb catalog is not proved by translations:check, which only proves the
    // two catalogs have the same keys — this proves the Norwegian strings are
    // the ones that actually render, including the closed-compound primary
    // sentence billing and decision_maker each read from their own composed
    // catalog key (see `primaryContactLabel`) rather than an interpolated
    // template — the unrecognised-role case below covers that template
    // instead. setLanguagePreference is frontend-shell's own seam, used
    // exactly as legal-badges.test.tsx uses it.
    setLanguagePreference("nb");
    renderBadges([
      { role: "billing", primary: true },
      { role: "decision_maker", primary: false },
    ]);

    expect(await screen.findByText("Faktura")).toBeInTheDocument();
    expect(screen.getByText("Beslutningstaker")).toBeInTheDocument();
    // A closed compound ("fakturakontakt"), not an interpolated
    // "Faktura-kontakt" — see `primaryContactLabel`.
    expect(screen.getByLabelText("Primær fakturakontakt")).toBeInTheDocument();
  });

  it("labels an unrecognised role with the generic interpolated sentence", async () => {
    // A role outside the vocabulary has no composed catalog key (there is no
    // way to pre-write one for a name the catalog has never seen), so it falls
    // back to the interpolated template — the fallback `primaryContactLabel`
    // and `contactRoleLabel` share with `addressTypeLabel`.
    renderBadges([{ role: "executive_sponsor", primary: true }]);

    expect(screen.getByText("executive_sponsor")).toBeInTheDocument();
    expect(screen.getByLabelText("Primary executive_sponsor contact")).toBeInTheDocument();
  });

  it("renders no badge at all for a contact with no roles", () => {
    // Not `toBeEmptyDOMElement`: MantineProvider always injects a <style>
    // element of its own, so the container is never literally empty. What the
    // component promises is that it contributes nothing, which is "no badge".
    const { container } = renderBadges([]);
    expect(container.querySelector(".mantine-Badge-root")).toBeNull();
    expect(screen.queryByText("Billing")).not.toBeInTheDocument();
    expect(screen.queryByText("Project")).not.toBeInTheDocument();
    expect(screen.queryByText("Decision maker")).not.toBeInTheDocument();
  });
});
