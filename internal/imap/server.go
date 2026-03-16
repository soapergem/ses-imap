// Package imap implements an IMAP server backed by DynamoDB metadata and S3 message bodies.
package imap

import (
	"log"
	"net"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"

	"github.com/soapergem/ses-imap/internal/config"
	"github.com/soapergem/ses-imap/internal/store"
)

// Server wraps the IMAP server.
type Server struct {
	cfg     *config.Config
	store   *store.Store
	imapSrv *imapserver.Server
}

// NewServer creates a new IMAP server.
func NewServer(cfg *config.Config, st *store.Store, auth *store.Auth) *Server {
	s := &Server{
		cfg:   cfg,
		store: st,
	}

	caps := imap.CapSet{
		imap.CapIMAP4rev1: {},
	}

	s.imapSrv = imapserver.New(&imapserver.Options{
		NewSession:   NewSession(cfg, st, auth),
		Caps:         caps,
		InsecureAuth: true, // TODO: add TLS support
	})

	return s
}

// ListenAndServe starts the IMAP server on the configured address.
func (s *Server) ListenAndServe() error {
	ln, err := net.Listen("tcp", s.cfg.IMAPAddr)
	if err != nil {
		return err
	}
	log.Printf("IMAP server listening on %s", s.cfg.IMAPAddr)
	return s.imapSrv.Serve(ln)
}

// Close shuts down the IMAP server.
func (s *Server) Close() error {
	return s.imapSrv.Close()
}
