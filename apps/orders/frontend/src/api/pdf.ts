import { appUrl } from "@vantigo/frontend-shell";
import { handleUnauthorized, refusalOf } from "./request";

export interface FetchedPdf {
  blob: Blob;
  fileName?: string;
}

/**
 * A PDF fetched before it is shown, so a refusal is a notification in the
 * reader's language rather than the problem JSON opened in a tab (O24).
 */
export const fetchPdf = async (url: string): Promise<FetchedPdf> => {
  const response = await fetch(appUrl(url), { credentials: "include" });
  if (response.status === 401) await handleUnauthorized();
  if (!response.ok) throw await refusalOf(response);
  const disposition = response.headers.get("Content-Disposition") ?? "";
  return { blob: await response.blob(), fileName: /filename="([^"]+)"/.exec(disposition)?.[1] };
};
