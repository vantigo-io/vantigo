/**
 * The mod-11 check a Norwegian organisation number has to pass: nine digits,
 * the first eight weighted 3 2 7 6 5 4 3 2, a remainder of 0 meaning a check
 * digit of 0, and a computed check digit of 10 meaning there is no valid
 * number with those eight digits at all.
 *
 * A mirror of the server's own `validNorwegianOrgNumber`
 * (`apps/server/internal/customers/values.go`), kept here for one reason: the
 * billing card's EHF hint promises what the server would derive, and the
 * server derives a Peppol recipient only from an identity whose id really is
 * an organisation number. A legacy identity that never passed this check
 * would otherwise be advertised as an EHF recipient that never materialises.
 */
export const validNorwegianOrgNumber = (digits: string): boolean => {
  if (!/^\d{9}$/.test(digits)) return false;
  const weights = [3, 2, 7, 6, 5, 4, 3, 2];
  const sum = weights.reduce((total, weight, index) => total + Number(digits[index]) * weight, 0);
  const remainder = sum % 11;
  const check = remainder === 0 ? 0 : 11 - remainder;
  return check !== 10 && check === Number(digits[8]);
};
