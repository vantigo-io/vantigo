package staff

import (
	"context"

	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

// PostStaffPeopleByIdAccountPass Show a person's pass
// (POST /api/v1/staff/people/{id}/account/pass)
//
// Task 4 (Entra delivery B) builds it: the person's provider link, the
// provider's RevealPass on no connection, the pass answered once with
// Cache-Control no-store. Until then no person has a pass to show.
func (s *server) PostStaffPeopleByIdAccountPass(context.Context, gen.PostStaffPeopleByIdAccountPassRequestObject) (gen.PostStaffPeopleByIdAccountPassResponseObject, error) {
	return gen.PostStaffPeopleByIdAccountPass404Response{}, nil
}
