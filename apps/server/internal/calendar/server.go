package calendar

import (
	"context"

	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// server implements gen.StrictServerInterface, the module's contract
// operations. Every one answers 501 (module.ErrNotImplemented) until its
// handler lands; each area will then implement its operations in its own
// file.
type server struct {
	deps module.Deps
}

var _ gen.StrictServerInterface = (*server)(nil)

// newServer builds the module's operations over d.
func newServer(d module.Deps) *server {
	return &server{deps: d}
}

func (s *server) GetCalendarMeta(context.Context, gen.GetCalendarMetaRequestObject) (gen.GetCalendarMetaResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (s *server) GetCalendarItems(context.Context, gen.GetCalendarItemsRequestObject) (gen.GetCalendarItemsResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (s *server) PostCalendarAppointments(context.Context, gen.PostCalendarAppointmentsRequestObject) (gen.PostCalendarAppointmentsResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (s *server) GetCalendarAppointmentsById(context.Context, gen.GetCalendarAppointmentsByIdRequestObject) (gen.GetCalendarAppointmentsByIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (s *server) PutCalendarAppointmentsById(context.Context, gen.PutCalendarAppointmentsByIdRequestObject) (gen.PutCalendarAppointmentsByIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (s *server) DeleteCalendarAppointmentsById(context.Context, gen.DeleteCalendarAppointmentsByIdRequestObject) (gen.DeleteCalendarAppointmentsByIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (s *server) PutCalendarAppointmentsByIdOccurrencesByDate(context.Context, gen.PutCalendarAppointmentsByIdOccurrencesByDateRequestObject) (gen.PutCalendarAppointmentsByIdOccurrencesByDateResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (s *server) DeleteCalendarAppointmentsByIdOccurrencesByDate(context.Context, gen.DeleteCalendarAppointmentsByIdOccurrencesByDateRequestObject) (gen.DeleteCalendarAppointmentsByIdOccurrencesByDateResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (s *server) PostCalendarAppointmentsByIdResponse(context.Context, gen.PostCalendarAppointmentsByIdResponseRequestObject) (gen.PostCalendarAppointmentsByIdResponseResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (s *server) GetCalendarColleaguesByUserIdItems(context.Context, gen.GetCalendarColleaguesByUserIdItemsRequestObject) (gen.GetCalendarColleaguesByUserIdItemsResponseObject, error) {
	return nil, module.ErrNotImplemented
}
