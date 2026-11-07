import { ActionIcon, Box, Group, Loader, Stack, Text, Tooltip, UnstyledButton } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconChevronDown, IconChevronRight, IconPlus, IconStar, IconStarFilled } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { useI18n, useLiveTopic } from "@vantigo/frontend-shell";
import { createContext, type DragEvent, type ReactNode, useContext, useRef, useState } from "react";
import {
  addFavourite,
  createArticle,
  favouritesQueryKey,
  favouritesQueryOptions,
  type KnowledgeMoveInput,
  type KnowledgeRootSection,
  type KnowledgeSection,
  type KnowledgeTreeNode,
  moveArticle,
  moveFavourite,
  removeFavourite,
  sections,
  treeQueryKey,
  treeQueryOptions,
} from "../api/articles";
import { refusalCode } from "../api/request";
import { setExpanded, useExpanded } from "../lib/expanded";
import { ARTICLE_ROUTE_PATH } from "../lib/routes";
import { type DropPlace, moveFor } from "../lib/tree-moves";
import { ArticleIcon } from "./article-icon";
import { VisibilityConfirm } from "./visibility-confirm";

/** The live topic every reader of the sidebar listens to (K13); `tree.changed` carries no id. */
const TREE_TOPIC = "knowledge.tree";

type Dragged = { kind: "article"; node: KnowledgeTreeNode } | { kind: "favourite"; id: string };

const placeOf = (event: DragEvent<HTMLElement>): DropPlace => {
  const rect = event.currentTarget.getBoundingClientRect();
  if (rect.height <= 0) return "inside";
  const y = (event.clientY - rect.top) / rect.height;
  return y < 0.25 ? "before" : y > 0.75 ? "after" : "inside";
};

interface TreeContextValue {
  activeId?: string;
  favouriteIds: ReadonlySet<string>;
  pollInterval: number | false;
  draggedRef: React.MutableRefObject<Dragged | null>;
  onDropArticle: (input: KnowledgeMoveInput) => void;
  onCreateChild: (parent: KnowledgeTreeNode) => void;
  onToggleFavourite: (node: KnowledgeTreeNode) => void;
}

const TreeContext = createContext<TreeContextValue | null>(null);
const useTree = () => {
  const context = useContext(TreeContext);
  if (!context) throw new Error("a tree row outside the sidebar tree");
  return context;
};

const moveErrorKey = (code: string | undefined) =>
  code === "move_into_descendant"
    ? "moveIntoDescendant"
    : code === "too_deep"
      ? "moveTooDeep"
      : code === "article_read_only"
        ? "moveReadOnly"
        : "moveFailed";

/**
 * The sidebar's tree (knowledge design K4, K11): Favourites, Workspace,
 * Shared and Private, each opened level by level as the person expands it.
 * Rows drag to move and reorder; a move between Private and the workspace is
 * confirmed first. It listens to `knowledge.tree` and refetches on every
 * `tree.changed`, polling only while that topic is not live.
 */
