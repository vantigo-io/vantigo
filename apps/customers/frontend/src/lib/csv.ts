import type { CustomerImportError } from "../api/import-export";

/**
 * The customers file (customers import/export design D1) as the browser reads
 * and writes it — for one purpose: the failed-rows file (D4), built from the
 * file the person picked and the errors the import answered. The server is the
 * authority on what a row means; this only has to number rows exactly as the
 * server does (so an error's `row` finds its line) and write a file the server
 * reads back. Semicolons, CRLF, a byte order mark, RFC 4180 quoting, the
 * formula guard: `internal/customers/csvfile.go`'s rules.
 */
const BOM = "\ufeff";
const SEPARATOR = ";";
const LINE_END = "\r\n";
const ERROR_COLUMN = "error";

/** One data row: its 1-based number (the server's) and its cells as written. */
export interface CsvRecord {
  row: number;
  cells: string[];
}

export interface CsvTable {
  header: string[];
  records: CsvRecord[];
}

/** Splits text into records — RFC 4180 with `;`, CRLF or LF — skipping empty lines as Go's encoding/csv does. */
const splitRecords = (text: string): string[][] => {
  const records: string[][] = [];
  let cells: string[] = [];
  let cell = "";
  let quoted = false;
  let started = false;
  const endRecord = () => {
    if (started) records.push([...cells, cell]);
    cells = [];
    cell = "";
    started = false;
  };
  for (let i = 0; i < text.length; i += 1) {
    const char = text[i];
    if (quoted) {
      if (char === '"' && text[i + 1] === '"') {
        cell += '"';
        i += 1;
      } else if (char === '"') quoted = false;
      else cell += char;
      continue;
    }
    if (char === '"') {
      quoted = true;
      started = true;
    } else if (char === SEPARATOR) {
      cells.push(cell);
      cell = "";
      started = true;
    } else if (char === "\n") endRecord();
    else if (char !== "\r" || text[i + 1] !== "\n") {
      cell += char;
      started = true;
    }
  }
  endRecord();
  return records;
};

/**
 * The file as the server reads it: the header, then every record that is not
 * all blank, numbered from 1 — a record whose every cell is blank is skipped
 * and takes no number, as `readCSVFile` skips it.
 */
export const parseCsv = (text: string): CsvTable => {
  const [header = [], ...rest] = splitRecords(text.startsWith(BOM) ? text.slice(BOM.length) : text);
  const records: CsvRecord[] = [];
  for (const cells of rest) {
    if (cells.every((cell) => cell.trim() === "")) continue;
    records.push({ row: records.length + 1, cells });
  }
  return { header, records };
};

/** One cell as it goes into the file: the formula guard, then quoting — the server's `csvCell`. */
export const csvCell = (value: string): string => {
  const guarded = /^[=+\-@\t\r]/.test(value) ? `'${value}` : value;
  return /[;"\r\n]/.test(guarded) ? `"${guarded.replaceAll('"', '""')}"` : guarded;
};

/** Rows as a customers file. */
export const toCsv = (rows: string[][]): string =>
  BOM + rows.map((cells) => cells.map(csvCell).join(SEPARATOR) + LINE_END).join("");

/**
 * The failed-rows file (D4): the header and, of the records, only those an
 * error names — each as it was, with an `error` column holding every problem
 * of that row (`column: message`, or the message alone for the row as a whole).
 * A file that already carries an `error` column — a failed-rows file being
 * re-run — has it overwritten rather than a second one added. The import
 * ignores that column, so the file goes straight back in once fixed.
 */
export const failedRowsCsv = (table: CsvTable, errors: CustomerImportError[]): string => {
  const problems = new Map<number, string[]>();
  for (const error of errors) {
    const text = error.column ? `${error.column}: ${error.message}` : error.message;
    problems.set(error.row, [...(problems.get(error.row) ?? []), text]);
  }
  const existing = table.header.findIndex((name) => name.trim().toLowerCase() === ERROR_COLUMN);
  const header = existing >= 0 ? table.header : [...table.header, ERROR_COLUMN];
  const at = existing >= 0 ? existing : table.header.length;
  const rows = table.records
    .filter((record) => problems.has(record.row))
    .map((record) => {
      const cells = [...record.cells];
      while (cells.length < header.length) cells.push("");
      cells[at] = (problems.get(record.row) ?? []).join(" | ");
      return cells;
    });
  return toCsv([header, ...rows]);
};
