// Package client provides a docreader gRPC client and the shared TLS / token
// authentication helpers used by both the standalone Go SDK in this package
// and the internal docparser wrapper. Keep all auth/TLS construction here so
// the two call sites cannot drift on security defaults.
package client

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// AuthConfig holds the docreader gRPC client TLS / token configuration.
type AuthConfig struct {
	TLSEnabled bool
	CertFile   string
	KeyFile    string
	CAFile     string
	// ServerName overrides the SNI / certificate-host check on the client.
	// When empty, the address passed to Dial is used by Go's TLS stack.
	ServerName string

	AuthToken string
}

func parseEnvBool(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "true", "1", "yes", "on":
		return true
	default:
		return false
	}
}

// LoadAuthConfigFromEnv reads docreader gRPC auth knobs from the process
// environment. The caller must pass the result to BuildDialOptions to apply
// them to a gRPC connection.
func LoadAuthConfigFromEnv() *AuthConfig {
	return &AuthConfig{
		TLSEnabled: parseEnvBool("GRPC_TLS_ENABLED"),
		CertFile:   strings.TrimSpace(os.Getenv("GRPC_TLS_CERT")),
		KeyFile:    strings.TrimSpace(os.Getenv("GRPC_TLS_KEY")),
		CAFile:     strings.TrimSpace(os.Getenv("GRPC_TLS_CA")),
		ServerName: strings.TrimSpace(os.Getenv("GRPC_TLS_SERVER_NAME")),
		AuthToken:  strings.TrimSpace(os.Getenv("GRPC_AUTH_TOKEN")),
	}
}

// IsTrustedDocReaderAddr reports whether addr is eligible to receive the
// platform-wide GRPC_AUTH_TOKEN. When DOCREADER_ADDR is configured, only that
// preconfigured operator endpoint matches; when DOCREADER_ADDR is unset (local
// standalone SDK usage), only loopback endpoints match.
func IsTrustedDocReaderAddr(addr string) bool {
	normalized := normalizeDocReaderAddr(addr)
	if normalized == "" {
		return false
	}
	configured := normalizeDocReaderAddr(os.Getenv("DOCREADER_ADDR"))
	if configured != "" {
		return strings.EqualFold(normalized, configured)
	}
	host := extractDocReaderHost(normalized)
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func normalizeDocReaderAddr(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil || u.User != nil {
			return ""
		}
		switch strings.ToLower(u.Scheme) {
		case "dns", "passthrough":
			if u.Host != "" {
				s = u.Host + u.Path
			} else {
				s = strings.TrimPrefix(u.Path, "/")
			}
		default:
			if u.Host == "" {
				return ""
			}
			s = u.Host
		}
	}
	s = strings.TrimSpace(s)
	if s == "" || strings.Contains(s, "@") {
		return ""
	}
	if h, p, err := net.SplitHostPort(s); err == nil {
		cleanHost := strings.TrimSuffix(strings.ToLower(strings.Trim(h, "[]")), ".")
		if cleanHost == "" {
			return ""
		}
		return net.JoinHostPort(cleanHost, p)
	}
	return strings.TrimSuffix(strings.ToLower(s), ".")
}

func extractDocReaderHost(addr string) string {
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return strings.Trim(h, "[]")
	}
	return strings.Trim(addr, "[]")
}

// EffectiveForAddr returns a copy of AuthConfig scoped to addr. Global bearer
// credentials are never attached to non-preconfigured external hosts or to
// untrusted plaintext endpoints.
func (c *AuthConfig) EffectiveForAddr(addr string) *AuthConfig {
	if c == nil {
		return &AuthConfig{}
	}
	cp := *c
	cp.AuthToken = strings.TrimSpace(cp.AuthToken)
	if cp.AuthToken != "" && !IsTrustedDocReaderAddr(addr) {
		cp.AuthToken = ""
	}
	return &cp
}

// BuildDialOptionsForAddr applies EffectiveForAddr before building DialOptions.
func (c *AuthConfig) BuildDialOptionsForAddr(addr string, maxMsgSize int) ([]grpc.DialOption, error) {
	return c.EffectiveForAddr(addr).BuildDialOptions(maxMsgSize)
}

// BuildDialOptions returns the gRPC DialOptions that apply the configured
// transport credentials and per-RPC token. Callers should append their own
// per-call options (load balancer, message size, etc.).
func (c *AuthConfig) BuildDialOptions(maxMsgSize int) ([]grpc.DialOption, error) {
	opts := []grpc.DialOption{
		grpc.WithDefaultServiceConfig(`{"loadBalancingPolicy":"round_robin"}`),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(maxMsgSize),
			grpc.MaxCallSendMsgSize(maxMsgSize),
		),
	}

	if c.TLSEnabled {
		creds, err := c.buildTLSCredentials()
		if err != nil {
			return nil, fmt.Errorf("failed to build TLS credentials: %w", err)
		}
		opts = append(opts, grpc.WithTransportCredentials(creds))
		Logger.Printf("INFO: TLS enabled for gRPC client")
	} else {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	if c.AuthToken != "" {
		// Only allow per-RPC tokens to ride a secured channel. This mirrors
		// gRPC's own oauth2 credentials behaviour and prevents the bearer
		// token from leaking on plaintext connections.
		opts = append(opts, grpc.WithPerRPCCredentials(&tokenAuth{
			token:           c.AuthToken,
			requireTLSGuard: c.TLSEnabled,
		}))
		Logger.Printf("INFO: Token authentication enabled for gRPC client (TLS=%v)", c.TLSEnabled)
	}

	return opts, nil
}

func (c *AuthConfig) buildTLSCredentials() (credentials.TransportCredentials, error) {
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: c.ServerName,
	}

	if c.CAFile != "" {
		caCert, err := os.ReadFile(c.CAFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA certificate: %w", err)
		}
		certPool := x509.NewCertPool()
		if !certPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA certificate")
		}
		tlsConfig.RootCAs = certPool
	}

	switch {
	case c.CertFile != "" && c.KeyFile != "":
		cert, err := tls.LoadX509KeyPair(c.CertFile, c.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load client certificate: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
		Logger.Printf("INFO: mTLS enabled (client certificate loaded)")
	case c.CertFile != "" || c.KeyFile != "":
		return nil, fmt.Errorf(
			"GRPC_TLS_CERT and GRPC_TLS_KEY must be set together for mTLS",
		)
	}

	return credentials.NewTLS(tlsConfig), nil
}

type tokenAuth struct {
	token string
	// requireTLSGuard mirrors AuthConfig.TLSEnabled; we expose it via
	// RequireTransportSecurity so gRPC will refuse to send the bearer token
	// over an insecure connection when the operator has enabled TLS.
	requireTLSGuard bool
}

func (t *tokenAuth) GetRequestMetadata(ctx context.Context, uri ...string) (map[string]string, error) {
	return map[string]string{
		"authorization": "Bearer " + t.token,
	}, nil
}

func (t *tokenAuth) RequireTransportSecurity() bool {
	return t.requireTLSGuard
}
