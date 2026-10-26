package staff

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/imagemeta"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// This file is the person's photo (person-glance design S1, §4 "The photo";
// plan readings 7 and 8): GET, PUT and DELETE /people/{id}/photo. The bytes
// live in the object store under the module's scope, the key, the type and
// when it was set on the person's row. The photo is HR's: set and removed by
// staff:manage and staff:personal-data alone (photo sync design S2 — the
// person's own picture is their Vantigo avatar, set through identity), and
// made the linked user's managed avatar once its write commits
// (person_photo_sync.go); read by everyone who reads the person's Work
// group, who are shown the linked user's own avatar while HR has set none.
// The documents' orders hold: the object
// is written before the person's lock and removed again when the write
// fails, and an object the row no longer names — the replaced photo, the
// removed one — is queued in the deletion ledger in the row's own
// transaction, never removed inline.

// photoCacheControl is what a served photo carries: the apps fetch it with
// photoUpdatedAt as v, so a new photo is a new URL and five minutes in the
// browser's own cache costs nothing (reading 7).
const photoCacheControl = "private, max-age=300"

// judgePhotoWriter is the person and whether the caller may set or remove
// their photo, on q: a caller who does not see the person is the 404
// refusal, one who sees them but holds neither staff:manage nor
// staff:personal-data the uniform 403 — the person themselves included
// (photo sync design S2).
func judgePhotoWriter(ctx context.Context, q *store.Queries, caller *callerContext, personID int64) (store.GetPersonRow, error) {
	row, err := readPerson(ctx, q, personID)
	if err != nil {
		return row, err
	}
	a, err := personAudience(ctx, q, caller, personID)
	if err != nil {
		return row, err
	}
	if !a.seesPerson() {
		return row, notFound()
	}
	if !a.Manage && !a.PersonalData {
		return row, forbidden()
	}
	return row, nil
}

// photoDownload sets the headers a served photo carries before the generated
// response writes its type, its length and the bytes: cached privately, never
// sniffed, and no disposition — it is shown in place.
type photoDownload struct {
	body gen.GetStaffPeopleByIdPhotoResponseObject
}

func (r photoDownload) VisitGetStaffPeopleByIdPhotoResponse(w http.ResponseWriter) error {
	w.Header().Set("Cache-Control", photoCacheControl)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	return r.body.VisitGetStaffPeopleByIdPhotoResponse(w)
}

// GetStaffPeopleByIdPhoto Get a person's photo
// (GET /api/v1/staff/people/{id}/photo)
//
// The person's effective picture (photo sync design §4): HR's photo when
// set, else the linked user's avatar. The row and the link are read in a
// snapshot, the object or the avatar after it, outside any transaction; v
// is ignored. A person the caller does not see, one with neither picture
// (an erased person's link is never read), one whose object the store no
// longer holds — removed between the snapshot
// and the read — and one whose avatar the directory cannot answer, is the
// bare 404; a store that cannot be read is 503.
func (s *server) GetStaffPeopleByIdPhoto(ctx context.Context, req gen.GetStaffPeopleByIdPhotoRequestObject) (gen.GetStaffPeopleByIdPhotoResponseObject, error) {
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	var (
		row  store.GetPersonRow
		link *string
	)
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		if row, err = readPerson(ctx, q, req.Id); err != nil {
			return err
		}
		a, err := personAudience(ctx, q, caller, req.Id)
		if err != nil {
			return err
		}
		if !a.seesPerson() {
			return notFound()
		}
		if row.PhotoObjectKey == nil && row.ErasedAt == nil {
			link, err = linkedUserID(ctx, q, req.Id)
		}
		return err
	})
	if _, ok := asRefusal(err); ok {
		return gen.GetStaffPeopleByIdPhoto404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	if row.PhotoObjectKey == nil || row.PhotoContentType == nil {
		entry := s.linkedAvatar(ctx, req.Id, link)
		if entry == nil {
			return gen.GetStaffPeopleByIdPhoto404Response{}, nil
		}
		return photoBody(req.Id, entry.ContentType, entry.Data)
	}
	data, err := s.readPhoto(ctx, *row.PhotoObjectKey)
	if errors.Is(err, storage.ErrNotExist) {
		return gen.GetStaffPeopleByIdPhoto404Response{}, nil
	}
	if err != nil {
		s.deps.Logger.ErrorContext(ctx, "staff: a photo could not be read from the store", "person_id", req.Id, "error", err.Error())
		return gen.GetStaffPeopleByIdPhoto503ApplicationProblemPlusJSONResponse(storageUnavailable()), nil
	}
	return photoBody(req.Id, *row.PhotoContentType, data)
}