export const SidebarTree = ({ activeId }: { activeId?: string }) => {
  const { t } = useI18n("knowledge");
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const refetchTree = () => {
    void queryClient.invalidateQueries({ queryKey: treeQueryKey });
    void queryClient.invalidateQueries({ queryKey: favouritesQueryKey });
  };
  const live = useLiveTopic(TREE_TOPIC, refetchTree);
  const pollInterval = live ? false : 60_000;
  const roots = useQuery({ ...treeQueryOptions(null), refetchInterval: pollInterval });
  const favourites = useQuery({ ...favouritesQueryOptions(), refetchInterval: pollInterval });
  const favouriteIds = new Set((favourites.data ?? []).map((node) => node.id));
  const draggedRef = useRef<Dragged | null>(null);
  const [pendingMove, setPendingMove] = useState<{ id: string; input: KnowledgeMoveInput } | null>(null);

  const move = useMutation({
    mutationFn: ({ id, input }: { id: string; input: KnowledgeMoveInput }) => moveArticle(id, input),
    onSuccess: () => {
      setPendingMove(null);
      refetchTree();
    },
    onError: (error, variables) => {
      if (refusalCode(error) === "visibility_change" && !variables.input.confirmVisibility) {
        setPendingMove(variables);
        return;
      }
      setPendingMove(null);
      notifications.show({ color: "red", message: t(moveErrorKey(refusalCode(error))) });
    },
  });

  const create = useMutation({
    mutationFn: (input: { parentId?: string; section?: KnowledgeRootSection }) =>
      createArticle({ ...input, title: "" }),
    onSuccess: (article, input) => {
      if (input.parentId) setExpanded(input.parentId, true);
      refetchTree();
      void navigate({ to: ARTICLE_ROUTE_PATH, params: { articleId: article.id } });
    },
    onError: () => notifications.show({ color: "red", message: t("createFailed") }),
  });

  const favourite = useMutation({
    mutationFn: (node: KnowledgeTreeNode) =>
      favouriteIds.has(node.id) ? removeFavourite(node.id) : addFavourite(node.id),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: favouritesQueryKey }),
    onError: () => notifications.show({ color: "red", message: t("favouriteFailed") }),
  });

  const reorderFavourite = useMutation({
    mutationFn: ({ id, afterId }: { id: string; afterId: string | null }) => moveFavourite(id, afterId),
    onSettled: () => void queryClient.invalidateQueries({ queryKey: favouritesQueryKey }),
  });

  const onDropArticle = (input: KnowledgeMoveInput) => {
    const item = draggedRef.current;
    draggedRef.current = null;
    if (item?.kind !== "article" || item.node.id === input.parentId) return;
    move.mutate({ id: item.node.id, input });
  };

  const context: TreeContextValue = {
    activeId,
    favouriteIds,
    pollInterval,
    draggedRef,
    onDropArticle,
    onCreateChild: (parent) => create.mutate({ parentId: parent.id }),
    onToggleFavourite: (node) => favourite.mutate(node),
  };

  const bySection = (section: KnowledgeSection) => (roots.data ?? []).filter((node) => node.section === section);
  const favouriteNodes = favourites.data ?? [];

  return (
    <TreeContext.Provider value={context}>
      <Stack gap="sm">
        {favouriteNodes.length > 0 && (
          <SectionBlock label={t("sectionFavourites")}>
            {favouriteNodes.map((node) => (
              <FavouriteRow
                key={node.id}
                node={node}
                onDrop={(place) => {
                  const item = draggedRef.current;
                  draggedRef.current = null;
                  if (item?.kind !== "favourite" || item.id === node.id) return;
                  const others = favouriteNodes.filter((other) => other.id !== item.id);
                  const at = others.findIndex((other) => other.id === node.id);
                  const afterId = place === "before" ? (others[at - 1]?.id ?? null) : node.id;
                  reorderFavourite.mutate({ id: item.id, afterId });
                }}
              />
            ))}
          </SectionBlock>
        )}
        {roots.isPending ? (
          <Loader size="xs" m="sm" />
        ) : roots.isError ? (
          <Text size="sm" c="red">
            {t("treeFailed")}
          </Text>
        ) : (
          sections.map((section) => {
            const nodes = bySection(section);
            if (section === "shared" && nodes.length === 0) return null;
            const root = section === "shared" ? undefined : (section as KnowledgeRootSection);
            return (
              <SectionBlock
                key={section}
                label={t(sectionLabelKey[section])}
                onCreate={root ? () => create.mutate({ section: root }) : undefined}
                createLabel={root ? t(root === "workspace" ? "newInWorkspace" : "newInPrivate") : undefined}
                onDrop={
                  root
                    ? () => onDropArticle({ parentId: null, afterId: nodes.at(-1)?.id ?? null, section: root })
                    : undefined
                }
              >
                {nodes.length === 0 ? (
                  <Text size="xs" c="dimmed" px="xs">
                    {t(section === "private" ? "privateEmpty" : "workspaceEmpty")}
                  </Text>
                ) : (
                  nodes.map((node) => <TreeRow key={node.id} node={node} siblings={nodes} depth={0} />)
                )}
              </SectionBlock>
            );
          })
        )}
      </Stack>
      <VisibilityConfirm
        opened={pendingMove !== null}
        busy={move.isPending}
        onCancel={() => setPendingMove(null)}
        onConfirm={() =>
          pendingMove && move.mutate({ id: pendingMove.id, input: { ...pendingMove.input, confirmVisibility: true } })
        }
      />
    </TreeContext.Provider>
  );
};

