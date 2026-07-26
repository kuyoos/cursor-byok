package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	serverconfig "cursor/internal/backend/server/config"
	"cursor/internal/netproxy"
)

const providerDiscoveryTimeout = 20 * time.Second
const providerDiscoveryMaxBodyBytes = 4 << 20

type ProviderModelsResult struct {
	Models     []string `json:"models"`
	StatusCode int      `json:"statusCode"`
	DurationMS int64    `json:"durationMS"`
}

type ProviderConnectivityResult struct {
	Reachable  bool   `json:"reachable"`
	StatusCode int    `json:"statusCode"`
	ModelCount int    `json:"modelCount"`
	DurationMS int64  `json:"durationMS"`
	Error      string `json:"error"`
}

func (s *ProxyService) FetchProviderModels(provider serverconfig.ProviderConfig) (ProviderModelsResult, error) {
	startedAt := time.Now()
	models, statusCode, err := s.fetchProviderModels(context.Background(), provider)
	result := ProviderModelsResult{
		Models: models, StatusCode: statusCode, DurationMS: time.Since(startedAt).Milliseconds(),
	}
	if err != nil {
		return result, err
	}
	return result, nil
}

func (s *ProxyService) TestProviderConnectivity(provider serverconfig.ProviderConfig) ProviderConnectivityResult {
	startedAt := time.Now()
	models, statusCode, err := s.fetchProviderModels(context.Background(), provider)
	result := ProviderConnectivityResult{
		Reachable: err == nil, StatusCode: statusCode, ModelCount: len(models), DurationMS: time.Since(startedAt).Milliseconds(),
	}
	if err != nil {
		result.Error = strings.TrimSpace(err.Error())
	}
	return result
}

func (s *ProxyService) TestProviderModel(provider serverconfig.ProviderConfig, model serverconfig.ProviderModelConfig) (ModelAdapterTestResult, error) {
	provider.Models = []serverconfig.ProviderModelConfig{model}
	provider.Models[0].Enabled = true
	normalized, err := serverconfig.NormalizeProviderConfigs([]serverconfig.ProviderConfig{provider})
	if err != nil {
		return ModelAdapterTestResult{}, err
	}
	adapters, err := serverconfig.ProjectEnabledModelAdapters(normalized)
	if err != nil {
		return ModelAdapterTestResult{}, err
	}
	if len(adapters) != 1 {
		return ModelAdapterTestResult{}, errors.New("模型配置无法生成测试渠道")
	}
	return s.TestModelAdapter(adapters[0])
}

func (s *ProxyService) fetchProviderModels(parent context.Context, provider serverconfig.ProviderConfig) ([]string, int, error) {
	probe := provider
	probe.Models = nil
	probe.Name = strings.TrimSpace(probe.Name)
	if probe.Name == "" {
		probe.Name = "临时中转站"
	}
	// 添加一个禁用占位模型，以复用中转站字段的统一规范化与校验。
	probe.Models = []serverconfig.ProviderModelConfig{{ModelID: "__discovery_probe__", DisplayName: "probe", Enabled: false, Available: true}}
	normalized, err := serverconfig.NormalizeProviderConfigs([]serverconfig.ProviderConfig{probe})
	if err != nil {
		return nil, 0, err
	}
	probe = normalized[0]

	ctx, cancel := context.WithTimeout(parent, providerDiscoveryTimeout)
	defer cancel()
	requestURL := strings.TrimRight(probe.BaseURL, "/") + "/" + strings.TrimLeft(probe.DiscoveryPath, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("创建模型发现请求失败: %w", err)
	}
	applyProviderDiscoveryHeaders(req.Header, probe)
	client := s.publicClient
	if client == nil {
		client = netproxy.NewHTTPClient(providerDiscoveryTimeout)
	}
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, 0, errors.New("获取模型超时")
		}
		return nil, 0, fmt.Errorf("中转站连接失败: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, providerDiscoveryMaxBodyBytes))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("读取模型列表失败: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		message := strings.TrimSpace(string(body))
		if len(message) > 1000 {
			message = message[:1000]
		}
		return nil, resp.StatusCode, fmt.Errorf("获取模型失败: HTTP %d %s", resp.StatusCode, message)
	}
	models, err := decodeProviderModels(body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return models, resp.StatusCode, nil
}

func applyProviderDiscoveryHeaders(header http.Header, provider serverconfig.ProviderConfig) {
	header.Set("Accept", "application/json")
	header.Set("Authorization", "Bearer "+provider.APIKey)
	if provider.Type == "anthropic" {
		header.Set("x-api-key", provider.APIKey)
		header.Set("anthropic-version", "2023-06-01")
	}
	if !provider.CustomHeadersEnabled {
		return
	}
	var custom map[string]string
	if json.Unmarshal([]byte(provider.CustomHeadersJSON), &custom) != nil {
		return
	}
	for key, value := range custom {
		header.Set(key, value)
	}
}

func decodeProviderModels(body []byte) ([]string, error) {
	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, errors.New("模型发现接口返回了无效 JSON")
	}
	items := payload
	if object, ok := payload.(map[string]any); ok {
		if data, exists := object["data"]; exists {
			items = data
		} else if models, exists := object["models"]; exists {
			items = models
		}
	}
	array, ok := items.([]any)
	if !ok {
		return nil, errors.New("模型发现接口响应中没有模型数组")
	}
	models := make([]string, 0, len(array))
	seen := make(map[string]struct{}, len(array))
	for _, item := range array {
		modelID := ""
		switch value := item.(type) {
		case string:
			modelID = strings.TrimSpace(value)
		case map[string]any:
			for _, key := range []string{"id", "name", "model", "modelID"} {
				if text, ok := value[key].(string); ok && strings.TrimSpace(text) != "" {
					modelID = strings.TrimSpace(text)
					break
				}
			}
		}
		key := strings.ToLower(modelID)
		if modelID == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		models = append(models, modelID)
	}
	if len(models) == 0 {
		return nil, errors.New("模型发现接口未返回任何有效模型")
	}
	sort.SliceStable(models, func(i, j int) bool { return strings.ToLower(models[i]) < strings.ToLower(models[j]) })
	return models, nil
}
