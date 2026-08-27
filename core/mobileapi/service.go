package mobileapi

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/beresta-app/beresta/core/account"
	"github.com/beresta-app/beresta/core/model"
	"github.com/beresta-app/beresta/core/store"
	coresync "github.com/beresta-app/beresta/core/sync"
	"github.com/beresta-app/beresta/core/sync/yjsadapter"
	"github.com/beresta-app/beresta/core/transport"
)

const maxMobileEvents = 128

type mobileEvent struct {
	Sequence uint64 `json:"sequence"`
	Type     string `json:"type"`
	Payload  any    `json:"payload,omitempty"`
}

// Service is the single value-oriented gomobile boundary. Every exported
// method accepts and returns strings, byte slices, integers, booleans, or an
// error. Long operations are canceled by request ID; UI events are polled in
// bounded batches instead of invoking callbacks on foreign threads.
type Service struct {
	mu              sync.Mutex
	root            context.Context
	cancelRoot      context.CancelFunc
	wrapper         *deviceWrapper
	account         *account.Account
	workspaceID     model.ID
	requests        map[string]context.CancelFunc
	events          []mobileEvent
	nextEvent       uint64
	coordinator     *coresync.Coordinator
	remote          *transport.HTTP
	repository      *store.SyncRepository
	syncErrorDetail string
	// syncGeneration is bumped by DisconnectServer so a ConnectServer call
	// already in flight (in particular reconnectSavedServer's background
	// attempt after unlock) can detect that the user disconnected while it
	// was still working and discard its result instead of silently
	// resurrecting a connection the user just turned off.
	syncGeneration uint64
}

// NewService consumes deviceSecret. Android must generate it randomly and
// persist it only as an Android-Keystore-wrapped ciphertext.
func NewService(deviceSecret []byte) (*Service, error) {
	wrapper, err := newDeviceWrapper(deviceSecret)
	if err != nil {
		return nil, err
	}
	root, cancel := context.WithCancel(context.Background())
	return &Service{root: root, cancelRoot: cancel, wrapper: wrapper, requests: make(map[string]context.CancelFunc)}, nil
}

func (s *Service) begin(requestID string) (context.Context, func(), error) {
	if requestID == "" || len(requestID) > 128 {
		return nil, nil, errors.New("mobileapi: invalid request identifier")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.requests[requestID]; exists {
		return nil, nil, errors.New("mobileapi: request identifier is already active")
	}
	ctx, cancel := context.WithCancel(s.root)
	s.requests[requestID] = cancel
	return ctx, func() {
		cancel()
		s.mu.Lock()
		delete(s.requests, requestID)
		s.mu.Unlock()
	}, nil
}

// Cancel requests cooperative cancellation and is idempotent.
func (s *Service) Cancel(requestID string) {
	s.mu.Lock()
	cancel := s.requests[requestID]
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *Service) emit(kind string, payload any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextEvent++
	s.events = append(s.events, mobileEvent{Sequence: s.nextEvent, Type: kind, Payload: payload})
	if len(s.events) > maxMobileEvents {
		copy(s.events, s.events[len(s.events)-maxMobileEvents:])
		s.events = s.events[:maxMobileEvents]
	}
}

// PollEvents returns at most limit events newer than afterSequence.
func (s *Service) PollEvents(afterSequence int64, limit int) (string, error) {
	if afterSequence < 0 || limit <= 0 || limit > maxMobileEvents {
		return "", errors.New("mobileapi: invalid event cursor")
	}
	s.mu.Lock()
	result := make([]mobileEvent, 0, limit)
	for _, event := range s.events {
		if event.Sequence > uint64(afterSequence) {
			result = append(result, event)
			if len(result) == limit {
				break
			}
		}
	}
	s.mu.Unlock()
	return marshal(result)
}

func (s *Service) CreateAccount(requestID, databasePath, passphrase string) (string, error) {
	ctx, done, err := s.begin(requestID)
	if err != nil {
		return "", err
	}
	defer done()
	created, err := account.Create(ctx, account.CreateOptions{DatabasePath: databasePath, Passphrase: []byte(passphrase), Wrapper: s.wrapper})
	if err != nil {
		return "", err
	}
	return s.activate(created)
}

func (s *Service) UnlockAccount(requestID, databasePath, passphrase string) (string, error) {
	ctx, done, err := s.begin(requestID)
	if err != nil {
		return "", err
	}
	defer done()
	unlocked, err := account.Unlock(ctx, account.UnlockOptions{DatabasePath: databasePath, Passphrase: []byte(passphrase), Wrapper: s.wrapper})
	if err != nil {
		return "", err
	}
	return s.activate(unlocked)
}

// EnableDeviceUnlock persists a platform-wrapped Root Key for the current
// unlocked account. Android calls it only after confirming that biometric or
// device-credential authentication protects its device secret.
func (s *Service) EnableDeviceUnlock(requestID string) error {
	ctx, done, err := s.begin(requestID)
	if err != nil {
		return err
	}
	defer done()
	value, _, err := s.accountState()
	if err != nil {
		return err
	}
	return value.EnableDeviceUnlock(ctx)
}

// UnlockWithDeviceKey unlocks the local account from the platform-authenticated
// Root Key envelope. It does not accept or retain a passphrase.
func (s *Service) UnlockWithDeviceKey(requestID, databasePath string) (string, error) {
	ctx, done, err := s.begin(requestID)
	if err != nil {
		return "", err
	}
	defer done()
	unlocked, err := account.UnlockWithDeviceKey(ctx, databasePath, s.wrapper)
	if err != nil {
		return "", err
	}
	return s.activate(unlocked)
}

func (s *Service) activate(value *account.Account) (string, error) {
	workspaces, err := value.Workspaces()
	if err != nil || len(workspaces) == 0 {
		value.Lock()
		return "", errors.New("mobileapi: account has no workspace")
	}
	sort.Slice(workspaces, func(i, j int) bool { return workspaces[i].Compare(workspaces[j]) < 0 })
	activeWorkspace := workspaces[0]
	if prefs, err := loadMobilePreferences(s.root, value.DB()); err == nil && prefs.ActiveWorkspaceID != "" {
		if preferred, err := parseID(prefs.ActiveWorkspaceID); err == nil {
			for _, workspace := range workspaces {
				if workspace == preferred {
					activeWorkspace = workspace
					break
				}
			}
		}
	}
	s.mu.Lock()
	previous, coordinator := s.account, s.coordinator
	s.account, s.workspaceID, s.coordinator, s.remote, s.repository, s.syncErrorDetail = value, activeWorkspace, nil, nil, nil, ""
	s.mu.Unlock()
	if coordinator != nil {
		coordinator.Detach()
	}
	if previous != nil {
		_ = previous.Lock()
	}
	result := map[string]any{"account_id": value.ID.String(), "device_id": value.DeviceID.String(), "workspace_id": activeWorkspace.String()}
	s.emit("account_unlocked", result)
	s.reconnectSavedServer(value)
	return marshal(result)
}

// reconnectSavedServer reattaches a previously configured server without
// making unlock depend on network availability: ConnectServer performs
// authentication in the background and leaves the complete local collection
// usable if the attempt fails.
func (s *Service) reconnectSavedServer(value *account.Account) {
	cfg, err := loadSyncConnectionConfig(s.root, value.DB())
	if err != nil || !cfg.Enabled {
		return
	}
	encoded, err := marshal(connectConfig{URL: cfg.URL, SecurityMode: cfg.SecurityMode, Fingerprint: cfg.Fingerprint, DeviceName: "Android"})
	if err != nil {
		return
	}
	go func() {
		_ = s.ConnectServer(fmt.Sprintf("auto-reconnect-%d", time.Now().UnixNano()), encoded)
	}()
}

func (s *Service) Lock() error {
	s.mu.Lock()
	value, coordinator := s.account, s.coordinator
	s.account, s.workspaceID, s.coordinator, s.remote, s.repository, s.syncErrorDetail = nil, model.Nil, nil, nil, nil, ""
	s.mu.Unlock()
	if coordinator != nil {
		coordinator.Detach()
	}
	if value != nil {
		if err := value.Lock(); err != nil {
			return err
		}
	}
	s.emit("account_locked", nil)
	return nil
}

func (s *Service) accountState() (*account.Account, model.ID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.account == nil {
		return nil, model.Nil, account.ErrAccountLocked
	}
	return s.account, s.workspaceID, nil
}

func (s *Service) Status() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.account == nil {
		return `{"unlocked":false}`, nil
	}
	return marshal(map[string]any{"unlocked": true, "account_id": s.account.ID.String(), "device_id": s.account.DeviceID.String(), "workspace_id": s.workspaceID.String()})
}

