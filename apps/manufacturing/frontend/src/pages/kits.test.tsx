import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { bomCreateBody, editorProblems, emptyEditor, newOperation, newOutput } from "../lib/bom-body";
import { sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { bomFixture, stubManufacturingApi } from "../test/server";

// Kits (manufacturing phase 2 M144): a kit BoM has lines only.

const ref = (id: number, sku: string) => ({
  id,
  sku,
  productName: sku,
  unit: "pcs",
  productType: "goods",
  productStatus: "active",
});

describe("a kit BoM", () => {
  it("sends its kind and lines alone, whatever else the editor still holds", () => {
    const state = emptyEditor(ref(1, "TOOLBOX"));
    state.header = { ...state.header, kind: "kit", makeToOrder: true };
    state.operations = [newOperation([])];
    state.outputs = [newOutput(ref(3, "OFFCUT"))];
    expect(editorProblems(state)).toEqual({});
    expect(bomCreateBody(state)).toMatchObject({ kind: "kit", makeToOrder: false, operations: [], outputs: [] });
  });

  it("shows a saved kit without operations or by-products, its kind fixed", async () => {
    const fetchMock = stubManufacturingApi({ boms: [bomFixture({ kind: "kit", operations: [], siteId: null })] });
    renderRoute("/manufacturing/boms/7");
    expect(await screen.findAllByText("Kit")).not.toHaveLength(0);
    expect(screen.queryByText("Operations")).not.toBeInTheDocument();
    expect(screen.queryByText("By-products")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Make to order")).not.toBeInTheDocument();
    expect(screen.getAllByLabelText("Kind").find((one) => one.tagName === "INPUT")).toBeDisabled();
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(sent(fetchMock, "PUT", /\/boms\/7$/)).toHaveLength(1));
    const [{ body }] = sent(fetchMock, "PUT", /\/boms\/7$/);
    expect(body).toMatchObject({ operations: [], outputs: [], makeToOrder: false });
    expect(body).not.toHaveProperty("kind");
  });
});
