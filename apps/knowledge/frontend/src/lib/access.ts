/**
 * What the caller may do across Knowledge beyond each article's own access,
 * as the host judges it from the session (knowledge design K2). The pages
 * never read the session themselves; the host hands this in.
 */
export interface KnowledgePageAccess {
  /** `knowledge:manage`: restore anyone's trashed article and empty the trash (K10). */
  canManage: boolean;
}

export const noKnowledgeAccess: KnowledgePageAccess = { canManage: false };
