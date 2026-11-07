import { queryOptions } from "@tanstack/react-query";
import { appUrl } from "@vantigo/frontend-shell";
import type { components } from "../api-schema";
import type { KnowledgeRootSection } from "./articles";
import { handleUnauthorized, json, KNOWLEDGE_QUERY_KEY, refusalOf, request, send } from "./request";

type Schemas = components["schemas"];

export type KnowledgeFile = Schemas["KnowledgeFile"];
export type KnowledgeBacklink = Schemas["KnowledgeBacklink"];
export type KnowledgeResolvedLink = Schemas["KnowledgeResolvedLink"];
export type KnowledgeImportResult = Schemas["KnowledgeImportResult"];

const base = "/api/v1/knowledge";

/** The largest file an article takes (knowledge design K19), checked before sending as the server does. */
export const MAX_FILE_BYTES = 25 * 1024 * 1024;

/** Where a file is served (K19): an image node's `src` is exactly this, and a file chip downloads from it. */
export const filePath = (fileId: string) => `${base}/files/${fileId}`;

/** The file id an image's `src` names, when it names one of the module's files. */
export const fileIdOfSrc = (src: string): string | null => {
  const match = /^\/api\/v1\/knowledge\/files\/([0-9a-f-]{36})$/i.exec(src);
  return match ? match[1] : null;
};

/**
 * Uploads a file into an article (`uploadKnowledgeFile`, K19): multipart,
 * the part `file`. Sent with XMLHttpRequest rather than fetch, so the editor
 * can show how far it got. A refusal is thrown as the rest of the package
 * throws them (`refusalOf`), with its code (`file_too_large`, `article_locked` …).
 */
export const uploadFile = (
  articleId: string,
  file: File,
  { onProgress, signal }: { onProgress?: (fraction: number) => void; signal?: AbortSignal } = {},
): Promise<KnowledgeFile> =>
  new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open("POST", appUrl(`${base}/articles/${articleId}/files`));
    xhr.withCredentials = true;
    xhr.responseType = "text";
    xhr.upload.onprogress = (event) => {
      if (event.lengthComputable && event.total > 0) onProgress?.(event.loaded / event.total);
    };
    xhr.onerror = () => reject(new Error(""));
    xhr.onabort = () => reject(new DOMException("aborted", "AbortError"));
    xhr.onload = async () => {
      if (xhr.status === 401) await handleUnauthorized();
      if (xhr.status >= 200 && xhr.status < 300) {
        resolve(JSON.parse(xhr.responseText) as KnowledgeFile);
        return;
      }
      const type = xhr.getResponseHeader("Content-Type") ?? "application/json";
      reject(
        await refusalOf(
          new Response(xhr.responseText || null, { status: xhr.status, headers: { "Content-Type": type } }),
        ),
      );
    };
    signal?.addEventListener("abort", () => xhr.abort());
    const form = new FormData();
    form.append("file", file);
    xhr.send(form);
  });

/**
 * Copies a file of another article the caller can read into this one
 * (`copyKnowledgeFile`, K19), so a pasted image or file chip names a file of
 * the article it is in. The copy has a new id.
 */
export const copyFile = (articleId: string, fileId: string, { signal }: { signal?: AbortSignal } = {}) =>
  send<KnowledgeFile>(`${base}/articles/${articleId}/files/copy`, { ...json("POST", { fileId }), signal });

export const backlinksQueryKey = (id: string) => [KNOWLEDGE_QUERY_KEY, "backlinks", id] as const;

/** The pages linking to this one that the reader may read (`listKnowledgeBacklinks`, K21). */
export const backlinksQueryOptions = (id: string) =>
  queryOptions({
    queryKey: backlinksQueryKey(id),
    queryFn: ({ signal }) => request<KnowledgeBacklink[]>(`${base}/articles/${id}/backlinks`, { signal }),
  });

/** The current title of each linked page, or that it is out of reach (`resolveKnowledgeLinks`, K21). */
export const resolveLinks = (ids: string[]) =>
  send<KnowledgeResolvedLink[]>(`${base}/links/resolve`, json("POST", { ids }));

