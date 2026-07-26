<script setup>
import Button from "@/components/ui/Button.vue";
import Card from "@/components/ui/Card.vue";
import Input from "@/components/ui/Input.vue";
import { showModal } from "@/composables/useModal";
import {
  appState,
  createEmptyProvider,
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
const search = ref({});
const syncing = ref({});
const connectivity = ref({});
const modelTests = ref({});

const enabledCount = computed(() => drafts.value.reduce(
  (total, provider) => total + provider.models.filter((model) => model.enabled).length,
  0,
));

function providerKey(provider, index) {
  return provider.id || `draft-${index}`;
}

function modelKey(provider, model, providerIndex, modelIndex) {
  return `${providerKey(provider, providerIndex)}/${model.id || model.modelID || modelIndex}`;
}

function visibleModels(provider, index) {
  const term = String(search.value[providerKey(provider, index)] || "").trim().toLowerCase();
  if (!term) return provider.models;
  return provider.models.filter((model) =>
    `${model.modelID} ${model.displayName}`.toLowerCase().includes(term),
  );
}

function providerUsage(provider) {
  return appState.homeMetrics.byProvider?.[provider.id] || {
    providerCalls: 0,
    totalTokens: 0,
  };
}

function providerSummary(provider) {
  const enabled = provider.models.filter((model) => model.enabled).length;
  const available = provider.models.filter((model) => model.available).length;
  const usage = providerUsage(provider);
  return `${enabled} 个已启用 · ${available} 个可用 · ${formatCompactInteger(usage.totalTokens)} Tokens · ${formatCompactInteger(usage.providerCalls)} 次调用`;
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
    drafts.value.splice(index, 1, result.provider);
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

function setVisibleEnabled(provider, index, enabled) {
  const visible = new Set(visibleModels(provider, index));
  provider.models.forEach((model) => {
    if (visible.has(model)) model.enabled = enabled;
  });
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
            <div class="text-xs" :class="connectionClass(provider, providerIndex)">{{ connectionText(provider, providerIndex) }}</div>
            <div class="flex items-center gap-2">
              <Button variant="default" :disabled="syncing[providerKey(provider, providerIndex)]" @click="testConnectivity(provider, providerIndex)">测试连通性</Button>
              <Button variant="default" :disabled="syncing[providerKey(provider, providerIndex)]" @click="fetchModels(provider, providerIndex)">
                {{ syncing[providerKey(provider, providerIndex)] ? "获取中..." : "获取模型" }}
              </Button>
            </div>
          </div>

          <div class="rounded-[8px] border border-[#343434] bg-[#232323]">
            <div class="flex flex-wrap items-center justify-between gap-2 border-b border-[#343434] p-3">
              <input v-model="search[providerKey(provider, providerIndex)]" class="h-8 min-w-[220px] flex-1 rounded-[6px] border border-[#3f3f3f] bg-[#1f1f1f] px-3 text-sm outline-none focus:border-[#10AD5D]" placeholder="搜索模型" />
              <div class="flex gap-2">
                <Button variant="text" @click="setVisibleEnabled(provider, providerIndex, true)">全选</Button>
                <Button variant="text" @click="setVisibleEnabled(provider, providerIndex, false)">清空</Button>
              </div>
            </div>

            <div v-if="provider.models.length === 0" class="p-5 text-center text-sm text-[#777]">点击“获取模型”，或保存后重新编辑。</div>
            <div v-else class="max-h-[320px] divide-y divide-[#343434] overflow-y-auto">
              <div v-for="(model, modelIndex) in visibleModels(provider, providerIndex)" :key="model.id || model.modelID" class="flex items-center gap-3 px-3 py-2.5">
                <input v-model="model.enabled" type="checkbox" class="size-4 shrink-0 accent-[#10AD5D]" />
                <div class="min-w-0 flex-1">
                  <div class="flex items-center gap-2">
                    <span class="truncate text-sm text-[#e5e5e5]">{{ model.displayName || model.modelID }}</span>
                    <span v-if="!model.available" class="rounded bg-[#4a3215] px-1.5 py-0.5 text-[10px] text-[#fbbf24]">本次未发现</span>
                  </div>
                  <div class="truncate text-xs text-[#777]">{{ model.modelID }}</div>
                </div>
                <div class="max-w-[260px] truncate text-xs" :class="modelTests[modelKey(provider, model, providerIndex, modelIndex)]?.status === 'error' ? 'text-[#f87171]' : 'text-[#8f8f8f]'">
                  {{ modelTests[modelKey(provider, model, providerIndex, modelIndex)]?.summaryText || "未测试" }}
                </div>
                <Button variant="text" :disabled="modelTests[modelKey(provider, model, providerIndex, modelIndex)]?.status === 'running'" @click="testModel(provider, model, providerIndex, modelIndex)">测试模型</Button>
              </div>
            </div>
          </div>
        </div>
      </Card>
    </div>
  </div>
</template>