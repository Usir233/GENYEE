package chat

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Usir233/GENYEE/cli/internal/cmdutil"
	"github.com/Usir233/GENYEE/cli/internal/iostreams"
	sdk "github.com/Usir233/GENYEE/client"
)

type w8NilSessChatSvc struct{}

func (w8NilSessChatSvc) CreateSession(context.Context, *sdk.CreateSessionRequest) (*sdk.Session, error) {
	return nil, nil
}

func (w8NilSessChatSvc) KnowledgeQAStream(_ context.Context, _ string, _ *sdk.KnowledgeQARequest, cb func(*sdk.StreamResponse) error, _ ...sdk.ResourceURLOptions) error {
	return cb(&sdk.StreamResponse{ResponseType: sdk.ResponseTypeError, Content: "fatal upstream", Done: true})
}

func TestW8_Chat_ValidatesIDsNilSessionAndTerminalError(t *testing.T) {
	iostreams.SetForTest(t)

	// 1. Unsafe session ID must fail before hitting network.
	err := runChat(context.Background(), &Options{
		Query:     "hi",
		KBID:      "kb_1",
		SessionID: "../admin",
	}, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, w8NilSessChatSvc{})
	require.Error(t, err)
	var ce *cmdutil.Error
	require.True(t, errors.As(err, &ce))
	assert.Equal(t, cmdutil.CodeInputInvalidArgument, ce.Code)

	// 2. Nil session from CreateSession must return typed error instead of panicking.
	err = runChat(context.Background(), &Options{
		Query: "hi",
		KBID:  "kb_1",
	}, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, w8NilSessChatSvc{})
	require.Error(t, err)
	require.True(t, errors.As(err, &ce))
	assert.Equal(t, cmdutil.CodeSessionCreateFailed, ce.Code)

	// 3. Terminal error frame with nil stream transport error must NOT emit ok=true.
	err = runChat(context.Background(), &Options{
		Query:     "hi",
		KBID:      "kb_1",
		SessionID: "sess_1",
	}, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, w8NilSessChatSvc{})
	require.Error(t, err)
}
