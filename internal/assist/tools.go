// Package assist implements the sal assistant's client-side tool loop: a
// curated tool catalog executed against /api/v1 with the caller's own
// token, so every action attributes to the user and runs under their exact
// permissions. The server's MCP tool surface requires agent-bound keys;
// CLI sessions are deliberately user-bound, hence this path.
package assist

import "github.com/jkthorne/saltare/cli/internal/api"

func obj(props map[string]any, required ...string) map[string]any {
	schema := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func str(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

// Tools is the assistant's catalog. Read-heavy by design: the only writes
// are task create/complete, which self-assign to the user. No post_message
// — the assistant speaking as the user needs a confirmation UX first.
func Tools() []api.ToolDef {
	return []api.ToolDef{
		{
			Name:        "search_workspace",
			Description: "Full-text search across the workspace's messages, tasks, and documents. Message results cover channels the user belongs to.",
			InputSchema: obj(map[string]any{
				"q":            str("Search terms."),
				"type":         map[string]any{"type": "string", "enum": []string{"all", "messages", "tasks", "documents"}, "description": "Restrict to one result type."},
				"channel_slug": str("Restrict message results to one channel."),
			}, "q"),
		},
		{
			Name:        "list_channels",
			Description: "List the workspace's channels, DMs, and agent DMs with unread counts.",
			InputSchema: obj(map[string]any{
				"kind": map[string]any{"type": "string", "enum": []string{"public_channel", "private_channel", "dm", "agent_dm", "thread", "discussion"}, "description": "Filter to one kind."},
			}),
		},
		{
			Name:        "read_channel_messages",
			Description: "Read a channel's recent messages, oldest first. Thread slugs work too.",
			InputSchema: obj(map[string]any{
				"channel_slug": str("The channel to read."),
				"limit":        map[string]any{"type": "integer", "description": "How many recent messages (default 30, max 50)."},
			}, "channel_slug"),
		},
		{
			Name:        "list_my_tasks",
			Description: "List the user's open tasks (or the whole workspace's with all=true).",
			InputSchema: obj(map[string]any{
				"state": map[string]any{"type": "string", "enum": []string{"open", "in_progress", "waiting", "completed", "cancelled"}, "description": "Filter by state."},
				"all":   map[string]any{"type": "boolean", "description": "true = every task in the workspace, not just the user's."},
			}),
		},
		{
			Name:        "create_task",
			Description: "Create a task assigned to the user. Give project as a slug or name when the workspace has several projects.",
			InputSchema: obj(map[string]any{
				"title":   str("The task title."),
				"project": str("Project slug or name. Optional when the workspace has exactly one project."),
			}, "title"),
		},
		{
			Name:        "complete_task",
			Description: "Mark a task completed by its slug.",
			InputSchema: obj(map[string]any{
				"slug": str("The task slug."),
			}, "slug"),
		},
		{
			Name:        "list_documents",
			Description: "List the workspace's documents.",
			InputSchema: obj(map[string]any{}),
		},
		{
			Name:        "read_document",
			Description: "Read a document's full markdown body by slug.",
			InputSchema: obj(map[string]any{
				"slug": str("The document slug."),
			}, "slug"),
		},
	}
}
