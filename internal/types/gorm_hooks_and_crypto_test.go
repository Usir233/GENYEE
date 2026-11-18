package types

import (
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestKnowledgeTagBeforeCreatePropagatesScanError(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	// Without knowledge_tags table, Scan(&maxSeqID) fails; BeforeCreate must propagate the error
	// instead of silently falling back to SeqID=1.
	tag := &KnowledgeTag{
		ID:              "tag-1",
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		Name:            "alpha",
	}
	err = tag.BeforeCreate(db)
	require.Error(t, err, "BeforeCreate must return DB Scan error instead of silently assigning SeqID=1")
	assert.Zero(t, tag.SeqID)
}

func TestKnowledgeTagConcurrentSQLiteSeqIDAssignment(t *testing.T) {
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })

	require.NoError(t, db.AutoMigrate(&KnowledgeTag{}))

	const workers = 12
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	seqIDs := make(chan int64, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			tag := &KnowledgeTag{
				ID:              fmt.Sprintf("tag-%d", idx),
				TenantID:        1,
				KnowledgeBaseID: "kb-1",
				Name:            fmt.Sprintf("name-%d", idx),
			}
			if createErr := db.Create(tag).Error; createErr != nil {
				errs <- createErr
				return
			}
			seqIDs <- tag.SeqID
		}(i)
	}
	wg.Wait()
	close(errs)
	close(seqIDs)

	for createErr := range errs {
		require.NoError(t, createErr)
	}
	seen := make(map[int64]bool, workers)
	for id := range seqIDs {
		assert.Greater(t, id, int64(0))
		assert.False(t, seen[id], "duplicate SeqID %d", id)
		seen[id] = true
	}
	assert.Len(t, seen, workers)
}

func TestMCPOAuthSecretsEncryptedAtRestAndPlaintextInMemoryAfterSave(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", "0123456789abcdef0123456789abcdef")

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&MCPOAuthClient{}, &MCPOAuthToken{}))

	client := &MCPOAuthClient{
		TenantID:     7,
		ServiceID:    "svc-1",
		ClientID:     "cid-1",
		ClientSecret: "plain-client-secret",
	}
	require.NoError(t, db.Create(client).Error)
	assert.Equal(t, "plain-client-secret", client.ClientSecret,
		"in-memory MCPOAuthClient.ClientSecret must stay plaintext after Create")

	var rawSecret sql.NullString
	require.NoError(
		t, db.Raw("SELECT client_secret FROM mcp_oauth_clients WHERE id = ?", client.ID).Scan(&rawSecret).Error,
	)
	assert.True(
		t, strings.HasPrefix(rawSecret.String, "enc:v1:"), "DB column must be encrypted, got %q", rawSecret.String,
	)

	token := &MCPOAuthToken{
		TenantID:      7,
		UserID:        "u-1",
		PrincipalType: PrincipalWebUser,
		PrincipalID:   "u-1",
		ServiceID:     "svc-1",
		AccessToken:   "plain-access-token",
		RefreshToken:  "plain-refresh-token",
	}
	require.NoError(t, db.Create(token).Error)
	assert.Equal(t, "plain-access-token", token.AccessToken,
		"in-memory MCPOAuthToken.AccessToken must stay plaintext after Create")
	assert.Equal(t, "plain-refresh-token", token.RefreshToken,
		"in-memory MCPOAuthToken.RefreshToken must stay plaintext after Create")

	var rawAccess, rawRefresh sql.NullString
	require.NoError(t, db.Raw(
		"SELECT access_token, refresh_token FROM mcp_oauth_tokens WHERE id = ?", token.ID,
	).Row().Scan(&rawAccess, &rawRefresh))
	assert.True(t, strings.HasPrefix(rawAccess.String, "enc:v1:"))
	assert.True(t, strings.HasPrefix(rawRefresh.String, "enc:v1:"))
}

func TestNilGuardsOnErrorConstructorsAndTemporaryDocument(t *testing.T) {
	require.NotPanics(t, func() {
		errFile := NewDuplicateFileError(nil)
		assert.NotEmpty(t, errFile.Error())
		errURL := NewDuplicateURLError(nil)
		assert.NotEmpty(t, errURL.Error())
		var doc *TemporaryDocument
		assert.Nil(t, doc.ViewableImages())
	})
}
