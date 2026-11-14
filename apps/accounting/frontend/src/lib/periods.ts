// The reports' default periods (A11).

/** The same day a year before: the default comparison; 29 February is 28 February the year before. */
export const yearBefore = (date: string) => {
  const year = Number(date.slice(0, 4)) - 1;
  const rest = date.slice(4) === "-02-29" ? "-02-28" : date.slice(4);
  return `${String(year).padStart(4, "0")}${rest}`;
};

/** The first day of the fiscal year today falls in, for a year starting in startMonth (1–12). */
export const fiscalYearStart = (today: string, startMonth: number) => {
  const year = Number(today.slice(0, 4));
  const month = Number(today.slice(5, 7));
  const start = month >= startMonth ? year : year - 1;
  return `${start}-${String(startMonth).padStart(2, "0")}-01`;
};
