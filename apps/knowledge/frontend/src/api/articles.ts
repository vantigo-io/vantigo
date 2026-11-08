import { keepPreviousData, queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { json, KNOWLEDGE_QUERY_KEY, request, send } from "./request";

type Schemas = components["schemas"];

export type KnowledgeArticle = Schemas["KnowledgeArticle"];
export type KnowledgeTreeNode = Schemas["KnowledgeTreeNode"];
export type KnowledgeSection = Schemas["KnowledgeSection"];
export type KnowledgeRootSection = Schemas["KnowledgeRootSection"];
export type KnowledgeAccessLevel = Schemas["KnowledgeAccessLevel"];
export type KnowledgeArticleAccess = Schemas["KnowledgeAccess"];
export type KnowledgeAccessUpdate = Schemas["KnowledgeAccessUpdate"];
export type KnowledgeMember = Schemas["KnowledgeMember"];
export type KnowledgeMemberInput = Schemas["KnowledgeMemberInput"];
export type KnowledgeActor = Schemas["KnowledgeActor"];
export type KnowledgeDocument = Schemas["KnowledgeDocument"];
export type KnowledgeSaved = Schemas["KnowledgeSaved"];
export type KnowledgeVersionSummary = Schemas["KnowledgeVersionSummary"];
export type KnowledgeVersion = Schemas["KnowledgeVersion"];
export type KnowledgeTrashItem = Schemas["KnowledgeTrashItem"];
export type KnowledgeRestored = Schemas["KnowledgeRestored"];
export type KnowledgeSearchHit = Schemas["KnowledgeSearchHit"];
export type KnowledgeExcerptSegment = Schemas["KnowledgeExcerptSegment"];
export type KnowledgeAssignableUser = Schemas["KnowledgeAssignableUser"];
export type KnowledgeProblem = Schemas["KnowledgeProblem"];
export type KnowledgeArticleUpdate = Schemas["KnowledgeArticleUpdate"];
export type KnowledgeMoveInput = Schemas["KnowledgeMoveInput"];
export type KnowledgeArticleCreate = Schemas["KnowledgeArticleCreate"];

/** The sidebar's sections in the order it shows them (K4, K11). */
export const sections: readonly KnowledgeSection[] = ["workspace", "shared", "private"];

const base = "/api/v1/knowledge";

/** Every key of the sidebar's tree, so a live `tree.changed` (K13) refetches all of it. */
export const treeQueryKey = [KNOWLEDGE_QUERY_KEY, "tree"] as const;
export const favouritesQueryKey = [KNOWLEDGE_QUERY_KEY, "favourites"] as const;
export const articleQueryKey = (id: string) => [KNOWLEDGE_QUERY_KEY, "article", id] as const;

/** Without a parent, the topmost articles of every section; with one, its children. */
export const treeQueryOptions = (parentId: string | null) =>
  queryOptions({
    queryKey: [...treeQueryKey, parentId ?? "root"],
    queryFn: ({ signal }) =>
      request<Schemas["KnowledgeTree"]>(`${base}/tree${parentId ? `?${new URLSearchParams({ parentId })}` : ""}`, {
        signal,
      }).then((answer) => answer.items),
  });

export const favouritesQueryOptions = () =>
  queryOptions({
    queryKey: favouritesQueryKey,
    queryFn: ({ signal }) =>
      request<Schemas["KnowledgeFavouriteList"]>(`${base}/favourites`, { signal }).then((answer) => answer.items),
  });

export const articleQueryOptions = (id: string) =>
  queryOptions({
    queryKey: articleQueryKey(id),
    queryFn: ({ signal }) => request<KnowledgeArticle>(`${base}/articles/${id}`, { signal }),
    retry: false,
  });

export const accessQueryOptions = (id: string) =>
  queryOptions({
    queryKey: [KNOWLEDGE_QUERY_KEY, "access", id],
    queryFn: ({ signal }) => request<KnowledgeArticleAccess>(`${base}/articles/${id}/access`, { signal }),
  });

export const versionsQueryOptions = (id: string) =>
  queryOptions({
    queryKey: [KNOWLEDGE_QUERY_KEY, "versions", id],
    queryFn: ({ signal }) =>
      request<Schemas["KnowledgeVersionList"]>(`${base}/articles/${id}/versions`, { signal }).then(
        (answer) => answer.items,
      ),
  });

export const versionQueryOptions = (id: string, version: number) =>
  queryOptions({
    queryKey: [KNOWLEDGE_QUERY_KEY, "versions", id, version],
    queryFn: ({ signal }) => request<KnowledgeVersion>(`${base}/articles/${id}/versions/${version}`, { signal }),
    staleTime: Number.POSITIVE_INFINITY,
  });

export const trashQueryOptions = () =>
  queryOptions({
    queryKey: [KNOWLEDGE_QUERY_KEY, "trash"],
    queryFn: ({ signal }) =>
      request<Schemas["KnowledgeTrashList"]>(`${base}/trash`, { signal }).then((answer) => answer.items),
  });

export const searchQueryOptions = (q: string, limit = 20) => {
  const term = q.trim();
  return queryOptions({
    queryKey: [KNOWLEDGE_QUERY_KEY, "search", term, limit],
    queryFn: ({ signal }) =>
      request<Schemas["KnowledgeSearchResult"]>(
        `${base}/search?${new URLSearchParams({ q: term, limit: String(limit) })}`,
        { signal },
      ).then((answer) => answer.items),
    enabled: term.length > 0,
  });
};

/** The directory's active users for the access dialog's picker (K4), at most twenty. */
export const assignableUsersQueryOptions = (query: string) => {
  const term = query.trim();
  return queryOptions({
    queryKey: [KNOWLEDGE_QUERY_KEY, "assignable-users", term],
    queryFn: ({ signal }) =>
      request<KnowledgeAssignableUser[]>(
        `${base}/assignable-users${term ? `?${new URLSearchParams({ query: term })}` : ""}`,
        { signal },
      ),
    placeholderData: keepPreviousData,
    staleTime: 30_000,
  });
};

export const createArticle = (input: KnowledgeArticleCreate) =>
  send<KnowledgeArticle>(`${base}/articles`, json("POST", input));

export const updateArticle = (id: string, input: KnowledgeArticleUpdate) =>
  send<KnowledgeArticle>(`${base}/articles/${id}`, json("PATCH", input));

export const saveContent = (id: string, input: Schemas["KnowledgeContentUpdate"]) =>
  send<KnowledgeSaved>(`${base}/articles/${id}/content`, json("PUT", input));

export const moveArticle = (id: string, input: KnowledgeMoveInput) =>
  send<KnowledgeTreeNode>(`${base}/articles/${id}/move`, json("POST", input));

export const updateAccess = (id: string, input: KnowledgeAccessUpdate) =>
  send<KnowledgeArticleAccess>(`${base}/articles/${id}/access`, json("PUT", input));

export const trashArticle = (id: string) => send<void>(`${base}/articles/${id}/trash`, { method: "POST" });

export const restoreArticle = (id: string) =>
  send<KnowledgeRestored>(`${base}/articles/${id}/restore`, { method: "POST" });

export const emptyTrash = () => send<void>(`${base}/trash`, { method: "DELETE" });

export const restoreVersion = (id: string, version: number, expectedVersion: number) =>
  send<KnowledgeSaved>(`${base}/articles/${id}/versions/${version}/restore`, json("POST", { expectedVersion }));

export const addFavourite = (id: string) => send<void>(`${base}/favourites/${id}`, { method: "PUT" });

export const removeFavourite = (id: string) => send<void>(`${base}/favourites/${id}`, { method: "DELETE" });

export const moveFavourite = (id: string, afterId: string | null) =>
  send<void>(`${base}/favourites/${id}/move`, json("POST", { afterId }));

/** The problem a refusal carried, as `refusalOf` kept it (a conflict's `problem`, or nothing). */
export const problemOf = (error: unknown): KnowledgeProblem | undefined => {
  const problem = (error as { problem?: unknown } | null)?.problem;
  return problem && typeof problem === "object" ? (problem as KnowledgeProblem) : undefined;
};

/** Deletes the article's live log, so the next open seeds from the saved JSON (K32, K35). */
export const resetLog = (id: string) => send<void>(`${base}/articles/${id}/collab/reset`, { method: "POST" });
