<script setup>
import Button from "@/components/ui/Button.vue";
import Card from "@/components/ui/Card.vue";
import Input from "@/components/ui/Input.vue";
import Select from "@/components/ui/Select.vue";
import Tooltip from "@/components/ui/Tooltip.vue";
import { showModal } from "@/composables/useModal";
import {
  appState,
  buildProviderModelDisplayName,
  createEmptyProvider,
  createEmptyProviderModel,
  discoverProviderModels,
  normalizeProviders,
  reloadUserConfig,
  runProviderConnectivityTest,
  runProviderModelTest,
  saveProviders,
  syncHomeMetrics,
  toUserError,
} from "@/state/appState";
import { formatCompactInteger } from "@/utils/numberFormat";
import { computed, onMounted, ref } from "vue";

const drafts = ref([]);
const loading = ref(true);
const saving = ref(false);
const fetchedModels = ref({});
const customModelIDs = ref({});
const syncing = ref({});
const connectivity = ref({});
const modelTests = ref({});
const reasoningEffortOptions = [
  { label: "低", value: "low" },
  { label: "中", value: "medium" },
  { label: "高", value: "high" },
  { label: "极高", value: "xhigh" },
  { label: "最高", value: "max" },
];

const anthropicThinkingEffortOptions = [
  { label: "低", value: "low" },
  { label: "中", value: "medium" },
  { label: "高", value: "high" },
  { label: "极高", value: "xhigh" },
  { label: "最高", value: "max" },
];

const enabledCount = computed(() => drafts.value.filter((provider) => provider.models.length > 0).length);

function providerKey(provider, index) {
  return provider.id || `draft-${index}`;
}

function selectedModel(provider) {
  return provider.models[0] || null;
}

function modelKey(provider, model, providerIndex) {
  return `${providerKey(provider, providerIndex)}/${model?.id || model?.modelID || "selected"}`;
}

function discoveredModels(provider, index) {
  const key = providerKey(provider, index);
  const selected = selectedModel(provider)?.modelID;
  return [...new Set([...(provider.discoveredModels || []), ...(fetchedModels.value[key] || []), selected].filter(Boolean))];
}

function providerUsage(provider) {
  return appState.homeMetrics.byProvider?.[provider.id] || {
    providerCalls: 0,
    totalTokens: 0,
  };
}

function providerUsageSummary(provider) {
  const usage = providerUsage(provider);
  return `累计调用 ${formatCompactInteger(usage.providerCalls)} 次 · 累计 ${formatCompactInteger(usage.totalTokens)} Tokens`;
}

function providerSummary(provider) {
  const model = selectedModel(provider);
  const selection = model ? `已选择 ${model.modelID}` : "未选择模型";
  return `${selection} · ${providerUsageSummary(provider)}`;
}

function contextWindowInput(model) {
  return model?.contextWindowTokens || "";
}

function setContextWindow(model, value) {
  const text = String(value || "").trim();
  model.contextWindowTokens = /^\d+$/.test(text) && Number(text) > 0 ? Number(text) : 0;
}

function addProvider() {
  drafts.value.push(createEmptyProvider());
}

async function removeProvider(index) {
  const provider = drafts.value[index];
  const enabled = provider?.models?.filter((model) => model.enabled).length || 0;
  const confirmed = await showModal({
    title: "删除中转站",
    content: `删除“${provider?.name || "未命名中转站"}”将停用 ${enabled} 个模型。历史用量不会删除。`,
    confirmText: "删除",
    showCancel: true,
  });
  if (confirmed === false) return;
  drafts.value.splice(index, 1);
}

async function showError(title, error) {
  await showModal({ title, content: toUserError(error) });
}

async function fetchModels(provider, index) {
  const key = providerKey(provider, index);
  syncing.value[key] = true;
  try {
    const result = await discoverProviderModels(provider);
    fetchedModels.value[key] = result.models || [];
    if (result.provider) {
      Object.assign(provider, result.provider);
      const saved = await saveProviders(drafts.value);
      if (!saved.ok) {
        await showError("保存获取到的模型失败", saved.error);
      }
    }
    connectivity.value[key] = {
      reachable: true,
      statusCode: result.statusCode,
      modelCount: result.models.length,
      durationMS: result.durationMS,
      error: "",
    };
  } catch (error) {
    await showError("获取模型失败", error);
  } finally {
    syncing.value[key] = false;
  }
}