type noteDTO struct {
	ID          string   `json:"id"`
	WorkspaceID string   `json:"workspace_id"`
	NotebookID  string   `json:"notebook_id,omitempty"`
	Title       string   `json:"title"`
	Pinned      bool     `json:"pinned"`
	Archived    bool     `json:"archived"`
	Deleted     bool     `json:"deleted"`
	CreatedMS   uint64   `json:"created_unix_ms"`
	UpdatedMS   int64    `json:"updated_unix_ms"`
	TagIDs      []string `json:"tag_ids"`
}

func mobileNote(note model.Note) noteDTO {
	return noteDTO{ID: note.ID.String(), WorkspaceID: note.WorkspaceID.String(), NotebookID: idString(note.NotebookID.Value), Title: note.Title.Value,
		Pinned: note.Flags.Value&model.NoteFlagPinned != 0, Archived: note.Flags.Value&model.NoteFlagArchived != 0,
		Deleted: note.Deleted.Value, CreatedMS: note.CreatedAt.PhysicalMS, TagIDs: []string{}}
}

func (s *Service) ListNotes(requestID string) (string, error) {
	ctx, done, err := s.begin(requestID)
	if err != nil {
		return "", err
	}
	defer done()
	value, workspaceID, err := s.accountState()
	if err != nil {
		return "", err
	}
	notes, err := value.ListNotes(ctx, workspaceID)
	if err != nil {
		return "", err
	}
	tagsByNote, err := store.NoteTagIDsByWorkspace(ctx, value.DB(), workspaceID)
	if err != nil {
		return "", err
	}
	metadataByNote, err := value.NoteListMetaByWorkspace(ctx, workspaceID)
	if err != nil {
		return "", err
	}
	result := make([]noteDTO, len(notes))
	for i, note := range notes {
		result[i] = mobileNote(note)
		result[i].UpdatedMS = int64(note.CreatedAt.PhysicalMS)
		if metadata, ok := metadataByNote[note.ID]; ok {
			result[i].UpdatedMS = metadata.UpdatedUnixMS
		}
		for _, tagID := range tagsByNote[note.ID] {
			result[i].TagIDs = append(result[i].TagIDs, tagID.String())
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].UpdatedMS == result[j].UpdatedMS {
			return result[i].ID < result[j].ID
		}
		return result[i].UpdatedMS > result[j].UpdatedMS
	})
	return marshal(result)
}

func (s *Service) CreateNote(requestID, notebookID, title string) (string, error) {
	ctx, done, err := s.begin(requestID)
	if err != nil {
		return "", err
	}
	defer done()
	value, workspaceID, err := s.accountState()
	if err != nil {
		return "", err
	}
	notebook, err := optionalID(notebookID)
	if err != nil {
		return "", err
	}
	note, err := value.CreateNote(ctx, workspaceID, notebook, title)
	if err != nil {
		return "", err
	}
	s.emit("notes_changed", map[string]string{"note_id": note.ID.String()})
	return marshal(mobileNote(note))
}

func (s *Service) GetNote(requestID, noteID string) (string, error) {
	ctx, done, err := s.begin(requestID)
	if err != nil {
		return "", err
	}
	defer done()
	value, workspaceID, id, err := s.noteContext(noteID)
	if err != nil {
		return "", err
	}
	note, err := value.GetNote(ctx, id)
	if err != nil || note.WorkspaceID != workspaceID {
		return "", coalesce(err, store.ErrWrongWorkspace)
	}
	body, err := noteText(ctx, value, workspaceID, id)
	if err != nil {
		return "", err
	}
	return marshal(map[string]any{"note": mobileNote(note), "body": body})
}

