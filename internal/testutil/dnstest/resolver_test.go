package dnstest

import (
	"context"
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInstallAnswersAndRestoresResolver(t *testing.T) {
	original := net.DefaultResolver
	restore, err := Install(map[string][]string{
		"public.fixture.invalid":  {"93.184.216.34", "2606:4700:4700::1111"},
		"private.fixture.invalid": {"127.0.0.1"},
		"missing.fixture.invalid": nil,
	})
	require.NoError(t, err)
	t.Cleanup(restore)
	for _, network := range []string{"udp", "tcp"} {
		t.Run(network, func(t *testing.T) {
			dial := net.DefaultResolver.Dial
			resolver := &net.Resolver{
				PreferGo: true,
				Dial: func(ctx context.Context, _ string, address string) (net.Conn, error) {
					return dial(ctx, network, address)
				},
			}
			ips, err := resolver.LookupIP(context.Background(), "ip", "public.fixture.invalid")
			require.NoError(t, err)
			require.ElementsMatch(t, []net.IP{
				net.ParseIP("93.184.216.34").To4(),
				net.ParseIP("2606:4700:4700::1111"),
			}, ips)
			ips, err = resolver.LookupIP(context.Background(), "ip4", "private.fixture.invalid")
			require.NoError(t, err)
			require.Equal(t, []net.IP{net.ParseIP("127.0.0.1").To4()}, ips)
			_, err = resolver.LookupIP(context.Background(), "ip", "missing.fixture.invalid")
			require.Error(t, err)
		})
	}
	restore()
	require.Same(t, original, net.DefaultResolver)
}

func TestInstallRejectsInvalidAddress(t *testing.T) {
	original := net.DefaultResolver
	_, err := Install(map[string][]string{"fixture.invalid": {"not an IP"}})
	require.Error(t, err)
	require.Same(t, original, net.DefaultResolver)
}
