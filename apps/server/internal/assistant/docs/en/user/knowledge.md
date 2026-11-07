---
title: Knowledge
description: The Knowledge app — who can use it, the sidebar's Favourites, Workspace, Shared and Private sections, creating and organising pages, writing with the editor, autosave and what happens when someone else changed the page, sharing a page and locking it, hiding it from the Assistant, history, trash, favourites, search, and seeing who else is on a page.
sidebar:
  order: 57
sources:
  - apps/knowledge/frontend
  - apps/host/frontend/src/routes/knowledge
  - apps/host/frontend/src/lib/knowledge-access.ts
---

The Knowledge app is the company's own handbook: routines, policies and how-tos, written
as pages that can be nested inside one another. A page can be open to everyone in the
company, shared with named people, or private to you. The rules behind each screen — the
permission every request is checked against and what a refusal means — are in the
Knowledge reference; this page is about what you do on each screen.

## Who can use Knowledge

The **Knowledge** tile in the app switcher is shown to everyone whose role has **Use
Knowledge** (`knowledge:articles`). **No role has it by default**: an administrator grants it
to a role under **Roles & access** ([Workspace
administration](/en/user/workspace-administration/)). **Administer Knowledge**
(`knowledge:manage`) is for administrators: it lets them see and change the access of every
page that is not private, restore anyone's trashed page and empty the trash.

