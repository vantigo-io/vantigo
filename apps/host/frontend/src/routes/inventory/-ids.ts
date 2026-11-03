/** Whether a path parameter is a positive whole number, so `GET /…/NaN` never reaches the API. */
export const isPositiveId = (value: string) =>
  /^\d+$/.test(value) && Number.isSafeInteger(Number(value)) && Number(value) > 0;