func (s *Service) SaveNote(requestID, noteID, title, body string) error {
	ctx, done, err := s.begin(requestID)
	if err != nil {
		return err
	}
	defer done()
	value, workspaceID, id, err := s.noteContext(noteID)
	if err != nil {
		return err
	}
	state, format, err := value.NoteDocumentState(ctx, workspaceID, id)
	if err != nil {
		return err
	}
	doc, err := yjsadapter.Restore(format, state)
	if err != nil {
		return err
	}
	defer doc.Close()
	// The mobile editor only ever sees the note's canonical Markdown
	// projection (see noteText), so writing body back must parse it as
	// Markdown rather than inserting it as plain text — otherwise a plain
	// Insert would flatten any bold/italic/list/link formatting from other
	// clients into literal, unrendered Markdown syntax.
	if err := doc.ReplaceMarkdown("body", body); err != nil {
		return err
	}
	update, err := doc.EncodeStateAsUpdate(yjsadapter.FormatV2)
	if err != nil {
		return err
	}
	if err := value.CommitNoteBody(ctx, account.NoteBodyCommand{WorkspaceID: workspaceID, NoteID: id, Update: update, UpdateFormat: yjsadapter.FormatV2, Title: &title}); err != nil {
		return err
	}
	s.emit("notes_changed", map[string]string{"note_id": id.String()})
	return nil
}

func (s *Service) DeleteNote(requestID, noteID string, deleted bool) error {
	ctx, done, err := s.begin(requestID)
	if err != nil {
		return err
	}
	defer done()
	value, workspaceID, id, err := s.noteContext(noteID)
	if err != nil {
		return err
	}
	if deleted {
		err = value.DeleteNote(ctx, workspaceID, id)
	} else {
		err = value.RestoreNote(ctx, workspaceID, id)
	}
	if err == nil {
		s.emit("notes_changed", map[string]string{"note_id": id.String()})
	}
	return err
}

func (s *Service) MoveNote(requestID, noteID, notebookID string) error {
	ctx, done, err := s.begin(requestID)
	if err != nil {
		return err
	}
	defer done()
	value, workspaceID, id, err := s.noteContext(noteID)
	if err != nil {
		return err
	}
	notebook, err := optionalID(notebookID)
	if err != nil {
		return err
	}
	if err := value.SetNoteNotebook(ctx, workspaceID, id, notebook); err != nil {
		return err
	}
	s.emit("notes_changed", map[string]string{"note_id": id.String()})
	return nil
}

// AddAttachmentData imports one bounded camera/document-provider result. The
// Android host passes bytes directly from a content URI; no plaintext path or
// shared-media cache is created.
func (s *Service) AddAttachmentData(requestID, noteID, displayName, mediaType string, contents []byte) error {
	ctx, done, err := s.begin(requestID)
	if err != nil {
		clear(contents)
		return err
	}
	defer done()
	defer clear(contents)
	value, workspaceID, id, err := s.noteContext(noteID)
	if err != nil {
		return err
	}
	attachment, err := value.AddAttachment(ctx, workspaceID, id, displayName, mediaType, bytes.NewReader(contents))
	if err != nil {
		return err
	}
	s.emit("attachments_changed", map[string]any{"note_id": id.String(), "blob_id": hex.EncodeToString(attachment.BlobID[:])})
	return nil
}

func (s *Service) ListNoteAttachments(requestID, noteID string) (string, error) {
	ctx, done, err := s.begin(requestID)
	if err != nil {
		return "", err
	}
	defer done()
	value, workspaceID, id, err := s.noteContext(noteID)
	if err != nil {
		return "", err
	}
	attachments, err := value.ListNoteAttachments(ctx, workspaceID, id)
	if err != nil {
		return "", err
	}
	result := make([]map[string]any, len(attachments))
	for i, a := range attachments {
		result[i] = map[string]any{
			"blob_id":      hex.EncodeToString(a.BlobID[:]),
			"display_name": a.DisplayName,
			"media_type":   a.MediaType,
			"size_bytes":   a.SizeBytes,
		}
	}
	return marshal(result)
}

// maxAttachmentPreviewBytes bounds ReadAttachmentData so an inline mobile
// preview never buffers an unbounded amount of decrypted plaintext in
// memory or across the platform channel. Mirrors desktop's
// maxAttachmentPreviewBytes (desktop/attachments.go).
const maxAttachmentPreviewBytes = 8 * 1024 * 1024

// errAttachmentPreviewTooLarge reports that an attachment exceeds
// maxAttachmentPreviewBytes and cannot be inline-previewed.
var errAttachmentPreviewTooLarge = fmt.Errorf("mobileapi: attachment exceeds the %d-byte inline preview limit", maxAttachmentPreviewBytes)

// boundedBuffer is a bytes.Buffer that refuses writes once it has
// accumulated more than limit bytes, so a decrypt-to-memory call fails
// closed instead of buffering an oversized attachment in full first.
type boundedBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (w *boundedBuffer) Write(p []byte) (int, error) {
	if w.buf.Len()+len(p) > w.limit {
		return 0, errAttachmentPreviewTooLarge
	}
	return w.buf.Write(p)
}

// ReadAttachmentData returns one attachment's decrypted, authenticated
// plaintext in full, for the bounded previews and thumbnails a mobile
// client renders inline. It fails closed at maxAttachmentPreviewBytes
// instead of streaming large files.
func (s *Service) ReadAttachmentData(requestID, blobIDHex string) ([]byte, error) {
	ctx, done, err := s.begin(requestID)
	if err != nil {
		return nil, err
	}
	defer done()
	value, workspaceID, err := s.accountState()
	if err != nil {
		return nil, err
	}
	raw, err := hex.DecodeString(blobIDHex)
	if err != nil {
		return nil, errors.New("mobileapi: invalid attachment identifier")
	}
	blobID, err := store.ParseBlobID(raw)
	if err != nil {
		return nil, err
	}
	dest := &boundedBuffer{limit: maxAttachmentPreviewBytes}
	if _, _, err := value.ReadAttachment(ctx, workspaceID, blobID, dest); err != nil {
		return nil, err
	}
	return dest.buf.Bytes(), nil
}

