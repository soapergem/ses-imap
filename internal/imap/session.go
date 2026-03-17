package imap

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/mail"
	"slices"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"

	"github.com/soapergem/ses-imap/internal/config"
	"github.com/soapergem/ses-imap/internal/store"
)

// Session implements imapserver.Session backed by DynamoDB + S3.
type Session struct {
	cfg   *config.Config
	store *store.Store
	auth  *store.Auth

	// Authenticated user (empty if not logged in).
	user string
	// Currently selected mailbox.
	selectedMailbox string
	// Cached messages for the selected mailbox.
	messages []*store.MessageMeta
}

var _ imapserver.Session = (*Session)(nil)
var _ imapserver.SessionMove = (*Session)(nil)

// Close is called when the client disconnects.
func (s *Session) Close() error {
	if s.user != "" {
		slog.Debug("session closed", "user", s.user)
	}
	return nil
}

// Login authenticates a user against SSM Parameter Store credentials.
func (s *Session) Login(username, password string) error {
	slog.Info("login attempt", "user", username)
	slog.Debug("login detail", "user_len", len(username), "pass_len", len(password))
	ctx := context.Background()
	if err := s.auth.Authenticate(ctx, username, password); err != nil {
		slog.Warn("login failed", "user", username)
		return imapserver.ErrAuthFailed
	}
	slog.Info("login success", "user", username)
	s.user = username
	return nil
}

// Select opens a mailbox.
func (s *Session) Select(mailbox string, options *imap.SelectOptions) (*imap.SelectData, error) {
	slog.Debug("SELECT", "user", s.user, "requested", mailbox)
	mailbox = s.resolveMailbox(mailbox)
	if err := s.checkMailboxAccess(mailbox); err != nil {
		slog.Warn("SELECT denied", "user", s.user, "mailbox", mailbox)
		return nil, err
	}

	ctx := context.Background()

	meta, err := s.store.EnsureMailbox(ctx, mailbox)
	if err != nil {
		return nil, err
	}

	messages, err := s.store.ListMessages(ctx, mailbox)
	if err != nil {
		return nil, err
	}

	s.selectedMailbox = mailbox
	s.messages = messages
	slog.Info("SELECT", "user", s.user, "mailbox", mailbox, "messages", len(messages))

	return &imap.SelectData{
		Flags:          []imap.Flag{imap.FlagSeen, imap.FlagAnswered, imap.FlagFlagged, imap.FlagDeleted, imap.FlagDraft},
		PermanentFlags: []imap.Flag{imap.FlagSeen, imap.FlagAnswered, imap.FlagFlagged, imap.FlagDeleted, imap.FlagDraft},
		NumMessages:    uint32(len(messages)),
		UIDNext:        imap.UID(meta.UIDNext),
		UIDValidity:    meta.UIDValidity,
	}, nil
}

// Create creates a new mailbox.
func (s *Session) Create(mailbox string, options *imap.CreateOptions) error {
	slog.Debug("CREATE", "user", s.user, "mailbox", mailbox)
	resolved := s.resolveMailbox(mailbox)
	if err := s.checkMailboxAccess(resolved); err != nil {
		return err
	}
	ctx := context.Background()
	_, err := s.store.EnsureMailbox(ctx, resolved)
	return err
}

// Delete deletes a mailbox. INBOX cannot be deleted.
func (s *Session) Delete(mailbox string) error {
	slog.Debug("DELETE", "user", s.user, "mailbox", mailbox)
	if strings.EqualFold(mailbox, "INBOX") {
		return fmt.Errorf("cannot delete INBOX")
	}
	resolved := s.resolveMailbox(mailbox)
	if err := s.checkMailboxAccess(resolved); err != nil {
		return err
	}
	ctx := context.Background()
	return s.store.DeleteMailbox(ctx, resolved)
}

// Rename renames a mailbox.
func (s *Session) Rename(mailbox, newName string, options *imap.RenameOptions) error {
	return fmt.Errorf("RENAME not supported")
}

// Subscribe subscribes to a mailbox.
func (s *Session) Subscribe(mailbox string) error {
	return nil // No-op.
}

// Unsubscribe unsubscribes from a mailbox.
func (s *Session) Unsubscribe(mailbox string) error {
	return nil // No-op.
}

