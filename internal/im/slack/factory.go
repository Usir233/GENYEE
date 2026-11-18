package slack

import (
	"context"
	"fmt"
	"strings"

	slackpkg "github.com/slack-go/slack"

	"github.com/Usir233/GENYEE/internal/im"
	"github.com/Usir233/GENYEE/internal/logger"
	secutils "github.com/Usir233/GENYEE/internal/utils"
)

// NewFactory returns an im.AdapterFactory for Slack channels.
// Supports "webhook" (Events API) and "websocket" (Socket Mode, default).
func NewFactory() im.AdapterFactory {
	return func(
		_ context.Context, channel *im.IMChannel, msgHandler func(context.Context, *im.IncomingMessage) error,
	) (im.Adapter, context.CancelFunc, error) {
		creds, err := im.ParseCredentials(channel.Credentials)
		if err != nil {
			return nil, nil, fmt.Errorf("parse slack credentials: %w", err)
		}

		mode := im.ResolveMode(channel, "websocket")

		switch mode {
		case "webhook":
			api := slackpkg.New(im.GetString(creds, "bot_token"),
				slackpkg.OptionHTTPClient(secutils.NewSSRFSafeHTTPClient(secutils.DefaultSSRFSafeHTTPClientConfig())))
			adapter := NewWebhookAdapter(api, im.GetString(creds, "signing_secret"))
			return adapter, func() {}, nil

		case "websocket":
			appToken := strings.TrimSpace(im.GetString(creds, "app_token"))
			botToken := strings.TrimSpace(im.GetString(creds, "bot_token"))
			if appToken == "" || botToken == "" {
				return nil, nil, fmt.Errorf("slack websocket mode requires app_token and bot_token")
			}
			client := NewLongConnClient(
				appToken,
				botToken,
				msgHandler,
			)

			adapter := NewAdapter(client, client.GetAPI())

			wsCtx, wsCancel := context.WithCancel(context.Background())
			go func() {
				startErr := client.Start(wsCtx)
				if wsCtx.Err() == nil {
					logger.Errorf(
						context.Background(), "[IM] Slack long connection stopped for channel %s: %v",
						channel.ID, startErr,
					)
				}
			}()

			return adapter, wsCancel, nil

		default:
			return nil, nil, fmt.Errorf("unsupported slack mode: %s", mode)
		}
	}
}