// RemoveAttachmentData removes noteID's reference to one attachment. The
// published blob itself is left in place for garbage collection.
func (s *Service) RemoveAttachmentData(requestID, noteID, blobIDHex string) error {
	ctx, done, err := s.begin(requestID)
	if err != nil {
		return err
	}
	defer done()
	value, workspaceID, id, err := s.noteContext(noteID)
	if err != nil {
		return err
	}
	raw, err := hex.DecodeString(blobIDHex)
	if err != nil {
		return errors.New("mobileapi: invalid attachment identifier")
	}
	blobID, err := store.ParseBlobID(raw)
	if err != nil {
		return err
	}
	if err := value.RemoveAttachment(ctx, workspaceID, id, blobID); err != nil {
		return err
	}
	s.emit("attachments_changed", map[string]any{"note_id": id.String(), "blob_id": blobIDHex})
	return nil
}

func (s *Service) Search(requestID, query string, limit int) (string, error) {
	ctx, done, err := s.begin(requestID)
	if err != nil {
		return "", err
	}
	defer done()
	value, workspaceID, err := s.accountState()
	if err != nil {
		return "", err
	}
	parsed, err := value.ParseSearchQuery(ctx, workspaceID, query)
	if err != nil {
		return "", err
	}
	parsed.Limit = limit
	rows, err := value.Search(ctx, workspaceID, parsed)
	if err != nil {
		return "", err
	}
	tagsByNote, err := store.NoteTagIDsByWorkspace(ctx, value.DB(), workspaceID)
	if err != nil {
		return "", err
	}
	metadataByNote, err := value.NoteListMetaByWorkspace(ctx, workspaceID)
	if err != nil {
		return "", err
	}
	result := make([]noteDTO, len(rows))
	for i, row := range rows {
		result[i] = mobileNote(row.Note)
		result[i].UpdatedMS = int64(row.Note.CreatedAt.PhysicalMS)
		if metadata, ok := metadataByNote[row.Note.ID]; ok {
			result[i].UpdatedMS = metadata.UpdatedUnixMS
		}
		for _, tagID := range tagsByNote[row.Note.ID] {
			result[i].TagIDs = append(result[i].TagIDs, tagID.String())
		}
	}
	return marshal(result)
}

func (s *Service) CreateNotebook(requestID, parentID, name string) (string, error) {
	ctx, done, err := s.begin(requestID)
	if err != nil {
		return "", err
	}
	defer done()
	value, workspaceID, err := s.accountState()
	if err != nil {
		return "", err
	}
	parent, err := optionalID(parentID)
	if err != nil {
		return "", err
	}
	notebook, err := value.CreateNotebook(ctx, workspaceID, parent, name)
	if err != nil {
		return "", err
	}
	s.emit("notebooks_changed", map[string]string{"notebook_id": notebook.ID.String()})
	return marshal(map[string]any{"id": notebook.ID.String(), "parent_id": idString(notebook.ParentID), "name": notebook.Name, "deleted": notebook.Deleted})
}

func (s *Service) ListNotebooks(requestID string) (string, error) {
	ctx, done, err := s.begin(requestID)
	if err != nil {
		return "", err
	}
	defer done()
	value, workspaceID, err := s.accountState()
	if err != nil {
		return "", err
	}
	rows, err := value.ListNotebooks(ctx, workspaceID)
	if err != nil {
		return "", err
	}
	result := make([]map[string]any, len(rows))
	for i, row := range rows {
		result[i] = map[string]any{"id": row.ID.String(), "parent_id": idString(row.ParentID), "name": row.Name, "deleted": row.Deleted}
	}
	return marshal(result)
}

func (s *Service) DeleteNotebook(requestID, notebookID string, deleted bool) error {
	ctx, done, err := s.begin(requestID)
	if err != nil {
		return err
	}
	defer done()
	value, workspaceID, err := s.accountState()
	if err != nil {
		return err
	}
	id, err := parseID(notebookID)
	if err != nil {
		return err
	}
	return value.SetNotebookDeleted(ctx, workspaceID, id, deleted)
}

func (s *Service) CreateTag(requestID, name string) (string, error) {
	ctx, done, err := s.begin(requestID)
	if err != nil {
		return "", err
	}
	defer done()
	value, workspaceID, err := s.accountState()
	if err != nil {
		return "", err
	}
	tag, err := value.CreateTag(ctx, workspaceID, name)
	if err != nil {
		return "", err
	}
	return marshal(map[string]any{"id": tag.ID.String(), "name": tag.Name, "deleted": tag.Deleted})
}

func (s *Service) DeleteTag(requestID, tagID string, deleted bool) error {
	ctx, done, err := s.begin(requestID)
	if err != nil {
		return err
	}
	defer done()
	value, workspaceID, err := s.accountState()
	if err != nil {
		return err
	}
	id, err := parseID(tagID)
	if err != nil {
		return err
	}
	return value.SetTagDeleted(ctx, workspaceID, id, deleted)
}

func (s *Service) SetNoteTag(requestID, noteID, tagID string, present bool) error {
	ctx, done, err := s.begin(requestID)
	if err != nil {
		return err
	}
	defer done()
	value, workspaceID, id, err := s.noteContext(noteID)
	if err != nil {
		return err
	}
	tag, err := parseID(tagID)
	if err != nil {
		return err
	}
	if err := value.SetNoteTag(ctx, workspaceID, id, tag, present); err != nil {
		return err
	}
	s.emit("notes_changed", map[string]string{"note_id": id.String()})
	return nil
}

