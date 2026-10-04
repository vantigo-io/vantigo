/**
 * The KID's check digits, ported from the server's `invoices/kid` package
 * (EHF and KID design D3) so the settings can preview the next KID while the
 * agreement is edited. The server computes the KID at issue; this only shows
 * what it will be.
 */

/** The Luhn check digit: weights 2 and 1 from the right, each product's digits summed, (10 − sum mod 10) mod 10. */
export const checkMod10 = (digits: string): string => {
  let sum = 0;
  for (let i = 0; i < digits.length; i++) {
    let d = Number(digits[digits.length - 1 - i]);
    if (i % 2 === 0) {
      d *= 2;
      if (d > 9) d -= 9;
    }
    sum += d;
  }
  return String((10 - (sum % 10)) % 10);
};

/** The MOD11 check digit: weights 2 to 7 repeating from the right, 11 − (sum mod 11); 0 for remainder 0, "-" for 1. */
export const checkMod11 = (digits: string): string => {
  let sum = 0;
  for (let i = 0; i < digits.length; i++) {
    sum += Number(digits[digits.length - 1 - i]) * (2 + (i % 6));
  }
  const remainder = sum % 11;
  if (remainder === 0) return "0";
  if (remainder === 1) return "-";
  return String(11 - remainder);
};

/**
 * `number`'s KID of `length` characters under `algorithm`: the number
 * zero-padded to the length less one, then the check digit. Undefined when
 * the number does not fit or the algorithm is not one the server knows.
 */
export const computeKid = (number: number, length: number, algorithm: string): string | undefined => {
  const digits = String(number);
  if (number < 1 || digits.length > length - 1) return undefined;
  const body = digits.padStart(length - 1, "0");
  if (algorithm === "mod10") return body + checkMod10(body);
  if (algorithm === "mod11") return body + checkMod11(body);
  return undefined;
};

/**
 * The settings' rule (D3): whether `next`, the next number to be issued, fits
 * in `length` − 1 digits, and whether fewer than two digits of headroom are
 * left — a hundredfold growth.
 */
export const kidFits = (next: number, length: number): { fits: boolean; headroomLow: boolean } => {
  const room = length - 1 - String(next).length;
  return room < 0 ? { fits: false, headroomLow: false } : { fits: true, headroomLow: room < 2 };
};