async function testConnectivity(provider, index) {
  const key = providerKey(provider, index);
  connectivity.value[key] = { testing: true };
  try {
    connectivity.value[key] = await runProviderConnectivityTest(provider);
  } catch (error) {
    connectivity.value[key] = { reachable: false, error: toUserError(error) };
  }
}

async function testModel(provider, model, providerIndex, modelIndex) {
  const key = modelKey(provider, model, providerIndex, modelIndex);
  modelTests.value[key] = { status: "running", summaryText: "测试中..." };
  try {
    modelTests.value[key] = await runProviderModelTest(provider, model);
  } catch (error) {
    modelTests.value[key] = { status: "error", summaryText: toUserError(error) };
  }
}

function selectModel(provider, modelID) {
  if (!modelID) {
    provider.models = [];
    return;
  }
  const current = selectedModel(provider);
  if (current?.modelID === modelID) return;
  const next = createEmptyProviderModel(modelID);
  next.enabled = true;
  provider.models = [next];
}

function addCustomModel(provider, index) {
  const key = providerKey(provider, index);
  const modelID = String(customModelIDs.value[key] || "").trim();
  if (!modelID) return;
  selectModel(provider, modelID);
  customModelIDs.value[key] = "";
}

async function removeSelectedModel(provider, index) {
  const model = selectedModel(provider);
  if (!model) return;
  const confirmed = await showModal({
    title: "删除模型",
    content: `删除“${model.modelID}”后，该中转站不会向 Cursor 提供任何模型。`,
    confirmText: "删除",
    showCancel: true,
  });
  if (confirmed === false) return;
  provider.models = [];
  delete modelTests.value[modelKey(provider, model, index)];
}

function connectionText(provider, index) {
  const state = connectivity.value[providerKey(provider, index)];
  if (!state) return "未测试";
  if (state.testing) return "测试中...";
  if (!state.reachable) return state.error || "不可访问";
  return `可访问 · HTTP ${state.statusCode} · ${state.modelCount} 个模型 · ${state.durationMS} ms`;
}

function connectionClass(provider, index) {
  const state = connectivity.value[providerKey(provider, index)];
  if (!state || state.testing) return "text-[#a3a3a3]";
  return state.reachable ? "text-[#4ade80]" : "text-[#f87171]";
}

async function applyChanges() {
  saving.value = true;
  try {
    const result = await saveProviders(drafts.value);
    if (!result.ok) {
      await showError("保存失败", result.error);
      return;
    }
    drafts.value = normalizeProviders(appState.providers);
  } catch (error) {
    await showError("保存失败", error);
  } finally {
    saving.value = false;
  }
}

onMounted(async () => {
  try {
    const config = await reloadUserConfig();
    drafts.value = normalizeProviders(config.providers);
    await syncHomeMetrics().catch(() => {});
  } catch (error) {
    await showError("加载失败", error);
  } finally {
    loading.value = false;
  }
});
</script>

