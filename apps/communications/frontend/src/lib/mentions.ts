/** A colleague picked with the @ picker: their user id and the name the note shows. */
export type Mention = { userId: string; displayName: string };

/** The text a mention leaves in a note: an @ and the person's name, as the readers see it. */
export const mentionText = (displayName: string) => `@${displayName}`;

const escapeRegExp = (value: string) => value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");

/**
 * The users a note mentions (virtual employees design V32): the ones picked whose @Name is still in the
 * text, each once, in the order they were picked. A mention whose name was deleted from the note is no
 * longer one, and a name typed by hand without the picker is not either: only the picker knows the id.
 * The name must stand alone: "@Hanna" does not count in "@Hannah", and a name in a word is not a mention.
 */
export const mentionsIn = (text: string, picked: Mention[]): string[] => {
  const ids: string[] = [];
  for (const { userId, displayName } of picked) {
    if (ids.includes(userId)) continue;
    const found = new RegExp(`(^|[^\\p{L}\\p{N}_])${escapeRegExp(mentionText(displayName))}(?![\\p{L}\\p{N}_])`, "u");
    if (found.test(text)) ids.push(userId);
  }
  return ids;
};
