import { MantineProvider } from "@mantine/core";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ProjectEconomyRoute } from "./-project-economy-route";

vi.mock("@tanstack/react-router", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-router")>();
  return { ...actual, useParams: () => ({ projectId: 31 }) };
});

vi.mock("@vantigo/projects-ui/pages/project-economy", () => ({
  ProjectEconomy: ({ projectId, expensesHref }: { projectId: number; expensesHref?: string }) => (
    <div>
      economy for project {projectId} → {expensesHref ?? "no expenses link"}
    </div>
  ),
}));

const renderRoute = (modules: string[]) => {
  window.__VANTIGO_APP__ = { basePath: "/", title: "Vantigo", support: {}, modules };
  render(
    <MantineProvider env="test">
      <ProjectEconomyRoute />
    </MantineProvider>,
  );
};

describe("the project page's economy route", () => {
  afterEach(() => {
    cleanup();
    delete window.__VANTIGO_APP__;
  });

  // The projects package knows no route of the Expenses app and imports
  // nothing from it, so the host is the one that can say where the expenses
  // behind "ready to invoice" live.
  it("points the ready-to-invoice row at the project's own Expenses tab", () => {
    renderRoute(["projects", "expenses"]);

    expect(screen.getByText(/economy for project 31 → \/projects\/31\/expenses/)).toBeInTheDocument();
  });

  it("offers no link at all when the installation did not mount expenses", () => {
    renderRoute(["projects"]);

    expect(screen.getByText(/no expenses link/)).toBeInTheDocument();
  });
});