func (s *Service) ListNoteTags(requestID, noteID string) (string, error) {
	ctx, done, err := s.begin(requestID)
	if err != nil {
		return "", err
	}
	defer done()
	value, workspaceID, id, err := s.noteContext(noteID)
	if err != nil {
		return "", err
	}
	note, err := value.GetNote(ctx, id)
	if err != nil || note.WorkspaceID != workspaceID {
		return "", coalesce(err, store.ErrWrongWorkspace)
	}
	tagIDs, err := store.NoteTagIDs(ctx, value.DB(), id)
	if err != nil {
		return "", err
	}
	result := make([]string, len(tagIDs))
	for i, tagID := range tagIDs {
		result[i] = tagID.String()
	}
	return marshal(result)
}

func (s *Service) ListTags(requestID string) (string, error) {
	ctx, done, err := s.begin(requestID)
	if err != nil {
		return "", err
	}
	defer done()
	value, workspaceID, err := s.accountState()
	if err != nil {
		return "", err
	}
	rows, err := value.ListTags(ctx, workspaceID)
	if err != nil {
		return "", err
	}
	result := make([]map[string]any, len(rows))
	for i, row := range rows {
		result[i] = map[string]any{"id": row.ID.String(), "name": row.Name, "deleted": row.Deleted}
	}
	return marshal(result)
}

func (s *Service) ListRevisions(requestID, noteID string) (string, error) {
	ctx, done, err := s.begin(requestID)
	if err != nil {
		return "", err
	}
	defer done()
	value, workspaceID, id, err := s.noteContext(noteID)
	if err != nil {
		return "", err
	}
	rows, err := value.ListRevisions(ctx, workspaceID, id)
	if err != nil {
		return "", err
	}
	result := make([]map[string]any, len(rows))
	for i, row := range rows {
		result[i] = map[string]any{"id": row.ID.String(), "checkpoint": row.Kind == store.RevisionKindCheckpoint, "created_unix_ms": row.CreatedUnixMS}
	}
	return marshal(result)
}

var diffOpNames = [...]string{"equal", "delete", "insert"}

// DiffRevisions returns a line-based diff from fromRevisionID to
// toRevisionID's Markdown content. An empty fromRevisionID diffs against
// the note's state before its first revision, mirroring desktop's
// DiffRevisions binding.
func (s *Service) DiffRevisions(requestID, noteID, fromRevisionID, toRevisionID string) (string, error) {
	ctx, done, err := s.begin(requestID)
	if err != nil {
		return "", err
	}
	defer done()
	value, workspaceID, id, err := s.noteContext(noteID)
	if err != nil {
		return "", err
	}
	from := model.Nil
	if fromRevisionID != "" {
		from, err = parseID(fromRevisionID)
		if err != nil {
			return "", err
		}
	}
	to, err := parseID(toRevisionID)
	if err != nil {
		return "", err
	}
	lines, err := value.DiffRevisions(ctx, workspaceID, id, from, to)
	if err != nil {
		return "", err
	}
	result := make([]map[string]any, len(lines))
	for i, line := range lines {
		result[i] = map[string]any{"op": diffOpNames[line.Op], "text": line.Text}
	}
	return marshal(result)
}

func (s *Service) RestoreRevision(requestID, noteID, revisionID string) error {
	ctx, done, err := s.begin(requestID)
	if err != nil {
		return err
	}
	defer done()
	value, workspaceID, id, err := s.noteContext(noteID)
	if err != nil {
		return err
	}
	revision, err := parseID(revisionID)
	if err != nil {
		return err
	}
	if err := value.RestoreRevision(ctx, workspaceID, id, revision); err != nil {
		return err
	}
	s.emit("notes_changed", map[string]string{"note_id": id.String()})
	return nil
}

func (s *Service) CreateBackup(requestID, destination string) (string, error) {
	ctx, done, err := s.begin(requestID)
	if err != nil {
		return "", err
	}
	defer done()
	value, _, err := s.accountState()
	if err != nil {
		return "", err
	}
	backup, err := value.CreateBackup(ctx, destination, store.BackupKindManual, time.Now())
	if err != nil {
		return "", err
	}
	s.emit("backup_completed", map[string]string{"backup_id": backup.ID.String()})
	return marshal(map[string]any{"id": backup.ID.String(), "location": backup.Location, "created_unix_ms": backup.CreatedUnixMS})
}

func (s *Service) ListBackups(requestID string) (string, error) {
	ctx, done, err := s.begin(requestID)
	if err != nil {
		return "", err
	}
	defer done()
	value, _, err := s.accountState()
	if err != nil {
		return "", err
	}
	var rows []store.Backup
	for kind := store.BackupKindDaily; kind <= store.BackupKindManual; kind++ {
		kindRows, err := value.ListBackups(ctx, kind)
		if err != nil {
			return "", err
		}
		rows = append(rows, kindRows...)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].CreatedUnixMS > rows[j].CreatedUnixMS })
	result := make([]map[string]any, len(rows))
	for i, row := range rows {
		result[i] = map[string]any{"id": row.ID.String(), "kind": row.Kind, "location": row.Location, "created_unix_ms": row.CreatedUnixMS, "corrupt": row.Corrupt}
	}
	return marshal(result)
}

func (s *Service) EnsureDailyBackup(requestID, destination string) (bool, error) {
	ctx, done, err := s.begin(requestID)
	if err != nil {
		return false, err
	}
	defer done()
	value, _, err := s.accountState()
	if err != nil {
		return false, err
	}
	created, err := value.EnsureDailyBackup(ctx, destination, time.Now())
	if err == nil && created {
		s.emit("backup_completed", map[string]string{"kind": "daily"})
	}
	return created, err
}

func (s *Service) PreviewBackup(requestID, backupID string) (string, error) {
	ctx, done, err := s.begin(requestID)
	if err != nil {
		return "", err
	}
	defer done()
	id, err := parseID(backupID)
	if err != nil {
		return "", err
	}
	value, _, err := s.accountState()
	if err != nil {
		return "", err
	}
	preview, err := value.PreviewBackup(ctx, id)
	if err != nil {
		return "", err
	}
	return marshal(preview)
}