// List lists mailboxes matching the given patterns.
// Returns INBOX plus all special-use folders with their attributes.
func (s *Session) List(w *imapserver.ListWriter, ref string, patterns []string, options *imap.ListOptions) error {
	slog.Debug("LIST", "user", s.user, "ref", ref, "patterns", patterns)

	// All mailboxes to advertise: INBOX + special folders.
	type mailboxEntry struct {
		name  string
		attrs []imap.MailboxAttr
	}
	mailboxes := []mailboxEntry{
		{"INBOX", []imap.MailboxAttr{imap.MailboxAttrHasNoChildren}},
	}
	for _, f := range specialFolders {
		mailboxes = append(mailboxes, mailboxEntry{
			name:  f.Name,
			attrs: []imap.MailboxAttr{f.Attr, imap.MailboxAttrHasNoChildren},
		})
	}

	for _, pattern := range patterns {
		for _, mb := range mailboxes {
			if imapserver.MatchList(mb.name, '/', ref, pattern) {
				if err := w.WriteList(&imap.ListData{
					Mailbox: mb.name,
					Attrs:   mb.attrs,
					Delim:   '/',
				}); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// Status returns the status of a mailbox.
func (s *Session) Status(mailbox string, options *imap.StatusOptions) (*imap.StatusData, error) {
	slog.Debug("STATUS", "user", s.user, "requested", mailbox)
	mailbox = s.resolveMailbox(mailbox)
	if err := s.checkMailboxAccess(mailbox); err != nil {
		return nil, err
	}

	ctx := context.Background()

	meta, err := s.store.GetMailboxMeta(ctx, mailbox)
	if err != nil {
		return nil, err
	}

	messages, err := s.store.ListMessages(ctx, mailbox)
	if err != nil {
		return nil, err
	}

	var unseen uint32
	for _, msg := range messages {
		if !store.HasFlag(msg, "\\Seen") {
			unseen++
		}
	}

	numMessages := uint32(len(messages))
	uidNext := imap.UID(meta.UIDNext)

	return &imap.StatusData{
		Mailbox:     mailbox,
		NumMessages: &numMessages,
		UIDNext:     uidNext,
		UIDValidity: meta.UIDValidity,
		NumUnseen:   &unseen,
	}, nil
}

// Append adds a message to a mailbox. Used by clients to save sent messages, drafts, etc.
func (s *Session) Append(mailbox string, r imap.LiteralReader, options *imap.AppendOptions) (*imap.AppendData, error) {
	slog.Debug("APPEND", "user", s.user, "mailbox", mailbox)
	resolved := s.resolveMailbox(mailbox)
	if err := s.checkMailboxAccess(resolved); err != nil {
		return nil, err
	}

	ctx := context.Background()

	// Read the full message body.
	body, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("reading message body: %w", err)
	}

	// Generate a unique S3 key for the appended message.
	parts := strings.SplitN(s.user, "@", 2)
	var s3Key string
	if len(parts) == 2 {
		s3Key = parts[1] + "/" + parts[0] + "/appended/" + generateID()
	} else {
		s3Key = s.user + "/appended/" + generateID()
	}

	// Write message body to S3.
	if err := s.store.PutMessageBody(ctx, s3Key, body); err != nil {
		return nil, fmt.Errorf("writing message to S3: %w", err)
	}

	// Parse headers.
	msg, err := mail.ReadMessage(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("parsing message headers: %w", err)
	}

	// Ensure the mailbox exists.
	if _, err := s.store.EnsureMailbox(ctx, resolved); err != nil {
		return nil, fmt.Errorf("ensuring mailbox: %w", err)
	}

	// Allocate a UID.
	uid, err := s.store.AllocateUID(ctx, resolved)
	if err != nil {
		return nil, fmt.Errorf("allocating UID: %w", err)
	}

	fromAddr, fromDisplay := parseAppendAddress(msg.Header.Get("From"))
	toAddr, _ := parseAppendAddress(msg.Header.Get("To"))

	// Determine flags.
	var flags []string
	if options != nil {
		for _, f := range options.Flags {
			flags = append(flags, string(f))
		}
	}

	meta := &store.MessageMeta{
		Mailbox:     resolved,
		UID:         uid,
		S3Key:       s3Key,
		MessageID:   msg.Header.Get("Message-ID"),
		FromAddr:    fromAddr,
		FromDisplay: fromDisplay,
		ToAddr:      toAddr,
		Subject:     msg.Header.Get("Subject"),
		Date:        msg.Header.Get("Date"),
		Size:        uint32(len(body)),
		Flags:       flags,
	}

	if err := s.store.PutMessageOnce(ctx, meta); err != nil {
		if errors.Is(err, store.ErrDuplicateMessage) {
			s.store.ReleaseUID(ctx, resolved, uid)
		}
		return nil, fmt.Errorf("writing message metadata: %w", err)
	}

	slog.Info("APPEND", "user", s.user, "mailbox", mailbox, "uid", uid, "subject", meta.Subject)

	return &imap.AppendData{
		UID:         imap.UID(uid),
		UIDValidity: 0,
	}, nil
}

// Poll checks for mailbox updates.
func (s *Session) Poll(w *imapserver.UpdateWriter, allowExpunge bool) error {
	// Refresh messages from DynamoDB.
	if s.selectedMailbox == "" {
		return nil
	}

	ctx := context.Background()
	messages, err := s.store.ListMessages(ctx, s.selectedMailbox)
	if err != nil {
		return err
	}

	if uint32(len(messages)) != uint32(len(s.messages)) {
		if err := w.WriteNumMessages(uint32(len(messages))); err != nil {
			return err
		}
		s.messages = messages
	}

	return nil
}

// Idle waits for mailbox updates.
func (s *Session) Idle(w *imapserver.UpdateWriter, stop <-chan struct{}) error {
	// Poll every 30 seconds.
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return nil
		case <-ticker.C:
			if err := s.Poll(w, false); err != nil {
				log.Printf("idle poll error: %v", err)
			}
		}
	}
}

// Unselect deselects the current mailbox.
func (s *Session) Unselect() error {
	s.selectedMailbox = ""
	s.messages = nil
	return nil
}

// Expunge permanently removes messages marked with \Deleted.
func (s *Session) Expunge(w *imapserver.ExpungeWriter, uids *imap.UIDSet) error {
	slog.Debug("EXPUNGE", "user", s.user)
	ctx := context.Background()

	var toExpunge []*store.MessageMeta
	for _, msg := range s.messages {
		if !store.HasFlag(msg, "\\Deleted") {
			continue
		}
		if uids != nil && !uids.Contains(imap.UID(msg.UID)) {
			continue
		}
		toExpunge = append(toExpunge, msg)
	}

	// Expunge in reverse order so sequence numbers stay valid.
	for i := len(toExpunge) - 1; i >= 0; i-- {
		msg := toExpunge[i]
		seqNum := s.seqNumForUID(msg.UID)
		if seqNum == 0 {
			continue
		}

		if err := s.store.DeleteMessage(ctx, s.selectedMailbox, msg.UID); err != nil {
			return err
		}

		if err := w.WriteExpunge(seqNum); err != nil {
			return err
		}
	}

	// Refresh cached messages.
	messages, err := s.store.ListMessages(ctx, s.selectedMailbox)
	if err != nil {
		return err
	}
	s.messages = messages

	return nil
}

// Search searches for messages matching the given criteria.
func (s *Session) Search(kind imapserver.NumKind, criteria *imap.SearchCriteria, options *imap.SearchOptions) (*imap.SearchData, error) {
	slog.Debug("SEARCH", "user", s.user, "kind", kind)
	var uids []imap.UID

	for _, msg := range s.messages {
		if matchesCriteria(msg, criteria) {
			uids = append(uids, imap.UID(msg.UID))
		}
	}

	if kind == imapserver.NumKindSeq {
		// Convert UIDs to sequence numbers.
		var seqSet imap.SeqSet
		for _, uid := range uids {
			seqNum := s.seqNumForUID(uint32(uid))
			if seqNum > 0 {
				seqSet.AddNum(seqNum)
			}
		}
		return &imap.SearchData{All: seqSet}, nil
	}

	var uidSet imap.UIDSet
	for _, uid := range uids {
		uidSet.AddNum(uid)
	}
	return &imap.SearchData{All: uidSet}, nil
}

// Fetch retrieves message data.
func (s *Session) Fetch(w *imapserver.FetchWriter, numSet imap.NumSet, options *imap.FetchOptions) error {
	ctx := context.Background()

	messages := s.resolveNumSet(numSet)
	slog.Debug("FETCH", "user", s.user, "numSet", numSet, "resolved", len(messages))

	for _, msg := range messages {
		seqNum := s.seqNumForUID(msg.UID)
		if seqNum == 0 {
			continue
		}

		msgWriter := w.CreateMessage(seqNum)

		if options.UID {
			msgWriter.WriteUID(imap.UID(msg.UID))
		}

		if options.Flags {
			flags := make([]imap.Flag, len(msg.Flags))
			for i, f := range msg.Flags {
				flags[i] = imap.Flag(f)
			}
			msgWriter.WriteFlags(flags)
		}

		if options.RFC822Size {
			msgWriter.WriteRFC822Size(int64(msg.Size))
		}

		if options.InternalDate {
			t, err := time.Parse(time.RFC1123Z, msg.Date)
			if err != nil {
				t, err = time.Parse("Mon, 2 Jan 2006 15:04:05 -0700", msg.Date)
				if err != nil {
					t = time.Now()
				}
			}
			msgWriter.WriteInternalDate(t)
		}

		if options.Envelope {
			body, err := s.store.FetchMessageBody(ctx, msg.S3Key)
			if err != nil {
				log.Printf("error fetching body for envelope: %v", err)
				if err := msgWriter.Close(); err != nil {
					return err
				}
				continue
			}
			envelope := imapserver.ExtractEnvelope(parseTextprotoHeader(body))
			msgWriter.WriteEnvelope(envelope)
		}

		if options.BodyStructure != nil {
			body, err := s.store.FetchMessageBody(ctx, msg.S3Key)
			if err != nil {
				log.Printf("error fetching body for structure: %v", err)
				if err := msgWriter.Close(); err != nil {
					return err
				}
				continue
			}
			bs := imapserver.ExtractBodyStructure(bytes.NewReader(body))
			msgWriter.WriteBodyStructure(bs)
		}

		for _, bs := range options.BodySection {
			body, err := s.store.FetchMessageBody(ctx, msg.S3Key)
			if err != nil {
				log.Printf("error fetching body section: %v", err)
				continue
			}

			sectionData := imapserver.ExtractBodySection(bytes.NewReader(body), bs)

			wc := msgWriter.WriteBodySection(bs, int64(len(sectionData)))
			if _, err := wc.Write(sectionData); err != nil {
				log.Printf("error writing body section: %v", err)
			}
			if err := wc.Close(); err != nil {
				return err
			}

			// Mark as \Seen if this is a non-PEEK fetch.
			if !bs.Peek && !store.HasFlag(msg, "\\Seen") {
				newFlags := append(msg.Flags, "\\Seen")
				if err := s.store.UpdateFlags(ctx, s.selectedMailbox, msg.UID, newFlags); err != nil {
					log.Printf("error marking message as seen: %v", err)
				}
				msg.Flags = newFlags
			}
		}

		if err := msgWriter.Close(); err != nil {
			return err
		}
	}

	return nil
}

// Store updates message flags.
func (s *Session) Store(w *imapserver.FetchWriter, numSet imap.NumSet, flags *imap.StoreFlags, options *imap.StoreOptions) error {
	slog.Debug("STORE", "user", s.user, "op", flags.Op, "flags", flags.Flags)
	ctx := context.Background()

	messages := s.resolveNumSet(numSet)

	for _, msg := range messages {
		newFlags := applyStoreFlags(msg.Flags, flags)

		if err := s.store.UpdateFlags(ctx, s.selectedMailbox, msg.UID, newFlags); err != nil {
			return err
		}
		msg.Flags = newFlags

		if !flags.Silent {
			seqNum := s.seqNumForUID(msg.UID)
			if seqNum == 0 {
				continue
			}
			msgWriter := w.CreateMessage(seqNum)
			imapFlags := make([]imap.Flag, len(newFlags))
			for i, f := range newFlags {
				imapFlags[i] = imap.Flag(f)
			}
			msgWriter.WriteFlags(imapFlags)
			msgWriter.WriteUID(imap.UID(msg.UID))
			if err := msgWriter.Close(); err != nil {
				return err
			}
		}
	}

	return nil
}

// Copy copies messages to another mailbox.
func (s *Session) Copy(numSet imap.NumSet, dest string) (*imap.CopyData, error) {
	slog.Debug("COPY", "user", s.user, "dest", dest)
	destResolved := s.resolveMailbox(dest)
	if err := s.checkMailboxAccess(destResolved); err != nil {
		return nil, err
	}

	ctx := context.Background()

	if _, err := s.store.EnsureMailbox(ctx, destResolved); err != nil {
		return nil, fmt.Errorf("ensuring destination mailbox: %w", err)
	}

	messages := s.resolveNumSet(numSet)
	var sourceUIDs, destUIDs imap.UIDSet

	for _, msg := range messages {
		uid, err := s.store.AllocateUID(ctx, destResolved)
		if err != nil {
			return nil, fmt.Errorf("allocating UID in destination: %w", err)
		}

		destMsg := &store.MessageMeta{
			Mailbox:     destResolved,
			UID:         uid,
			S3Key:       msg.S3Key,
			MessageID:   msg.MessageID,
			FromAddr:    msg.FromAddr,
			FromDisplay: msg.FromDisplay,
			ToAddr:      msg.ToAddr,
			Subject:     msg.Subject,
			Date:        msg.Date,
			Size:        msg.Size,
			Flags:       msg.Flags,
		}

		if err := s.store.PutMessageOnce(ctx, destMsg); err != nil {
			if errors.Is(err, store.ErrDuplicateMessage) {
				s.store.ReleaseUID(ctx, destResolved, uid)
				continue
			}
			return nil, fmt.Errorf("copying message: %w", err)
		}

		sourceUIDs.AddNum(imap.UID(msg.UID))
		destUIDs.AddNum(imap.UID(uid))
	}

	return &imap.CopyData{
		UIDValidity: 0, // Will be filled by the client from SELECT.
		SourceUIDs:  sourceUIDs,
		DestUIDs:    destUIDs,
	}, nil
}

// Move moves messages to another mailbox (COPY + EXPUNGE atomically).
func (s *Session) Move(w *imapserver.MoveWriter, numSet imap.NumSet, dest string) error {
	slog.Debug("MOVE", "user", s.user, "dest", dest)
	destResolved := s.resolveMailbox(dest)
	if err := s.checkMailboxAccess(destResolved); err != nil {
		return err
	}

	ctx := context.Background()

	if _, err := s.store.EnsureMailbox(ctx, destResolved); err != nil {
		return fmt.Errorf("ensuring destination mailbox: %w", err)
	}

	messages := s.resolveNumSet(numSet)
	var sourceUIDs, destUIDs imap.UIDSet

	// Copy to destination.
	for _, msg := range messages {
		uid, err := s.store.AllocateUID(ctx, destResolved)
		if err != nil {
			return fmt.Errorf("allocating UID in destination: %w", err)
		}

		destMsg := &store.MessageMeta{
			Mailbox:     destResolved,
			UID:         uid,
			S3Key:       msg.S3Key,
			MessageID:   msg.MessageID,
			FromAddr:    msg.FromAddr,
			FromDisplay: msg.FromDisplay,
			ToAddr:      msg.ToAddr,
			Subject:     msg.Subject,
			Date:        msg.Date,
			Size:        msg.Size,
			Flags:       msg.Flags,
		}

		if err := s.store.PutMessageOnce(ctx, destMsg); err != nil {
			if errors.Is(err, store.ErrDuplicateMessage) {
				s.store.ReleaseUID(ctx, destResolved, uid)
				continue
			}
			return fmt.Errorf("moving message: %w", err)
		}

		sourceUIDs.AddNum(imap.UID(msg.UID))
		destUIDs.AddNum(imap.UID(uid))
	}

	// Write COPYUID response.
	if err := w.WriteCopyData(&imap.CopyData{
		UIDValidity: 0,
		SourceUIDs:  sourceUIDs,
		DestUIDs:    destUIDs,
	}); err != nil {
		return err
	}

	// Expunge source messages in reverse order.
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		seqNum := s.seqNumForUID(msg.UID)
		if seqNum == 0 {
			continue
		}

		if err := s.store.DeleteMessage(ctx, s.selectedMailbox, msg.UID); err != nil {
			return fmt.Errorf("expunging moved message: %w", err)
		}

		if err := w.WriteExpunge(seqNum); err != nil {
			return err
		}
	}

	// Refresh cached messages.
	refreshed, err := s.store.ListMessages(ctx, s.selectedMailbox)
	if err != nil {
		return err
	}
	s.messages = refreshed

	return nil
}

