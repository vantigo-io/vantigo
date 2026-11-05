package contracts

// Event is a domain event a module publishes through events.Publish onto the
// platform outbox, on its own transaction (orders design O1). Each event type
// is a DTO in contracts/events_<module>.go whose JSON tags are its wire form;
// a breaking change to that form is a new version, and handlers name the
// versions they accept.
//
// A payload carries ids, numbers, amounts and states — never personal data:
// no names, addresses, e-mails or phones (O3). A handler that needs them
// reads its own copy, or a contract, after its transaction, and a person's
// erasure then needs no sweep of the outbox.
type Event interface {
	// EventName is the event's name, "<module>.<aggregate>.<what>", as
	// "orders.sales_order.confirmed".
	EventName() string
	// EventVersion is the payload's wire version, from 1.
	EventVersion() int
	// AggregateID is the id of the aggregate the event is about, as text.
	// Deliveries to one handler are ordered per aggregate — the name less
	// its last segment, with this id, so quote 42 and sales order 42 are
	// two — and a publisher publishes an aggregate's events only while it
	// holds that aggregate's row lock.
	AggregateID() string
}
