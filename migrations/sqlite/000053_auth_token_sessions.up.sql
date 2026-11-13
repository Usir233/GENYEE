-- Access and refresh rotations share one revocable login session.
ALTER TABLE auth_tokens ADD COLUMN session_id VARCHAR(36) NOT NULL DEFAULT '';
CREATE INDEX idx_auth_tokens_session_id ON auth_tokens(session_id);

-- Existing independent tokens cannot be paired safely. Require a new login.
UPDATE auth_tokens SET is_revoked = TRUE WHERE session_id = '';
