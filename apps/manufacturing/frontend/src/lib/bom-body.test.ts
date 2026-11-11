import { describe, expect, it } from "vitest";
import type { ManufacturingBom } from "../api/boms";
import type { ManufacturingVariantRef } from "../api/variants";
import {
  bomCreateBody,
  bomUpdateBody,
  editorOf,
  editorProblems,
  emptyEditor,
  newLine,
  newOperation,
  removeOperation,
} from "./bom-body";

const variant = (id: number, sku: string): ManufacturingVariantRef => ({
  id,
  sku,
  productName: sku,
  unit: "pcs",
  productType: "goods",
  productStatus: "active",
});

const STAMP = "2026-11-11T09:00:00Z";

const savedBom = (): ManufacturingBom => ({
  id: 7,
  variant: variant(1, "TABLE"),
  code: "Rev B",
  outputQuantity: "1",
  isDefault: true,
  makeToOrder: false,
  leadTimeDays: 2,
  note: "",
  status: "active",
  revision: 4,
  siteId: 1,
  createdAt: STAMP,
  updatedAt: STAMP,
  operations: [
    {
      id: 31,
      key: "31",
      sequence: 20,
      name: "Paint",
      workCenterId: 2,
      workCenterCode: "PAINT",
      workCenterName: "Paint booth",
      setupMinutes: null,
      minutesPerCycle: "12.5",
      instructions: "",
    },
    {
      id: 30,
      key: "30",
      sequence: 10,
      name: "Cut",
      workCenterId: 1,
      workCenterCode: "SAW",
      workCenterName: "Saw",
      setupMinutes: "15",
      minutesPerCycle: "4",
      instructions: "**Measure twice**",
    },
  ],
  lines: [
    {
      id: 51,
      variant: variant(3, "PAINT-W"),
      quantity: "0.25",
      scrapPercent: "5",
      operationId: 31,
      operationKey: "31",
      sortOrder: 20,
      note: "",
    },
    {
      id: 50,
      variant: variant(2, "BOARD"),
      quantity: "4",
      scrapPercent: "0",
      operationId: 30,
      operationKey: "30",
      sortOrder: 10,
      note: "Oak",
    },
  ],
});

describe("the BoM editor's client keys", () => {
  it("keys a new operation with a generated key and sends it without an id", () => {
    const state = emptyEditor(variant(1, "TABLE"));
    const first = newOperation(state.operations);
    const second = newOperation([first]);
    expect(first.key).toMatch(/^new-\d+$/);
    expect(second.key).not.toBe(first.key);
    expect([first.sequence, second.sequence]).toEqual([10, 20]);
    const body = bomCreateBody({
      ...state,
      operations: [
        { ...first, name: "Cut", workCenterId: 1, minutesPerCycle: "4" },
        { ...second, name: "Paint", workCenterId: 2, minutesPerCycle: "2,5", setupMinutes: "10" },
      ],
    });
    expect(body.operations).toEqual([
      { key: first.key, sequence: 10, name: "Cut", workCenterId: 1, minutesPerCycle: "4", instructions: "" },
      {
        key: second.key,
        sequence: 20,
        name: "Paint",
        workCenterId: 2,
        setupMinutes: "10",
        minutesPerCycle: "2.5",
        instructions: "",
      },
    ]);
    expect(body.operations.every((operation) => !("id" in operation))).toBe(true);
  });

  it("keys a saved operation by its server key and sends its id, in sequence order", () => {
    const state = editorOf(savedBom());
    expect(state.operations.map((operation) => [operation.key, operation.id])).toEqual([
      ["30", 30],
      ["31", 31],
    ]);
    const body = bomUpdateBody(state, 4);
    expect(body.revision).toBe(4);
    expect(body.operations.map((operation) => [operation.key, operation.id])).toEqual([
      ["30", 30],
      ["31", 31],
    ]);
    expect(body.operations[0]).toMatchObject({ setupMinutes: "15", instructions: "**Measure twice**" });
    // The work center's setup applies: nothing is sent.
    expect("setupMinutes" in body.operations[1]).toBe(false);
    expect("variantId" in body).toBe(false);
    expect("isDefault" in body).toBe(false);
  });

  it("maps each line to its operation's key, new and saved alike", () => {
    const state = editorOf(savedBom());
    const added = newOperation(state.operations);
    const line = { ...newLine(state.lines, variant(4, "SCREW")), quantity: "8", operationKey: added.key };
    const body = bomUpdateBody(
      {
        ...state,
        operations: [...state.operations, { ...added, name: "Assemble", workCenterId: 1, minutesPerCycle: "6" }],
        lines: [...state.lines, line],
      },
      4,
    );
    expect(body.lines).toEqual([
      { variantId: 2, quantity: "4", scrapPercent: "0", operationKey: "30", sortOrder: 10, note: "Oak" },
      { variantId: 3, quantity: "0.25", scrapPercent: "5", operationKey: "31", sortOrder: 20, note: "" },
      { variantId: 4, quantity: "8", scrapPercent: "0", operationKey: added.key, sortOrder: 30, note: "" },
    ]);
  });

  it("clears the operation of every line that named a removed operation", () => {
    const state = removeOperation(editorOf(savedBom()), "31");
    expect(state.operations.map((operation) => operation.key)).toEqual(["30"]);
    expect(state.lines.map((line) => line.operationKey)).toEqual(["30", null]);
    expect(bomUpdateBody(state, 4).lines.map((line) => line.operationKey)).toEqual(["30", null]);
  });

  it("never sends a key no operation has", () => {
    const state = editorOf(savedBom());
    const stale = { ...state, lines: [{ ...state.lines[0], operationKey: "gone" }] };
    expect(bomUpdateBody(stale, 4).lines[0].operationKey).toBeNull();
  });

  it("sends a new BoM's variant and default, and an empty code as null", () => {
    const state = emptyEditor(variant(1, "TABLE"));
    const body = bomCreateBody({ ...state, header: { ...state.header, isDefault: true, outputQuantity: "2,5" } });
    expect(body).toMatchObject({ variantId: 1, isDefault: true, code: null, outputQuantity: "2.5", leadTimeDays: 0 });
  });
});

describe("what the editor tells before it sends", () => {
  it("names the fields by the server's paths", () => {
    const state = emptyEditor();
    const operation = newOperation([]);
    const line = newLine([]);
    expect(
      editorProblems({
        ...state,
        header: { ...state.header, outputQuantity: "1.2345" },
        operations: [{ ...operation, minutesPerCycle: "0" }],
        lines: [{ ...line, quantity: "-1", scrapPercent: "100" }],
      }),
    ).toEqual({
      variantId: "required",
      outputQuantity: "quantity",
      "operations[0].name": "required",
      "operations[0].workCenterId": "required",
      "operations[0].minutesPerCycle": "positive",
      "lines[0].variantId": "required",
      "lines[0].quantity": "quantity",
      "lines[0].scrapPercent": "scrap",
    });
  });

  it("has nothing to say of a complete BoM", () => {
    expect(editorProblems(editorOf(savedBom()))).toEqual({});
  });
});
