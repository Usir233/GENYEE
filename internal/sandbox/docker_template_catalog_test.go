package sandbox

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/client"
	"github.com/stretchr/testify/require"
)

func resetDockerImagePullsForTest(t *testing.T) {
	t.Helper()
	dockerImagePulls.mu.Lock()
	dockerImagePulls.state = make(map[string]*dockerPullState)
	dockerImagePulls.now = time.Now
	dockerImagePulls.mu.Unlock()
	t.Cleanup(func() {
		dockerImagePulls.mu.Lock()
		dockerImagePulls.state = make(map[string]*dockerPullState)
		dockerImagePulls.now = time.Now
		dockerImagePulls.mu.Unlock()
	})
}

func dockerImagePullStateCount() int {
	dockerImagePulls.mu.Lock()
	defer dockerImagePulls.mu.Unlock()
	return len(dockerImagePulls.state)
}

func newCatalogTestDockerClient(t *testing.T, engine *fakeDockerEngine, img string) *DockerRemoteClient {
	t.Helper()
	settings, err := dockerSettingsFromConfig(&Config{
		Type:        SandboxTypeDocker,
		DockerHost:  "unix:///tmp/test-docker-catalog.sock",
		DockerImage: img,
	})
	require.NoError(t, err)
	settings.IdleTTL = 0
	return newDockerRemoteClientWithAPI(engine, settings)
}

func TestDockerTemplateCatalogEvictsStaleFailedPullWhenImageAppears(t *testing.T) {
	resetDockerImagePullsForTest(t)

	engine := newFakeDockerEngine()
	pullErr := errors.New("registry unreachable")
	pulledCh := make(chan struct{}, 1)
	engine.pullHook = func(_ context.Context, _ string) (client.ImagePullResponse, error) {
		defer func() {
			select {
			case pulledCh <- struct{}{}:
			default:
			}
		}()
		return nil, pullErr
	}

	const img = "genyee/genyee-sandbox:latest"
	rc := newCatalogTestDockerClient(t, engine, img)

	got, err := rc.EnsureStandardTemplate(context.Background())
	require.NoError(t, err)
	require.NotNil(t, got)

	select {
	case <-pulledCh:
	case <-time.After(2 * time.Second):
		t.Fatal("background pull did not run")
	}

	require.Eventually(t, func() bool {
		return rc.pendingTemplate(img).Status == "failed"
	}, time.Second, 10*time.Millisecond)
	require.Equal(t, 1, dockerImagePullStateCount())

	// Once the image is loaded on the daemon, EnsureStandardTemplate and
	// ListTemplates must purge the stale failed-pull entry from memory.
	engine.mu.Lock()
	engine.imagePresent[img] = true
	engine.images = []image.Summary{{
		ID:       "sha256:abc",
		RepoTags: []string{img},
		Created:  time.Now().Unix(),
	}}
	engine.mu.Unlock()

	ready, err := rc.EnsureStandardTemplate(context.Background())
	require.NoError(t, err)
	require.Equal(t, "ready", ready.Status)
	require.Equal(t, 0, dockerImagePullStateCount(),
		"EnsureStandardTemplate must clear stale failed pull entry when image is present")
}

func TestDockerTemplateCatalogBoundsFailedPullCacheGrowth(t *testing.T) {
	resetDockerImagePullsForTest(t)

	engine := newFakeDockerEngine()
	engine.pullHook = func(_ context.Context, _ string) (client.ImagePullResponse, error) {
		return nil, errors.New("unauthorized")
	}

	for i := range 300 {
		img := fmt.Sprintf("registry.example.com/custom/sandbox:v%d", i)
		rc := newCatalogTestDockerClient(t, engine, img)
		_, err := rc.EnsureStandardTemplate(context.Background())
		require.NoError(t, err)
	}

	require.Eventually(t, func() bool {
		dockerImagePulls.mu.Lock()
		defer dockerImagePulls.mu.Unlock()
		for _, st := range dockerImagePulls.state {
			if !st.done {
				return false
			}
		}
		return true
	}, 3*time.Second, 20*time.Millisecond)

	require.LessOrEqual(t, dockerImagePullStateCount(), 256,
		"failed pull cache must remain bounded across arbitrary failing image names")
}

func TestDockerTemplateCatalogExpiresFailedPullAfterTTL(t *testing.T) {
	resetDockerImagePullsForTest(t)

	current := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	dockerImagePulls.mu.Lock()
	dockerImagePulls.now = func() time.Time { return current }
	dockerImagePulls.mu.Unlock()

	engine := newFakeDockerEngine()
	engine.pullHook = func(_ context.Context, _ string) (client.ImagePullResponse, error) {
		return nil, errors.New("network timeout")
	}

	const img = "genyee/genyee-sandbox:latest"
	rc := newCatalogTestDockerClient(t, engine, img)
	_, err := rc.EnsureStandardTemplate(context.Background())
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return rc.pendingTemplate(img).Status == "failed"
	}, time.Second, 10*time.Millisecond)
	require.Equal(t, 1, dockerImagePullStateCount())

	dockerImagePulls.mu.Lock()
	current = current.Add(dockerFailedPullTTL + time.Second)
	dockerImagePulls.mu.Unlock()

	require.Equal(t, "missing", rc.pendingTemplate(img).Status)
	require.Equal(t, 0, dockerImagePullStateCount())
}
