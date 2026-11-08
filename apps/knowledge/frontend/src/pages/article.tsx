import { Alert, Anchor, Breadcrumbs, Button, Group, Modal, Stack, Text, TextInput } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertTriangle, IconEye, IconLock, IconTrash } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import type { Editor, JSONContent } from "@tiptap/react";
import { ContentSkeleton, PageSection, useI18n, useLiveTopic, usePresence } from "@vantigo/frontend-shell";
import { type MutableRefObject, useCallback, useEffect, useRef, useState } from "react";
import {
  accessQueryOptions,
  articleQueryKey,
  articleQueryOptions,
  favouritesQueryKey,
  type KnowledgeArticle,
  type KnowledgeSaved,
  type KnowledgeVersion,
  problemOf,
  resetLog,
  saveContent,
  treeQueryKey,
  updateArticle,
  versionsQueryOptions,
} from "../api/articles";
import { NotFoundError, refusalCode } from "../api/request";
import { AccessDialog } from "../components/access-dialog";
import { Backlinks } from "../components/backlinks";
import { HistoryDrawer } from "../components/history-drawer";
import { IconPicker } from "../components/icon-picker";
import { KnowledgeLayout } from "../components/knowledge-layout";
import { PageMenu } from "../components/page-menu";
import { EditingBanner, PresenceAvatars } from "../components/presence";
import { ArticleEditor } from "../editor/article-editor";
import { seedState } from "../editor/collab/seed";
import { useCollabProvider } from "../editor/collab/use-collab";
import { useCursorUser } from "../editor/collab/user";
import { editorExtensions } from "../editor/extensions";
import { useArticleEditor } from "../editor/use-article-editor";
import { pathIsFile } from "../lib/document-path";
import { clearDraft, isDraftUnsaved, type LocalDraft, readDraft, writeDraft } from "../lib/draft";
import { expandAll } from "../lib/expanded";
import { useKnowledgeFormat } from "../lib/format";
import { recordModules } from "../lib/record-modules";
import { ARTICLE_ROUTE_PATH } from "../lib/routes";

/** How long typing must pause before the editor saves (K9). */
const AUTOSAVE_DELAY_MS = 1500;
/** In live co-editing the JSON is saved 5 s after the document last changed, at most 30 s apart, each with 0–2 s of jitter (K32). */
const COLLAB_IDLE_MS = 5_000;
const COLLAB_MAX_MS = 30_000;
const COLLAB_JITTER_MS = 2_000;

/** A document as text with its keys in order, so two JSONs of one document compare equal whatever order the server stored them in. */
const canon = (value: unknown): string =>
  JSON.stringify(value, (_key, item) =>
    item && typeof item === "object" && !Array.isArray(item)
      ? Object.fromEntries(Object.entries(item as Record<string, unknown>).sort(([a], [b]) => a.localeCompare(b)))
      : item,
  );

/** An article's live topic (K13). */
const articleTopic = (id: string) => `knowledge.article:${id}`;

type SaveState = "saved" | "unsaved" | "saving" | "failed";

/** What the page's live handler must know of the editor: whether it holds text not yet saved. */
interface EditorFlags {
  dirty: boolean;
  /** The editor is the collaborative one: a save by someone else is no reason to hold back a refetch. */
  collab: boolean;
}

/**
 * One article (knowledge design K3, K7, K9): its icon, its title and the
 * editor, saved 1.5 s after typing stops. A save names the version it was
 * typed over; somebody else's save in between opens the conflict dialog. Live
 * events refresh a reader's view, never an editor's own unsaved text.
 */
