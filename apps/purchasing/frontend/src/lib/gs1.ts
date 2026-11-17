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
  /**
   * The lot, or the serial, may be in the code but could not be read for
   * sure — a variable-length field ran to the end with no FNC1 though more
   * fields could follow it, or an AI this reader does not know stopped it —:
   * the screen asks for it rather than guess.
   */
  lotUnread?: boolean;
  serialUnread?: boolean;
}

const GS = "\u001d";

/**
 * The fixed lengths of the data of the AIs whose data has a fixed length, by
 * the AI's digits: those read here and the common ones a label carries
 * beside them, which are skipped (GS1 General Specifications 3.2).
 */
const fixedLength: Record<string, number> = {
  "00": 18,
  "01": 14,
  "02": 14,
  "03": 14,
  "11": 6,
  "12": 6,
  "13": 6,
  "15": 6,
  "16": 6,
  "17": 6,
  "20": 2,
  "410": 13,
  "411": 13,
  "412": 13,
  "413": 13,
  "414": 13,
  "415": 13,
  "416": 13,
  "417": 13,
  "422": 3,
  "424": 3,
  "426": 3,
};

/** The variable-length AIs understood, with their longest data; all but 10 and 21 are skipped. */
const variableLength: Record<string, number> = {
  "10": 20,
  "21": 20,
  "22": 20,
  "240": 30,
  "241": 30,
  "250": 30,
  "251": 30,
  "30": 8,
  "37": 8,
  "400": 30,
  "401": 30,
  "403": 30,
  "420": 20,
  "90": 30,
  "91": 90,
  "92": 90,
  "93": 90,
  "94": 90,
  "95": 90,
  "96": 90,
  "97": 90,
  "98": 90,
  "99": 90,
};

/**
 * The AI at the head of text and its data's length: fixed, or the longest a
 * variable one takes. 310n–369n, a measure with n decimals, is four digits
 * and six of data.
 */
const aiAt = (text: string): { ai: string; fixed?: number; longest?: number } | undefined => {
  if (/^3[1-6]\d\d/.test(text)) return { ai: text.slice(0, 4), fixed: 6 };
  for (const n of [2, 3]) {
    const ai = text.slice(0, n);
    if (fixedLength[ai] !== undefined) return { ai, fixed: fixedLength[ai] };
    if (variableLength[ai] !== undefined) return { ai, longest: variableLength[ai] };
  }
  return undefined;
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

/**
 * Whether text reads through to its end as known fixed-length fields whose
 * data fits — dates that are dates —: what fields swallowed by a variable
 * one before them would be. A tail that would be another variable field is
 * not judged, or every lot with a 10 or 21 in it would be.
 */
const readsThrough = (text: string, now: Date): boolean => {
  const read = rawPairs(text);
  if (!read || read.stopped || read.pairs.length === 0) return false;
  return read.pairs.every(
    ([ai, data]) =>
      (fixedLength[ai] !== undefined || ai.length === 4) && (!/^1[1-7]$/.test(ai) || gs1Date(data, now) !== undefined),
  );
};

interface RawPairs {
  pairs: [string, string][];
  /** An AI this reader does not know stopped the reading there. */
  stopped: boolean;
  /** The variable-length fields that ran to the end without an FNC1, by AI. */
  open: string[];
}

/** The raw form's AI and data pairs: fixed-length fields run on, variable ones end at GS or at the end. */
const rawPairs = (text: string): RawPairs | null => {
  const out: RawPairs = { pairs: [], stopped: false, open: [] };
  let at = 0;
  while (at < text.length) {
    if (text[at] === GS) {
      at += 1;
      continue;
    }
    const head = aiAt(text.slice(at));
    if (!head) {
      out.stopped = true;
      return out;
    }
    at += head.ai.length;
    if (head.fixed !== undefined) {
      const data = text.slice(at, at + head.fixed);
      if (data.length !== head.fixed || data.includes(GS)) return null;
      out.pairs.push([head.ai, data]);
      at += head.fixed;
      continue;
    }
    const end = text.indexOf(GS, at);
    const data = text.slice(at, end === -1 ? text.length : end);
    if (data.length === 0 || data.length > (head.longest ?? 0)) return null;
    if (end === -1) out.open.push(head.ai);
    out.pairs.push([head.ai, data]);
    at += data.length;
  }
  return out;
};

/**
 * The raw form. A variable-length field that runs to the end of a string
 * with no FNC1 in it might have swallowed fields after it — a scanner that
 * drops FNC1 sends `…10LOT4217270531` for lot LOT42 and expiry 2027-05-31 —:
 * when some tail of its data reads as known fields, the lot or serial is
 * reported unread instead of guessed. An AI this reader does not know ends
 * the reading of a string a scanner marked GS1: what came before it stands,
 * and a lot or serial not read by then is reported unread. Without the mark
 * the string is GS1 only when it reads through.
 */
const readRaw = (text: string, prefixed: boolean, now: Date): Gs1Read | null => {
  const raw = rawPairs(text);
  if (!raw || (raw.stopped && !prefixed)) return null;
  const read = fromPairs(raw.pairs, now);
  if (!read) return null;
  const noSeparator = !text.includes(GS);
  for (const [ai, data] of raw.pairs) {
    if (!raw.open.includes(ai) || !noSeparator || (ai !== "10" && ai !== "21")) continue;
    let ambiguous = false;
    for (let cut = 1; cut < data.length && !ambiguous; cut++) ambiguous = readsThrough(data.slice(cut), now);
    if (!ambiguous) continue;
    if (ai === "10") {
      delete read.lot;
      read.lotUnread = true;
    } else {
      delete read.serial;
      read.serialUnread = true;
    }
  }
  if (raw.stopped) {
    if (read.lot === undefined) read.lotUnread = true;
    if (read.serial === undefined) read.serialUnread = true;
  }
  return read;
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
  return readRaw(body, prefixed, now);
};
