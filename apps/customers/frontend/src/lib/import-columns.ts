/** A file column's name as the import pages show it: the header, or its position when the header is blank. */
export const columnLabel = (
  header: string[],
  index: number,
  t: (key: string, values?: Record<string, unknown>) => string,
): string => header[index]?.trim() || t("importColumnNumber", { number: index + 1 });
