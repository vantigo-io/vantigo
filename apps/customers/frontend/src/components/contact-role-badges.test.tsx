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
    // the ones that actually render, including the interpolated role name inside
    // the primary sentence, which is the one string a missing placeholder would
    // break silently. setLanguagePreference is frontend-shell's own seam, used
    // exactly as legal-badges.test.tsx uses it.
    setLanguagePreference("nb");
    renderBadges([
      { role: "billing", primary: true },
      { role: "decision_maker", primary: false },
    ]);

    expect(await screen.findByText("Faktura")).toBeInTheDocument();
    expect(screen.getByText("Beslutningstaker")).toBeInTheDocument();
    expect(screen.getByLabelText("Primær faktura-kontakt")).toBeInTheDocument();
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
