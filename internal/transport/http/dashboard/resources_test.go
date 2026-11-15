package dashboard

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	appcredential "github.com/thkx/agentmesh/internal/credential"
	domaincredential "github.com/thkx/agentmesh/internal/domain/credential"
	domainprovider "github.com/thkx/agentmesh/internal/domain/provider"
)

type fakeCredentials struct{}

func (fakeCredentials) Set(context.Context, domainprovider.ID, domaincredential.Secret) error {
	return nil
}
func (fakeCredentials) Delete(context.Context, domainprovider.ID) error { return nil }
func (fakeCredentials) Status(_ context.Context, id domainprovider.ID) (domaincredential.Record, error) {
	return domaincredential.Record{ProviderID: id, Source: domaincredential.SourceStored, Status: domaincredential.StatusConfigured}, nil
}

var _ appcredential.Manager = fakeCredentials{}

func TestDashboardResourcePagesUseExistingRegistries(t *testing.T) {
	providers := domainprovider.NewProviderRegistry()
	if err := providers.Register(domainprovider.Provider{ID: "test-provider", Name: "Test Provider", Type: domainprovider.TypeOpenAI, Kind: domainprovider.KindCloud, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	models := domainprovider.NewModelRegistry()
	if err := models.Register(domainprovider.Model{ID: "test-model", ProviderID: "test-provider", Name: "model-v1", Enabled: true, SupportsStreaming: true}); err != nil {
		t.Fatal(err)
	}

	r := gin.New()
	New(Dependencies{Providers: providers, Models: models, Credentials: fakeCredentials{}}).RegisterRoutes(r)
	for _, tc := range []struct{ path, want string }{
		{"/dashboard/providers", "Test Provider"},
		{"/dashboard/models", "test-model"},
	} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), tc.want) {
			t.Fatalf("%s: status=%d body=%s", tc.path, rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "secret-value") {
			t.Fatalf("%s leaked a credential", tc.path)
		}
	}
}

type failingCredentials struct{}
func (failingCredentials) Set(context.Context, domainprovider.ID, domaincredential.Secret) error { return nil }
func (failingCredentials) Delete(context.Context, domainprovider.ID) error { return nil }
func (failingCredentials) Status(context.Context, domainprovider.ID) (domaincredential.Record, error) {
	return domaincredential.Record{}, errors.New("secret-value must never be rendered")
}

func TestDashboardEscapesProviderNamesAndRedactsCredentialErrors(t *testing.T) {
	providers := domainprovider.NewProviderRegistry()
	name := "<script>alert(1)</script>"
	if err := providers.Register(domainprovider.Provider{ID:"safe-provider", Name:name, Type:domainprovider.TypeOpenAI, Kind:domainprovider.KindCloud, Enabled:true}); err != nil { t.Fatal(err) }
	r := gin.New()
	New(Dependencies{Providers:providers, Credentials:failingCredentials{}}).RegisterRoutes(r)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dashboard/providers", nil))
	body := rec.Body.String()
	if rec.Code != http.StatusOK { t.Fatalf("status=%d body=%s", rec.Code, body) }
	if strings.Contains(body, name) || !strings.Contains(body, "&lt;script&gt;") {
		t.Fatalf("provider name was not HTML-escaped: %s", body)
	}
	if strings.Contains(body, "secret-value") {
		t.Fatalf("credential error leaked into HTML: %s", body)
	}
}
