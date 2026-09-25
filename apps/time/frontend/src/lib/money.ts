/**
 * What an entry's hours bill at (work types design D3), exactly: hours ×
 * rate × multiplier, rounded half up to cents once — the server's own
 * arithmetic, so the approval queue says what the economy says. Floats never
 * multiply money here: each figure is taken to its hundredths (the scale the
 * server stores all three at) and the product is formed in BigInt. "Half up"
 * is the server's: a half cent goes away from zero (`roundHalfUpCents` in Go,
 * `ROUND` in SQL), so a negative amount rounds as its positive twin does.
 */
export const billedAmount = (rate: number, hours: number, multiplierPercent?: number | null): number => {
  const hundredths = (value: number): bigint => BigInt(Math.round(value * 100));
  // cents × hundredths of an hour × hundredths of a percent is the value
  // times 10^8; a cent is 10^6 of those.
  const product = hundredths(rate) * hundredths(hours) * hundredths(multiplierPercent ?? 100);
  // BigInt division truncates toward zero, so the half is added on the
  // product's own side of zero.
  const cents = (product + (product < 0n ? -500_000n : 500_000n)) / 1_000_000n;
  return Number(cents) / 100;
};