// photoBody is the photo answer for data of contentType, with its headers.
func photoBody(personID int64, contentType string, data []byte) (gen.GetStaffPeopleByIdPhotoResponseObject, error) {
	length := int64(len(data))
	switch contentType {
	case "image/png":
		return photoDownload{body: gen.GetStaffPeopleByIdPhoto200ImagepngResponse{Body: bytes.NewReader(data), ContentLength: length}}, nil
	case "image/jpeg":
		return photoDownload{body: gen.GetStaffPeopleByIdPhoto200ImagejpegResponse{Body: bytes.NewReader(data), ContentLength: length}}, nil
	}
	return nil, fmt.Errorf("staff: person %d's photo is %q, which is no photo type", personID, contentType)
}

// readPhoto is a photo's bytes, read whole — at most 5 MiB — so the answer
// can say its length.
func (s *server) readPhoto(ctx context.Context, key string) ([]byte, error) {
	body, err := s.objectGet(ctx, key)
	if err != nil {
		return nil, err
	}
	defer func() { _ = body.Close() }()
	return io.ReadAll(body)
}

// PutStaffPeopleByIdPhoto Set a person's photo
// (PUT /api/v1/staff/people/{id}/photo)
//
// The refusals come in the order that tells a caller least: the person they
// may not see (404), the uniform 403, the file (400 photo_invalid), then the
// state of things (409 person_erased, at the lock). The file's metadata —
// Exif, XMP, IPTC, PNG's text — is stripped before it is stored
// (imagemeta.Strip), and one that cannot be walked is photo_invalid too.
// The object is written
// before the transaction — a store that cannot write is 503 with nothing
// recorded — and under the person's lock the caller is judged again, the row
// set, the replaced object queued in the ledger and photo_set recorded with
// the types alone. Whatever refuses or fails after the write removes the
// object. Once it has committed, the photo is made the linked user's managed
// avatar (syncManagedAvatar): a failure there is logged, never the
// request's, and the accounts worker puts it right — the identity link made
// due now (nudgeAccountLink), so the directory has it within the hour.
func (s *server) PutStaffPeopleByIdPhoto(ctx context.Context, req gen.PutStaffPeopleByIdPhotoRequestObject) (gen.PutStaffPeopleByIdPhotoResponseObject, error) {
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		_, err := judgePhotoWriter(ctx, q, caller, req.Id)
		return err
	})
	if r, ok := asRefusal(err); ok {
		if r.status == http.StatusForbidden {
			return gen.PutStaffPeopleByIdPhoto403JSONResponse(apicommon.ForbiddenBody()), nil
		}
		return gen.PutStaffPeopleByIdPhoto404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	form := readPhotoForm(req.Body)
	contentType, f := parsePhoto(form)
	if f.failed() {
		return gen.PutStaffPeopleByIdPhoto400ApplicationProblemPlusJSONResponse(invalid(invalidPhotoTitle, f)), nil
	}
	data, err := imagemeta.Strip(contentType, form.data)
	if err != nil {
		var broken fieldErrors
		broken.addCoded(documentFormField, codePhotoInvalid, photoTypeMessage)
		return gen.PutStaffPeopleByIdPhoto400ApplicationProblemPlusJSONResponse(invalid(invalidPhotoTitle, broken)), nil
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}

	key := photoObjectKey(req.Id)
	if err := s.objectPut(ctx, key, bytes.NewReader(data), contentType); err != nil {
		s.deps.Logger.ErrorContext(ctx, "staff: a photo could not be written to the store", "person_id", req.Id, "error", err.Error())
		return gen.PutStaffPeopleByIdPhoto503ApplicationProblemPlusJSONResponse(storageUnavailable()), nil
	}

	var (
		saved store.GetPersonRow
		link  *string
	)
	err = s.withPersonLock(ctx, req.Id, refuseErased, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		current, err := judgePhotoWriter(ctx, txq, caller, req.Id)
		if err != nil {
			return err
		}
		row, err := txq.SetPersonPhoto(ctx, store.SetPersonPhotoParams{
			PhotoObjectKey: key, PhotoContentType: contentType, Now: s.deps.Clock(), ID: req.Id,
		})
		if err != nil {
			return fmt.Errorf("staff: set person %d's photo: %w", req.Id, err)
		}
		saved = store.GetPersonRow(row)
		if err := s.queuePhotoObject(ctx, txq, req.Id, current.PhotoObjectKey); err != nil {
			return err
		}
		if link, err = linkedUserID(ctx, txq, req.Id); err != nil {
			return err
		}
		change := fieldChange{To: contentType}
		if current.PhotoContentType != nil {
			change.From = *current.PhotoContentType
		}
		return s.recordChanges(ctx, txq, req.Id, eventPhotoSet, who, changes{"photo": change})
	})
	if err != nil {
		s.removeObject(ctx, req.Id, key)
	}
	if errors.Is(err, errPersonNotFound) {
		return gen.PutStaffPeopleByIdPhoto404Response{}, nil
	}
	if r, ok := asRefusal(err); ok {
		switch r.status {
		case http.StatusForbidden:
			return gen.PutStaffPeopleByIdPhoto403JSONResponse(apicommon.ForbiddenBody()), nil
		case http.StatusConflict:
			return gen.PutStaffPeopleByIdPhoto409ApplicationProblemPlusJSONResponse(r.problem), nil
		}
		return gen.PutStaffPeopleByIdPhoto404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	if saved.PhotoUpdatedAt != nil {
		s.syncManagedAvatar(ctx, who, req.Id, link, &contracts.ManagedAvatar{
			ContentType: contentType, Data: data, Source: managedAvatarSource, Stamp: photoStamp(*saved.PhotoUpdatedAt),
		})
	}
	s.nudgeAccountLink(ctx, req.Id)
	out, err := s.respondPerson(ctx, caller, saved, link)
	if err != nil {
		return nil, err
	}
	return gen.PutStaffPeopleByIdPhoto200JSONResponse(out), nil
}

