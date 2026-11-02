import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { renderWithProviders } from "../test/render";
import { Markdown } from "./markdown";

describe("Markdown", () => {
  it("renders GitHub tables as tables", () => {
    renderWithProviders(<Markdown>{"| Customer | Overdue |\n| --- | --- |\n| Acme | 3 |\n| Nordic | 1 |"}</Markdown>);
    expect(screen.getByRole("table")).toBeInTheDocument();
    expect(screen.getByRole("columnheader", { name: "Customer" })).toBeInTheDocument();
    expect(screen.getByRole("cell", { name: "Nordic" })).toBeInTheDocument();
  });

  it("never renders raw HTML, and drops images", () => {
    const { container } = renderWithProviders(
      <Markdown>
        {
          'Hello <script>alert(1)</script> <b onclick="x()">bold</b>\n\n<img src="https://evil.example/p.png">\n\n![chart](https://evil.example/leak?d=1)'
        }
      </Markdown>,
    );
    expect(container.querySelector("script")).toBeNull();
    expect(container.querySelector("img")).toBeNull();
    expect(container.querySelector("b")).toBeNull();
    expect(container.querySelector("[onclick]")).toBeNull();
  });

  it("opens an in-app link through the host and leaves the page alone", async () => {
    const onNavigate = vi.fn();
    renderWithProviders(<Markdown onNavigate={onNavigate}>{"See [Acme](/customers/42) now."}</Markdown>);
    await userEvent.click(screen.getByRole("link", { name: "Acme" }));
    expect(onNavigate).toHaveBeenCalledWith("/customers/42");
  });

  it("opens the documentation in a new tab", () => {
    renderWithProviders(<Markdown>{"[Docs](/docs/en/user/energy/#add)"}</Markdown>);
    const docs = screen.getByRole("link", { name: "Docs" });
    expect(docs).toHaveAttribute("target", "_blank");
    expect(docs).toHaveAttribute("rel", expect.stringContaining("noopener"));
  });

  it("links only the app's own routes and the documentation; every other address is text with the address shown", () => {
    const { container } = renderWithProviders(
      <Markdown>
        {
          "[Web](https://example.com/x) [Mail](mailto:a@example.com) [Call](tel:+4712345678) [Bad](javascript:alert(1)) [Odd](//evil.example/x) [Unknown](/admin/secrets) [Up](/customers/../admin) [Rel](customers/42) [Ok](/customers/42)"
        }
      </Markdown>,
    );
    for (const name of ["Web", "Mail", "Call", "Bad", "Odd", "Unknown", "Up", "Rel"])
      expect(screen.queryByRole("link", { name })).not.toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Ok" })).toHaveAttribute("href", "/customers/42");
    // The person sees where each of them pointed.
    const shown = [...container.querySelectorAll("[data-blocked-link] code")].map((one) => one.textContent);
    expect(shown).toEqual([
      "https://example.com/x",
      "mailto:a@example.com",
      "tel:+4712345678",
      "javascript:alert(1)",
      "//evil.example/x",
      "/admin/secrets",
      "/customers/../admin",
      "customers/42",
    ]);
  });
});
