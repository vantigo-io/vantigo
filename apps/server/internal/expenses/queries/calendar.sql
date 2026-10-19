-- name: CalendarClaims :many
-- CalendarClaims is this module's calendar source (calendar design D1): one
-- person's own travel claims whose journey, departure to return, overlaps the
-- window, whatever their status. The window arrives as two instants — the
-- first day's midnight and the midnight after the last day, in the viewer's
-- zone — because a journey is instants, and a half-open overlap keeps a trip
-- that returns at the window's first midnight off it.
SELECT id, purpose, destination, departure_at, return_at, status
FROM expenses.claims
WHERE user_id = @user_id
  AND departure_at < @window_end::timestamptz
  AND return_at > @window_start::timestamptz
ORDER BY departure_at, id;
