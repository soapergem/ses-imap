package imap

import (
	"github.com/emersion/go-imap/v2"
)

// specialFolder defines a hardcoded special-use folder.
type specialFolder struct {
	Name string
	Attr imap.MailboxAttr
}

// specialFolders is the list of hardcoded special-use folders.
// These are always advertised in LIST responses.
var specialFolders = []specialFolder{
	{"Sent", imap.MailboxAttrSent},
	{"Trash", imap.MailboxAttrTrash},
	{"Drafts", imap.MailboxAttrDrafts},
	{"Junk", imap.MailboxAttrJunk},
	{"Archive", imap.MailboxAttrArchive},
}

// isSpecialFolder returns true if the given name matches a special-use folder.
func isSpecialFolder(name string) bool {
	for _, f := range specialFolders {
		if f.Name == name {
			return true
		}
	}
	return false
}
