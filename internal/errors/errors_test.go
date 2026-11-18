package errors

import (
	"fmt"
	"net/http"
	"testing"
)

func TestIsAppErrorUnwrapsWrappedAndRejectsTypedNil(t *testing.T) {
	base := NewNotFoundError("document missing")
	wrapped := fmt.Errorf("service layer: %w", base)

	got, ok := IsAppError(wrapped)
	if !ok || got == nil {
		t.Fatalf("IsAppError(wrapped) = (%v, %v), want (%v, true)", got, ok, base)
	}
	if got.HTTPCode != http.StatusNotFound || got.Code != ErrNotFound {
		t.Fatalf("unexpected unwrapped AppError: %+v", got)
	}

	var typedNil *AppError
	var errIface error = typedNil
	if gotNil, okNil := IsAppError(errIface); okNil || gotNil != nil {
		t.Fatalf("IsAppError(typedNil) = (%v, %v), want (nil, false)", gotNil, okNil)
	}
	if s := typedNil.Error(); s != "" {
		t.Fatalf("(*AppError)(nil).Error() = %q, want empty string", s)
	}
	if gotDetails := typedNil.WithDetails("ignored"); gotNilDetails(gotDetails) {
		t.Fatalf("(*AppError)(nil).WithDetails() = %+v, want nil", gotDetails)
	}
}

func gotNilDetails(e *AppError) bool {
	return e != nil
}

func TestTypedErrorConstructorsForTenantEntitySizeAndWikiConflict(t *testing.T) {
	tenantErr := NewTenantRequiredError("")
	if tenantErr.Code != ErrTenantRequired ||
		tenantErr.HTTPCode != http.StatusConflict ||
		tenantErr.Message != "Workspace required" {
		t.Fatalf("unexpected NewTenantRequiredError: %+v", tenantErr)
	}

	largeErr := NewRequestEntityTooLargeError("")
	if largeErr.Code != ErrRequestEntityTooLarge ||
		largeErr.HTTPCode != http.StatusRequestEntityTooLarge ||
		largeErr.Message == "" {
		t.Fatalf("unexpected NewRequestEntityTooLargeError: %+v", largeErr)
	}

	wikiErr := NewWikiVersionConflictError("", 7)
	if wikiErr.Code != ErrWikiVersionConflict || wikiErr.HTTPCode != http.StatusConflict {
		t.Fatalf("unexpected NewWikiVersionConflictError: %+v", wikiErr)
	}
	details, ok := wikiErr.Details.(map[string]int)
	if !ok || details["current_version"] != 7 {
		t.Fatalf("expected current_version=7 details, got %#v", wikiErr.Details)
	}
}