const sectionLabelKey: Record<KnowledgeSection, string> = {
  workspace: "sectionWorkspace",
  shared: "sectionShared",
  private: "sectionPrivate",
};

const SectionBlock = ({
  label,
  children,
  onCreate,
  createLabel,
  onDrop,
}: {
  label: string;
  children: ReactNode;
  onCreate?: () => void;
  createLabel?: string;
  onDrop?: () => void;
}) => (
  <Box role="group" aria-label={label}>
    <Group
      justify="space-between"
      px="xs"
      mb={2}
      onDragOver={onDrop ? (event) => event.preventDefault() : undefined}
      onDrop={
        onDrop
          ? (event) => {
              event.preventDefault();
              onDrop();
            }
          : undefined
      }
    >
      <Text size="xs" fw={700} c="dimmed" tt="uppercase">
        {label}
      </Text>
      {onCreate && createLabel && (
        <Tooltip label={createLabel}>
          <ActionIcon size="xs" variant="subtle" color="gray" aria-label={createLabel} onClick={onCreate}>
            <IconPlus size={14} />
          </ActionIcon>
        </Tooltip>
      )}
    </Group>
    {children}
  </Box>
);

const rowStyle = (active: boolean, over: DropPlace | null): React.CSSProperties => ({
  borderRadius: "var(--mantine-radius-sm)",
  background: active || over === "inside" ? "var(--mantine-color-default-hover)" : undefined,
  boxShadow:
    over === "before"
      ? "inset 0 2px 0 var(--mantine-primary-color-filled)"
      : over === "after"
        ? "inset 0 -2px 0 var(--mantine-primary-color-filled)"
        : undefined,
});

const RowTitle = ({ node }: { node: KnowledgeTreeNode }) => {
  const { t } = useI18n("knowledge");
  return (
    <Link
      to={ARTICLE_ROUTE_PATH}
      params={{ articleId: node.id }}
      draggable={false}
      style={{
        display: "flex",
        alignItems: "center",
        gap: 6,
        flex: 1,
        minWidth: 0,
        color: "inherit",
        textDecoration: "none",
        padding: "3px 0",
      }}
    >
      <ArticleIcon icon={node.icon} size={14} />
      <Text size="sm" truncate>
        {node.title || t("untitled")}
      </Text>
    </Link>
  );
};

const FavouriteStar = ({ node }: { node: KnowledgeTreeNode }) => {
  const { t } = useI18n("knowledge");
  const { favouriteIds, onToggleFavourite } = useTree();
  const on = favouriteIds.has(node.id);
  const label = t(on ? "removeFavourite" : "addFavourite");
  return (
    <ActionIcon
      size="xs"
      variant="subtle"
      color={on ? "yellow" : "gray"}
      aria-label={label}
      title={label}
      onClick={() => onToggleFavourite(node)}
    >
      {on ? <IconStarFilled size={12} /> : <IconStar size={12} />}
    </ActionIcon>
  );
};

