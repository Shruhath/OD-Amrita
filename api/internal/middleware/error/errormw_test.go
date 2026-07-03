// errormw_test.go — red-phase ATDD for Story 1.5a DoD 5: the gin error-
// middleware (the deferred 1.4 continuation, landing with the first error-
// returning endpoint). errormw is the SINGLE place the {code, message, details}
// envelope is rendered — handlers/middleware only c.Error(DomainError). It must
// render the GENERATED contracts.Error, map the code -> HTTP status via
// domain.HTTPStatus, and NEVER leak a raw internal error.
//
// RED: errormw.Handler does not exist yet -> compile-red until Task 6.
package errormw

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	contracts "github.com/Shruhath/OD-Amrita/contracts/generated/go"

	"github.com/Shruhath/OD-Amrita/api/internal/domain"
)

// serve installs Handler() as the outermost middleware, then a route that
// pushes `pushed` via c.Error and aborts — mirroring how real handlers signal
// errors. It returns the recorded response the middleware rendered.
func serve(t *testing.T, pushed error) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(Handler())
	r.GET("/x", func(c *gin.Context) {
		_ = c.Error(pushed)
		c.Abort()
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	return w
}

// TestErrorMW_RendersDomainError — a DomainError renders as the generated Error
// envelope at the code's mapped status (AUTH_DOMAIN_REJECTED -> 403), with the
// code and message preserved.
func TestErrorMW_RendersDomainError(t *testing.T) {
	const msg = "Only @amrita.edu accounts can access this system."
	w := serve(t, domain.DomainError{Code: contracts.ErrorCodeAUTHDOMAINREJECTED, Message: msg})

	if w.Code != http.StatusForbidden {
		t.Errorf("[P0] AUTH_DOMAIN_REJECTED must render at 403 (domain.HTTPStatus), got %d", w.Code)
	}
	var body contracts.Error
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("[P0] body must be the generated Error envelope: %v (%s)", err, w.Body.String())
	}
	if body.Code != contracts.ErrorCodeAUTHDOMAINREJECTED {
		t.Errorf("[P0] envelope code: want AUTH_DOMAIN_REJECTED, got %q", string(body.Code))
	}
	if body.Message != msg {
		t.Errorf("[P0] envelope message not preserved, got %q", body.Message)
	}
}

// TestErrorMW_UnknownError500 — a non-DomainError (e.g. a raw driver error) maps
// to a generic 500 INTERNAL envelope and the raw cause NEVER reaches the body.
func TestErrorMW_UnknownError500(t *testing.T) {
	const secret = "pq: password authentication failed for user od_runtime"
	w := serve(t, errors.New(secret))

	if w.Code != http.StatusInternalServerError {
		t.Errorf("[P0] a non-DomainError must render 500, got %d", w.Code)
	}
	var body contracts.Error
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("[P0] the 500 body must still be the Error envelope: %v", err)
	}
	if body.Code != contracts.ErrorCodeINTERNAL {
		t.Errorf("[P0] unknown error must map to INTERNAL, got %q", string(body.Code))
	}
	if strings.Contains(w.Body.String(), "password authentication failed") {
		t.Error("[P0] raw internal error leaked into the client response body")
	}
}
