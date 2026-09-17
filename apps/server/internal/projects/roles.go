package projects

// The fixed set of project roles and what each one grants inside this module
// (design §5, D6). Roles are defined in code, not in data, so a role editor
// could be added later without a schema change; nothing outside this file
// decides what a role means.

// capability is one thing a role lets its holder do with a project.
type capability string

const (
	capSee           capability = "see"
	capSeeFinancials capability = "see-financials"
	capManage        capability = "manage"
)

// roleCapabilities is what each project role grants inside this module.
// member and viewer are identical here on purpose: they differ in what later
// modules grant them (spec §5).
var roleCapabilities = map[string][]capability{
	"manager": {capSee, capSeeFinancials, capManage},
	"member":  {capSee},
	"viewer":  {capSee},
}

// roleManager is the role the creator of a project is given (D6). It is the
// only role this task assigns; the roles endpoints (Task 7) assign the rest.
const roleManager = "manager"

// validRole reports whether role is one of the three roles this module
// knows.
func validRole(role string) bool { _, ok := roleCapabilities[role]; return ok }