// seqNumForUID returns the 1-based sequence number for a UID in the current mailbox.
func (s *Session) seqNumForUID(uid uint32) uint32 {
	for i, msg := range s.messages {
		if msg.UID == uid {
			return uint32(i + 1)
		}
	}
	return 0
}

// resolveNumSet resolves an IMAP NumSet (sequence numbers or UIDs) to messages.
func (s *Session) resolveNumSet(numSet imap.NumSet) []*store.MessageMeta {
	var result []*store.MessageMeta

	switch set := numSet.(type) {
	case imap.SeqSet:
		for i, msg := range s.messages {
			seqNum := uint32(i + 1)
			if set.Contains(seqNum) {
				result = append(result, msg)
			}
		}
	case imap.UIDSet:
		for _, msg := range s.messages {
			if set.Contains(imap.UID(msg.UID)) {
				result = append(result, msg)
			}
		}
	}

	return result
}

// matchesCriteria checks if a message matches IMAP search criteria.
func matchesCriteria(msg *store.MessageMeta, criteria *imap.SearchCriteria) bool {
	if criteria == nil {
		return true
	}

	for _, flag := range criteria.Flag {
		if !store.HasFlag(msg, string(flag)) {
			return false
		}
	}

	for _, flag := range criteria.NotFlag {
		if store.HasFlag(msg, string(flag)) {
			return false
		}
	}

	if criteria.Header != nil {
		for _, field := range criteria.Header {
			switch strings.ToLower(field.Key) {
			case "subject":
				if !strings.Contains(strings.ToLower(msg.Subject), strings.ToLower(field.Value)) {
					return false
				}
			case "from":
				if !strings.Contains(strings.ToLower(msg.FromAddr), strings.ToLower(field.Value)) &&
					!strings.Contains(strings.ToLower(msg.FromDisplay), strings.ToLower(field.Value)) {
					return false
				}
			case "to":
				if !strings.Contains(strings.ToLower(msg.ToAddr), strings.ToLower(field.Value)) {
					return false
				}
			}
		}
	}

	return true
}

