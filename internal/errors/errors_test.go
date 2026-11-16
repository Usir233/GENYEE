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
}
