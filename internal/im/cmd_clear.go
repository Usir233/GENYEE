package im

import "context"

// ClearCommand implements /clear.
// It soft-deletes the current ChannelSession and clears the LLM context so
// the next message starts a completely fresh conversation.
type ClearCommand struct{}

func newClearCommand() *ClearCommand { return &ClearCommand{} }

// Name returns the slash-command identifier.
func (c *ClearCommand) Name() string { return "clear" }

// Description returns the localized command summary.
func (c *ClearCommand) Description() string {
	return "清空对话记忆，下次消息将开始全新会话"
}

// Execute runs the /clear command.
func (c *ClearCommand) Execute(_ context.Context, _ *CommandContext, _ []string) (*CommandResult, error) {
	return &CommandResult{
		Content: "✅ 对话已清空，下次消息将开始全新会话。",
		Action:  ActionClear,
	}, nil
}