func (s *Service) RestoreWholeBackup(requestID, backupID, destination string) (string, error) {
	ctx, done, err := s.begin(requestID)
	if err != nil {
		return "", err
	}
	defer done()
	id, err := parseID(backupID)
	if err != nil {
		return "", err
	}
	value, _, err := s.accountState()
	if err != nil {
		return "", err
	}
	result, err := value.RestoreWhole(ctx, id, destination, time.Now())
	if err != nil {
		return "", err
	}
	s.emit("collection_restored", map[string]string{"backup_id": backupID})
	return marshal(result)
}

func (s *Service) ImportBackupSet(requestID, location string, kind int) (string, error) {
	ctx, done, err := s.begin(requestID)
	if err != nil {
		return "", err
	}
	defer done()
	value, _, err := s.accountState()
	if err != nil {
		return "", err
	}
	record, err := value.ImportBackupSet(ctx, location, kind, time.Now())
	if err != nil {
		return "", err
	}
	s.emit("backup_imported", map[string]string{"backup_id": record.ID.String()})
	return marshal(map[string]any{"id": record.ID.String(), "created_unix_ms": record.CreatedUnixMS})
}

type connectConfig struct {
	URL          string `json:"url"`
	InviteCode   string `json:"invite_code"`
	Fingerprint  string `json:"fingerprint"`
	SecurityMode string `json:"security_mode"`
	DeviceName   string `json:"device_name"`
}

func (s *Service) ConnectServer(requestID, encoded string) error {
	ctx, done, err := s.begin(requestID)
	if err != nil {
		return err
	}
	defer done()
	var config connectConfig
	if err := strictJSON(encoded, &config); err != nil {
		return err
	}
	value, workspaceID, err := s.accountState()
	if err != nil {
		return err
	}
	s.mu.Lock()
	generation := s.syncGeneration
	s.mu.Unlock()
	remote, err := transport.NewHTTP(transport.HTTPConfig{BaseURL: config.URL, SecurityMode: transport.HTTPSecurityMode(config.SecurityMode), PinnedFingerprint: config.Fingerprint, DeviceID: value.DeviceID, SignChallenge: value.SignDeviceChallenge})
	if err != nil {
		return err
	}
	if config.InviteCode != "" {
		registration, err := value.ServerRegistrationData(ctx, workspaceID)
		if err != nil {
			return err
		}
		if err := remote.Register(ctx, transport.RegistrationRequest{InviteCode: config.InviteCode, DeviceName: config.DeviceName, Data: registration}); err != nil {
			return err
		}
	}
	if err := refreshMobileDevices(ctx, value, remote, workspaceID); err != nil {
		return err
	}
	worker, repository, err := s.buildWorkspaceWorker(value, workspaceID, remote)
	if err != nil {
		return err
	}
	coordinator := coresync.NewCoordinator(s.root)
	remote.BeginSync()
	if err := coordinator.Attach(worker); err != nil {
		return err
	}
	s.mu.Lock()
	stale := s.syncGeneration != generation
	s.mu.Unlock()
	if stale {
		// DisconnectServer ran while this attempt was still working (most
		// often reconnectSavedServer's background retry after unlock,
		// raced by the user tapping Disconnect before it finished): drop
		// the result instead of silently resurrecting a connection the
		// user just turned off.
		coordinator.Detach()
		return errors.New("mobileapi: server connection was disabled before this connection attempt finished")
	}
	// Persisted only once the coordinator has actually attached, so a
	// failed attach never leaves the on-disk config claiming an active
	// connection that isn't running.
	if err := saveSyncConnectionConfig(ctx, value.DB(), syncConnectionConfig{
		Enabled: true, URL: config.URL, SecurityMode: config.SecurityMode, Fingerprint: config.Fingerprint,
	}); err != nil {
		coordinator.Detach()
		return err
	}
	s.mu.Lock()
	previous := s.coordinator
	s.coordinator, s.remote, s.repository = coordinator, remote, repository
	s.mu.Unlock()
	if previous != nil {
		previous.Detach()
	}
	return nil
}