/** One article in the tree, its children below it once expanded. */
const TreeRow = ({
  node,
  siblings,
  depth,
}: {
  node: KnowledgeTreeNode;
  siblings: readonly KnowledgeTreeNode[];
  depth: number;
}) => {
  const { t } = useI18n("knowledge");
  const { activeId, draggedRef, onDropArticle, onCreateChild } = useTree();
  const expanded = useExpanded();
  const open = expanded.has(node.id);
  const [over, setOver] = useState<DropPlace | null>(null);
  const canWrite = node.permission === "write";
  const toggleLabel = t(open ? "collapse" : "expand", { title: node.title || t("untitled") });

  return (
    <div>
      <Group
        gap={2}
        wrap="nowrap"
        pl={depth * 12}
        pr={4}
        draggable={canWrite}
        data-testid={`tree-row-${node.id}`}
        style={rowStyle(activeId === node.id, over)}
        onDragStart={(event) => {
          event.dataTransfer?.setData("text/plain", node.id);
          draggedRef.current = { kind: "article", node };
        }}
        onDragEnd={() => setOver(null)}
        onDragOver={(event) => {
          if (draggedRef.current?.kind !== "article") return;
          event.preventDefault();
          setOver(placeOf(event));
        }}
        onDragLeave={() => setOver(null)}
        onDrop={(event) => {
          event.preventDefault();
          event.stopPropagation();
          const place = placeOf(event);
          setOver(null);
          const item = draggedRef.current;
          if (item?.kind !== "article" || item.node.id === node.id) return;
          if (place === "inside") setExpanded(node.id, true);
          onDropArticle(moveFor(node, place, siblings, item.node.id));
        }}
      >
        {node.hasChildren ? (
          <ActionIcon
            size="xs"
            variant="subtle"
            color="gray"
            aria-label={toggleLabel}
            aria-expanded={open}
            onClick={() => setExpanded(node.id, !open)}
          >
            {open ? <IconChevronDown size={12} /> : <IconChevronRight size={12} />}
          </ActionIcon>
        ) : (
          <Box w={22} />
        )}
        <RowTitle node={node} />
        <FavouriteStar node={node} />
        {canWrite && (
          <ActionIcon
            size="xs"
            variant="subtle"
            color="gray"
            aria-label={t("newChild", { title: node.title || t("untitled") })}
            onClick={() => onCreateChild(node)}
          >
            <IconPlus size={12} />
          </ActionIcon>
        )}
      </Group>
      {open && node.hasChildren && <TreeChildren parentId={node.id} depth={depth + 1} />}
    </div>
  );
};

const TreeChildren = ({ parentId, depth }: { parentId: string; depth: number }) => {
  const { pollInterval } = useTree();
  const children = useQuery({ ...treeQueryOptions(parentId), refetchInterval: pollInterval });
  if (children.isPending) return <Loader size="xs" ml={depth * 12 + 24} my={4} />;
  const nodes = children.data ?? [];
  return (
    <div>
      {nodes.map((child) => (
        <TreeRow key={child.id} node={child} siblings={nodes} depth={depth} />
      ))}
    </div>
  );
};

/** A favourite: draggedRef among the favourites to reorder them (K11). */
const FavouriteRow = ({ node, onDrop }: { node: KnowledgeTreeNode; onDrop: (place: DropPlace) => void }) => {
  const { activeId, draggedRef } = useTree();
  const [over, setOver] = useState<DropPlace | null>(null);
  return (
    <Group
      gap={2}
      wrap="nowrap"
      pr={4}
      pl={22}
      draggable
      data-testid={`favourite-row-${node.id}`}
      style={rowStyle(activeId === node.id, over)}
      onDragStart={(event) => {
        event.dataTransfer?.setData("text/plain", node.id);
        draggedRef.current = { kind: "favourite", id: node.id };
      }}
      onDragOver={(event) => {
        if (draggedRef.current?.kind !== "favourite") return;
        event.preventDefault();
        setOver(placeOf(event) === "before" ? "before" : "after");
      }}
      onDragLeave={() => setOver(null)}
      onDrop={(event) => {
        event.preventDefault();
        setOver(null);
        onDrop(placeOf(event) === "before" ? "before" : "after");
      }}
    >
      <UnstyledButton component="div" style={{ flex: 1, minWidth: 0, display: "flex" }}>
        <RowTitle node={node} />
      </UnstyledButton>
      <FavouriteStar node={node} />
    </Group>
  );
};