// DeleteStaffPeopleByIdPhoto Remove a person's photo
// (DELETE /api/v1/staff/people/{id}/photo)
//
// The same callers as the set, judged in a snapshot first — so a caller who
// may not see an erased person learns nothing from its 409 — and again under
// the lock, where a person with no photo is the 404. The row is cleared, the
// object queued in the ledger and photo_removed recorded with the type, in
// one transaction; once it has committed, the linked user's managed avatar
// is cleared, as the set made it, and the identity link made due now.
func (s *server) DeleteStaffPeopleByIdPhoto(ctx context.Context, req gen.DeleteStaffPeopleByIdPhotoRequestObject) (gen.DeleteStaffPeopleByIdPhotoResponseObject, error) {
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		_, err := judgePhotoWriter(ctx, q, caller, req.Id)
		return err
	})
	if r, ok := asRefusal(err); ok {
		if r.status == http.StatusForbidden {
			return gen.DeleteStaffPeopleByIdPhoto403JSONResponse(apicommon.ForbiddenBody()), nil
		}
		return gen.DeleteStaffPeopleByIdPhoto404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	var link *string
	err = s.withPersonLock(ctx, req.Id, refuseErased, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		current, err := judgePhotoWriter(ctx, txq, caller, req.Id)
		if err != nil {
			return err
		}
		if current.PhotoObjectKey == nil {
			return notFound()
		}
		if link, err = linkedUserID(ctx, txq, req.Id); err != nil {
			return err
		}
		if _, err := txq.ClearPersonPhoto(ctx, store.ClearPersonPhotoParams{Now: s.deps.Clock(), ID: req.Id}); err != nil {
			return fmt.Errorf("staff: clear person %d's photo: %w", req.Id, err)
		}
		if err := s.queuePhotoObject(ctx, txq, req.Id, current.PhotoObjectKey); err != nil {
			return err
		}
		var change fieldChange
		if current.PhotoContentType != nil {
			change.From = *current.PhotoContentType
		}
		return s.recordChanges(ctx, txq, req.Id, eventPhotoRemoved, who, changes{"photo": change})
	})
	if errors.Is(err, errPersonNotFound) {
		return gen.DeleteStaffPeopleByIdPhoto404Response{}, nil
	}
	if r, ok := asRefusal(err); ok {
		switch r.status {
		case http.StatusForbidden:
			return gen.DeleteStaffPeopleByIdPhoto403JSONResponse(apicommon.ForbiddenBody()), nil
		case http.StatusConflict:
			return gen.DeleteStaffPeopleByIdPhoto409ApplicationProblemPlusJSONResponse(r.problem), nil
		}
		return gen.DeleteStaffPeopleByIdPhoto404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	s.syncManagedAvatar(ctx, who, req.Id, link, nil)
	s.nudgeAccountLink(ctx, req.Id)
	return gen.DeleteStaffPeopleByIdPhoto204Response{}, nil
}

// queuePhotoObject queues a photo's object in the deletion ledger, in the
// transaction that stopped the row naming it; nothing when key is nil. The
// ledger removes it once the transaction has committed — the photo replaced
// or removed, the person deleted or erased.
func (s *server) queuePhotoObject(ctx context.Context, txq *store.Queries, personID int64, key *string) error {
	if key == nil {
		return nil
	}
	if err := txq.InsertObjectDeletion(ctx, store.InsertObjectDeletionParams{ObjectKey: *key, Now: s.deps.Clock()}); err != nil {
		return fmt.Errorf("staff: queue person %d's photo object: %w", personID, err)
	}
	return nil
}
