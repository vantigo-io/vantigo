package projects

import (
	"fmt"
	"strings"
)

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

// The three roles. roleManager is also the role the creator of a project is
// given (D6).
const (
	roleManager = "manager"
	roleMember  = "member"
	roleViewer  = "viewer"
)

// roleCapabilities is what each project role grants inside this module.
// member and viewer are identical here on purpose: they differ in what later
// modules grant them (spec §5).
var roleCapabilities = map[string][]capability{
	roleManager: {capSee, capSeeFinancials, capManage},
	roleMember:  {capSee},
	roleViewer:  {capSee},
}

// projectRoles is the enumeration in the order a message names it, which a
// map's iteration order cannot be.
var projectRoles = []string{roleManager, roleMember, roleViewer}

// validRole reports whether role is one of the three roles this module
// knows.
func validRole(role string) bool { _, ok := roleCapabilities[role]; return ok }

// validateProjectRole is the role rule of the assignment operation:
// required, and one of the three. It reads like the status rule because it is
// the same kind of rule — a closed enumeration on a dedicated operation.
func validateProjectRole(raw string) (string, string) {
	if strings.TrimSpace(raw) == "" {
		return "", "A role cannot be null or empty"
	}
	if !validRole(raw) {
		return "", fmt.Sprintf("A role must be one of %s, but was '%s'", quotedList(projectRoles), raw)
	}
	return raw, ""
}

// manageRank orders a project's people the way the contract lists them:
// managers first, then everyone else. It is a rank rather than a boolean so
// the sort reads as one comparison chain.
func manageRank(role string) int {
	if role == roleManager {
		return 0
	}
	return 1
}
