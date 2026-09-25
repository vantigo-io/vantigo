/**
 * What an entry's hours bill at (work types design D3), exactly: hours ×
 * rate × multiplier, rounded half up to cents once — the server's own
 * arithmetic, so the approval queue says what the economy says. Floats never
 * multiply money here: each figure is taken to its hundredths (the scale the
 * server stores all three at) and the product is formed in BigInt.
 */
export const billedAmount = (rate: number, hours: number, multiplierPercent?: number | null): number => {
  const hundredths = (value: number): bigint => BigInt(Math.round(value * 100));
  // cents × hundredths of an hour × hundredths of a percent is the value
  // times 10^8; a cent is 10^6 of those.
  const product = hundredths(rate) * hundredths(hours) * hundredths(multiplierPercent ?? 100);
  const cents = (product + 500_000n) / 1_000_000n;
  return Number(cents) / 100;
};
