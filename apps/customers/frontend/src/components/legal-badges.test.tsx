import { MantineProvider } from "@mantine/core";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { setLanguagePreference } from "@vantigo/frontend-shell";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { CopyableBadge, LegalCountryBadge, LegalSourceBadge, LegalTypeBadge, LegalValueBadge } from "./legal-badges";

const renderBadge = (children: ReactNode) => render(<MantineProvider>{children}</MantineProvider>);

afterEach(() => {
  setLanguagePreference("auto");
});

describe("LegalTypeBadge", () => {
  it.each([["business"], ["person"]])("capitalizes the %s type and shows an icon", (type) => {
    const { container } = renderBadge(<LegalTypeBadge type={type} />);

    expect(screen.getByText(type.charAt(0).toUpperCase() + type.slice(1))).toBeInTheDocument();
    expect(container.querySelector("svg")).toBeInTheDocument();
  });

  it("degrades gracefully for unknown types", () => {
    const { container } = renderBadge(<LegalTypeBadge type="charity" />);

    expect(screen.getByText("Charity")).toBeInTheDocument();
    expect(container.querySelector("svg")).not.toBeInTheDocument();
  });
});

describe("LegalValueBadge", () => {
  it("shows the value with the legal type's icon", () => {
    const { container } = renderBadge(<LegalValueBadge type="business">ACME AS</LegalValueBadge>);

    expect(screen.getByText("ACME AS")).toBeInTheDocument();
    expect(container.querySelector("svg")).toBeInTheDocument();
  });
});

describe("LegalCountryBadge", () => {
  it("shows the uppercase country code with its flag", () => {
    const { container } = renderBadge(<LegalCountryBadge country="no" />);

    expect(screen.getByText("NO")).toBeInTheDocument();
    expect(container.querySelector("svg")).toBeInTheDocument();
  });

  it("degrades gracefully for unknown country codes", () => {
    const { container } = renderBadge(<LegalCountryBadge country="zz" />);

    expect(screen.getByText("ZZ")).toBeInTheDocument();
    expect(container.querySelector("svg")).not.toBeInTheDocument();
  });
});

describe("LegalSourceBadge", () => {
  it("shows the Brreg logo linked to their website", () => {
    renderBadge(<LegalSourceBadge source="brreg" />);

    const link = screen.getByRole("link", { name: "Brønnøysundregistrene" });
    expect(link).toHaveAttribute("href", "https://www.brreg.no");
    expect(screen.getByAltText("Brønnøysundregistrene")).toBeInTheDocument();
  });

  it("deep-links the Brreg logo to the entity page when a legal id is given", () => {
    renderBadge(<LegalSourceBadge source="brreg" legalId="923609016" />);

    expect(screen.getByRole("link", { name: "Brønnøysundregistrene" })).toHaveAttribute(
      "href",
      "https://virksomhet.brreg.no/nb/oppslag/enheter/923609016",
    );
  });

  it("shows a manual entry badge without a logo", () => {
    renderBadge(<LegalSourceBadge source="manual" />);

    expect(screen.getByText("Manual entry")).toBeInTheDocument();
    expect(screen.queryByRole("link")).not.toBeInTheDocument();
  });

  it("localizes the manual entry label", async () => {
    setLanguagePreference("nb");
    renderBadge(<LegalSourceBadge source="manual" />);

    expect(await screen.findByText("Manuell registrering")).toBeInTheDocument();
  });

  it("falls back to the raw code for unknown sources", () => {
    renderBadge(<LegalSourceBadge source="futurereg" />);

    expect(screen.getByText("futurereg")).toBeInTheDocument();
  });
});

describe("CopyableBadge", () => {
  it("copies the value on click and confirms it in the tooltip", async () => {
    const writeText = vi.spyOn(navigator.clipboard, "writeText");

    renderBadge(
      <CopyableBadge tooltip="The customer id." copyValue="1001">
        #1001
      </CopyableBadge>,
    );

    const badge = screen.getByRole("button", { name: "#1001" });
    await userEvent.click(badge);

    expect(writeText).toHaveBeenCalledWith("1001");
    expect(await screen.findByText("Copied!")).toBeInTheDocument();
  });

  it("renders as a non-interactive badge without a copy value", () => {
    renderBadge(<CopyableBadge tooltip="Info only.">Plain</CopyableBadge>);

    expect(screen.queryByRole("button")).not.toBeInTheDocument();
    expect(screen.getByText("Plain")).toBeInTheDocument();
  });
});

describe("copyable legal badges", () => {
  it("copies the legal value on click", async () => {
    const writeText = vi.spyOn(navigator.clipboard, "writeText");

    renderBadge(
      <LegalValueBadge type="business" copyable>
        923609016
      </LegalValueBadge>,
    );

    await userEvent.click(screen.getByRole("button", { name: "923609016" }));

    expect(writeText).toHaveBeenCalledWith("923609016");
  });

  it("copies the country code on click", async () => {
    const writeText = vi.spyOn(navigator.clipboard, "writeText");

    renderBadge(<LegalCountryBadge country="no" copyable />);

    await userEvent.click(screen.getByRole("button", { name: "NO" }));

    expect(writeText).toHaveBeenCalledWith("NO");
  });
});