// applyStoreFlags applies a STORE flags operation to existing flags.
func applyStoreFlags(existing []string, sf *imap.StoreFlags) []string {
	newFlags := make([]string, len(sf.Flags))
	for i, f := range sf.Flags {
		newFlags[i] = string(f)
	}

	switch sf.Op {
	case imap.StoreFlagsSet:
		return newFlags
	case imap.StoreFlagsAdd:
		flags := make([]string, len(existing))
		copy(flags, existing)
		for _, f := range newFlags {
			if !slices.Contains(flags, f) {
				flags = append(flags, f)
			}
		}
		return flags
	case imap.StoreFlagsDel:
		var flags []string
		for _, ef := range existing {
			if !slices.Contains(newFlags, ef) {
				flags = append(flags, ef)
			}
		}
		return flags
	}
	return existing
}

// resolveMailbox maps client-visible mailbox names to DynamoDB partition keys.
//   - INBOX -> gordon@gemovationlabs.com
//   - Sent  -> gordon@gemovationlabs.com/Sent
//   - Trash -> gordon@gemovationlabs.com/Trash
func (s *Session) resolveMailbox(mailbox string) string {
	if strings.EqualFold(mailbox, "INBOX") {
		return s.user
	}
	if isSpecialFolder(mailbox) {
		return s.user + "/" + mailbox
	}
	return mailbox
}

