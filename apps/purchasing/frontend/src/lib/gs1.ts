/**
 * A GS1-128 element string as the receive screen reads it (purchasing design
 * A21): the GTIN a scan looks up, and the lot, serial and dates it fills in.
 * Inventory has no GS1 reader on the client — its server's gs1.go only prints
 * labels — so the reader lives here, with the screen that needs it.
 *
 * Two forms are read: the raw one a scanner sends (an optional symbology
 * identifier such as `]C1`, each variable-length field ended by FNC1, ASCII
 * 29, unless it is the last), and the human-readable one printed under the
 * bars, `(01)…(17)…(10)…`. Anything else is not GS1, and the scan is the code
 * itself.
 */
export interface Gs1Read {
  /** (01), or (02) on a logistic unit: a GTIN-14. */
  gtin?: string;
  /** (10) batch or lot number. */
  lot?: string;
  /** (21) serial number. */
  serial?: string;
  /** (11) production date, as YYYY-MM-DD. */
  productionDate?: string;
  /** (15) best before, as YYYY-MM-DD. */
  bestBefore?: string;
  /** (17) expiry, as YYYY-MM-DD. */
  expiry?: string;
}

const GS = "\u001d";

/** The fixed lengths of the data of the AIs read here and of the common ones a label carries beside them. */
const fixedLength: Record<string, number> = {
  "00": 18,
  "01": 14,
  "02": 14,
  "11": 6,
  "12": 6,
  "13": 6,
  "15": 6,
  "16": 6,
  "17": 6,
  "20": 2,
};

/** The variable-length AIs understood, with their longest data. */
const variableLength: Record<string, number> = {
  "10": 20,
  "21": 20,
  "30": 8,
  "37": 8,
};

/** Symbology identifiers a scanner may put first: GS1-128, GS1 DataMatrix, GS1 QR. */
const symbologyPrefix = /^\](C1|d2|Q3|e0)/;

/**
 * A YYMMDD date as YYYY-MM-DD, or undefined when it is no date. DD 00 is the
 * month's last day; the century is GS1's sliding window (GS1 General
 * Specifications 7.12): up to 49 years ahead, otherwise up to 50 years back.
 */
export const gs1Date = (text: string, now: Date = new Date()): string | undefined => {
  if (!/^\d{6}$/.test(text)) return undefined;
  const yy = Number(text.slice(0, 2));
  const month = Number(text.slice(2, 4));
  let day = Number(text.slice(4, 6));
  if (month < 1 || month > 12) return undefined;
  const current = now.getUTCFullYear();
  const century = Math.floor(current / 100) * 100;
  const difference = yy - (current % 100);
  const year = difference >= 51 ? century - 100 + yy : difference <= -50 ? century + 100 + yy : century + yy;
  const lastDay = new Date(Date.UTC(year, month, 0)).getUTCDate();
  if (day === 0) day = lastDay;
  if (day > lastDay) return undefined;
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${year}-${pad(month)}-${pad(day)}`;
};

/** The fields read from AI and data pairs, or null when a date is no date or no GTIN is named. */
const fromPairs = (pairs: readonly [string, string][], now: Date): Gs1Read | null => {
  const read: Gs1Read = {};
  for (const [ai, data] of pairs) {
    switch (ai) {
      case "01":
      case "02":
        if (!/^\d{14}$/.test(data)) return null;
        read.gtin ??= data;
        break;
      case "10":
        read.lot = data;
        break;
      case "21":
        read.serial = data;
        break;
      case "11":
      case "15":
      case "17": {
        const date = gs1Date(data, now);
        if (!date) return null;
        if (ai === "11") read.productionDate = date;
        else if (ai === "15") read.bestBefore = date;
        else read.expiry = date;
        break;
      }
    }
  }
  return read.gtin ? read : null;
};

/** `(01)…(10)…`: every AI in parentheses, its data up to the next one. */
const readHumanReadable = (text: string, now: Date): Gs1Read | null => {
  if (!/^(\(\d{2,4}\)[^()]+)+$/.test(text)) return null;
  const pairs = [...text.matchAll(/\((\d{2,4})\)([^()]+)/g)].map((m) => [m[1], m[2].trim()] as [string, string]);
  return fromPairs(pairs, now);
};

/** The raw form: fixed-length fields run on, variable ones end at GS or at the end. */
const readRaw = (text: string, now: Date): Gs1Read | null => {
  const pairs: [string, string][] = [];
  let at = 0;
  while (at < text.length) {
    if (text[at] === GS) {
      at += 1;
      continue;
    }
    const ai = text.slice(at, at + 2);
    at += 2;
    const fixed = fixedLength[ai];
    if (fixed !== undefined) {
      const data = text.slice(at, at + fixed);
      if (data.length !== fixed || data.includes(GS)) return null;
      pairs.push([ai, data]);
      at += fixed;
      continue;
    }
    const longest = variableLength[ai];
    if (longest === undefined) return null;
    const end = text.indexOf(GS, at);
    const data = text.slice(at, end === -1 ? text.length : end);
    if (data.length === 0 || data.length > longest) return null;
    pairs.push([ai, data]);
    at += data.length;
  }
  return fromPairs(pairs, now);
};

/**
 * A scan read as GS1, or null when it is not one: then the scan is a plain
 * code (a SKU or a barcode) and is looked up as it is. A raw string without a
 * symbology identifier is GS1 only when it starts with (01) or (02) and reads
 * through to its end.
 */
export const readGs1 = (scan: string, now: Date = new Date()): Gs1Read | null => {
  const text = scan.trim();
  if (text.startsWith("(")) return readHumanReadable(text, now);
  const prefixed = symbologyPrefix.test(text);
  const body = text.replace(symbologyPrefix, "");
  if (!prefixed && !/^0[12]\d{14}/.test(body)) return null;
  return readRaw(body, now);
};