// buildWorkspaceWorker constructs (without attaching) the sync worker for
// one workspace against remote: a fresh SyncRepository/SyncProcessor pair
// and the Prepare/Bootstrap/ReviewSnapshot/PublishSnapshot/Progress hooks
// every workspace sync worker needs. ConnectServer and attachWorkspaceSync
// both call this so the hook wiring is defined once.
func (s *Service) buildWorkspaceWorker(value *account.Account, workspaceID model.ID, remote *transport.HTTP) (*coresync.Worker, *store.SyncRepository, error) {
	repository, err := store.NewSyncRepository(value.DB(), "http")
	if err != nil {
		return nil, nil, err
	}
	processor, err := account.NewSyncProcessor(value, account.SyncProcessorOptions{})
	if err != nil {
		return nil, nil, err
	}
	var lastSnapshot uint64
	var lastCatalogDigest [32]byte
	var lastReviewed model.ID
	worker, err := coresync.NewWorker(workspaceID, repository, remote, processor, coresync.WorkerOptions{
		Prepare: func(ctx context.Context) error { return refreshMobileDevices(ctx, value, remote, workspaceID) },
		Bootstrap: func(ctx context.Context) error {
			if err := refreshMobileDevices(ctx, value, remote, workspaceID); err != nil {
				return err
			}
			snapshot, err := remote.LatestSnapshot(ctx, workspaceID)
			if err != nil {
				return err
			}
			ack, err := value.ApplyWorkspaceSnapshot(ctx, snapshot, repository, processor)
			if err != nil {
				return err
			}
			_, err = remote.AcknowledgeSnapshot(ctx, ack)
			return err
		},
		ReviewSnapshot: func(ctx context.Context, _ coresync.Cursor) error {
			snapshot, err := remote.LatestSnapshot(ctx, workspaceID)
			if errors.Is(err, transport.ErrNotFound) {
				return nil
			}
			if err != nil || snapshot.ID == lastReviewed {
				return err
			}
			// Apply a snapshot ahead of the local cursor as well as one at or
			// behind it. The account method replays only the missing verified
			// operations; skipping an ahead snapshot stranded a newly joined
			// workspace with no notes or notebooks.
			ack, err := value.ApplyWorkspaceSnapshot(ctx, snapshot, repository, processor)
			if err != nil {
				return err
			}
			if _, err := remote.AcknowledgeSnapshot(ctx, ack); err != nil {
				return err
			}
			lastReviewed = snapshot.ID
			return nil
		},
		SyncAttachments: func(ctx context.Context) error {
			return value.SynchronizeWorkspaceAttachments(ctx, workspaceID, remote)
		},
		PublishSnapshot: func(ctx context.Context, cursor coresync.Cursor) error {
			// This device's own catalog (notebooks/tags/attachments, which
			// travel only inside snapshots, never as incremental operations)
			// can still contain an EnsureNotebookPlaceholder/EnsureTagPlaceholder
			// stand-in applied from a pulled note-metadata operation, ahead of
			// ever reviewing the sharer's own snapshot. Publishing that
			// placeholder-only catalog would overwrite the server's "latest"
			// snapshot with incomplete data - and since neither side's local
			// catalog digest ever changes again afterward, neither device
			// would ever republish a corrected one, permanently stranding
			// this member with hidden placeholders instead of the real
			// notebooks/tags. Deferring self-publish until every placeholder
			// resolves lets a future ReviewSnapshot catch up first.
			if pending, err := store.HasPendingSyncPlaceholders(ctx, value.DB(), workspaceID); err != nil {
				return err
			} else if pending {
				return nil
			}
			catalogDigest, err := value.WorkspaceCatalogDigest(ctx, workspaceID)
			if err != nil {
				return err
			}
			if cursor.LastSequence <= lastSnapshot && catalogDigest == lastCatalogDigest {
				return nil
			}
			if lastSnapshot != 0 && cursor.LastSequence-lastSnapshot < 1000 && catalogDigest == lastCatalogDigest {
				return nil
			}
			snapshot, err := value.CreateWorkspaceSnapshot(ctx, workspaceID, repository)
			if err != nil {
				return err
			}
			if err := remote.PutSnapshot(ctx, snapshot); err != nil {
				return err
			}
			ack, err := value.ApplyWorkspaceSnapshot(ctx, snapshot, repository, processor)
			if err != nil {
				return err
			}
			if _, err := remote.AcknowledgeSnapshot(ctx, ack); err != nil {
				return err
			}
			lastSnapshot = cursor.LastSequence
			lastCatalogDigest = catalogDigest
			lastReviewed = snapshot.ID
			return nil
		},
		Progress: func(progress coresync.Progress) {
			status := transport.StatusActive
			switch progress.Phase {
			case coresync.PhaseCurrent:
				status = transport.StatusCurrent
				remote.CompleteSync()
				s.setSyncError("")
			case coresync.PhaseBackoff:
				status = transport.StatusOffline
				remote.SyncOffline()
				s.setSyncError(progress.ErrorDetail)
			case coresync.PhaseQuarantine:
				status = transport.StatusFailed
				remote.SyncFailed()
				s.setSyncError(progress.ErrorDetail)
			default:
				remote.BeginSync()
			}
			s.emit("sync_status", map[string]string{"status": string(status)})
			s.emit("sync_progress", map[string]any{"workspace_id": progress.WorkspaceID.String(), "phase": progress.Phase, "pulled": progress.Pulled, "pushed": progress.Pushed, "cursor": progress.Cursor, "retry_ms": progress.RetryIn.Milliseconds(), "error_class": progress.ErrorClass, "error_detail": progress.ErrorDetail})
			if progress.Phase == coresync.PhaseCurrent {
				s.emit("workspace_synced", map[string]string{"workspace_id": progress.WorkspaceID.String()})
			}
		},
	})
	if err != nil {
		return nil, nil, err
	}
	return worker, repository, nil
}

func (s *Service) setSyncError(detail string) {
	s.mu.Lock()
	s.syncErrorDetail = detail
	s.mu.Unlock()
	s.emit("sync_error", map[string]string{"detail": detail})
}

// attachWorkspaceSync builds a sync worker for workspaceID and swaps it into
// s's live sync state, detaching whatever coordinator was previously
// attached and making workspaceID the cached active workspace
// (s.workspaceID) every workspace-scoped method resolves through
// accountState. It requires sync to already be enabled (s.remote set by a
// prior ConnectServer); ShareWorkspace, AcceptWorkspaceGrant, and
// SetActiveWorkspace use this to redirect the running sync worker at a
// different workspace without re-registering or re-diagnosing the server
// connection.
func (s *Service) attachWorkspaceSync(value *account.Account, workspaceID model.ID) error {
	s.mu.Lock()
	remote := s.remote
	s.mu.Unlock()
	if remote == nil {
		return errors.New("mobileapi: server synchronization is disabled")
	}
	worker, repository, err := s.buildWorkspaceWorker(value, workspaceID, remote)
	if err != nil {
		return err
	}
	coordinator := coresync.NewCoordinator(s.root)
	remote.BeginSync()
	if err := coordinator.Attach(worker); err != nil {
		return err
	}
	s.mu.Lock()
	previous := s.coordinator
	s.workspaceID, s.coordinator, s.repository = workspaceID, coordinator, repository
	s.mu.Unlock()
	if previous != nil {
		previous.Detach()
	}
	return nil
}

