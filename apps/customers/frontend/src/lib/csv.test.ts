import { describe, expect, it } from "vitest";
import { csvCell, failedRowsCsv, parseCsv } from "./csv";

describe("parseCsv", () => {
  it("reads the export's form — BOM, semicolons, CRLF, quotes — and numbers rows as the server does", () => {
    const table = parseCsv('\ufeffname;phone\r\n"Fjord; ""Nord"" AS";\'+47 22\r\n;\r\n"to\r\nlinjer";1\r\n');
    expect(table.header).toEqual(["name", "phone"]);
    // The all-blank record takes no number, exactly as readCSVFile skips it.
    expect(table.records).toEqual([
      { row: 1, cells: ['Fjord; "Nord" AS', "'+47 22"] },
      { row: 2, cells: ["to\r\nlinjer", "1"] },
    ]);
  });

  it("skips an empty line before the header, as Go's encoding/csv does", () => {
    const table = parseCsv("\r\nname\r\nA\r\n");
    expect(table.header).toEqual(["name"]);
    expect(table.records).toEqual([{ row: 1, cells: ["A"] }]);
  });

  it("takes LF, no BOM, and skips empty lines", () => {
    expect(parseCsv("name\nA\n\nB").records).toEqual([
      { row: 1, cells: ["A"] },
      { row: 2, cells: ["B"] },
    ]);
  });
});

describe("csvCell", () => {
  it("guards a formula and quotes what would end the cell", () => {
    expect(csvCell("=SUM(A1)")).toBe("'=SUM(A1)");
    expect(csvCell("a;b")).toBe('"a;b"');
    expect(csvCell('say "hi"')).toBe('"say ""hi"""');
    expect(csvCell("'+47 22")).toBe("'+47 22");
    expect(csvCell("Fjord AS")).toBe("Fjord AS");
  });
});

describe("failedRowsCsv", () => {
  const table = parseCsv("\ufeffname;email\r\nOk AS;ok@x.no\r\nFeil AS;nei\r\nKort AS\r\n");

  it("keeps only the rows that failed, as they were, with an error column appended", () => {
    const csv = failedRowsCsv(table, [
      { row: 2, column: "email", message: "An email address must look like name@example.com, but was 'nei'" },
      { row: 3, column: null, message: "This row has 1 cells, but the header has 2" },
      { row: 2, column: "name", message: "Second problem" },
    ]);
    expect(csv).toBe(
      "\ufeffname;email;error\r\n" +
        "Feil AS;nei;email: An email address must look like name@example.com, but was 'nei' | name: Second problem\r\n" +
        "Kort AS;;This row has 1 cells, but the header has 2\r\n",
    );
  });

  it("keeps every cell of a row longer than the header, the error after the last of them", () => {
    const long = parseCsv("name;email\r\nA;a@x.no;extra\r\n");
    expect(failedRowsCsv(long, [{ row: 1, column: null, message: "This row has 3 cells, but the header has 2" }])).toBe(
      "\ufeffname;email;error\r\nA;a@x.no;extra;This row has 3 cells, but the header has 2\r\n",
    );
  });

  it("overwrites the error column of a failed-rows file being re-run rather than adding a second", () => {
    const rerun = parseCsv("name;error\r\nFeil AS;old problem\r\n");
    expect(failedRowsCsv(rerun, [{ row: 1, column: "name", message: "new problem" }])).toBe(
      "\ufeffname;error\r\nFeil AS;name: new problem\r\n",
    );
  });
});