export const ArticlePage = ({ articleId }: { articleId: string }) => {
  const { t } = useI18n("knowledge");
  const queryClient = useQueryClient();
  const flagsRef = useRef<EditorFlags>({ dirty: false, collab: false });
  // Which article a live `trashed` was heard for, so opening another forgets it.
  const [trashedId, setTrashedId] = useState<string | null>(null);
  const trashed = trashedId === articleId;
  const live = useLiveTopic(articleTopic(articleId), (event) => {
    const refetch = () => void queryClient.invalidateQueries({ queryKey: articleQueryKey(articleId) });
    if (!("ids" in event)) {
      if (!flagsRef.current.dirty || flagsRef.current.collab) refetch();
      return;
    }
    if (event.kind === "trashed") setTrashedId(articleId);
    else if (event.kind === "content.changed") {
      if (!flagsRef.current.dirty || flagsRef.current.collab) refetch();
    } else if (event.kind === "meta.changed") {
      refetch();
      void queryClient.invalidateQueries({ queryKey: accessQueryOptions(articleId).queryKey });
    }
  });
  const article = useQuery({
    ...articleQueryOptions(articleId),
    refetchInterval: live ? false : 60_000,
  });

  useEffect(() => {
    if (article.data) expandAll(article.data.path.map((item) => item.id));
  }, [article.data]);

  const lostAccess = article.error instanceof NotFoundError;
  return (
    <KnowledgeLayout activeId={articleId}>
      {article.data ? (
        <ArticleView
          key={article.data.id}
          article={article.data}
          flagsRef={flagsRef}
          trashed={trashed}
          lostAccess={lostAccess}
          live={live}
        />
      ) : article.isPending ? (
        <ContentSkeleton rows={6} rowHeight={32} />
      ) : (
        <PageSection>
          <Alert color={lostAccess ? "gray" : "red"} title={t(lostAccess ? "noAccessTitle" : "loadFailed")}>
            {lostAccess ? t("noAccessBody") : null}
          </Alert>
        </PageSection>
      )}
    </KnowledgeLayout>
  );
};

interface Conflict {
  name: string | null;
  currentVersion: number | null;
}

