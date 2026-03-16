package imap

import (
	"bufio"
	"bytes"

	"github.com/emersion/go-message/textproto"
)

// parseTextprotoHeader parses raw RFC 5322 bytes into a textproto.Header
// suitable for use with imapserver.ExtractEnvelope.
func parseTextprotoHeader(raw []byte) textproto.Header {
	r := bufio.NewReader(bytes.NewReader(raw))
	h, _ := textproto.ReadHeader(r)
	return h
}
