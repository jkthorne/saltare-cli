package assist

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jkthorne/saltare-cli/internal/api"
)

// maxResultBytes caps a tool_result payload — oversized results burn input
// tokens on every following turn of the conversation.
const maxResultBytes = 32 * 1024

// Executor runs one tool_use block against the REST API as the signed-in
// user. Failures come back as is_error tool_results (the model recovers),
// never as Go errors — only ctx cancellation aborts the loop upstream.
type Executor struct {
	Client *api.Client
	UserID int64 // create_task self-assigns to this user
}

func (e *Executor) Execute(ctx context.Context, use api.ContentBlock) api.ContentBlock {
	payload, err := e.dispatch(ctx, use.Name, use.Input)
	if err != nil {
		return api.ContentBlock{Type: "tool_result", ToolUseID: use.ID, Content: "Error: " + err.Error(), IsError: true}
	}
	return api.ContentBlock{Type: "tool_result", ToolUseID: use.ID, Content: capJSON(payload)}
}

func (e *Executor) dispatch(ctx context.Context, name string, input map[string]any) (any, error) {
	switch name {
	case "search_workspace":
		return e.searchWorkspace(ctx, input)
	case "list_channels":
		return e.listChannels(ctx, input)
	case "read_channel_messages":
		return e.readChannelMessages(ctx, input)
	case "list_my_tasks":
		return e.listMyTasks(ctx, input)
	case "create_task":
		return e.createTask(ctx, input)
	case "complete_task":
		return e.completeTask(ctx, input)
	case "list_documents":
		return e.listDocuments(ctx)
	case "read_document":
		return e.readDocument(ctx, input)
	default:
		return nil, fmt.Errorf("unknown tool %q", name)
	}
}

func (e *Executor) searchWorkspace(ctx context.Context, input map[string]any) (any, error) {
	q := strField(input, "q")
	if q == "" {
		return nil, fmt.Errorf("q is required")
	}
	results, err := e.Client.Search(ctx, q, api.SearchOpts{
		Type:    strField(input, "type"),
		Channel: strField(input, "channel_slug"),
	})
	if err != nil {
		return nil, err
	}

	messages := make([]map[string]any, 0, len(results.Messages))
	for _, m := range results.Messages {
		messages = append(messages, map[string]any{
			"id": m.ID, "channel_slug": m.Channel.Slug, "sender": m.Sender.Name,
			"body": clip(m.Body, 300), "created_at": m.CreatedAt,
		})
	}
	tasks := make([]map[string]any, 0, len(results.Tasks))
	for _, t := range results.Tasks {
		tasks = append(tasks, map[string]any{"slug": t.Slug, "title": t.Title, "state": t.State, "due_date": t.DueDate})
	}
	documents := make([]map[string]any, 0, len(results.Documents))
	for _, d := range results.Documents {
		documents = append(documents, map[string]any{"slug": d.Slug, "title": d.Title})
	}
	return map[string]any{"messages": messages, "tasks": tasks, "documents": documents}, nil
}

func (e *Executor) listChannels(ctx context.Context, input map[string]any) (any, error) {
	channels, err := e.Client.Channels(ctx, api.ChannelsOpts{Kind: strField(input, "kind")})
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(channels))
	for _, c := range channels {
		out = append(out, map[string]any{"slug": c.Slug, "name": c.Title(), "kind": c.Kind, "unread": c.Unread()})
	}
	return out, nil
}

func (e *Executor) readChannelMessages(ctx context.Context, input map[string]any) (any, error) {
	slug := strField(input, "channel_slug")
	if slug == "" {
		return nil, fmt.Errorf("channel_slug is required")
	}
	limit := intField(input, "limit", 30)
	if limit > 50 {
		limit = 50
	}
	messages, err := e.Client.Messages(ctx, slug, 1, limit)
	if err != nil {
		return nil, err
	}
	// The API returns newest first; flip so the transcript reads downward.
	out := make([]map[string]any, 0, len(messages))
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		if m.IsSystemEvent() {
			continue
		}
		out = append(out, map[string]any{"id": m.ID, "sender": m.Sender.Name, "body": m.Body, "created_at": m.CreatedAt})
	}
	return out, nil
}