const ArticleView = ({
  article,
  flagsRef,
  trashed,
  lostAccess,
  live,
}: {
  article: KnowledgeArticle;
  flagsRef: MutableRefObject<EditorFlags>;
  trashed: boolean;
  lostAccess: boolean;
  /** The article's live topic is confirmed: the page may edit together with others (K33). */
  live: boolean;
}) => {
  const { t, dateTime } = useKnowledgeFormat();
  const queryClient = useQueryClient();
  const [title, setTitle] = useState(article.title);
  const [saveState, setSaveState] = useState<SaveState>("saved");
  const [conflict, setConflict] = useState<Conflict | null>(null);
  const [problem, setProblem] = useState<"tooLarge" | { invalid: string; file: boolean } | null>(null);
  const [draft, setDraft] = useState<LocalDraft | null>(() => {
    const stored = readDraft(article.id);
    return isDraftUnsaved(stored, article) ? stored : null;
  });
  const [focused, setFocused] = useState(false);
  const [dirty, setDirtyState] = useState(false);
  const [accessOpen, setAccessOpen] = useState(false);
  const [historyOpen, setHistoryOpen] = useState(false);
  // A save answered 404: the page was trashed or the person lost access while editing.
  const [gone, setGone] = useState(false);
  // The editor is the collaborative one (K33); false is K-A's single editor.
  const [collabOn, setCollabOn] = useState(false);
  // The log was reset while text was unsent: the text is offered back as a draft (K32).
  const [replaced, setReplaced] = useState(false);
  // A reset's refetch is under way: the rebuilt editor waits for it (K32).
  const [refreshing, setRefreshing] = useState(false);
  const noAccess = lostAccess || gone;

  const canWrite = article.permission === "write" && !article.locked && !trashed && !noAccess;

  // The save's state, in refs: the timer and the live handler read them after the render that set them.
  const base = useRef(article.version);
  const titleRef = useRef(article.title);
  const savedTitle = useRef(article.title);
  // What the server holds, as text, so a collaborative save of an unchanged page is skipped (K32).
  const lastSaved = useRef(canon({ title: article.title, content: article.content }));
  const changes = useRef(0);
  const saving = useRef(false);
  const again = useRef(false);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const maxTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const editorRef = useRef<Editor | null>(null);
  const blocked = useRef(false);
  // The collaborative save's inputs, read by timers and the save itself.
  const collabSave = useRef(false);
  const collabOnRef = useRef(false);
  const canWriteRef = useRef(canWrite);
  const rebuilding = useRef(false);
  // A save that waited for the log, and a restore that waits for its change to be acknowledged.
  const held = useRef(false);
  const restorePending = useRef(false);
  const hasLogRef = useRef(false);

  const setDirty = useCallback(
    (value: boolean) => {
      flagsRef.current.dirty = value;
      setDirtyState(value);
    },
    [flagsRef],
  );

  const cursorUser = useCursorUser(t("collabSomeone"));
  const loadSource = useCallback(async () => {
    const fresh = await queryClient.fetchQuery({ ...articleQueryOptions(article.id), staleTime: 0 });
    return { content: fresh.content, version: fresh.version };
  }, [article.id, queryClient]);
  const buildSeed = useCallback(
    (content: unknown) =>
      seedState(
        content,
        editorExtensions(t("editorPlaceholder"), {
          articleId: article.id,
          moduleName: (module) => t(recordModules[module]),
        }),
      ),
    [article.id, t],
  );
  const clearSaveTimers = useCallback(() => {
    if (timer.current) clearTimeout(timer.current);
    if (maxTimer.current) clearTimeout(maxTimer.current);
    timer.current = null;
    maxTimer.current = null;
  }, []);
  // What this tab typed and the server does not hold is put aside as the local draft, and said so (K32).
  const keepAsDraft = useCallback(() => {
    const current = editorRef.current;
    if (!current || !flagsRef.current.dirty || !canWriteRef.current) return;
    const content = current.getJSON();
    if (canon({ title: titleRef.current, content }) === lastSaved.current) return;
    writeDraft(article.id, { title: titleRef.current, content, baseVersion: base.current, savedAt: Date.now() });
    setDraft(readDraft(article.id));
    setReplaced(true);
  }, [article.id, flagsRef]);
  const settleAfterDraft = useCallback(() => {
    clearSaveTimers();
    again.current = false;
    held.current = false;
    changes.current = 0;
    flagsRef.current.dirty = false;
    setDirtyState(false);
    setSaveState("saved");
    setTitle(savedTitle.current);
    titleRef.current = savedTitle.current;
  }, [clearSaveTimers, flagsRef]);
  const onReset = useCallback(() => {
    // The log was deleted by a save without the live connection, a restore or a rebuild (K32): this copy is gone.
    // Whatever differs from the saved text is kept, whether or not it had reached the log.
    if (!rebuilding.current) keepAsDraft();
    rebuilding.current = false;
    settleAfterDraft();
    setCollabOn(false);
    setRefreshing(true);
    void queryClient
      .fetchQuery({ ...articleQueryOptions(article.id), staleTime: 0 })
      .catch(() => undefined)
      .finally(() => setRefreshing(false));
  }, [article.id, keepAsDraft, queryClient, settleAfterDraft]);
  const { provider, state: collabState } = useCollabProvider({
    articleId: article.id,
    enabled: live && !noAccess && !trashed,
    canWrite,
    recheck: `${article.version}${article.updatedAt}${article.locked}${article.permission}`,
    loadSource,
    buildSeed,
    onReset,
  });
  const collabBroken = collabState.status === "broken";
  // The live copy cannot be used: the saved version is shown, read-only, until it is rebuilt (K35).
  const readOnly = !canWrite || collabBroken || collabState.readOnly;
  // Between the first live answer and the editor binding to the log the stored text is shown, still (K29).
  const waitingForLog =
    refreshing ||
    (provider !== null && !collabOn && (collabState.status === "syncing" || collabState.status === "seeding"));
  // A broken live copy is left at once: the saved text is shown, read-only, until it is rebuilt (K35).
  const collab = collabOn && !collabBroken && provider && cursorUser ? { provider, user: cursorUser } : null;
  const inCollab = collab !== null;
  // Where the next save goes: through the log's save while the live copy is in step, K-A's otherwise (K33).
  const collabCanSave =
    collab !== null && collabState.status === "ready" && !collabState.paused && !collabState.readOnly;
  useEffect(() => {
    collabSave.current = collabCanSave;
    collabOnRef.current = inCollab;
    canWriteRef.current = canWrite && !collabBroken;
    hasLogRef.current = collabState.hasLog;
    flagsRef.current.collab = inCollab;
  });

  const save = useCallback(async () => {
    const editor = editorRef.current;
    if (!editor || blocked.current || !canWriteRef.current) return;
    if (saving.current) {
      again.current = true;
      return;
    }
    // Once the page has a live log, K-A's save would be refused (or reset the log under the others):
    // the text waits for the log's save instead.
    if ((collabOnRef.current || hasLogRef.current) && !collabSave.current) {
      held.current = true;
      return;
    }
    if (maxTimer.current) clearTimeout(maxTimer.current);
    maxTimer.current = null;
    const viaLog = collabSave.current;
    saving.current = true;
    // What this save sends, taken before it is sent: text typed while it is under way belongs to the next save.
    const at = changes.current;
    const sentTitle = titleRef.current;
    const sentContent = editor.getJSON();
    if (viaLog && canon({ title: sentTitle, content: sentContent }) === lastSaved.current) {
      // Somebody's save already holds exactly this: nothing to send.
      saving.current = false;
      if (changes.current === at) {
        setDirty(false);
        setSaveState("saved");
      }
      return;
    }
    setSaveState("saving");
    try {
      const saved = await saveContent(
        article.id,
        viaLog
          ? { title: sentTitle, content: sentContent, mode: "collab" }
          : { title: sentTitle, content: sentContent, expectedVersion: base.current },
      );
      base.current = saved.version;
      lastSaved.current = canon({ title: sentTitle, content: sentContent });
      setProblem(null);
      queryClient.setQueryData<KnowledgeArticle>(articleQueryKey(article.id), (old) =>
        old
          ? {
              ...old,
              title: sentTitle,
              content: sentContent,
              version: saved.version,
              updatedAt: saved.updatedAt,
              updatedBy: saved.updatedBy,
            }
          : old,
      );
      if (sentTitle !== savedTitle.current) {
        savedTitle.current = sentTitle;
        void queryClient.invalidateQueries({ queryKey: treeQueryKey });
        void queryClient.invalidateQueries({ queryKey: favouritesQueryKey });
      }
      if (changes.current === at) {
        setDirty(false);
        clearDraft(article.id);
        setSaveState("saved");
      } else again.current = true;
    } catch (error) {
      const code = refusalCode(error);
      const status = (error as { status?: number } | null)?.status;
      setSaveState("failed");
      if (error instanceof NotFoundError) {
        // Access lost or the page trashed meanwhile: stop saving, keep the local draft.
        blocked.current = true;
        setGone(true);
        void queryClient.invalidateQueries({ queryKey: articleQueryKey(article.id) });
      } else if (code === "version_conflict") {
        const answer = problemOf(error);
        blocked.current = true;
        setConflict({ name: answer?.updatedBy?.displayName ?? null, currentVersion: answer?.currentVersion ?? null });
      } else if (code === "collab_active") {
        // Others are editing through the live log: this text is kept aside and the page joins them.
        keepAsDraft();
        settleAfterDraft();
        provider?.resync();
        void queryClient.invalidateQueries({ queryKey: articleQueryKey(article.id) });
      } else if (code === "collab_inactive") {
        // The log was reset under this copy: ask for it again; the next change saves.
        provider?.resync();
      } else if (code === "document_too_large" || status === 413) {
        setProblem("tooLarge");
      } else if (code === "invalid_document") {
        const path = problemOf(error)?.path ?? "";
        setProblem({ invalid: path, file: pathIsFile(editorRef.current?.getJSON(), path) });
      } else if (code === "article_locked" || code === "article_read_only") {
        notifications.show({ color: "yellow", message: t(code === "article_locked" ? "lockedNow" : "readOnlyNow") });
        void queryClient.invalidateQueries({ queryKey: articleQueryKey(article.id) });
      }
    } finally {
      saving.current = false;
      if (again.current && !blocked.current) {
        again.current = false;
        void save();
      }
    }
  }, [article.id, keepAsDraft, provider, queryClient, setDirty, settleAfterDraft, t]);

  const schedule = useCallback(() => {
    if (timer.current) clearTimeout(timer.current);
    const viaLog = collabSave.current;
    const delay = viaLog ? COLLAB_IDLE_MS + Math.random() * COLLAB_JITTER_MS : AUTOSAVE_DELAY_MS;
    timer.current = setTimeout(() => {
      clearSaveTimers();
      void save();
    }, delay);
    // However long the typing goes on, the log's text reaches the JSON at least this often.
    if (viaLog && !maxTimer.current) {
      maxTimer.current = setTimeout(
        () => {
          clearSaveTimers();
          void save();
        },
        COLLAB_MAX_MS + Math.random() * COLLAB_JITTER_MS,
      );
    }
  }, [clearSaveTimers, save]);

  const changed = useCallback(() => {
    // A reader sees the others' text arrive; none of it is theirs to save.
    if (!canWriteRef.current) return;
    changes.current += 1;
    setDirty(true);
    setSaveState("unsaved");
    const editor = editorRef.current;
    // The local draft is K-A's safety net; in live co-editing the log is the net.
    if (editor && !collabOnRef.current)
      writeDraft(article.id, {
        title: titleRef.current,
        content: editor.getJSON(),
        baseVersion: base.current,
        savedAt: Date.now(),
      });
    schedule();
  }, [article.id, schedule, setDirty]);

  const editor = useArticleEditor({
    content: article.content,
    editable: !readOnly && !waitingForLog,
    articleId: article.id,
    onUpdate: changed,
    collab,
  });
  useEffect(() => {
    editorRef.current = editor;
  }, [editor]);

  // The log is read and the page is quiet: the editor binds to it (K33). Text typed in K-A mode is saved first.
  const logReady = provider !== null && collabState.status === "ready" && collabState.hasLog && !collabBroken;
  const bindToLog = useCallback(() => {
    // Text typed in K-A mode against a log that is live is put aside as a draft, not saved over it.
    if (flagsRef.current.dirty) {
      keepAsDraft();
      settleAfterDraft();
    }
    setCollabOn(true);
  }, [flagsRef, keepAsDraft, settleAfterDraft]);
  useEffect(() => {
    if (!logReady || collabOn || cursorUser === null || conflict) return;
    if (saving.current) return;
    bindToLog();
  }, [logReady, collabOn, cursorUser, conflict, dirty, saveState, bindToLog]);
  // A save that waited for the log goes when the log can take it; a restore, once its change is acknowledged.
  useEffect(() => {
    if (!collabCanSave) return;
    if (held.current) {
      held.current = false;
      schedule();
    }
    if (restorePending.current && !collabState.unsent) {
      restorePending.current = false;
      clearSaveTimers();
      void save();
    }
  }, [collabCanSave, collabState.unsent, schedule, save, clearSaveTimers]);
  // The caret shows the name once the profile has loaded.
  useEffect(() => {
    if (editor && !editor.isDestroyed && inCollab && cursorUser) editor.commands.updateUser(cursorUser);
  }, [editor, inCollab, cursorUser]);

  // Someone else's save, heard live or polled: shown at once, unless this editor holds unsaved text.
  // In live co-editing the log carries their text; only the title and the version token follow.
  useEffect(() => {
    if (!editor || article.version <= base.current) return;
    if (collabOnRef.current) {
      base.current = article.version;
      lastSaved.current = canon({ title: article.title, content: article.content });
      if (titleRef.current === savedTitle.current) {
        setTitle(article.title);
        titleRef.current = article.title;
      }
      savedTitle.current = article.title;
      return;
    }
    if (flagsRef.current.dirty) return;
    base.current = article.version;
    lastSaved.current = canon({ title: article.title, content: article.content });
    editor.commands.setContent(article.content, { emitUpdate: false });
    setTitle(article.title);
    titleRef.current = article.title;
    savedTitle.current = article.title;
  }, [article.version, article.content, article.title, editor, flagsRef]);

  // Leaving: a last save, best effort; the local draft covers what it cannot. Only on unmount,
  // through a ref, so a new `save` (a language switch, say) never runs it mid-session;
  // `flagsRef` is the page's own ref and never changes.
  const saveRef = useRef(save);
  useEffect(() => {
    saveRef.current = save;
  }, [save]);
  useEffect(() => {
    const flags = flagsRef.current;
    return () => {
      if (timer.current || maxTimer.current) {
        clearSaveTimers();
        void saveRef.current();
      }
      flags.dirty = false;
      flags.collab = false;
    };
  }, [flagsRef, clearSaveTimers]);

  const members = usePresence(articleTopic(article.id), focused && dirty ? "editing" : "viewing");

  const icon = useMutation({
    mutationFn: (value: string) => updateArticle(article.id, { icon: value }),
    onSuccess: (updated) => {
      queryClient.setQueryData<KnowledgeArticle>(articleQueryKey(article.id), (old) =>
        old ? { ...old, icon: updated.icon } : updated,
      );
      void queryClient.invalidateQueries({ queryKey: treeQueryKey });
      void queryClient.invalidateQueries({ queryKey: favouritesQueryKey });
    },
    onError: (error) => lockedOrFailed(error),
  });

  // A PATCH can meet a lock taken meanwhile (409 article_locked): said as a content save's is.
  const lockedOrFailed = (error: unknown) => {
    if (refusalCode(error) === "article_locked") {
      notifications.show({ color: "yellow", message: t("lockedNow") });
      void queryClient.invalidateQueries({ queryKey: articleQueryKey(article.id) });
    } else notifications.show({ color: "red", message: t("updateFailed") });
  };

  const reload = async () => {
    blocked.current = false;
    setConflict(null);
    clearDraft(article.id);
    changes.current = 0;
    setDirty(false);
    setSaveState("saved");
    const fresh = await queryClient.fetchQuery({ ...articleQueryOptions(article.id), staleTime: 0 });
    base.current = fresh.version;
    lastSaved.current = canon({ title: fresh.title, content: fresh.content });
    editor?.commands.setContent(fresh.content, { emitUpdate: false });
    setTitle(fresh.title);
    titleRef.current = fresh.title;
    savedTitle.current = fresh.title;
  };

  const keepMine = async () => {
    setConflict(null);
    if (conflict?.currentVersion) base.current = conflict.currentVersion;
    else base.current = (await queryClient.fetchQuery({ ...articleQueryOptions(article.id), staleTime: 0 })).version;
    blocked.current = false;
    void save();
  };

  const restoreDraft = () => {
    if (!draft || !editor) return;
    editor.commands.setContent(draft.content, { emitUpdate: false });
    setTitle(draft.title);
    titleRef.current = draft.title;
    // Saved over the version it was typed on, so a save by someone else since is a conflict, not lost.
    if (!collabOnRef.current) base.current = draft.baseVersion;
    setDraft(null);
    setReplaced(false);
    changed();
  };

  const discardDraft = () => {
    clearDraft(article.id);
    setDraft(null);
    setReplaced(false);
  };

  const onRestored = (saved: KnowledgeSaved) => {
    // A pending autosave would now be a save over the restored version: drop it.
    clearSaveTimers();
    again.current = false;
    base.current = saved.version - 1;
    changes.current = 0;
    setDirty(false);
    setSaveState("saved");
    clearDraft(article.id);
    void queryClient.invalidateQueries({ queryKey: articleQueryKey(article.id) });
    void queryClient.invalidateQueries({ queryKey: versionsQueryOptions(article.id).queryKey });
  };

  // In live co-editing a restore replaces the text in one transaction, which the others see as one change
  // and can undo, and saves it through the log's save; the restore endpoint would delete the log (K33).
  const restoreInEditor = (version: KnowledgeVersion) => {
    if (!editor) return;
    clearSaveTimers();
    editor.commands.setContent(version.content as JSONContent, { emitUpdate: false });
    setTitle(version.title);
    titleRef.current = version.title;
    changes.current += 1;
    setDirty(true);
    void queryClient.invalidateQueries({ queryKey: versionsQueryOptions(article.id).queryKey });
    // Saved once the others' copies hold it (the change acknowledged), so the JSON is never behind the log's text.
    restorePending.current = true;
  };

  const rebuild = useMutation({
    mutationFn: () => resetLog(article.id),
    onMutate: () => {
      rebuilding.current = true;
    },
    onError: () => {
      rebuilding.current = false;
      notifications.show({ color: "red", message: t("collabRebuildFailed") });
    },
  });

  return (
    <Stack gap="md">
      <PageSection>
        <Stack gap="sm">
          <Group justify="space-between" wrap="nowrap" align="flex-start">
            <Breadcrumbs separatorMargin={4}>
              {article.path.map((item) => (
                <Anchor
                  key={item.id}
                  size="xs"
                  c="dimmed"
                  renderRoot={(props) => <Link to={ARTICLE_ROUTE_PATH} params={{ articleId: item.id }} {...props} />}
                >
                  {item.title || t("untitled")}
                </Anchor>
              ))}
            </Breadcrumbs>
            <Group gap="sm" wrap="nowrap">
              <PresenceAvatars members={members} />
              <Text size="xs" c={saveState === "failed" ? "red" : "dimmed"} role="status" aria-live="polite">
                {readOnly ? "" : t(`save_${saveState}`)}
              </Text>
              <PageMenu
                article={article}
                onOpenAccess={() => setAccessOpen(true)}
                onOpenHistory={() => setHistoryOpen(true)}
              />
            </Group>
          </Group>
          <IconPicker
            value={article.icon}
            disabled={article.permission !== "write" || trashed}
            onChange={(value) => icon.mutate(value)}
          />
          <TextInput
            variant="unstyled"
            size="xl"
            aria-label={t("titleLabel")}
            placeholder={t("untitled")}
            value={title}
            readOnly={readOnly}
            maxLength={200}
            styles={{ input: { fontSize: "2rem", fontWeight: 700, height: "auto" } }}
            onChange={(event) => {
              setTitle(event.currentTarget.value);
              titleRef.current = event.currentTarget.value;
              changed();
            }}
          />
          <Text size="xs" c="dimmed">
            {t("updatedBy", {
              name: article.updatedBy?.displayName ?? t("formerUser"),
              when: dateTime(article.updatedAt),
            })}
          </Text>
        </Stack>
      </PageSection>

      {trashed && (
        <Alert color="red" icon={<IconTrash size={16} />} title={t("trashedBanner")}>
          {t("trashedBannerBody")}
        </Alert>
      )}
      {noAccess && (
        <Alert color="gray" title={t("noAccessTitle")}>
          {t("noAccessBody")}
        </Alert>
      )}
      {article.locked && !trashed && (
        <Alert color="blue" icon={<IconLock size={16} />} role="status">
          {t(article.permission === "write" ? "lockedBannerWriter" : "lockedBanner")}
        </Alert>
      )}
      {article.permission === "read" && !article.locked && !trashed && (
        <Alert color="gray" icon={<IconEye size={16} />} role="status">
          {t("readOnlyBanner")}
        </Alert>
      )}
      {!inCollab && <EditingBanner members={members} />}
      {inCollab && collabState.status !== "ready" && !readOnly && (
        <Text size="xs" c="dimmed" role="status">
          {t("collabOffline")}
        </Text>
      )}
      {collabState.paused && (
        <Alert color="yellow" icon={<IconAlertTriangle size={16} />} title={t("collabPausedTitle")} role="status">
          {t("collabPausedBody")}
        </Alert>
      )}
      {collabBroken && (
        <Alert color="red" icon={<IconAlertTriangle size={16} />} title={t("collabBrokenTitle")} role="alert">
          <Text size="sm">{t(canWrite ? "collabBrokenBodyWriter" : "collabBrokenBodyReader")}</Text>
          {canWrite && (
            <Button mt="xs" size="xs" loading={rebuild.isPending} onClick={() => rebuild.mutate()}>
              {t("collabRebuild")}
            </Button>
          )}
        </Alert>
      )}
      {replaced && (
        <Alert color="yellow" icon={<IconAlertTriangle size={16} />} title={t("collabReplacedTitle")} role="status">
          {t("collabReplacedBody")}
        </Alert>
      )}
      {draft && !readOnly && (!inCollab || replaced) && (
        <Alert color="blue" title={t("draftTitle")}>
          <Text size="sm">{t("draftBody", { when: dateTime(new Date(draft.savedAt).toISOString()) })}</Text>
          <Group mt="xs" gap="xs">
            <Button size="xs" onClick={restoreDraft}>
              {t("draftRestore")}
            </Button>
            <Button size="xs" variant="default" onClick={discardDraft}>
              {t("draftDiscard")}
            </Button>
          </Group>
        </Alert>
      )}
      {problem === "tooLarge" && (
        <Alert color="red" icon={<IconAlertTriangle size={16} />} title={t("tooLargeTitle")} role="alert">
          {t("tooLargeBody")}
        </Alert>
      )}
      {problem && problem !== "tooLarge" && (
        <Alert color="red" icon={<IconAlertTriangle size={16} />} title={t("invalidTitle")} role="alert">
          {problem.file ? t("invalidFileBody") : t("invalidBody", { path: problem.invalid })}
        </Alert>
      )}

      <PageSection
        onFocusCapture={() => setFocused(true)}
        onBlurCapture={(event) => {
          if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setFocused(false);
        }}
      >
        <ArticleEditor
          editor={editor}
          editable={!readOnly && !waitingForLog}
          label={t("contentLabel")}
          articleId={article.id}
        />
      </PageSection>
      {!noAccess && <Backlinks articleId={article.id} />}

      <Modal
        opened={conflict !== null}
        onClose={() => undefined}
        withCloseButton={false}
        closeOnClickOutside={false}
        closeOnEscape={false}
        centered
        title={conflict?.name ? t("conflictTitle", { name: conflict.name }) : t("conflictTitleSomeone")}
      >
        <Text size="sm">{t("conflictBody")}</Text>
        <Group justify="flex-end" mt="lg">
          <Button variant="default" onClick={() => void reload()}>
            {t("conflictReload")}
          </Button>
          <Button onClick={() => void keepMine()}>{t("conflictKeepMine")}</Button>
        </Group>
      </Modal>

      <AccessDialog
        articleId={article.id}
        // parentId is null under a parent the reader cannot see too: a Shared top item may not be a root.
        isRoot={article.parentId === null && article.section !== "shared"}
        opened={accessOpen}
        onClose={() => setAccessOpen(false)}
        onLostAccess={() => {
          blocked.current = true;
          setGone(true);
          void queryClient.invalidateQueries({ queryKey: treeQueryKey });
          void queryClient.invalidateQueries({ queryKey: favouritesQueryKey });
        }}
      />
      <HistoryDrawer
        article={article}
        opened={historyOpen}
        onClose={() => setHistoryOpen(false)}
        canRestore={article.permission === "write" && !article.locked && !trashed}
        onRestored={onRestored}
        onRestoreInEditor={collabCanSave ? restoreInEditor : undefined}
      />
    </Stack>
  );
};
