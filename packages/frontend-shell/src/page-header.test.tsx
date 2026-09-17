import { MantineProvider } from "@mantine/core";
import { render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { describe, expect, it } from "vitest";
import { ShellLinkProvider } from "./link-context";
import { PageHeader } from "./page-header";

const wrap = (ui: ReactNode) => render(<MantineProvider env="test">{ui}</MantineProvider>);

describe("PageHeader", () => {
  it("renders the eyebrow, title and description", () => {
    wrap(<PageHeader eyebrow="Customers" title="All customers" description="Everyone you bill." />);

    expect(screen.getByText("Customers")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "All customers" })).toBeInTheDocument();
    expect(screen.getByText("Everyone you bill.")).toBeInTheDocument();
  });

  it("renders breadcrumbs as plain anchors without a link component, the last one as text", () => {
    wrap(<PageHeader title="Acme" breadcrumbs={[{ label: "Customers", to: "/customers" }, { label: "Acme" }]} />);

    expect(screen.getByRole("link", { name: "Customers" })).toHaveAttribute("href", "/customers");
    // The current page appears twice: as the final crumb and as the title.
    expect(screen.getAllByText("Acme")).toHaveLength(2);
    expect(screen.queryByRole("link", { name: "Acme" })).not.toBeInTheDocument();
  });

  it("renders breadcrumb links through the provided link component", () => {
    const FakeLink = ({ to, children }: { to: string; children?: ReactNode }) => <a href={`#fake${to}`}>{children}</a>;
    wrap(
      <ShellLinkProvider link={FakeLink}>
        <PageHeader title="Acme" breadcrumbs={[{ label: "Customers", to: "/customers" }, { label: "Acme" }]} />
      </ShellLinkProvider>,
    );

    expect(screen.getByRole("link", { name: "Customers" })).toHaveAttribute("href", "#fake/customers");
  });

  it("renders no eyebrow and no breadcrumb trail when neither is given", () => {
    wrap(<PageHeader title="Dashboard" />);

    expect(screen.getByRole("heading", { name: "Dashboard" })).toBeInTheDocument();
    expect(screen.queryByRole("navigation")).not.toBeInTheDocument();
  });
});
