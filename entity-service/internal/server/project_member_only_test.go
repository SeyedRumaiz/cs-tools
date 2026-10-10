package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/apierror"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/service"
)

func TestProjectMemberOnly(t *testing.T) {
	const mine = "11111111-1111-1111-1111-111111111111"
	const other = "22222222-2222-2222-2222-222222222222"
	cases := []struct {
		name      string
		access    fakeAccess
		projectID string
		want      int
		called    bool
	}{
		{"internal caller passes for any project", fakeAccess{scope: service.AccessScope{Unrestricted: true}}, other, http.StatusOK, true},
		{"member passes for own project", fakeAccess{scope: service.AccessScope{ViewerEmail: "c@example.com", ProjectIDs: []string{mine}}}, mine, http.StatusOK, true},
		{"member id match ignores case", fakeAccess{scope: service.AccessScope{ViewerEmail: "c@example.com", ProjectIDs: []string{mine}}}, "11111111-1111-1111-1111-111111111111", http.StatusOK, true},
		{"customer is refused for another project with 404", fakeAccess{scope: service.AccessScope{ViewerEmail: "c@example.com", ProjectIDs: []string{mine}}}, other, http.StatusNotFound, false},
		{"customer with no projects is refused", fakeAccess{scope: service.AccessScope{ViewerEmail: "c@example.com"}}, mine, http.StatusNotFound, false},
		{"zero scope fails closed", fakeAccess{}, mine, http.StatusNotFound, false},
		{"malformed id is 400", fakeAccess{scope: service.AccessScope{Unrestricted: true}}, "not-a-uuid", http.StatusBadRequest, false},
		{"unauthenticated is 401", fakeAccess{err: &apierror.UnauthorizedError{Msg: "no token"}}, mine, http.StatusUnauthorized, false},
		{"unverified identity is 503", fakeAccess{err: &apierror.ServiceUnavailableError{Msg: "unverified"}}, mine, http.StatusServiceUnavailable, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			h := projectMemberOnly(tc.access, func(w http.ResponseWriter, _ *http.Request) { called = true })
			req := httptest.NewRequest(http.MethodPost, "/projects/x/contacts/search", nil)
			req.SetPathValue("id", tc.projectID)
			rec := httptest.NewRecorder()
			h(rec, req)
			if called != tc.called {
				t.Fatalf("handler called = %v, want %v", called, tc.called)
			}
			if !tc.called && rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
		})
	}
}
