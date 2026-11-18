package asr

import (
	"net/http"
	"testing"
	"time"

	secutils "github.com/Usir233/GENYEE/internal/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewASRHTTPClient_DefaultTimeoutAndSharedTransport(t *testing.T) {
	zeroClient := newASRHTTPClient(0)
	customClient := newASRHTTPClient(45 * time.Second)

	assert.Equal(t, defaultASRRequestTimeout, zeroClient.Timeout)
	assert.Equal(t, 45*time.Second, customClient.Timeout)

	firstGuard, ok := zeroClient.Transport.(*secutils.SSRFValidatingRoundTripper)
	require.True(t, ok)
	secondGuard, ok := customClient.Transport.(*secutils.SSRFValidatingRoundTripper)
	require.True(t, ok)
	assert.Same(t, firstGuard.Base, secondGuard.Base)
	assert.Equal(t, http.RoundTripper(sharedASRHTTPTransport), firstGuard.Base)
}
