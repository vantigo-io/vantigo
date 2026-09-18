package projects_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// A task's comments (design §3.1, §4.1, §7): the task's own history, which is
// why tasks write nothing to the project timeline. They are read oldest first
// and paged like the timeline, because a conversation is read from the top;
// they are written by anyone who may write the project's work, and edited or
// deleted under the narrower rule a piece of somebody's writing deserves —
// the author edits their own, the author or a manager deletes it.

// commentsPath is one task's comments; commentPath is one comment of it,
// always addressed under the task it belongs to.
func commentsPath(taskID int32) string {
	return fmt.Sprintf("/api/v1/projects/tasks/%d/comments", taskID)
}

func commentPath(taskID int32, commentID int64) string {
	return fmt.Sprintf("%s/%d", commentsPath(taskID), commentID)
}

// commentJSON decodes CommentResponse; commentAuthorJSON its author, which is
// embedded rather than named by id alone, exactly as a task's assignee is.
// editedAt is a pointer because a comment nobody has edited has no key at all.
type commentJSON struct {
	Id        int64             `json:"id"`
	Author    commentAuthorJSON `json:"author"`
	Body      string            `json:"body"`
	CreatedAt time.Time         `json:"createdAt"`
	EditedAt  *time.Time        `json:"editedAt"`
}

type commentAuthorJSON struct {
	UserId      uuid.UUID `json:"userId"`
	DisplayName string    `json:"displayName"`
	Active      bool      `json:"active"`
}

// commentPageJSON decodes PaginatedResponseOfCommentResponse, the same
// envelope the timeline answers in.
type commentPageJSON struct {
	Data       []commentJSON  `json:"data"`
	Pagination paginationJSON `json:"pagination"`
}

// readComments returns the raw list response, for a case whose subject is the
// refusal; getComments is it for a test that expects to be shown the page.
func readComments(t *testing.T, c *modtest.Client, taskID int32, query string) *modtest.Response {
	t.Helper()
	path := commentsPath(taskID)
	if query != "" {
		path += "?" + query
	}
	return c.Do(http.MethodGet, path, nil)
}

func getComments(t *testing.T, c *modtest.Client, taskID int32, query string) commentPageJSON {
	t.Helper()
	r := readComments(t, c, taskID, query)
	if r.Status != http.StatusOK {
		t.Fatalf("list comments: status %d body %s, want 200", r.Status, r.Body)
	}
	var page commentPageJSON
	r.JSON(&page)
	return page
}

// postComment sends one comment and returns the response; addComment is it
// for a test that expects the comment to be created.
func postComment(t *testing.T, c *modtest.Client, taskID int32, body map[string]any) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodPost, commentsPath(taskID), body)
}

func addComment(t *testing.T, c *modtest.Client, taskID int32, body string) commentJSON {
	t.Helper()
	r := postComment(t, c, taskID, map[string]any{"body": body})
	if r.Status != http.StatusCreated {
		t.Fatalf("add comment: status %d body %s, want 201", r.Status, r.Body)
	}
	var comment commentJSON
	r.JSON(&comment)
	return comment
}

// putComment sends one edit and returns the response; changeComment is it for
// a test that expects the edit to be applied.
func putComment(t *testing.T, c *modtest.Client, taskID int32, commentID int64, body map[string]any) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodPut, commentPath(taskID, commentID), body)
}

func changeComment(t *testing.T, c *modtest.Client, taskID int32, commentID int64, body string) commentJSON {
	t.Helper()
	r := putComment(t, c, taskID, commentID, map[string]any{"body": body})
	if r.Status != http.StatusOK {
		t.Fatalf("edit comment: status %d body %s, want 200", r.Status, r.Body)
	}
	var comment commentJSON
	r.JSON(&comment)
	return comment
}

func deleteComment(t *testing.T, c *modtest.Client, taskID int32, commentID int64) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodDelete, commentPath(taskID, commentID), nil)
}

// commentBodies is what a page assertion is actually about: which comments
// came back and in what order.
func commentBodies(comments []commentJSON) []string {
	out := make([]string, 0, len(comments))
	for _, comment := range comments {
		out = append(out, comment.Body)
	}
	return out
}

// insertCommentBy writes one comment straight into the table under an author
// id nothing else knows, which is the only way to build the case where the
// user directory cannot name a comment's author: every author the endpoints
// can produce is a signed-in user identity still knows.
func insertCommentBy(t *testing.T, h *modtest.Harness, taskID int32, authorID uuid.UUID, body string) {
	t.Helper()
	h.Exec(t, `INSERT INTO projects.task_comments (task_id, author_user_id, body, created_at)
	           VALUES ($1, $2, $3, now())`, taskID, authorID, body)
}

