package modelcmd

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Usir233/GENYEE/cli/internal/cmdutil"
	"github.com/Usir233/GENYEE/cli/internal/iostreams"
	sdk "github.com/Usir233/GENYEE/client"
)

// fakeUpdateSvc scripts GetModel (fetch baseline) and captures the
// UpdateModelRequest so tests can assert the surgical overlay.
type fakeUpdateSvc struct {
	cur         *sdk.Model
	gotReq      *sdk.UpdateModelRequest
	gotID       string
	gotCredID   string
	gotCreds    *sdk.ModelCredentialsRequest
	defaults    sdk.DefaultSettings
	gotDefaults *sdk.DefaultSettings
}

func (f *fakeUpdateSvc) GetModel(_ context.Context, _ string) (*sdk.Model, error) {
	return f.cur, nil
}

func (f *fakeUpdateSvc) UpdateModel(_ context.Context, id string, req *sdk.UpdateModelRequest) (*sdk.Model, error) {
	f.gotID = id
	f.gotReq = req
	return &sdk.Model{
		ID: id, Name: req.Name, DisplayName: req.DisplayName,
		Description: req.Description, Type: f.cur.Type,
		Parameters: req.Parameters, IsDefault: f.cur.IsDefault,
	}, nil
}

func (f *fakeUpdateSvc) UpdateModelCredentials(
	_ context.Context,
	id string,
	req *sdk.ModelCredentialsRequest,
) (*sdk.CredentialsResponse, error) {
	f.gotCredID = id
	f.gotCreds = req
	return &sdk.CredentialsResponse{Fields: map[string]sdk.CredentialFieldMetadata{"api_key": {Configured: true}}}, nil
}

func (f *fakeUpdateSvc) GetDefaultSettings(_ context.Context) (*sdk.DefaultSettings, error) {
	cp := f.defaults
	return &cp, nil
}

func (f *fakeUpdateSvc) UpdateDefaultSettings(
	_ context.Context,
	settings *sdk.DefaultSettings,
) (*sdk.DefaultSettings, error) {
	cp := *settings
	f.defaults = cp
	f.gotDefaults = &cp
	return &cp, nil
}

func baseModel() *sdk.Model {
	return &sdk.Model{
		ID: "mdl_x", Name: "keep-name", DisplayName: "old", Description: "olddesc",
		Type:       sdk.ModelTypeKnowledgeQA,
		Parameters: sdk.ModelParameters{"base_url": "http://old", "provider": "generic"},
		IsDefault:  false,
	}
}

// TestModelUpdate_SurgicalOverlay: only touched fields change; the rest (name,
// description, existing params) round-trip from the fetched baseline.
func TestModelUpdate_SurgicalOverlay(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeUpdateSvc{cur: baseModel()}
	opts := &UpdateOptions{DisplayName: "new-display", flags: modelUpdateFlags{displayName: true}}
	require.NoError(
		t,
		runUpdate(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc, "mdl_x", nil),
	)

	require.NotNil(t, svc.gotReq)
	assert.Equal(t, "mdl_x", svc.gotID, "id preserved (in-place update)")
	assert.Equal(t, "new-display", svc.gotReq.DisplayName, "display-name overlaid")
	assert.Equal(t, "keep-name", svc.gotReq.Name, "untouched name round-trips")
	assert.Equal(t, "olddesc", svc.gotReq.Description, "untouched description round-trips")
	assert.Equal(t, "http://old", svc.gotReq.Parameters["base_url"], "untouched params round-trip")
	assert.Nil(t, svc.gotCreds, "credentials endpoint must not be called when not rotating")
}

// TestModelUpdate_RotateKeyAndBaseURL: --api-key-stdin calls UpdateModelCredentials
// while --base-url updates UpdateModelRequest.Parameters without leaking api_key.
func TestModelUpdate_RotateKeyAndBaseURL(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeUpdateSvc{cur: baseModel()}
	opts := &UpdateOptions{
		BaseURL: "http://new", APIKeyStdin: true,
		StdinReader: strings.NewReader("NEW-KEY\n"),
		flags:       modelUpdateFlags{baseURL: true},
	}
	require.NoError(
		t,
		runUpdate(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc, "mdl_x", nil),
	)
	require.NotNil(t, svc.gotReq)
	assert.Equal(t, "http://new", svc.gotReq.Parameters["base_url"])
	assert.NotContains(t, svc.gotReq.Parameters, "api_key", "api_key must go to /credentials, not UpdateModel")
	require.NotNil(t, svc.gotCreds)
	assert.Equal(t, "mdl_x", svc.gotCredID)
	require.NotNil(t, svc.gotCreds.APIKey)
	assert.Equal(t, "NEW-KEY", *svc.gotCreds.APIKey)
}

func TestModelUpdate_RotateKeyOnlySkipsMetadataPut(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeUpdateSvc{cur: baseModel()}
	opts := &UpdateOptions{
		APIKeyStdin: true,
		StdinReader: strings.NewReader("  ONLY-KEY \n"),
	}
	require.NoError(
		t,
		runUpdate(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc, "mdl_x", nil),
	)
	assert.Nil(t, svc.gotReq, "credential-only rotation must not issue redundant PUT /models/{id}")
	require.NotNil(t, svc.gotCreds)
	require.NotNil(t, svc.gotCreds.APIKey)
	assert.Equal(t, "ONLY-KEY", *svc.gotCreds.APIKey)
}

func TestModelUpdate_DefaultSetAndUnsetUpdatesWorkspaceKV(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeUpdateSvc{
		cur:      baseModel(),
		defaults: sdk.DefaultSettings{EmbeddingModelID: "emb_keep"},
	}

	// Set --default -> binds chat_model_id while preserving other slots.
	setOpts := &UpdateOptions{Default: true, flags: modelUpdateFlags{def: true}}
	require.NoError(
		t,
		runUpdate(context.Background(), setOpts, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc, "mdl_x", nil),
	)
	require.NotNil(t, svc.gotDefaults)
	assert.Equal(t, "mdl_x", svc.gotDefaults.ChatModelID)
	assert.Equal(t, "emb_keep", svc.gotDefaults.EmbeddingModelID)

	// Unset --default=false -> clears chat_model_id when pointing at mdl_x.
	svc.gotDefaults = nil
	unsetOpts := &UpdateOptions{Default: false, flags: modelUpdateFlags{def: true}}
	require.NoError(
		t,
		runUpdate(context.Background(), unsetOpts, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc, "mdl_x", nil),
	)
	require.NotNil(t, svc.gotDefaults)
	assert.Equal(t, "", svc.gotDefaults.ChatModelID)
	assert.Equal(t, "emb_keep", svc.gotDefaults.EmbeddingModelID)
}

// TestModelUpdate_RequiresAtLeastOneFlag: a bare `model update <id>` is rejected
// before any network call, matching agent update.
func TestModelUpdate_RequiresAtLeastOneFlag(t *testing.T) {
	assert.False(t, modelUpdateHasFlag(&UpdateOptions{}))
	assert.True(t, modelUpdateHasFlag(&UpdateOptions{flags: modelUpdateFlags{displayName: true}}))
	assert.True(t, modelUpdateHasFlag(&UpdateOptions{APIKeyStdin: true}))
	assert.True(t, modelUpdateHasFlag(&UpdateOptions{Params: []string{"k=v"}}))
}
