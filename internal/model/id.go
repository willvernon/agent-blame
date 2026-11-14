package model

// Ids are derived from the source, never random, so importing the same
// transcript twice, or seeing one tool call from both a hook and a
// transcript, yields the same id.

// PromptID is the id of a Claude Code prompt.
func PromptID(session, promptID string) string {
	return "cc:" + session + ":prompt:" + promptID
}

// ToolID is the id of a Claude Code tool call.
func ToolID(session, toolUseID string) string {
	return "cc:" + session + ":tool:" + toolUseID
}

// CommitID is the id of a git commit.
func CommitID(sha string) string { return "git:" + sha }

// BranchID is the id of a branch row.
func BranchID(name string) string { return "branch:" + name }

// SessionRootID is the id of the placeholder prompt that holds tool calls
// seen before any prompt in a session (for example after a resume).
func SessionRootID(session string) string { return "cc:" + session + ":prompt:earlier" }
