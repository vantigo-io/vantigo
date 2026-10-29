import { cleanup, render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { Route as CalendarSettingsRoute } from "./settings";

// The route's component is code-split (the router plugin runs under Vitest too), so it is preloaded.
// The calendar's page and Entra's button are their packages' own, tested there;
// here they only show whether the host fills the page's connection.button slot.
vi.mock("@vantigo/calendar-ui", () => ({
  SettingsPage: ({
    connectionButton,
  }: {
    connectionButton?: (props: { feature: "calendar"; returnTo: string }) => ReactNode;
  }) => (
    <div>
      {connectionButton ? connectionButton({ feature: "calendar", returnTo: "/calendar/settings" }) : "no slot"}
    </div>
  ),
}));
vi.mock("@vantigo/entra-ui", () => ({
  OutlookConnectButton: ({ feature, returnTo }: { feature: string; returnTo: string }) => (
    <p>
      Entra's button for {feature}, back to {returnTo}
    </p>
  ),
}));

const renderPage = async (modules: string[]) => {
  window.__VANTIGO_APP__ = { basePath: "/", title: "Vantigo", support: {}, modules };
  const Page = CalendarSettingsRoute.options.component as (() => ReactNode) & { preload?: () => Promise<void> };
  await Page.preload?.();
  render(<Page />);
};

afterEach(() => {
  cleanup();
  delete window.__VANTIGO_APP__;
});

describe("My calendar's connection.button slot", () => {
  it("is filled with Entra's button, handed the slot's props, while the Entra module is on", async () => {
    await renderPage(["calendar", "entra"]);
    expect(await screen.findByText("Entra's button for calendar, back to /calendar/settings")).toBeInTheDocument();
  });

  it("is left empty while the Entra module is off", async () => {
    await renderPage(["calendar"]);
    expect(await screen.findByText("no slot")).toBeInTheDocument();
  });
});
