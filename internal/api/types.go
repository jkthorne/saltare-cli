package api

import "time"

// Wire types mirror app/serializers/api/v1/*. Nullable JSON fields are
// pointers; `body` decodes null to "" which is fine for display.

type Sender struct {
	Type string `json:"type"` // "User" | "Agent"
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type Message struct {
	ID                  int64          `json:"id"`
	ChannelID           int64          `json:"channel_id"`
	ThreadRootMessageID *int64         `json:"thread_root_message_id"`
	Body                string         `json:"body"`
	Sender              Sender         `json:"sender"`
	EditedAt            *time.Time     `json:"edited_at"`
	PinnedAt            *time.Time     `json:"pinned_at"`
	ArchivedAt          *time.Time     `json:"archived_at"`
	SystemEvent         *string        `json:"system_event"`
	Metadata            map[string]any `json:"metadata"` // event context; system events only
	CreatedAt           time.Time      `json:"created_at"`
	UpdatedAt           time.Time      `json:"updated_at"`
}

func (m Message) IsSystemEvent() bool { return m.SystemEvent != nil && *m.SystemEvent != "" }

type Channel struct {
	ID              int64      `json:"id"`
	Slug            string     `json:"slug"`
	Name            string     `json:"name"`
	DisplayName     string     `json:"display_name"` // viewer-relative; DMs read as the other participant
	DmPairKey       *string    `json:"dm_pair_key"`
	Description     *string    `json:"description"`
	Kind            string     `json:"kind"`
	Archived        bool       `json:"archived"`
	MessagesCount   int        `json:"messages_count"`
	MembersCount    int        `json:"members_count"`
	CreatorID       int64      `json:"creator_id"`
	HostType        *string    `json:"host_type"`
	HostID          *int64     `json:"host_id"`
	ParentChannelID *int64     `json:"parent_channel_id"`
	RootMessageID   *int64     `json:"root_message_id"`
	Member          bool       `json:"member"`
	LastReadAt      *time.Time `json:"last_read_at"`
	UnreadCount     *int       `json:"unread_count"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

func (c Channel) Unread() int {
	if c.UnreadCount == nil {
		return 0
	}
	return *c.UnreadCount
}

// Title is the sidebar/header label: the server's viewer-relative
// display_name when present, else the stored name (older servers).
func (c Channel) Title() string {
	if c.DisplayName != "" {
		return c.DisplayName
	}
	return c.Name
}

type Workspace struct {
	ID   int64  `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
	Plan string `json:"plan"`
}

type User struct {
	ID    int64  `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

type Device struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Platform string `json:"platform"`
}

// DeviceSession is the POST /api/v1/auth/token (and /refresh) payload.
type DeviceSession struct {
	TokenType        string    `json:"token_type"`
	AccessToken      string    `json:"access_token"`
	RefreshToken     string    `json:"refresh_token"`
	ExpiresAt        time.Time `json:"expires_at"`
	RefreshExpiresAt time.Time `json:"refresh_expires_at"`
	Scopes           []string  `json:"scopes"`
	Device           Device    `json:"device"`
	Workspace        Workspace `json:"workspace"`
	User             User      `json:"user"`
}

// Me is the GET /api/v1/me payload.
type Me struct {
	Workspace Workspace `json:"workspace"`
	User      User      `json:"user"`
}
