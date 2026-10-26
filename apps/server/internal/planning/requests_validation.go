package planning

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/planning/gen"
	"github.com/vantigo-io/vantigo/server/internal/planning/store"
)

// This file is a request's field rules (design S16, §4.7, §13 amendment 3):
// the title, the period as S4 shapes it, hours a week only, the note, and the
// requirements judged against Staff's catalogue as the caller may see it. A
// requirement names a competence the catalogue answers — one it does not,
// unknown or archived, is a field error on its competenceId — and a level
// only on a competence that has one. A hidden competence is accepted: it is
// still held by those who have it. Every rule here is pure; the handlers of
// requests_write.go read what they judge.

// The request statuses (S16): open is demand, filled exactly while a row
// names it, withdrawn for good.
const (
	requestOpen      = "open"
	requestFilled    = "filled"
	requestWithdrawn = "withdrawn"
)

// The bounds of a request's requirements (S16).
const (
	maxRequirements = 10
	maxLevel        = 4
)

// maxRequestsListed is the most requests GET /requests answers (§6).
const maxRequestsListed = 500

// requestFields is a valid PlanningRequestInput: the hours a week in
// hundredths, the period at midnight UTC, the title trimmed, the note nil
// when empty, the requirements in the body's order.
type requestFields struct {
	projectID    int32
	title        string
	from, to     time.Time
	hours        int64
	note         *string
	requirements []requirementField
}

// requirementField is one requirement of a body: a competence of Staff's and
// the least level, nil for none.
type requirementField struct {
	competenceID int64
	minLevel     *int16
}

// parseRequest validates a request's body on its own (S16): every rule a
// field error on its own field, every failure reported at once,
// period_too_long coded on toDate. The requirements' competences are judged
// against the catalogue apart (requirementErrors), since that is a read.
func parseRequest(body gen.PlanningRequestInput) (requestFields, fieldErrors) {
	var f fieldErrors
	r := requestFields{projectID: body.ProjectId, from: dayOf(body.FromDate), to: dayOf(body.ToDate)}
	if body.ProjectId <= 0 {
		f.add("projectId", "projectId is required and must be a project's id")
	}
	r.title = strings.TrimSpace(body.Title)
	switch {
	case r.title == "":
		f.add("title", "title is required")
	case utf8.RuneCountInString(r.title) > maxTitleRunes:
		f.add("title", fmt.Sprintf("title is at most %d characters", maxTitleRunes))
	}
	if body.FromDate.IsZero() {
		f.add("fromDate", "fromDate is required")
	}
	if body.ToDate.IsZero() {
		f.add("toDate", "toDate is required")
	}
	if !body.FromDate.IsZero() && !body.ToDate.IsZero() {
		f.merge(periodErrors(r.from, r.to))
	}
	// A request has no person whose position a percentage could be read
	// against: hours a week only, the allocation's hours rule (S16).
	hours, message := loadOfInput(loadUnitHours, body.HoursPerWeek)
	if message != "" {
		f.add("hoursPerWeek", "hoursPerWeek must be more than 0 and at most 80, with at most two decimals")
	}
	r.hours = hours
	r.note = textField(&f, "note", body.Note, maxNoteRunes)
	if body.Requirements == nil {
		return r, f
	}
	if len(*body.Requirements) > maxRequirements {
		f.add("requirements", fmt.Sprintf("A request has at most %d requirements, but %d were given", maxRequirements, len(*body.Requirements)))
		return r, f
	}
	seen := map[int64]bool{}
	for i, req := range *body.Requirements {
		field := fmt.Sprintf("requirements[%d]", i)
		if seen[req.CompetenceId] {
			f.add(field+".competenceId", "This competence is required once already")
			continue
		}
		seen[req.CompetenceId] = true
		var level *int16
		if req.MinLevel != nil {
			if *req.MinLevel < 0 || *req.MinLevel > maxLevel {
				f.add(field+".minLevel", fmt.Sprintf("minLevel must be from 0 to %d", maxLevel))
				continue
			}
			v := int16(*req.MinLevel)
			level = &v
		}
		r.requirements = append(r.requirements, requirementField{competenceID: req.CompetenceId, minLevel: level})
	}
	return r, f
}

// requirementErrors judges each requirement of a body against the catalogue
// the caller may see (S16): a competence it does not answer is a field error
// on the requirement's competenceId, a level on a competence without one a
// field error on its minLevel. The index is the body's, so the errors are
// matched against the parsed requirements by competence.
func requirementErrors(body gen.PlanningRequestInput, catalogue map[int64]contracts.CompetenceEntry) fieldErrors {
	var f fieldErrors
	if body.Requirements == nil || len(*body.Requirements) > maxRequirements {
		return f
	}
	for i, req := range *body.Requirements {
		field := fmt.Sprintf("requirements[%d]", i)
		entry, ok := catalogue[req.CompetenceId]
		switch {
		case !ok:
			f.add(field+".competenceId", "Staff answers no competence with this id: it is unknown or archived")
		case req.MinLevel != nil && !entry.HasLevel:
			f.add(field+".minLevel", fmt.Sprintf("Competence %s has no level, so a requirement of it takes no minLevel", entry.Key))
		}
	}
	return f
}

// competencesByID is the catalogue by competence id.
func competencesByID(entries []contracts.CompetenceEntry) map[int64]contracts.CompetenceEntry {
	out := make(map[int64]contracts.CompetenceEntry, len(entries))
	for _, e := range entries {
		out[e.ID] = e
	}
	return out
}

// refuseRequestNotOpen is the 409 a fill, a change or a withdrawal of a
// request that is filled or withdrawn answers (S16, plan reading 2).
func refuseRequestNotOpen(r store.PlanningRequest) error {
	return refuseConflict(conflict(codeRequestNotOpen, "The request is not open",
		fmt.Sprintf("Request %d is %s: it takes no fill, no change and no withdrawal.", r.ID, r.Status)))
}

// refuseRequestOnClosedProject is the 409 a request written on a completed
// or cancelled project answers (S16): such a project takes no request.
func refuseRequestOnClosedProject(project *contracts.ProjectEntry) error {
	return refuseConflict(conflict(codeProjectClosed, "The project is closed",
		fmt.Sprintf("Project %s is %s: it takes no request and no change to one.", project.Code, project.Status)))
}

// requestWarnings is rule 6's warning of a request's write: the period
// leaving the project's dates. Never nil.
func requestWarnings(project *contracts.ProjectEntry, from, to time.Time) []gen.PlanningWarning {
	return allocationWarnings(overbooking{}, project, from, to)
}