// Comments come back oldest first — a conversation is read from the top — and
// each carries its author, named through the user directory. The task's own
// comment count moves with them, so a reader of the project's tree sees that
// there is something to read without reading it.
func TestPostProjectsTasksByTaskIdComments_AddsOldestFirstAndCountsOnTheTask(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, authorID := signIn(t, h, "projects:create")
	setDisplayName(t, h, authorID, "Mia Manager")
	project := createProject(t, c, map[string]any{"code": "CMADD100"})
	task := createTask(t, c, project.Id, nil)

	first := addComment(t, c, task.Id, "Ser bra ut")
	addComment(t, c, task.Id, "Venter på svar")
	// The body is trimmed before it is stored, exactly as a task's title is.
	addComment(t, c, task.Id, "  Nå er vi i gang  ")

	if first.Author.UserId != authorID || first.Author.DisplayName != "Mia Manager" || !first.Author.Active {
		t.Errorf("author = %+v, want the caller, named and active", first.Author)
	}
	if first.EditedAt != nil {
		t.Errorf("editedAt = %v on a comment nobody has edited, want it absent", first.EditedAt)
	}
	page := getComments(t, c, task.Id, "")
	if got := commentBodies(page.Data); !equalStrings(got, []string{"Ser bra ut", "Venter på svar", "Nå er vi i gang"}) {
		t.Errorf("comments = %v, want the three in the order they were written", got)
	}
	if page.Pagination.TotalCount != 3 || page.Pagination.Page != 1 {
		t.Errorf("pagination = %+v, want three comments on page 1", page.Pagination)
	}
	if got := taskInTree(t, c, project.Id, task.Id); got.CommentCount != 3 {
		t.Errorf("the task's commentCount = %d, want 3", got.CommentCount)
	}
}

// The page envelope is the timeline's, and so is its refusal: a page below one
// or a page size outside the range is a bad request, not an empty page.
func TestGetProjectsTasksByTaskIdComments_PagesLikeTheTimeline(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "CMPAG100"})
	task := createTask(t, c, project.Id, nil)
	for i := 1; i <= 3; i++ {
		addComment(t, c, task.Id, fmt.Sprintf("Kommentar %d", i))
	}

	page := getComments(t, c, task.Id, "page=1&pageSize=2")
	if got := commentBodies(page.Data); !equalStrings(got, []string{"Kommentar 1", "Kommentar 2"}) {
		t.Errorf("page 1 = %v, want the two oldest", got)
	}
	if !page.Pagination.HasNextPage || page.Pagination.TotalPages != 2 {
		t.Errorf("pagination = %+v, want two pages with a next one", page.Pagination)
	}
	second := getComments(t, c, task.Id, "page=2&pageSize=2")
	if got := commentBodies(second.Data); !equalStrings(got, []string{"Kommentar 3"}) {
		t.Errorf("page 2 = %v, want the newest alone", got)
	}
	if r := readComments(t, c, task.Id, "page=0"); r.Status != http.StatusBadRequest {
		t.Errorf("page=0: status %d body %s, want 400", r.Status, r.Body)
	}
}

// A comment is somebody's writing: only its author may change the words. The
// edit stamps editedAt, so a reader can tell a comment that was rewritten from
// one that was not — and a manager, who may delete it, still may not put words
// in its author's mouth.
func TestPutProjectsTasksByTaskIdCommentsByCommentId_AuthorEditsAndStampsEditedAt(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager, _ := signIn(t, h, "projects:create")
	project := createProject(t, manager, map[string]any{"code": "CMEDT100"})
	task := createTask(t, manager, project.Id, nil)
	member, memberID := signIn(t, h)
	addRole(t, h, project.Id, memberID, "member")
	other, otherID := signIn(t, h)
	addRole(t, h, project.Id, otherID, "member")

	comment := addComment(t, member, task.Id, "Frst utkast")
	h.Advance(time.Hour)

	edited := changeComment(t, member, task.Id, comment.Id, "  Første utkast  ")
	if edited.Body != "Første utkast" {
		t.Errorf("body = %q, want the trimmed correction", edited.Body)
	}
	if edited.EditedAt == nil || !edited.EditedAt.After(edited.CreatedAt) {
		t.Errorf("editedAt = %v, createdAt = %v, want a stamp after the comment was written", edited.EditedAt, edited.CreatedAt)
	}
	if edited.Author.UserId != memberID {
		t.Errorf("author = %+v, want it still the member's", edited.Author)
	}

	for name, c := range map[string]*modtest.Client{"another member": other, "the manager": manager} {
		r := putComment(t, c, task.Id, comment.Id, map[string]any{"body": "Noe helt annet"})
		if r.Status != http.StatusForbidden {
			t.Errorf("%s editing it: status %d body %s, want 403", name, r.Status, r.Body)
		}
	}
	page := getComments(t, member, task.Id, "")
	if got := commentBodies(page.Data); !equalStrings(got, []string{"Første utkast"}) {
		t.Errorf("comments = %v, want only the author's own correction", got)
	}
}