// clientMailboxName maps a DynamoDB partition key back to the client-visible name.
//   - gordon@gemovationlabs.com       -> INBOX
//   - gordon@gemovationlabs.com/Sent  -> Sent
func (s *Session) clientMailboxName(partition string) string {
	if strings.EqualFold(partition, s.user) {
		return "INBOX"
	}
	prefix := s.user + "/"
	if strings.HasPrefix(strings.ToLower(partition), strings.ToLower(prefix)) {
		return partition[len(prefix):]
	}
	return partition
}

// checkMailboxAccess verifies that the authenticated user is allowed to access
// the given mailbox. The resolved partition key must be the user's own mailbox
// or a sub-folder of it.
func (s *Session) checkMailboxAccess(mailbox string) error {
	if s.user == "" {
		return fmt.Errorf("not authenticated")
	}
	resolved := s.resolveMailbox(mailbox)
	lower := strings.ToLower(resolved)
	userLower := strings.ToLower(s.user)
	if lower == userLower || strings.HasPrefix(lower, userLower+"/") {
		return nil
	}
	return fmt.Errorf("access denied to mailbox %q", mailbox)
}

// generateID generates a random hex ID for appended messages.
func generateID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp if crypto/rand fails.
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// parseAppendAddress extracts the email address and display name from an RFC 5322 address.
func parseAppendAddress(raw string) (addr, display string) {
	if raw == "" {
		return "", ""
	}
	parsed, err := mail.ParseAddress(raw)
	if err != nil {
		return raw, ""
	}
	return parsed.Address, parsed.Name
}
