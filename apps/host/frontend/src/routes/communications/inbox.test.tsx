import { cleanup, render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { Route as CommunicationsInboxRoute } from "./inbox";

vi.mock("@vantigo/communications-ui/pages/inbox", () => ({
  InboxPage: ({
    connectionButton,
  }: {
    connectionButton?: (props: { feature: "mail"; returnTo: string }) => ReactNode;
  }) => (
    <div>{connectionButton ? connectionButton({ feature: "mail", returnTo: "/communications/inbox" }) : "no slot"}</div>
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
  const Page = CommunicationsInboxRoute.options.component as (() => ReactNode) & { preload?: () => Promise<void> };
  await Page.preload?.();
  render(<Page />);
};

afterEach(() => {
  cleanup();
  delete window.__VANTIGO_APP__;
});

describe("Communications inbox connection.button slot", () => {
  it("is filled with Entra's button, handed the slot's props, while the Entra module is on", async () => {
    await renderPage(["communications", "entra"]);
    expect(await screen.findByText("Entra's button for mail, back to /communications/inbox")).toBeInTheDocument();
  });

  it("is left empty while the Entra module is off", async () => {
    await renderPage(["communications"]);
    expect(await screen.findByText("no slot")).toBeInTheDocument();
  });
});
