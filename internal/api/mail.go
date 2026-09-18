package api

import (
	"context"
	"net/url"
	"strings"
)

// InboxPath is the folder a mail count means when nobody says otherwise. The
// server seeds every mailbox with it (Mailbox::STANDARD_FOLDERS) and a
// provider sync replaces the list wholesale, so a mailbox can come back
// without one — Mailbox.Inbox falls back rather than reporting zero.
const InboxPath = "INBOX"

// Mailbox is one connected mail account. Read-only here: connecting a mailbox
// is an OAuth dance that belongs in a browser, and `sal` has no mail:write.
type Mailbox struct {
	Slug        string       `json:"slug"`
	Address     string       `json:"address"`
	DisplayName string       `json:"display_name"`
	Provider    string       `json:"provider"`
	Status      string       `json:"status"`
	LastError   *string      `json:"last_error"`
	Folders     []MailFolder `json:"folders"`
}

type MailFolder struct {
	Path   string `json:"path"`
	Name   string `json:"name"`
	Total  int    `json:"total"`
	Unread int    `json:"unread"`
}

// Inbox is the folder a badge counts. Deliberately not the sum over folders: a
// thousand unread in Spam is not a thousand things waiting for you, and a
// count that includes them is a count you learn to ignore.
func (m Mailbox) Inbox() MailFolder {
	for _, f := range m.Folders {
		if strings.EqualFold(f.Path, InboxPath) {
			return f
		}
	}
	if len(m.Folders) > 0 {
		return m.Folders[0]
	}
	return MailFolder{}
}

// Name is what a row calls this account: the human label when the provider
// gave one, the address when it did not.
func (m Mailbox) Name() string {
	if m.DisplayName != "" {
		return m.DisplayName
	}
	return m.Address
}

// Mailboxes lists the caller's connected accounts, with per-folder counts.
// Requires mail:read, which joined the CLI grant after posta's; a session
// minted before that gets a missing_scope APIError, and the daemon reads that
// as "this reader has no mail" rather than as a failure.
func (c *Client) Mailboxes(ctx context.Context) ([]Mailbox, error) {
	var out struct {
		Data []Mailbox `json:"data"`
	}
	if err := c.get(ctx, "/api/v1/mail/mailboxes", url.Values{}, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}
