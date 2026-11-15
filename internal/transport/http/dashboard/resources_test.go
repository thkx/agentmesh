package dashboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	appcredential "github.com/thkx/agentmesh/internal/credential"
	domaincredential "github.com/thkx/agentmesh/internal/domain/credential"
	domainprovider "github.com/thkx/agentmesh/internal/domain/provider"
)

type fakeCredentials struct{}
func (fakeCredentials) Set(context.Context, domainprovider.ID, domaincredential.Secret) error { return nil }
func (fakeCredentials) Delete(context.Context, domainprovider.ID) error { return nil }
func (fakeCredentials) Status(_ context.Context, id domainprovider.ID) (domaincredential.Record, error) {
	return domaincredential.Record{ProviderID:id, Source:domaincredential.SourceStored, Status:domaincredential.StatusConfigured}, nil
}
var _ appcredential.Manager = fakeCredentials{}

func TestDashboardResourcePagesUseExistingRegistries(t *testing.T) {
	providers := domainprovider.NewProviderRegistry()
	if err := providers.Register(domainprovider.Provider{ID:"test-provider", Name:"Test Provider", Type:domainprovider.TypeOpenAI, Kind:domainprovider.KindCloud, Enabled:true}); err != nil { t.Fatal(err) }
	models := domainprovider.NewModelRegistry()
	if err := models.Register(domainprovider.Model{ID:"test-model", ProviderID:"test-provider", Name:"model-v1", Enabled:true, SupportsStreaming:true}); err != nil { t.Fatal(err) }

	r := gin.New()
	New(Dependencies{Providers:providers, Models:models, Credentials:fakeCredentials{}}).RegisterRoutes(r)
	for _, tc := range []struct{path, want string}{
		{"/dashboard/providers", "Test Provider"},
		{"/dashboard/models", "test-model"},
	} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), tc.want) {
			t.Fatalf("%s: status=%d body=%s", tc.path, rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "secret-value") { t.Fatalf("%s leaked a credential", tc.path) }
	}
}
