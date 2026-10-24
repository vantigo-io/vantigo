// Stubs for the competence operations of delivery A (staff competence design
// §6; the plan, docs/superpowers/plans/2026-10-25-staff-competence.md): Task 2
// added the operations to the contract, and these keep the package compiling
// until Task 3a (the catalogue and the person's rows) replaces them method by
// method and deletes this file.

package staff

import (
	"context"
	"errors"

	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

// errNotImplemented is what every stub answers: a 500 until its task lands.
var errNotImplemented = errors.New("staff: not implemented yet (plan task 3)")

func (s *server) GetStaffCompetenceCategories(context.Context, gen.GetStaffCompetenceCategoriesRequestObject) (gen.GetStaffCompetenceCategoriesResponseObject, error) {
	return nil, errNotImplemented
}

func (s *server) PostStaffCompetenceCategories(context.Context, gen.PostStaffCompetenceCategoriesRequestObject) (gen.PostStaffCompetenceCategoriesResponseObject, error) {
	return nil, errNotImplemented
}

func (s *server) PutStaffCompetenceCategoriesById(context.Context, gen.PutStaffCompetenceCategoriesByIdRequestObject) (gen.PutStaffCompetenceCategoriesByIdResponseObject, error) {
	return nil, errNotImplemented
}

func (s *server) GetStaffCompetences(context.Context, gen.GetStaffCompetencesRequestObject) (gen.GetStaffCompetencesResponseObject, error) {
	return nil, errNotImplemented
}

func (s *server) PostStaffCompetences(context.Context, gen.PostStaffCompetencesRequestObject) (gen.PostStaffCompetencesResponseObject, error) {
	return nil, errNotImplemented
}

func (s *server) PutStaffCompetencesById(context.Context, gen.PutStaffCompetencesByIdRequestObject) (gen.PutStaffCompetencesByIdResponseObject, error) {
	return nil, errNotImplemented
}

func (s *server) GetStaffPeopleByIdCompetences(context.Context, gen.GetStaffPeopleByIdCompetencesRequestObject) (gen.GetStaffPeopleByIdCompetencesResponseObject, error) {
	return nil, errNotImplemented
}

func (s *server) PostStaffPeopleByIdCompetences(context.Context, gen.PostStaffPeopleByIdCompetencesRequestObject) (gen.PostStaffPeopleByIdCompetencesResponseObject, error) {
	return nil, errNotImplemented
}

func (s *server) PutStaffPersonCompetencesById(context.Context, gen.PutStaffPersonCompetencesByIdRequestObject) (gen.PutStaffPersonCompetencesByIdResponseObject, error) {
	return nil, errNotImplemented
}

func (s *server) DeleteStaffPersonCompetencesById(context.Context, gen.DeleteStaffPersonCompetencesByIdRequestObject) (gen.DeleteStaffPersonCompetencesByIdResponseObject, error) {
	return nil, errNotImplemented
}

func (s *server) PostStaffPersonCompetencesByIdVerify(context.Context, gen.PostStaffPersonCompetencesByIdVerifyRequestObject) (gen.PostStaffPersonCompetencesByIdVerifyResponseObject, error) {
	return nil, errNotImplemented
}

func (s *server) PostStaffPersonCompetencesByIdRenew(context.Context, gen.PostStaffPersonCompetencesByIdRenewRequestObject) (gen.PostStaffPersonCompetencesByIdRenewResponseObject, error) {
	return nil, errNotImplemented
}

func (s *server) PostStaffPersonCompetencesByIdEvidence(context.Context, gen.PostStaffPersonCompetencesByIdEvidenceRequestObject) (gen.PostStaffPersonCompetencesByIdEvidenceResponseObject, error) {
	return nil, errNotImplemented
}