What you can do with a page then depends on the page's own access: whether you can read it
and whether you can edit it ([Share a page](#share-a-page)). A page you may not read is not
shown to you at all.

## The sidebar

The sidebar of the app lists the pages as a tree, in these sections:

| Section | What is in it |
| --- | --- |
| **Favourites** | The pages you have marked with **Add to favourites**. Shown only when you have any. |
| **Workspace** | The top-level pages that belong to the company, with their subpages. |
| **Shared** | Pages other people have shared with you by name, that are not inside a page you already see in the Workspace. |
| **Private** | Your own private pages, with their subpages. Only you see this section's pages. |

A top-level page is in the **Workspace** when it is open to everyone in the company, and in
**Private** when it is only for you. Sharing a private page with a person puts it in that
person's **Shared** section. Beside each page, **Expand** and **Collapse** open and close its
subpages.

The sidebar also has the box **Search Knowledge** ([Search](#search)) and, below the
sections, **Trash** ([The trash](#the-trash)).

## Create a page

Press **New page in the Workspace** to make a page the whole company can edit, or **New
private page** to make one only you can see. In the dialog **New article**, choose **Where**
(the Workspace or Private; the dialog says *Everyone in the company can edit it.* or *Only you
can see it, until you share it.*), give it a **Title** and press **Create**.

To create a page inside another page, press **+** (*New page in …*) beside that page in the
sidebar. A subpage starts with the access of its parent
([Share a page](#share-a-page)). Pages can be nested at most 10 levels deep.

A page without a title shows as **Untitled**. Give the page an **Icon** with **Add icon**; **Change
icon** and **Remove icon** change it later.

## Write on a page

Click in the page and write. Select text and a small menu appears with the formatting:
**Bold**, **Italic**, **Underline**, **Strikethrough**, **Code**, **Link**, the headings
**Heading 1**, **Heading 2** and **Heading 3**, **Bullet list**, **Numbered list**, **Task
list** and **Quote**.

The Markdown shortcuts work as you type, at the start of a line:

| Type | You get |
| --- | --- |
| `#`, `##`, `###` and a space | Heading 1, 2 or 3 |
| `-` and a space | A bullet list |
| `1.` and a space | A numbered list |
| `[ ]` and a space | A task list: tick each task off as it is done |
| `>` and a space | A quote |
| three backticks | A code block |

A **Link** asks for the **Link address**. Clicking a link in a page opens it in a new tab for an
outside address (**Open in a new tab**) and in the same tab for an address inside Vantigo
(**Open in this tab**); **Remove link** takes it away. A page can be at most 2 MiB.

## Blocks, pictures and links

**Add a block.** Type `/` at the start of a line, or use the **Add a block** button (+) beside a
block, and the **Blocks** menu opens; keep typing to narrow it (*No block matches.* when nothing
does). It offers **Text**, the headings, the lists, **Quote**, **Toggle**, **Info callout**,
**Tip callout**, **Warning callout**, **Danger callout**, **Code block**, **Table**, **Divider**,
**Table of contents**, **Image**, **File** and **Link to a page**.

**Move a block.** Drag the grip beside a block (**Drag to move**) to put it somewhere else on
the page.

- **Callouts.** A callout is a coloured box. Click its icon (*Callout: Info. Change its kind or
  emoji*) to change its **Kind** (**Info**, **Tip**, **Warning**, **Danger**) or choose an
  **Emoji**, or **No emoji**.
- **Toggles.** A **Toggle** is a heading you click to open or fold the text under it.
- **Tables.** Insert a **Table**, then open its **Table** menu from a cell to **Add a row above**
  or **below**, **Add a column to the left** or **right**, **Remove the row**, **Remove the
  column**, turn the **Header row on or off** or **Remove the table**.
- **Code blocks.** A **Code block** has a **Language** to pick, or **Detect automatically**.
- **Table of contents.** It lists the page's headings and follows them as you write them; a page with no
  headings says *Add headings, and they are listed here.*

**Images and files.** Paste or drop a file onto the page, or use **Image** or **File** in the Blocks
menu. A file can be at most **25 MiB**. A JPEG or PNG shows as a picture (its camera and location
data are removed; choose **Small**, **Medium** or **Full width** under **Image width**); anything
else, including SVG, is shown as a file you **Download**. The upload needs edit access and an
unlocked page; otherwise it says why (*The page is locked, so nothing can be added to it.*).
Pasting a picture or file copied from another page puts a copy into this page. A file lives with its page: an unused one is cleaned away after a week.

**Mention a person.** Type `@` and pick someone under **People**. They are told by mail, with a link,
but only if they can open the page, and at most **once an hour per page**; you are never
notified of your own mention.

**Link to another page.** Type `[[` and search under **Pages**, or choose **Link to a page**. Pasting an
address from Vantigo turns it into a link too (an address with a `?` or `#` part stays an ordinary link). A link shows the page's current title and icon;
if you cannot read the page it shows **No access** and does not reveal the title, and if the page was
deleted for good it shows **Deleted**. A trashed page also shows **No access**.

**Linked from.** At the foot of a page, **Linked from** lists the pages you can read that link to it.

## Export and import Markdown

In the **Page menu**, **Export as Markdown** downloads the page as a `.md` file. Choose **Include
subpages** to download a `.zip` with one file per subpage you can read, laid out as an Obsidian
vault; images and files go in an `assets` folder, which also makes the download a `.zip`. Links to
pages you cannot read become **No access**. An export of more than 2000 files or about 1 GiB is refused.

**Import Markdown** turns a `.md` file, or a `.zip` from Obsidian or a Notion export, into pages. Choose the
**File**, then **Put the pages** **Under this page**, **At the top of the Workspace** or **At the top of
Private**. Folders become subpages, a folder and a file of the same name become one page, Obsidian
callouts become callouts, and `[[links]]` between the files become page links. A first heading that repeats
the title is left out. JPEG and PNG images come along as page pictures. What is skipped is
listed afterwards under **Some things could not be carried over**: other file types, HTML, images on the
web, hidden folders such as `.obsidian`. The upload can be at most 50 MiB (a zip at most 500 files, 200 MiB unpacked).
Parts the editor cannot hold are left out and listed as well; images inside headings and tables become their description text. Only two imports run at once, so a busy one asks you to try again in ten seconds. It is all or nothing: **Open** the result from the list that follows.

## Saving, and when someone else changed the page

There is no Save button. The page saves by itself a moment after you stop typing, and the
status beside the title says where it stands: **Unsaved changes**, **Saving…**, **Saved** or
**Not saved**. Under the title, *Last changed by …* says who saved last and when.

**Not saved** means the save failed; the text is still on your screen and Vantigo keeps
trying. Two cases are explained on the page: *This page is too large to save* (over 2 MiB;
shorten it) and *This page could not be saved*, which names something the editor does not
support — undo your last change.

If someone else saved the page while you were editing, a dialog says **… changed this page
while you were editing**, with two choices:

- **Reload** shows their version and drops your changes.
- **Keep mine** saves your text over theirs. Their version is not lost: it stays in the
  [history](#history-and-restore).

If you lose your connection or close the tab before a change is saved, the text is kept in
this browser. The next time you open the page it says **Unsaved changes on this device**,
with the time, and offers **Restore them** or **Discard**.

## Organise pages by dragging

Drag a page in the sidebar and drop it:

- **onto** another page, to make it a subpage of that page (inside);
- **above** or **below** another page, to put it before or after it, at the same level.

A page cannot be moved into one of its own subpages, and you need to be able to edit a page to
move it. When a move changes who can read the page — a page moved between **Private** and the
Workspace, or under a page with other access — a confirmation asks **Change who can read this
page?** and says that other people will gain or lose access to it and its subpages. Press
**Change access** to go on.

## Share a page

Press **Share** at the top of the page. The dialog **Share** shows who has access; only people
who can edit the page can change it.

**Everyone in the company** is set to one of:

- **Can edit** — everyone with Knowledge can read and edit the page;
- **Can read** — everyone can read it, only the people named below can edit it;
- **No access** — only the people named below see it.

A subpage can instead be set to **As the parent page**, which follows its parent and shows the
result, for instance *Can edit, from …*. A top-level page always has a setting of its own.

A page with a setting of its own — a top-level page, or a subpage not set to **As the parent
page** — keeps its own access: *This page has its own access. People added on parent pages don't
apply here.* Someone you add to a parent page therefore does not get into it, or into the pages
below it; add them on the page itself. A page set to **As the parent page** follows its parent in
both respects.

Under **People**, add named people with **Add a person**: search by name, choose **Can edit** or
**Can read** for them and press **Add**. Change a person's level in the list or remove them with
**Remove …**. A person's access also reaches the subpages that follow their parent. Someone
must always be able to edit the page: you cannot remove or lower the last person who can edit
it unless everyone has **Can edit**.

**Private pages are private from administrators too.** Having **Administer Knowledge** does not
show a private page, and an administrator cannot restore or read it; only you, and those you
share it with, see it.

## Lock a page

Open the **Page menu** and choose **Lock page** to stop the page from being edited, for
everyone, until it is unlocked. A locked page shows *This page is locked.*; people who can edit it
see *Unlock it from the page menu to edit it.* and use **Unlock page**. Locking changes nothing
about who can read the page.

## Hide a page from the Assistant

The [Assistant](/en/user/assistant/) can read the pages you can read in order to answer questions.
Turn on **Hidden from the Assistant** in the **Page menu** to keep a page out of its reach. It
hides the page **and everything inside it**: a subpage shows **Hidden through a parent page**.

This is not an access rule. People who may read the page still read it; it only stops the
Assistant from using it.

## History and restore

Every save leaves a version. Open **History** from the **Page menu**: it lists the versions
(*Saved by …, when*), with **Current** at the top. Open one to read it; **All versions** goes
back to the list. **Restore this version** makes it the page's text again — the version you
replace is kept in the history too. If the page changed in the meantime, the dialog says so;
open the history again and retry.

## The trash

**Move to trash** in the **Page menu** moves the page, with its subpages, to **Trash**, after a
confirmation. A trashed page is kept for 30 days and then deleted for good, with its history.

**Trash** lists the pages with when they were trashed and when they will be deleted for good
(**Deleted for good**). A page trashed together with its subpages shows *and 3 subpages*.
**Restore** brings the page back, and a group trashed together is restored together. If its
old parent is gone, the page comes back at the top of its section. You see the pages you could
read; with **Administer Knowledge** you see and restore anyone's that is not private.

**Empty trash**, for administrators with **Administer Knowledge**, deletes everything in the trash
for good, with its history. It cannot be undone.

## Favourites

Press **Add to favourites** beside a page in the sidebar to put it under **Favourites**, at the
top; **Remove from favourites** takes it away. Favourites are yours alone.

## Search

Type in **Search Knowledge** at the top of the sidebar to list the pages that match, under
**Search results**; *No pages match.* says nothing was found. The search reads titles and text,
and finds both Norwegian and English word forms — *rutine* also finds *rutinene*, *contract* finds
*contracts*. It only shows pages you may read.

The search in the top bar (Ctrl+K, or Cmd+K on a Mac) finds Knowledge pages too, beside the
other apps' pages, and opens them directly.

## See who else is on the page

When someone else is editing the page you have open, the top of the page says *Kari is editing*
(or *Kari and Ola are editing*); other people who have the page open show under **Also here**,
as editing or viewing. The page also refreshes by itself when someone else changes it, and
the sidebar when pages are created, moved or trashed — you do not need to reload. If you are
in the middle of typing when someone else saves, you get the dialog described under
[Saving](#saving-and-when-someone-else-changed-the-page).