func (e *Executor) listMyTasks(ctx context.Context, input map[string]any) (any, error) {
	all, _ := input["all"].(bool)
	tasks, err := e.Client.Tasks(ctx, api.TasksOpts{State: strField(input, "state"), Mine: !all})
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, map[string]any{"slug": t.Slug, "title": t.Title, "state": t.State, "due_date": t.DueDate})
	}
	return out, nil
}

func (e *Executor) createTask(ctx context.Context, input map[string]any) (any, error) {
	title := strField(input, "title")
	if title == "" {
		return nil, fmt.Errorf("title is required")
	}
	project, err := e.resolveProject(ctx, strField(input, "project"))
	if err != nil {
		return nil, err
	}
	task, err := e.Client.CreateTask(ctx, project.ID, title, e.UserID, api.TaskCreateOpts{})
	if err != nil {
		return nil, err
	}
	return map[string]any{"slug": task.Slug, "title": task.Title, "project": project.Slug, "state": task.State}, nil
}

func (e *Executor) resolveProject(ctx context.Context, want string) (*api.Project, error) {
	projects, err := e.Client.Projects(ctx)
	if err != nil {
		return nil, err
	}
	if len(projects) == 0 {
		return nil, fmt.Errorf("the workspace has no projects")
	}
	if want == "" {
		if len(projects) == 1 {
			return &projects[0], nil
		}
		return nil, fmt.Errorf("several projects exist — pass project as one of: %s", projectSlugs(projects))
	}
	for i := range projects {
		if projects[i].Slug == want || strings.EqualFold(projects[i].Name, want) {
			return &projects[i], nil
		}
	}
	return nil, fmt.Errorf("no project %q — options: %s", want, projectSlugs(projects))
}

func projectSlugs(projects []api.Project) string {
	slugs := make([]string, len(projects))
	for i, p := range projects {
		slugs[i] = p.Slug
	}
	return strings.Join(slugs, ", ")
}

func (e *Executor) completeTask(ctx context.Context, input map[string]any) (any, error) {
	slug := strField(input, "slug")
	if slug == "" {
		return nil, fmt.Errorf("slug is required")
	}
	task, err := e.Client.UpdateTaskState(ctx, slug, "completed")
	if err != nil {
		return nil, err
	}
	return map[string]any{"slug": task.Slug, "title": task.Title, "state": task.State}, nil
}

func (e *Executor) listDocuments(ctx context.Context) (any, error) {
	documents, err := e.Client.Documents(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(documents))
	for _, d := range documents {
		out = append(out, map[string]any{"slug": d.Slug, "title": d.Title, "published": d.Published, "updated_at": d.UpdatedAt})
	}
	return out, nil
}

func (e *Executor) readDocument(ctx context.Context, input map[string]any) (any, error) {
	slug := strField(input, "slug")
	if slug == "" {
		return nil, fmt.Errorf("slug is required")
	}
	document, err := e.Client.Document(ctx, slug)
	if err != nil {
		return nil, err
	}
	return map[string]any{"slug": document.Slug, "title": document.Title, "body": document.Body}, nil
}

func strField(input map[string]any, key string) string {
	s, _ := input[key].(string)
	return strings.TrimSpace(s)
}

func intField(input map[string]any, key string, fallback int) int {
	switch v := input[key].(type) {
	case float64: // JSON numbers decode as float64
		return int(v)
	case int:
		return v
	default:
		return fallback
	}
}

func clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

func capJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("marshal error: %v", err)
	}
	if len(raw) <= maxResultBytes {
		return string(raw)
	}
	return string(raw[:maxResultBytes]) + "\n…(truncated)"
}
