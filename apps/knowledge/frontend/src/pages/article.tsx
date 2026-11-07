import { Alert, Anchor, Breadcrumbs, Button, Group, Modal, Stack, Text, TextInput } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertTriangle, IconEye, IconLock, IconTrash } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import type { Editor } from "@tiptap/react";
import { ContentSkeleton, PageSection, useI18n, useLiveTopic, usePresence } from "@vantigo/frontend-shell";
import { type MutableRefObject, useCallback, useEffect, useRef, useState } from "react";
import {
  accessQueryOptions,
  articleQueryKey,
  articleQueryOptions,
  favouritesQueryKey,
  type KnowledgeArticle,
  type KnowledgeSaved,
  problemOf,
  saveContent,
  treeQueryKey,
  updateArticle,
  versionsQueryOptions,
} from "../api/articles";
import { NotFoundError, refusalCode } from "../api/request";
import { AccessDialog } from "../components/access-dialog";
import { HistoryDrawer } from "../components/history-drawer";
import { IconPicker } from "../components/icon-picker";
import { KnowledgeLayout } from "../components/knowledge-layout";
import { PageMenu } from "../components/page-menu";
import { EditingBanner, PresenceAvatars } from "../components/presence";
import { ArticleEditor } from "../editor/article-editor";
import { useArticleEditor } from "../editor/use-article-editor";
import { clearDraft, isDraftUnsaved, type LocalDraft, readDraft, writeDraft } from "../lib/draft";
import { expandAll } from "../lib/expanded";
import { useKnowledgeFormat } from "../lib/format";
import { ARTICLE_ROUTE_PATH } from "../lib/routes";

/** How long typing must pause before the editor saves (K9). */
const AUTOSAVE_DELAY_MS = 1500;

/** An article's live topic (K13). */
const articleTopic = (id: string) => `knowledge.article:${id}`;

type SaveState = "saved" | "unsaved" | "saving" | "failed";

/** What the page's live handler must know of the editor: whether it holds text not yet saved. */
interface EditorFlags {
  dirty: boolean;
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
  const flagsRef = useRef<EditorFlags>({ dirty: false });
  // Which article a live `trashed` was heard for, so opening another forgets it.
  const [trashedId, setTrashedId] = useState<string | null>(null);
  const trashed = trashedId === articleId;
  const live = useLiveTopic(articleTopic(articleId), (event) => {
    const refetch = () => void queryClient.invalidateQueries({ queryKey: articleQueryKey(articleId) });
    if (!("ids" in event)) {
      if (!flagsRef.current.dirty) refetch();
      return;
    }
    if (event.kind === "trashed") setTrashedId(articleId);
    else if (event.kind === "content.changed") {
      if (!flagsRef.current.dirty) refetch();
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
}: {
  article: KnowledgeArticle;
  flagsRef: MutableRefObject<EditorFlags>;
  trashed: boolean;
  lostAccess: boolean;
}) => {
  const { t, dateTime } = useKnowledgeFormat();
  const queryClient = useQueryClient();
  const [title, setTitle] = useState(article.title);
  const [saveState, setSaveState] = useState<SaveState>("saved");
  const [conflict, setConflict] = useState<Conflict | null>(null);
  const [problem, setProblem] = useState<"tooLarge" | { invalid: string } | null>(null);
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
  const noAccess = lostAccess || gone;

  const readOnly = article.permission !== "write" || article.locked || trashed || noAccess;

  // The save's state, in refs: the timer and the live handler read them after the render that set them.
  const base = useRef(article.version);
  const titleRef = useRef(article.title);
  const savedTitle = useRef(article.title);
  const changes = useRef(0);
  const saving = useRef(false);
  const again = useRef(false);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const editorRef = useRef<Editor | null>(null);
  const blocked = useRef(false);

  const setDirty = useCallback(
    (value: boolean) => {
      flagsRef.current.dirty = value;
      setDirtyState(value);
    },
    [flagsRef],
  );

  const save = useCallback(async () => {
    const editor = editorRef.current;
    if (!editor || blocked.current) return;
    if (saving.current) {
      again.current = true;
      return;
    }
    saving.current = true;
    // What this save sends, taken before it is sent: text typed while it is under way belongs to the next save.
    const at = changes.current;
    const sentTitle = titleRef.current;
    const sentContent = editor.getJSON();
    setSaveState("saving");
    try {
      const saved = await saveContent(article.id, {
        title: sentTitle,
        content: sentContent,
        expectedVersion: base.current,
      });
      base.current = saved.version;
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
      } else if (code === "document_too_large" || status === 413) {
        setProblem("tooLarge");
      } else if (code === "invalid_document") {
        setProblem({ invalid: problemOf(error)?.path ?? "" });
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
  }, [article.id, queryClient, setDirty, t]);

  const schedule = useCallback(() => {
    if (timer.current) clearTimeout(timer.current);
    timer.current = setTimeout(() => {
      timer.current = null;
      void save();
    }, AUTOSAVE_DELAY_MS);
  }, [save]);

  const changed = useCallback(() => {
    changes.current += 1;
    setDirty(true);
    setSaveState("unsaved");
    const editor = editorRef.current;
    if (editor)
      writeDraft(article.id, {
        title: titleRef.current,
        content: editor.getJSON(),
        baseVersion: base.current,
        savedAt: Date.now(),
      });
    schedule();
  }, [article.id, schedule, setDirty]);

  const editor = useArticleEditor({ content: article.content, editable: !readOnly, onUpdate: changed });
  useEffect(() => {
    editorRef.current = editor;
  }, [editor]);

  // Someone else's save, heard live or polled: shown at once, unless this editor holds unsaved text.
  useEffect(() => {
    if (!editor || article.version <= base.current || flagsRef.current.dirty) return;
    base.current = article.version;
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
      if (timer.current) {
        clearTimeout(timer.current);
        void saveRef.current();
      }
      flags.dirty = false;
    };
  }, [flagsRef]);

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
    base.current = draft.baseVersion;
    setDraft(null);
    changed();
  };

  const discardDraft = () => {
    clearDraft(article.id);
    setDraft(null);
  };

  const onRestored = (saved: KnowledgeSaved) => {
    // A pending autosave would now be a save over the restored version: drop it.
    if (timer.current) {
      clearTimeout(timer.current);
      timer.current = null;
    }
    again.current = false;
    base.current = saved.version - 1;
    changes.current = 0;
    setDirty(false);
    setSaveState("saved");
    clearDraft(article.id);
    void queryClient.invalidateQueries({ queryKey: articleQueryKey(article.id) });
    void queryClient.invalidateQueries({ queryKey: versionsQueryOptions(article.id).queryKey });
  };

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
      <EditingBanner members={members} />
      {draft && !readOnly && (
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
          {t("invalidBody", { path: problem.invalid })}
        </Alert>
      )}

      <PageSection
        onFocusCapture={() => setFocused(true)}
        onBlurCapture={(event) => {
          if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setFocused(false);
        }}
      >
        <ArticleEditor editor={editor} editable={!readOnly} label={t("contentLabel")} />
      </PageSection>

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
      />
    </Stack>
  );
};