func (s *Service) SyncNow() error {
	value, workspaceID, err := s.accountState()
	if err != nil {
		return err
	}
	s.mu.Lock()
	coordinator, remote, repository := s.coordinator, s.remote, s.repository
	s.mu.Unlock()
	if coordinator == nil || remote == nil {
		return errors.New("mobileapi: server synchronization is disabled")
	}
	// A manually requested sync is also an explicit retry request. Keep the
	// encrypted operation intact, but release it from quarantine so a worker
	// rebuilt after a fixed client version can verify it again. If it remains
	// invalid it is immediately quarantined again and never applied.
	if repository != nil {
		entries, listErr := repository.ListQuarantine(s.root, workspaceID)
		if listErr != nil {
			return listErr
		}
		for _, entry := range entries {
			if retryErr := repository.RetryQuarantined(s.root, workspaceID, entry.OperationID); retryErr != nil {
				return retryErr
			}
		}
	}
	if !coordinator.Enabled() {
		if err := s.attachWorkspaceSync(value, workspaceID); err != nil {
			return err
		}
		s.mu.Lock()
		coordinator, remote = s.coordinator, s.remote
		s.mu.Unlock()
		if coordinator == nil || remote == nil {
			return errors.New("mobileapi: synchronization worker did not start")
		}
	}
	// Set the transport state before waking the worker so callers can wait for
	// this specific manually requested cycle instead of observing a stale
	// "current" status from the previous one.
	remote.BeginSync()
	if !coordinator.Trigger() {
		remote.SyncFailed()
		const detail = "synchronization worker is not running"
		s.setSyncError(detail)
		return errors.New("mobileapi: " + detail)
	}
	return nil
}

// DisconnectServer detaches networking only: the local database, full
// collection, and previously downloaded data remain untouched. The saved
// connection details stay on disk (with enabled cleared) so the connect
// dialog still shows them and can reconnect without retyping.
func (s *Service) DisconnectServer() error {
	s.mu.Lock()
	coordinator, value := s.coordinator, s.account
	s.coordinator, s.remote, s.repository, s.syncErrorDetail = nil, nil, nil, ""
	s.syncGeneration++
	s.mu.Unlock()
	if coordinator != nil {
		coordinator.Detach()
	}
	if value != nil {
		cfg, err := loadSyncConnectionConfig(s.root, value.DB())
		if err != nil {
			return err
		}
		cfg.Enabled = false
		if err := saveSyncConnectionConfig(s.root, value.DB(), cfg); err != nil {
			return err
		}
	}
	s.emit("sync_status", map[string]string{"status": string(transport.StatusDisabled)})
	s.emit("sync_error", map[string]string{"detail": ""})
	return nil
}

// SyncStatus reports the current synchronization transport's status
// (see core/transport.Status), without requiring an active request ID: it
// only reads cached in-memory state. It reports "disabled" whenever no
// server is currently attached, including while the account is locked.
func (s *Service) SyncStatus() (string, error) {
	s.mu.Lock()
	remote := s.remote
	s.mu.Unlock()
	if remote == nil {
		return string(transport.StatusDisabled), nil
	}
	return string(remote.Status(s.root)), nil
}

// SyncError returns the diagnostic detail from the latest failed full sync
// cycle. It is intentionally bounded by core/sync and contains no key or
// operation payload material.
func (s *Service) SyncError() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.syncErrorDetail
}

// SyncConnectionInfo returns the last server connection configured on this
// device, so the connect dialog can prefill its fields after being reopened.
// Enabled is false once the user disconnects, but URL/SecurityMode/
// Fingerprint remain so reconnecting does not require retyping them.
func (s *Service) SyncConnectionInfo(requestID string) (string, error) {
	ctx, done, err := s.begin(requestID)
	if err != nil {
		return "", err
	}
	defer done()
	value, _, err := s.accountState()
	if err != nil {
		return "", err
	}
	cfg, err := loadSyncConnectionConfig(ctx, value.DB())
	if err != nil {
		return "", err
	}
	return marshal(cfg)
}

func (s *Service) Close() {
	s.cancelRoot()
	_ = s.Lock()
	s.wrapper.close()
}

func (s *Service) noteContext(raw string) (*account.Account, model.ID, model.ID, error) {
	value, workspaceID, err := s.accountState()
	if err != nil {
		return nil, model.Nil, model.Nil, err
	}
	id, err := parseID(raw)
	return value, workspaceID, id, err
}

func noteText(ctx context.Context, value *account.Account, workspaceID, noteID model.ID) (string, error) {
	state, format, err := value.NoteDocumentState(ctx, workspaceID, noteID)
	if err != nil {
		return "", err
	}
	doc, err := yjsadapter.Restore(format, state)
	if err != nil {
		return "", err
	}
	defer doc.Close()
	return doc.Markdown("body")
}

func optionalID(raw string) (model.ID, error) {
	if raw == "" {
		return model.Nil, nil
	}
	return parseID(raw)
}

func parseID(raw string) (model.ID, error) {
	if len(raw) != 36 || strings.ToLower(raw) != raw {
		return model.Nil, errors.New("mobileapi: invalid identifier")
	}
	decoded, err := hex.DecodeString(strings.ReplaceAll(raw, "-", ""))
	if err != nil {
		return model.Nil, errors.New("mobileapi: invalid identifier")
	}
	id, err := model.ParseID(decoded)
	if err != nil || id.String() != raw {
		return model.Nil, errors.New("mobileapi: invalid identifier")
	}
	return id, nil
}

func idString(id model.ID) string {
	if id.IsZero() {
		return ""
	}
	return id.String()
}

func strictJSON(encoded string, target any) error {
	decoder := json.NewDecoder(strings.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("mobileapi: invalid JSON request: %w", err)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("mobileapi: JSON request has trailing data")
	}
	return nil
}

func marshal(value any) (string, error) {
	encoded, err := json.Marshal(value)
	return string(encoded), err
}

func coalesce(actual, fallback error) error {
	if actual != nil {
		return actual
	}
	return fallback
}

func refreshMobileDevices(ctx context.Context, value *account.Account, remote *transport.HTTP, workspaceID model.ID) error {
	// Shared-workspace operations are signed by every member's device. The
	// account-wide device list contains only this user's devices and therefore
	// cannot authenticate operations received from another workspace member.
	rows, err := remote.ListWorkspaceMemberDevices(ctx, workspaceID.String())
	if err != nil {
		return err
	}
	records := make([]account.RemoteDeviceRecord, 0, len(rows))
	for _, row := range rows {
		id, err := parseID(row.ID)
		if err != nil {
			return err
		}
		records = append(records, account.RemoteDeviceRecord{ID: id, PublicKey: row.SigningPublic, Active: row.RevokedAt == nil})
	}
	return value.UpsertRemoteDevices(ctx, records)
}
