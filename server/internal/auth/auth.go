package auth

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/StarMoe-org/starmoe-status/server/internal/config"
)

var (
	ErrUnauthorized = errors.New("unauthorized")
	ErrForbidden    = errors.New("forbidden")
)

// Authenticator handles cluster tokens, bot tokens, and Cloudflare Access admin JWTs.
type Authenticator struct {
	clusterHashes map[string][32]byte
	botHash       *[32]byte
	adminDevHash  *[32]byte

	cfTeamDomain string
	cfAUD        string
	httpClient   *http.Client

	mu        sync.RWMutex
	jwks      map[string]*rsa.PublicKey
	jwksExpAt time.Time
}

// New creates an Authenticator from Config.
func New(cfg *config.Config) *Authenticator {
	team := strings.TrimSpace(cfg.CFAccessTeamDomain)
	if team != "" && !strings.HasPrefix(team, "https://") && !strings.HasPrefix(team, "http://") {
		team = "https://" + team
	}
	team = strings.TrimRight(team, "/")

	return &Authenticator{
		clusterHashes: cfg.ClusterTokenHashes,
		botHash:       cfg.BotTokenHash,
		adminDevHash:  cfg.AdminDevTokenHash,
		cfTeamDomain:  team,
		cfAUD:         cfg.CFAccessAUD,
		httpClient:    &http.Client{Timeout: 8 * time.Second},
		jwks:          make(map[string]*rsa.PublicKey),
	}
}

// VerifyBearerHash compares the SHA-256 of the Authorization Bearer token against expected in constant time.
func VerifyBearerHash(authHeader string, expected [32]byte) bool {
	tok := extractBearer(authHeader)
	if tok == "" {
		return false
	}
	sum := sha256.Sum256([]byte(tok))
	return subtle.ConstantTimeCompare(sum[:], expected[:]) == 1
}

// VerifyClusterToken verifies that the Bearer token matches the configured token for cluster.
func (a *Authenticator) VerifyClusterToken(authHeader, cluster string) bool {
	expected, ok := a.clusterHashes[cluster]
	if !ok {
		return false
	}
	return VerifyBearerHash(authHeader, expected)
}

// VerifyBotToken verifies the moebot plugin Bearer token.
func (a *Authenticator) VerifyBotToken(authHeader string) bool {
	if a.botHash == nil {
		return false
	}
	return VerifyBearerHash(authHeader, *a.botHash)
}

// VerifyAdmin authenticates an admin request via Cloudflare Access JWT or optional ADMIN_DEV_TOKEN.
func (a *Authenticator) VerifyAdmin(ctx context.Context, r *http.Request) (string, error) {
	// 1. Cloudflare Access JWT (header or cookie)
	if a.cfTeamDomain != "" && a.cfAUD != "" {
		token := strings.TrimSpace(r.Header.Get("Cf-Access-Jwt-Assertion"))
		if token == "" {
			if c, err := r.Cookie("CF_Authorization"); err == nil {
				token = strings.TrimSpace(c.Value)
			}
		}
		if token != "" {
			email, err := a.verifyCFAccessJWT(ctx, token)
			if err == nil {
				return email, nil
			}
		}
	}

	// 2. Optional ADMIN_DEV_TOKEN for local testing
	if a.adminDevHash != nil {
		if VerifyBearerHash(r.Header.Get("Authorization"), *a.adminDevHash) {
			return "dev-admin@local", nil
		}
	}

	return "", ErrUnauthorized
}

type jwtHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
}

type jwtClaims struct {
	Aud   any    `json:"aud"`
	Iss   string `json:"iss"`
	Email string `json:"email"`
	Sub   string `json:"sub"`
	Exp   int64  `json:"exp"`
	Nbf   int64  `json:"nbf"`
}

func (a *Authenticator) verifyCFAccessJWT(ctx context.Context, token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", errors.New("malformed jwt")
	}

	hdrBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", fmt.Errorf("decode header: %w", err)
	}
	var hdr jwtHeader
	if err := json.Unmarshal(hdrBytes, &hdr); err != nil {
		return "", fmt.Errorf("parse header: %w", err)
	}
	if hdr.Alg != "RS256" {
		return "", fmt.Errorf("unsupported jwt alg %q", hdr.Alg)
	}

	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("decode payload: %w", err)
	}
	var claims jwtClaims
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return "", fmt.Errorf("parse payload: %w", err)
	}

	now := time.Now().Unix()
	if claims.Exp > 0 && now > claims.Exp+30 {
		return "", errors.New("jwt expired")
	}
	if claims.Nbf > 0 && now < claims.Nbf-30 {
		return "", errors.New("jwt not yet valid")
	}
	if strings.TrimRight(claims.Iss, "/") != a.cfTeamDomain {
		return "", fmt.Errorf("unexpected iss %q", claims.Iss)
	}
	if !audContains(claims.Aud, a.cfAUD) {
		return "", errors.New("aud mismatch")
	}

	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return "", fmt.Errorf("decode signature: %w", err)
	}

	pubKey, err := a.getPublicKey(ctx, hdr.Kid)
	if err != nil {
		return "", err
	}

	signingInput := parts[0] + "." + parts[1]
	digest := sha256.Sum256([]byte(signingInput))
	if err := rsa.VerifyPKCS1v15(pubKey, crypto.SHA256, digest[:], sig); err != nil {
		return "", fmt.Errorf("invalid jwt signature: %w", err)
	}

	if claims.Email != "" {
		return claims.Email, nil
	}
	return claims.Sub, nil
}

func (a *Authenticator) getPublicKey(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	a.mu.RLock()
	if time.Now().Before(a.jwksExpAt) {
		if k, ok := a.jwks[kid]; ok {
			a.mu.RUnlock()
			return k, nil
		}
	}
	a.mu.RUnlock()

	if err := a.refreshJWKS(ctx); err != nil {
		return nil, err
	}

	a.mu.RLock()
	defer a.mu.RUnlock()
	k, ok := a.jwks[kid]
	if !ok {
		return nil, fmt.Errorf("unknown jwks kid %q", kid)
	}
	return k, nil
}

type jwksResponse struct {
	Keys []struct {
		Kid string `json:"kid"`
		Kty string `json:"kty"`
		N   string `json:"n"`
		E   string `json:"e"`
	} `json:"keys"`
}

func (a *Authenticator) refreshJWKS(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.cfTeamDomain+"/cdn-cgi/access/certs", nil)
	if err != nil {
		return err
	}
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetch cf jwks: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("cf jwks status %d", resp.StatusCode)
	}

	var body jwksResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return fmt.Errorf("decode cf jwks: %w", err)
	}

	keys := make(map[string]*rsa.PublicKey, len(body.Keys))
	for _, k := range body.Keys {
		if k.Kty != "RSA" || k.Kid == "" || k.N == "" || k.E == "" {
			continue
		}
		nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
		if err != nil {
			continue
		}
		eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
		if err != nil {
			continue
		}
		var e int
		for _, b := range eBytes {
			e = e<<8 | int(b)
		}
		keys[k.Kid] = &rsa.PublicKey{
			N: new(big.Int).SetBytes(nBytes),
			E: e,
		}
	}

	a.mu.Lock()
	a.jwks = keys
	a.jwksExpAt = time.Now().Add(15 * time.Minute)
	a.mu.Unlock()
	return nil
}

func audContains(aud any, target string) bool {
	switch v := aud.(type) {
	case string:
		return v == target
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok && s == target {
				return true
			}
		}
	}
	return false
}

func extractBearer(header string) string {
	parts := strings.SplitN(strings.TrimSpace(header), " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}
