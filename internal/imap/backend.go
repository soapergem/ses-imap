package imap

import (
	"github.com/emersion/go-imap/v2/imapserver"

	"github.com/soapergem/ses-imap/internal/config"
	"github.com/soapergem/ses-imap/internal/store"
)

// NewSession creates a new IMAP session for an incoming connection.
func NewSession(cfg *config.Config, st *store.Store, auth *store.Auth) func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
	return func(conn *imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
		return &Session{
			cfg:   cfg,
			store: st,
			auth:  auth,
		}, &imapserver.GreetingData{}, nil
	}
}
