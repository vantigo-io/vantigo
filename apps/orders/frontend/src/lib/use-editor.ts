import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { ApiValidationError, isVersionConflict, ORDERS_QUERY_KEY, refusedLines } from "../api/request";
import type { Order, Quote, Subscription } from "../api/types";
import { lineRefusalWords, refusalMessage } from "./errors";
import { useOrdersFormat } from "./format";
import { type HeaderState, headerOf } from "./header";
import { type EditableLine, fromLine, incompleteLines } from "./lines";

/**
 * A quote's, an order's or a subscription's editor state: the header and the lines as the
 * person edits them, from the document the page read. The page keys the
 * editor by the document's version, so a save's answer — or a reload after a
 * conflict — starts it afresh from the server's document, amounts and all.
 */
export const useEditorState = (document: Quote | Order | Subscription) => {
  const { t } = useOrdersFormat();
  const [header, setHeader] = useState<HeaderState>(() => headerOf(document));
  // A kit's child lines are the server's, written from their kit
  // (manufacturing phase 2 M145): the editor holds the kit alone.
  const ownLines = document.lines.filter((line) => line.parentLineId == null);
  const [lines, setLines] = useState<EditableLine[]>(() => ownLines.map((line) => fromLine(line)));
  const [dirty, setDirty] = useState(false);
  const [refused, setRefused] = useState<ReadonlyMap<number, string>>(new Map());
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [conflict, setConflict] = useState(false);

  const patchHeader = (patch: Partial<HeaderState>) => {
    setHeader((current) => ({ ...current, ...patch }));
    setDirty(true);
  };
  const changeLines = (next: EditableLine[]) => {
    setLines(next);
    setDirty(true);
    setRefused(new Map());
  };

  /** Marks the rows the save would send incomplete; true when there are none. */
  const checkLines = (template = false) => {
    const problems = incompleteLines(lines, { template });
    setRefused(new Map([...problems].map(([index, key]) => [index, t(key)])));
    return problems.size === 0;
  };

  /**
   * Says a refusal where it belongs: a stale version as the conflict banner,
   * a line's code under its row, a field's under its input, the rest as a
   * notification. Answers whether it was said on the page itself.
   */
  const showRefusal = (error: unknown, title: string) => {
    if (isVersionConflict(error)) {
      setConflict(true);
      return;
    }
    const failing = refusedLines(error);
    if (failing.length > 0)
      setRefused(new Map(failing.map((line) => [line.index, lineRefusalWords(line.code, line.detail, t)])));
    if (error instanceof ApiValidationError) {
      const onFields: Record<string, string> = {};
      for (const [field, messages] of Object.entries(error.errors)) {
        const row = /^lines\[(\d+)\]/.exec(field);
        if (row) setRefused((current) => new Map([...current, [Number(row[1]), messages[0] ?? ""]]));
        else onFields[field] = messages[0] ?? "";
      }
      setFieldErrors(onFields);
    }
    notifications.show({ color: "red", title, message: refusalMessage(error, t) });
  };

  return {
    header,
    lines,
    dirty,
    refused,
    fieldErrors,
    conflict,
    setConflict,
    patchHeader,
    changeLines,
    checkLines,
    showRefusal,
    hasOptional: lines.some((line) => line.optional),
    stale: lines.some((line) => line.changed) || lines.length !== ownLines.length,
  };
};

/**
 * A write that answers the document: its answer replaces the cached one —
 * which starts the editor afresh from it — every other orders query is read
 * again, and the person is told it is done; a refusal is said by the editor.
 */
export const useDocumentWrite = <D extends Quote | Order | Subscription, A = void>(options: {
  detailKey: readonly unknown[];
  write: (argument: A) => Promise<D>;
  done?: string;
  errorTitle: string;
  showRefusal: (error: unknown, title: string) => void;
  onDone?: (document: D) => void;
}) => {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: options.write,
    onSuccess: async (document) => {
      queryClient.setQueryData(options.detailKey, document);
      // Every list, summary, log and document list moves with it; the
      // detail is the answer just cached, and is not read again.
      await queryClient.invalidateQueries({
        queryKey: [ORDERS_QUERY_KEY],
        predicate: (query) => query.queryKey[2] !== "detail",
      });
      if (options.done) notifications.show({ color: "green", message: options.done });
      options.onDone?.(document);
    },
    onError: (error) => options.showRefusal(error, options.errorTitle),
  });
};
