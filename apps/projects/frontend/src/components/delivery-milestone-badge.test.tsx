import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { renderWithProviders } from "../test/render";
import { DeliveryMilestoneBadge } from "./delivery-milestone-badge";

describe("DeliveryMilestoneBadge", () => {
  it("renders milestone name with open diamond icon", () => {
    renderWithProviders(<DeliveryMilestoneBadge milestone={{ id: 1, name: "Release Beta" }} />);
    expect(screen.getByText("Release Beta")).toBeInTheDocument();
  });

  it("renders completed milestone with teal style", () => {
    renderWithProviders(<DeliveryMilestoneBadge milestone={{ id: 2, name: "Release GA" }} completed />);
    expect(screen.getByText("Release GA")).toBeInTheDocument();
  });
});
