package client

import (
	"reflect"
	"testing"
)

func TestDecodeProviderModelsSupportsCommonShapes(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []string
	}{
		{name: "openai data", body: `{"data":[{"id":"model-b"},{"id":"model-a"}]}`, want: []string{"model-a", "model-b"}},
		{name: "models mixed", body: `{"models":["model-c",{"name":"model-a"},{"model":"model-b"}]}`, want: []string{"model-a", "model-b", "model-c"}},
		{name: "direct array dedupe", body: `["Model-A",{"modelID":"model-a"},"model-b"]`, want: []string{"Model-A", "model-b"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := decodeProviderModels([]byte(test.body))
			if err != nil {
				t.Fatalf("decode models: %v", err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("expected %#v, got %#v", test.want, got)
			}
		})
	}
}

func TestDecodeProviderModelsRejectsInvalidOrEmptyPayload(t *testing.T) {
	for _, body := range []string{`not-json`, `{}`, `{"data":[]}`, `{"data":[{}]}`} {
		if _, err := decodeProviderModels([]byte(body)); err == nil {
			t.Fatalf("expected payload %q to fail", body)
		}
	}
}