// A comment is deleted by its author or by a manager of the project — the one
// person who answers for what stands on it — and by nobody else.
func TestDeleteProjectsTasksByTaskIdCommentsByCommentId_AuthorOrManagerOnly(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager, _ := signIn(t, h, "projects:create")
	project := createProject(t, manager, map[string]any{"code": "CMDEL100"})
	task := createTask(t, manager, project.Id, nil)
	member, memberID := signIn(t, h)
	addRole(t, h, project.Id, memberID, "member")
	other, otherID := signIn(t, h)
	addRole(t, h, project.Id, otherID, "member")

	byMember := addComment(t, member, task.Id, "Medlemmets kommentar")
	if r := deleteComment(t, other, task.Id, byMember.Id); r.Status != http.StatusForbidden {
		t.Errorf("another member's delete: status %d body %s, want 403", r.Status, r.Body)
	}
	if r := deleteComment(t, manager, task.Id, byMember.Id); r.Status != http.StatusNoContent {
		t.Errorf("the manager's delete: status %d body %s, want 204", r.Status, r.Body)
	}

	own := addComment(t, member, task.Id, "Enda en")
	if r := deleteComment(t, member, task.Id, own.Id); r.Status != http.StatusNoContent {
		t.Errorf("the author's delete: status %d body %s, want 204", r.Status, r.Body)
	}
	// Two deletes racing must not both answer 204.
	if r := deleteComment(t, member, task.Id, own.Id); r.Status != http.StatusNotFound {
		t.Errorf("deleting it twice: status %d body %s, want 404", r.Status, r.Body)
	}
	if got := taskInTree(t, manager, project.Id, task.Id); got.CommentCount != 0 {
		t.Errorf("the task's commentCount = %d, want none left", got.CommentCount)
	}
}

// The body rule: non-blank, at most 4000 characters, on the add and on the
// edit alike.
func TestComments_InvalidBody_Returns400OnTheField(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "CMVAL100"})
	task := createTask(t, c, project.Id, nil)
	comment := addComment(t, c, task.Id, "Ser bra ut")

	for name, r := range map[string]*modtest.Response{
		"empty":        postComment(t, c, task.Id, map[string]any{"body": ""}),
		"blank":        postComment(t, c, task.Id, map[string]any{"body": "   "}),
		"long":         postComment(t, c, task.Id, map[string]any{"body": longText(4001)}),
		"blank edit":   putComment(t, c, task.Id, comment.Id, map[string]any{"body": "  "}),
		"long edit":    putComment(t, c, task.Id, comment.Id, map[string]any{"body": longText(4001)}),
		"unknown task": postComment(t, c, 999_999, map[string]any{"body": "Ser bra ut"}),
	} {
		want := http.StatusBadRequest
		if name == "unknown task" {
			want = http.StatusNotFound
		}
		if r.Status != want {
			t.Errorf("%s: status %d body %s, want %d", name, r.Status, r.Body, want)
			continue
		}
		if want != http.StatusBadRequest {
			continue
		}
		var problem validationProblemJSON
		r.JSON(&problem)
		if len(problem.Errors["body"]) == 0 {
			t.Errorf("%s: errors = %v, want one under \"body\"", name, problem.Errors)
		}
	}
	// A body of exactly 4000 characters is allowed: the rule is the column's.
	if r := postComment(t, c, task.Id, map[string]any{"body": longText(4000)}); r.Status != http.StatusCreated {
		t.Errorf("4000 characters: status %d body %s, want 201", r.Status, r.Body)
	}
}

// A comment survives the account that wrote it: an author identity no longer
// knows is named "Unknown user" and inactive rather than losing the comment,
// and an account disabled afterwards keeps its name and renders inactive —
// the same rule a task's assignee follows.
func TestGetProjectsTasksByTaskIdComments_AuthorGoneOrDisabled_StillRenders(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager, _ := signIn(t, h, "projects:create")
	project := createProject(t, manager, map[string]any{"code": "CMAUT100"})
	task := createTask(t, manager, project.Id, nil)
	member, memberID := signIn(t, h)
	addRole(t, h, project.Id, memberID, "member")
	setDisplayName(t, h, memberID, "Mia Medlem")

	addComment(t, member, task.Id, "Skrevet av et medlem")
	stranger := uuid.New()
	insertCommentBy(t, h, task.Id, stranger, "Skrevet av en konto som er borte")
	disableUser(t, h, memberID)

	page := getComments(t, manager, task.Id, "")
	if len(page.Data) != 2 {
		t.Fatalf("comments = %v, want both", commentBodies(page.Data))
	}
	if got := page.Data[0].Author; got.DisplayName != "Mia Medlem" || got.Active {
		t.Errorf("disabled author = %+v, want it named and inactive", got)
	}
	if got := page.Data[1].Author; got.UserId != stranger || got.DisplayName != "Unknown user" || got.Active {
		t.Errorf("unknown author = %+v, want \"Unknown user\" and inactive", got)
	}
}

