import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { renderWithProviders } from "../test/render";
import { EntraPage } from "./entra";

describe("EntraPage", () => {
  it("names the module", async () => {
    renderWithProviders(<EntraPage />);
    expect(await screen.findByRole("heading", { name: "Microsoft Entra ID" })).toBeInTheDocument();
  });
});
