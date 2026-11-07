import type { KnowledgeMoveInput, KnowledgeTreeNode } from "../api/articles";

/** Where a dragged row lands relative to the row it is dropped on. */
export type DropPlace = "before" | "inside" | "after";

/**
 * The move a drop asks for (K3): inside a row makes it the row's first child;
 * before or after a row puts it beside it, under the row's parent. A root's
 * section goes with a drop at the top level, so a move between Workspace and
 * Private says where it is going.
 */
export const moveFor = (
  target: KnowledgeTreeNode,
  place: DropPlace,
  siblings: readonly KnowledgeTreeNode[],
  draggedId: string,
): KnowledgeMoveInput => {
  if (place === "inside") return { parentId: target.id, afterId: null };
  // A Shared item's parent may be one the reader cannot see, which the server answers as null. It is
  // then shown at the top, but it is no root's sibling: beside it is the top level, first in place.
  if (target.parentId === null && target.section === "shared") return { parentId: null, afterId: null };
  const others = siblings.filter((node) => node.id !== draggedId);
  const index = others.findIndex((node) => node.id === target.id);
  const afterId = place === "after" ? target.id : index > 0 ? (others[index - 1]?.id ?? null) : null;
  const input: KnowledgeMoveInput = { parentId: target.parentId, afterId };
  if (target.parentId === null && target.section !== "shared") input.section = target.section;
  return input;
};