// Design §7's authorization: seeing the project is enough to read a task's
// comments, writing one takes the member or manager role, and an outsider is
// told the task does not exist — in exactly the words an unknown id is told
// that.
func TestComments_Authorization_ViewerReadsMemberWritesOutsiderIsNotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager, _ := signIn(t, h, "projects:create")
	project := createProject(t, manager, map[string]any{"code": "CMAUZ100"})
	task := createTask(t, manager, project.Id, nil)
	comment := addComment(t, manager, task.Id, "Ser bra ut")

	viewer, viewerID := signIn(t, h)
	addRole(t, h, project.Id, viewerID, "viewer")
	member, memberID := signIn(t, h)
	addRole(t, h, project.Id, memberID, "member")
	outsider, _ := signIn(t, h)

	if got := getComments(t, viewer, task.Id, ""); len(got.Data) != 1 {
		t.Errorf("viewer's comments = %v, want the task's one comment", commentBodies(got.Data))
	}
	for name, r := range map[string]*modtest.Response{
		"add":    postComment(t, viewer, task.Id, map[string]any{"body": "Nei"}),
		"edit":   putComment(t, viewer, task.Id, comment.Id, map[string]any{"body": "Nei"}),
		"delete": deleteComment(t, viewer, task.Id, comment.Id),
	} {
		if r.Status != http.StatusForbidden {
			t.Errorf("viewer's %s: status %d body %s, want 403", name, r.Status, r.Body)
		}
	}

	own := addComment(t, member, task.Id, "Medlemmets kommentar")
	if got := changeComment(t, member, task.Id, own.Id, "Medlemmets rettelse"); got.Body != "Medlemmets rettelse" {
		t.Errorf("member's edit = %+v, want it applied", got)
	}

	existing := readComments(t, outsider, task.Id, "")
	unknown := readComments(t, outsider, 999_999, "")
	if existing.Status != http.StatusNotFound || unknown.Status != http.StatusNotFound {
		t.Fatalf("outsider: existing %d, unknown %d, want 404 for both", existing.Status, unknown.Status)
	}
	if string(existing.Body) != string(unknown.Body) {
		t.Errorf("outsider's 404 body %q differs from an unknown task's %q", existing.Body, unknown.Body)
	}
	for name, r := range map[string]*modtest.Response{
		"add":    postComment(t, outsider, task.Id, map[string]any{"body": "Nei"}),
		"edit":   putComment(t, outsider, task.Id, comment.Id, map[string]any{"body": "Nei"}),
		"delete": deleteComment(t, outsider, task.Id, comment.Id),
	} {
		if r.Status != http.StatusNotFound {
			t.Errorf("outsider's %s: status %d body %s, want 404", name, r.Status, r.Body)
		}
	}
	if got := getComments(t, manager, task.Id, ""); len(got.Data) != 2 {
		t.Errorf("comments = %v, want the two the refusals did not touch", commentBodies(got.Data))
	}
}

// A comment of another task is not found under the task in the path: the item
// and the task it hangs off are one address, so a caller cannot reach into a
// task they were not asking about.
func TestComments_CommentOfAnotherTask_IsNotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "CMOTH100"})
	mine := createTask(t, c, project.Id, map[string]any{"title": "Min oppgave"})
	other := createTask(t, c, project.Id, map[string]any{"title": "Annen oppgave"})
	comment := addComment(t, c, other.Id, "Hører til den andre")

	elsewhere := putComment(t, c, mine.Id, comment.Id, map[string]any{"body": "Nei"})
	unknown := putComment(t, c, mine.Id, 999_999, map[string]any{"body": "Nei"})
	if elsewhere.Status != http.StatusNotFound || unknown.Status != http.StatusNotFound {
		t.Fatalf("elsewhere %d, unknown %d, want 404 for both", elsewhere.Status, unknown.Status)
	}
	if string(elsewhere.Body) != string(unknown.Body) {
		t.Errorf("another task's comment answered %q, an unknown one %q", elsewhere.Body, unknown.Body)
	}
	if r := deleteComment(t, c, mine.Id, comment.Id); r.Status != http.StatusNotFound {
		t.Errorf("deleting another task's comment: status %d body %s, want 404", r.Status, r.Body)
	}
	if got := getComments(t, c, other.Id, ""); len(got.Data) != 1 || got.Data[0].Body != "Hører til den andre" {
		t.Errorf("the other task's comments = %v, want them untouched", commentBodies(got.Data))
	}
}
