package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Usir233/GENYEE/cli/internal/cmdutil"
	"github.com/Usir233/GENYEE/cli/internal/config"
	"github.com/Usir233/GENYEE/cli/internal/iostreams"
	sdk "github.com/Usir233/GENYEE/client"
	"github.com/stretchr/testify/require"
)

func TestLoginPreservesHTTPFailureClassification(t *testing.T) {
	for _, apiKey := range []bool{false, true} {
		for _, tc := range []struct {
			status int
			code   cmdutil.ErrorCode
		}{
			{401, cmdutil.CodeAuthBadCredential},
			{403, cmdutil.CodeAuthForbidden},
			{429, cmdutil.CodeServerRateLimited},
			{503, cmdutil.CodeServerError},
		} {
			t.Run(
				fmt.Sprintf("api_key_%v_status_%d", apiKey, tc.status),
				func(t *testing.T) {
					_, _ = iostreams.SetForTest(t)
					f := &cmdutil.Factory{
						Config: func() (*config.Config, error) {
							return &config.Config{
								CurrentProfile: "prod",
								Profiles:       map[string]config.Profile{"prod": {Host: "https://fixture.invalid"}},
							}, nil
						},
					}
					failure := &sdk.APIError{StatusCode: tc.status, Body: "fixture failure"}
					saved := defaultAPIKeyValidator
					defaultAPIKeyValidator = func(
						context.Context,
						string,
						string,
					) (*sdk.AuthUser, error) {
						return nil, failure
					}
					t.Cleanup(func() { defaultAPIKeyValidator = saved })
					err := runLogin(
						context.Background(),
						&LoginOptions{
							WithToken:   apiKey,
							StdinReader: strings.NewReader("fixture-key"),
							Email:       "fixture@example.invalid",
							Password:    "fixture",
						},
						&cmdutil.FormatOptions{Mode: cmdutil.FormatJSON},
						f,
						&fakeLoginService{err: failure},
					)
					ce := cmdutil.AsError(err)
					require.NotNil(t, ce)
					require.Equal(t, tc.code, ce.Code)
				},
			)
		}
	}
}

func TestStatusWithExplicitEnvironmentHostIgnoresBrokenConfig(t *testing.T) {
	t.Setenv("GENYEE_TOKEN", "fixture-token")
	t.Setenv("GENYEE_API_KEY", "")
	t.Setenv("GENYEE_HOST", "https://fixture.invalid")
	out, _ := iostreams.SetForTest(t)
	f := &cmdutil.Factory{Config: func() (*config.Config, error) { return nil, errors.New("broken local config") }}
	svc := &fakeStatusService{resp: newCurrentUserResponse(&sdk.AuthUser{ID: "fixture-user"}, nil)}
	require.NoError(t, runStatus(context.Background(), &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, f, svc))
	var env struct {
		Data statusResult `json:"data"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &env))
	require.Equal(t, "https://fixture.invalid", env.Data.Host)
	require.Equal(t, "fixture-user", env.Data.UserID)
	require.Equal(t, "GENYEE_TOKEN env (stateless)", env.Data.AuthSource)
}