export const resolvedLinkQueryKey = (id: string) => [KNOWLEDGE_QUERY_KEY, "link", id] as const;

/**
 * One link chip's target, for its node view. The chips on a page ask in the
 * same tick, so they are gathered into one request of up to 200 ids.
 */
interface Batch {
  ids: Set<string>;
  waiters: Map<string, ((link: KnowledgeResolvedLink) => void)[]>;
  fail: ((error: unknown) => void)[];
}

let pending: Batch | null = null;

const flush = async () => {
  const batch = pending;
  pending = null;
  if (!batch) return;
  const ids = [...batch.ids];
  try {
    for (let start = 0; start < ids.length; start += 200) {
      const answer = await resolveLinks(ids.slice(start, start + 200));
      for (const link of answer) for (const done of batch.waiters.get(link.id) ?? []) done(link);
    }
    // An id the answer leaves out is one the server no longer knows.
    for (const id of ids)
      for (const done of batch.waiters.get(id) ?? []) done({ id, state: "deleted", title: null, icon: null });
  } catch (error) {
    for (const fail of batch.fail) fail(error);
  }
};

export const resolveLink = (id: string): Promise<KnowledgeResolvedLink> =>
  new Promise((resolve, reject) => {
    if (!pending) {
      pending = { ids: new Set(), waiters: new Map(), fail: [] };
      queueMicrotask(() => void flush());
    }
    pending.ids.add(id);
    let settled = false;
    const once = (link: KnowledgeResolvedLink) => {
      if (settled) return;
      settled = true;
      resolve(link);
    };
    pending.waiters.set(id, [...(pending.waiters.get(id) ?? []), once]);
    pending.fail.push((error) => {
      if (!settled) reject(error);
    });
  });

export const resolvedLinkQueryOptions = (id: string) =>
  queryOptions({
    queryKey: resolvedLinkQueryKey(id),
    queryFn: () => resolveLink(id),
    staleTime: 30_000,
  });

/** The name a download is saved under: the server's `Content-Disposition`, else the fallback. */
const filenameOf = (response: Response, fallback: string) => {
  const header = response.headers.get("Content-Disposition") ?? "";
  const encoded = /filename\*=UTF-8''([^;]+)/i.exec(header);
  if (encoded) return decodeURIComponent(encoded[1]);
  const plain = /filename="?([^";]+)"?/i.exec(header);
  return plain ? plain[1] : fallback;
};

/**
 * Exports the article as Markdown (`exportKnowledgeArticle`, K23), with its
 * subpages as a `.zip`, and hands it to the browser as a download.
 */
export const exportArticle = async (id: string, { subtree, title }: { subtree: boolean; title: string }) => {
  const query = subtree ? `?${new URLSearchParams({ subtree: "true" })}` : "";
  const response = await fetch(appUrl(`${base}/articles/${id}/export${query}`), { credentials: "include" });
  if (response.status === 401) await handleUnauthorized();
  if (!response.ok) throw await refusalOf(response);
  const isZip = (response.headers.get("Content-Type") ?? "").includes("zip");
  const name = filenameOf(response, `${title.trim() || "article"}.${isZip ? "zip" : "md"}`);
  const blob = await response.blob();
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = name;
  document.body.append(link);
  link.click();
  link.remove();
  setTimeout(() => URL.revokeObjectURL(url), 0);
  return name;
};

/**
 * Imports one `.md`, or a `.zip` of them (an Obsidian vault, a Notion
 * export), under a parent or at the top of a section (`importKnowledgeMarkdown`, K23).
 */
export const importMarkdown = async (
  file: File,
  target: { parentId: string } | { section: KnowledgeRootSection },
): Promise<KnowledgeImportResult> => {
  const form = new FormData();
  form.append("file", file);
  if ("parentId" in target) form.append("parentId", target.parentId);
  else form.append("section", target.section);
  return send<KnowledgeImportResult>(`${base}/import`, { method: "POST", body: form });
};
