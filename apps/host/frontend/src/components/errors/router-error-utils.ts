const networkErrorMessage = /failed to fetch|networkerror|network request failed|load failed|fetch failed/i;

const errorName = (error: unknown) =>
  error && typeof error === "object" && "name" in error && typeof error.name === "string" ? error.name : undefined;

const errorMessage = (error: unknown) =>
  error && typeof error === "object" && "message" in error && typeof error.message === "string" ? error.message : "";

export const isNetworkError = (error: unknown) =>
  (errorName(error) === "TypeError" || errorName(error) === "NetworkError") &&
  networkErrorMessage.test(errorMessage(error));

export const errorStatus = (error: unknown) => {
  if (!error || typeof error !== "object" || !("status" in error)) return undefined;
  return typeof error.status === "number" ? error.status : undefined;
};

export type RouterErrorKind = "offline" | "forbidden" | "unexpected";

export const routerErrorKind = (error: unknown): RouterErrorKind => {
  if (isNetworkError(error)) return "offline";
  if (errorStatus(error) === 403) return "forbidden";
  return "unexpected";
};
