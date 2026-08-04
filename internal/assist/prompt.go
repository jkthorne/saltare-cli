package assist

import "time"

// SystemPrompt grounds the assistant in the workspace, the user, and its
// tool access. Shared by the TUI session (ctrl+g) and `sal ask`.
func SystemPrompt(workspaceName, userName string, withTools bool) string {
	prompt := "You are the built-in assistant of sal, the terminal client for the Saltare workspace \"" + workspaceName + "\"."
	if userName != "" {
		prompt += " You are talking to " + userName + "."
	}
	prompt += " Today is " + time.Now().Format("Monday, January 2, 2006") + "."
	if withTools {
		prompt += " Your tools read the workspace (search, channels, messages, tasks, documents) and can create or" +
			" complete tasks; everything runs as the user and task writes are assigned to them." +
			" Ground answers in real workspace data instead of guessing, and refer to channels, tasks, and documents" +
			" by their slug so the user can jump to them."
	} else {
		prompt += " You cannot run tools or read workspace data in this session; say so if asked."
	}
	prompt += " Be concise. Plain text or simple markdown only — it renders in a terminal."
	return prompt
}