<template>
  <div class="flex h-full min-h-0 flex-col overflow-hidden px-4 pb-4 text-[#e5e5e5]">
    <div class="flex shrink-0 items-center justify-between gap-4 pb-4">
      <div>
        <div class="text-base font-medium text-white">模型中转站</div>
        <div class="mt-1 text-xs text-[#8f8f8f]">{{ drafts.length }} 个中转站 · {{ enabledCount }} 个模型将显示在 Cursor</div>
      </div>
      <div class="flex items-center gap-2">
        <Button variant="default" :disabled="saving" @click="addProvider">新增中转站</Button>
        <Button variant="primary" :disabled="saving || loading" @click="applyChanges">
          {{ saving ? "应用中..." : "应用更改" }}
        </Button>
      </div>
    </div>

    <div v-if="loading" class="flex flex-1 items-center justify-center text-sm text-[#a3a3a3]">加载中...</div>
    <div v-else-if="drafts.length === 0" class="flex flex-1 items-center justify-center rounded-[8px] border border-dashed border-[#3a3a3a] text-sm text-[#a3a3a3]">
      尚未配置中转站。新增后可自动获取模型列表。
    </div>

    <div v-else class="min-h-0 flex-1 space-y-3 overflow-y-auto pr-1">
      <Card v-for="(provider, providerIndex) in drafts" :key="providerKey(provider, providerIndex)">
        <div class="space-y-4">
          <div class="flex items-start justify-between gap-3">
            <div class="min-w-0 flex-1">
              <div class="truncate text-sm font-medium text-white">{{ provider.name || "未命名中转站" }}</div>
              <div class="mt-1 truncate text-xs text-[#8f8f8f]">{{ providerSummary(provider) }}</div>
            </div>
            <Button variant="text" :disabled="saving" @click="removeProvider(providerIndex)">删除</Button>
          </div>

          <div class="grid grid-cols-1 gap-3 md:grid-cols-2">
              <label class="space-y-1 text-xs text-[#a3a3a3]">
                <span>中转站名称</span>
                <input v-model="provider.name" class="h-9 w-full rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-3 text-sm text-white outline-none focus:border-[#10AD5D]" placeholder="例如：主中转站" />
              </label>
              <label class="space-y-1 text-xs text-[#a3a3a3]">
                <span>协议</span>
                <select v-model="provider.type" class="h-9 w-full rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-3 text-sm text-white outline-none focus:border-[#10AD5D]">
                  <option value="openai">OpenAI</option>
                  <option value="anthropic">Anthropic</option>
                </select>
              </label>
              <label class="space-y-1 text-xs text-[#a3a3a3]">
                <span>接口地址</span>
                <input v-model="provider.baseURL" class="h-9 w-full rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-3 text-sm text-white outline-none focus:border-[#10AD5D]" placeholder="https://example.com/v1" />
              </label>
              <label class="space-y-1 text-xs text-[#a3a3a3]">
                <span>访问密钥</span>
                <Input v-model="provider.apiKey" type="password" allow-visibility-toggle autocomplete="off" placeholder="sk-..." />
              </label>
              <label class="space-y-1 text-xs text-[#a3a3a3] md:col-span-2">
                <span>模型发现路径</span>
                <input v-model="provider.discoveryPath" class="h-9 w-full rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-3 text-sm text-white outline-none focus:border-[#10AD5D]" placeholder="/models" />
              </label>
          </div>

          <div class="flex flex-wrap items-center justify-between gap-2 border-t border-[#343434] pt-3">
            <div class="flex min-w-0 items-center gap-2 text-xs" :class="connectionClass(provider, providerIndex)">
              <Tooltip
                v-if="connectivity[providerKey(provider, providerIndex)]?.error"
                :content="connectivity[providerKey(provider, providerIndex)].error"
                copyable
              />
              <span class="truncate">{{ connectionText(provider, providerIndex) }}</span>
            </div>
            <div class="flex items-center gap-2">
              <Button variant="default" :disabled="syncing[providerKey(provider, providerIndex)]" @click="testConnectivity(provider, providerIndex)">测试连通性</Button>
              <Button variant="default" :disabled="syncing[providerKey(provider, providerIndex)]" @click="fetchModels(provider, providerIndex)">
                {{ syncing[providerKey(provider, providerIndex)] ? "获取中..." : "获取模型" }}
              </Button>
            </div>
          </div>

          <div class="space-y-3 rounded-[8px] border border-[#343434] bg-[#232323] p-3">
            <div class="text-sm font-medium text-white">Cursor 模型</div>
            <div class="grid grid-cols-1 gap-3 md:grid-cols-[minmax(0,1fr)_auto]">
              <label class="space-y-1 text-xs text-[#a3a3a3]">
                <span>从已获取模型中选择</span>
                <select
                  :value="selectedModel(provider)?.modelID || ''"
                  class="h-9 w-full rounded-[6px] border border-[#3f3f3f] bg-[#1f1f1f] px-3 text-sm text-white outline-none focus:border-[#10AD5D]"
                  @change="selectModel(provider, $event.target.value)"
                >
                  <option value="">暂不提供模型</option>
                  <option v-for="modelID in discoveredModels(provider, providerIndex)" :key="modelID" :value="modelID">{{ modelID }}</option>
                </select>
              </label>
              <Button variant="text" :disabled="!selectedModel(provider)" @click="removeSelectedModel(provider, providerIndex)">删除模型</Button>
            </div>

            <div class="flex flex-wrap gap-2">
              <input
                v-model="customModelIDs[providerKey(provider, providerIndex)]"
                class="h-9 min-w-[220px] flex-1 rounded-[6px] border border-[#3f3f3f] bg-[#1f1f1f] px-3 text-sm text-white outline-none focus:border-[#10AD5D]"
                placeholder="手动输入专属模型 ID，例如 gpt-5.5"
                @keyup.enter="addCustomModel(provider, providerIndex)"
              />
              <Button variant="default" @click="addCustomModel(provider, providerIndex)">添加专属模型</Button>
            </div>

            <div v-if="selectedModel(provider)" class="space-y-3 border-t border-[#343434] pt-3">
              <div class="grid grid-cols-1 gap-3 md:grid-cols-2">
                <label class="space-y-1 text-xs text-[#a3a3a3]">
                  <span>上下文窗口 Token</span>
                  <input
                    :value="contextWindowInput(selectedModel(provider))"
                    type="text"
                    inputmode="numeric"
                    placeholder="例如：200000（留空使用默认值）"
                    class="h-9 w-full rounded-[6px] border border-[#3f3f3f] bg-[#1f1f1f] px-3 text-sm text-white outline-none focus:border-[#10AD5D]"
                    @input="setContextWindow(selectedModel(provider), $event.target.value)"
                  />
                </label>
                <label class="space-y-1 text-xs text-[#a3a3a3]">
                  <span>{{ provider.type === "anthropic" ? "思考等级" : "推理强度" }}</span>
                  <Select
                    v-if="provider.type === 'openai'"
                    v-model="selectedModel(provider).reasoningEffort"
                    :options="reasoningEffortOptions"
                  />
                  <Select
                    v-else
                    v-model="selectedModel(provider).anthropicThinkingEffort"
                    :options="anthropicThinkingEffortOptions"
                  />
                </label>
                <div class="space-y-1 text-xs text-[#a3a3a3]">
                  <div>Cursor 悬停备注</div>
                  <div class="flex h-9 items-center rounded-[6px] border border-[#343434] bg-[#1f1f1f] px-3 text-sm text-[#d4d4d4]">
                    {{ providerUsageSummary(provider) }}
                  </div>
                </div>

                <div class="min-w-0">
                  <div class="text-xs text-[#a3a3a3]">Cursor 显示名称</div>
                  <div class="mt-1 truncate text-sm text-white">{{ buildProviderModelDisplayName(provider, selectedModel(provider)) }}</div>
                </div>
                <div class="flex items-center gap-3">
                  <div class="flex items-center gap-2">
                    <Tooltip
                      v-if="modelTests[modelKey(provider, selectedModel(provider), providerIndex)]?.status === 'error'"
                      :content="modelTests[modelKey(provider, selectedModel(provider), providerIndex)]?.summaryText || '模型测试失败'"
                      copyable
                    />
                    <div class="max-w-[260px] truncate text-xs" :class="modelTests[modelKey(provider, selectedModel(provider), providerIndex)]?.status === 'error' ? 'text-[#f87171]' : 'text-[#8f8f8f]'">
                      {{ modelTests[modelKey(provider, selectedModel(provider), providerIndex)]?.summaryText || "未测试" }}
                    </div>
                  </div>
                  <Button variant="text" :disabled="modelTests[modelKey(provider, selectedModel(provider), providerIndex)]?.status === 'running'" @click="testModel(provider, selectedModel(provider), providerIndex, 0)">测试模型</Button>
                </div>
              </div>
            </div>
            <div v-else class="text-xs text-[#777]">每个中转站只会向 Cursor 提供一个模型。</div>
          </div>
        </div>
      </Card>
    </div>
  </div>
</template>