package modelgateway

import (
	"context"
	"testing"

	"github.com/thkx/agentmesh/internal/domain/provider"
)

type dynamicGatewayTestRouter struct {
	modelKey string
	called   bool
}

func (r *dynamicGatewayTestRouter) Supports(_ context.Context, modelKey string) (bool, error) {
	r.modelKey = modelKey
	return true, nil
}

func (r *dynamicGatewayTestRouter) Generate(_ context.Context, req GenerateRequest) (*GenerateResponse, error) {
	r.called = true
	if req.ProviderModel != "glm-5.3-flash" {
		return nil, ErrNilResponse
	}
	return &GenerateResponse{Content: "ok"}, nil
}

func (r *dynamicGatewayTestRouter) ResolveModel(_ context.Context, id string) (provider.Model, bool, error) {
	if id != "model-glm-53-flash" {
		return provider.Model{}, false, nil
	}
	return provider.Model{
		ID: id, ProviderID: provider.ID("ai-resource"),
		Name: "glm-5.3-flash", DisplayName: "GLM 5.3 Flash",
		Enabled: true,
	}, true, nil
}

func TestGatewayResolvesDynamicModelWithoutLegacyProvider(t *testing.T) {
	models, err := NewStaticResolver()
	if err != nil {
		t.Fatal(err)
	}
	router := &dynamicGatewayTestRouter{}
	gateway, err := NewGateway(NewRegistry(), models)
	if err != nil {
		t.Fatal(err)
	}
	gateway.SetDynamicRouter(router)

	resp, err := gateway.Generate(context.Background(), GenerateRequest{
		ModelID: "model-glm-53-flash",
		Messages: []Message{{Role: RoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "ok" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if !router.called || router.modelKey != "glm-5.3-flash" {
		t.Fatalf("dynamic router was not called with upstream model key: %+v", router)
	}
	if resp.ModelID != "model-glm-53-flash" || resp.Model != "glm-5.3-flash" {
		t.Fatalf("response identity was not normalized: %+v", resp)
	}
}
