import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import { TaxCategoriesPage } from "./tax-categories";

const endpoint = "/api/v1/products/tax-categories";
const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const categories = [{ id: 1, name: "Standard", kind: "Standard" as const, rate: 0.25 }];

const stubTaxCategoriesFetch = (deleteResponse = new Response(null, { status: 204 })) =>
  stubFetch((url: RequestInfo | URL, init?: RequestInit) => {
    if (String(url) === endpoint && (init?.method ?? "GET") === "GET")
      return Promise.resolve(jsonResponse(200, categories));
    if (String(url) === `${endpoint}/1` && init?.method === "DELETE") return Promise.resolve(deleteResponse);
    return Promise.resolve(new Response(null, { status: 404 }));
  });

const renderPage = () =>
  render(
    <MantineProvider>
      <QueryClientProvider
        client={new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })}
      >
        <TaxCategoriesPage />
      </QueryClientProvider>
    </MantineProvider>,
  );

describe("TaxCategoriesPage", () => {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it("renders tax categories and converts fractional rates to percentages", async () => {
    stubTaxCategoriesFetch();
    renderPage();

    expect((await screen.findAllByText("Standard")).length).toBeGreaterThan(0);
    expect(screen.getByRole("cell", { name: "25.00%" })).toBeInTheDocument();
  });

  it("shows the friendly message when deleting an in-use category returns 409", async () => {
    stubTaxCategoriesFetch(jsonResponse(409, { detail: "Tax category is referenced by products." }));
    renderPage();

    await screen.findAllByText("Standard");
    fireEvent.click(screen.getByRole("button", { name: "Delete Standard" }));

    expect(await screen.findByText("This tax category is in use and cannot be deleted.")).toBeInTheDocument();
  });
});
