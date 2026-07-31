<script setup>
import {
  CategoryScale,
  Chart as ChartJS,
  Filler,
  Legend,
  LinearScale,
  LineElement,
  PointElement,
  Tooltip,
} from "chart.js";
import { Line } from "vue-chartjs";
import { computed, onMounted, onUnmounted, ref } from "vue";
import { useRouter } from "vue-router";
import { appState, reloadUserConfig, syncHomeMetrics, toUserError } from "@/state/appState";
import { formatCompactInteger, formatInteger } from "@/utils/numberFormat";

ChartJS.register(CategoryScale, Filler, Legend, LinearScale, LineElement, PointElement, Tooltip);

const TOKEN_PRICE_PER_MILLION = {
  input: 5,
  output: 25,
  cacheRead: 0.5,
  cacheWrite: 6.25,
};

const router = useRouter();
const range = ref("30");
const providerFilter = ref("all");
const autoRefresh = ref(true);
const refreshing = ref(false);
const errorMessage = ref("");
const lastUpdatedAt = ref("");
let refreshTimer = null;

const rangeOptions = [
  { label: "7天", value: "7" },
  { label: "30天", value: "30" },
  { label: "90天", value: "90" },
  { label: "全部", value: "all" },
];

function number(value) {
  const result = Number(value);
  return Number.isFinite(result) ? Math.max(0, result) : 0;
}

function formatTokens(value) {
  return `${(number(value) / 1_000_000).toLocaleString("en-US", {
    maximumFractionDigits: 2,
  })}M`;
}

function formatCost(value) {
  const amount = number(value);
  if (amount > 0 && amount < 0.01) {
    return "<$0.01";
  }
  return `$${amount.toFixed(2)}`;
}

function formatRate(value) {
  const rate = Number(value);
  return Number.isFinite(rate) ? `${(rate * 100).toFixed(1)}%` : "暂无数据";
}

function formatDate(value, withTime = false) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value || "未知";
  }
  const dateText = date.toLocaleDateString("zh-CN", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
  }).replaceAll("/", "-");
  if (!withTime) {
    return dateText;
  }
  return `${dateText} ${date.toLocaleTimeString("zh-CN", {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
  })}`;
}

function estimateCost(inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens) {
  return (
    number(inputTokens) / 1_000_000 * TOKEN_PRICE_PER_MILLION.input +
    number(outputTokens) / 1_000_000 * TOKEN_PRICE_PER_MILLION.output +
    number(cacheReadTokens) / 1_000_000 * TOKEN_PRICE_PER_MILLION.cacheRead +
    number(cacheWriteTokens) / 1_000_000 * TOKEN_PRICE_PER_MILLION.cacheWrite
  );
}

function dailyTokens(item) {
  return number(item.inputTokens) + number(item.outputTokens) +
    number(item.cacheReadTokens) + number(item.cacheWriteTokens);
}

function providerName(providerID) {
  const provider = appState.providers.find((item) => item.id === providerID);
  return provider?.name || providerID || "未标记中转站";
}

const availableProviders = computed(() => {
  const providerIDs = new Set([
    ...Object.keys(appState.homeMetrics.byProvider || {}),
    ...appState.homeMetrics.recentEvents.map((item) => item.providerID).filter(Boolean),
  ]);
  return [...providerIDs].map((id) => ({ value: id, label: providerName(id) }));
});

const dailyRows = computed(() => {
  const rows = [...(appState.homeMetrics.daily || [])]
    .filter((item) => item.date)
    .sort((left, right) => left.date.localeCompare(right.date));
  if (range.value === "all") {
    return rows;
  }
  const days = Number(range.value);
  const cutoff = new Date();
  cutoff.setHours(0, 0, 0, 0);
  cutoff.setDate(cutoff.getDate() - days + 1);
  const cutoffText = cutoff.toISOString().slice(0, 10);
  return rows.filter((item) => item.date >= cutoffText);
});

const rangeEvents = computed(() => {
  const events = [...(appState.homeMetrics.recentEvents || [])]
    .filter((item) => item.kind !== "turn_finalized")
    .filter((item) => providerFilter.value === "all" || item.providerID === providerFilter.value)
    .sort((left, right) => String(right.at).localeCompare(String(left.at)));
  if (range.value === "all") {
    return events;
  }
  const days = Number(range.value);
  const cutoff = Date.now() - days * 24 * 60 * 60 * 1000;
  return events.filter((item) => new Date(item.at).getTime() >= cutoff);
});

const rangeTotals = computed(() => {
  if (range.value === "all") {
    return {
      calls: number(appState.homeMetrics.providerCallsTotal || appState.homeMetrics.turnsTotal),
      turns: number(appState.homeMetrics.turnsTotal),
      validTurns: number(appState.homeMetrics.validTurnsTotal),
      input: Math.max(0, number(appState.homeMetrics.promptTokensTotal) - number(appState.homeMetrics.cacheReadTokens) - number(appState.homeMetrics.cacheWriteTokens)),
      output: Math.max(0, number(appState.homeMetrics.requestTokensTotal) - number(appState.homeMetrics.promptTokensTotal)),
      cacheRead: number(appState.homeMetrics.cacheReadTokens),
      cacheWrite: number(appState.homeMetrics.cacheWriteTokens),
    };
  }
  return dailyRows.value.reduce((totals, item) => ({
    calls: totals.calls + number(item.providerCalls),
    turns: totals.turns + number(item.turnsTotal),
    validTurns: totals.validTurns + number(item.validTurnsTotal),
    input: totals.input + number(item.inputTokens),
    output: totals.output + number(item.outputTokens),
    cacheRead: totals.cacheRead + number(item.cacheReadTokens),
    cacheWrite: totals.cacheWrite + number(item.cacheWriteTokens),
  }), {
    calls: 0,
    turns: 0,
    validTurns: 0,
    input: 0,
    output: 0,
    cacheRead: 0,
    cacheWrite: 0,
  });
});

const totalTokens = computed(() => {
  const totals = rangeTotals.value;
  return totals.input + totals.output + totals.cacheRead + totals.cacheWrite;
});

const cacheHitRate = computed(() => {
  const denominator = rangeTotals.value.input + rangeTotals.value.cacheRead;
  return denominator > 0 ? rangeTotals.value.cacheRead / denominator : null;
});

const estimatedCost = computed(() => estimateCost(
  rangeTotals.value.input,
  rangeTotals.value.output,
  rangeTotals.value.cacheRead,
  rangeTotals.value.cacheWrite,
));

const chartData = computed(() => ({
  labels: dailyRows.value.map((item) => item.date),
  datasets: [
    {
      label: "Tokens",
      data: dailyRows.value.map(dailyTokens),
      borderColor: "#f08b4f",
      backgroundColor: "rgba(240, 139, 79, 0.14)",
      fill: true,
      tension: 0.32,
      pointRadius: 2,
      pointHoverRadius: 4,
      pointBackgroundColor: "#f08b4f",
      yAxisID: "tokens",
    },
    {
      label: "估算成本",
      data: dailyRows.value.map((item) => estimateCost(
        item.inputTokens,
        item.outputTokens,
        item.cacheReadTokens,
        item.cacheWriteTokens,
      )),
      borderColor: "#e34c69",
      backgroundColor: "transparent",
      borderDash: [6, 5],
      tension: 0.32,
      pointRadius: 2,
      pointHoverRadius: 4,
      pointBackgroundColor: "#e34c69",
      yAxisID: "cost",
    },
  ],
}));

const chartOptions = computed(() => ({
  responsive: true,
  maintainAspectRatio: false,
  interaction: { mode: "index", intersect: false },
  scales: {
    tokens: {
      beginAtZero: true,
      position: "left",
      grid: { color: "rgba(255,255,255,0.08)" },
      ticks: {
        color: "#8c8c8c",
        callback: (value) => formatTokens(value),
      },
      title: { display: true, text: "Tokens", color: "#bdbdbd" },
    },
    cost: {
      beginAtZero: true,
      position: "right",
      grid: { drawOnChartArea: false },
      ticks: {
        color: "#8c8c8c",
        callback: (value) => `$${value}`,
      },
      title: { display: true, text: "USD（估算）", color: "#bdbdbd" },
    },
    x: {
      grid: { color: "rgba(255,255,255,0.04)" },
      ticks: { color: "#8c8c8c", maxRotation: 0, autoSkip: true, maxTicksLimit: 8 },
    },
  },
  plugins: {
    legend: {
      position: "top",
      align: "end",
      labels: { color: "#bdbdbd", usePointStyle: true, boxWidth: 8 },
    },
    tooltip: {
      callbacks: {
        label: (context) => context.dataset.yAxisID === "cost"
          ? `${context.dataset.label}: ${formatCost(context.raw)}`
          : `${context.dataset.label}: ${formatTokens(context.raw)}`,
      },
    },
  },
}));

const modelRows = computed(() => Object.entries(appState.homeMetrics.byProviderModel || {})
  .map(([key, item]) => ({
    key,
    provider: providerName(item.providerID),
    model: item.model || item.modelConfigID || key,
    calls: number(item.providerCalls),
    tokens: number(item.totalTokens),
    cost: estimateCost(item.inputTokens, item.outputTokens, item.cacheReadTokens, item.cacheWriteTokens),
    input: number(item.inputTokens),
    output: number(item.outputTokens),
  }))
  .filter((item) => providerFilter.value === "all" || item.key.startsWith(`${providerFilter.value}/`))
  .sort((left, right) => right.tokens - left.tokens));

const providerRows = computed(() => Object.entries(appState.homeMetrics.byProvider || {})
  .map(([key, item]) => ({
    key,
    provider: providerName(item.providerID || key),
    calls: number(item.providerCalls),
    tokens: number(item.totalTokens),
    cost: estimateCost(item.inputTokens, item.outputTokens, item.cacheReadTokens, item.cacheWriteTokens),
  }))
  .filter((item) => providerFilter.value === "all" || item.key === providerFilter.value)
  .sort((left, right) => right.tokens - left.tokens));

async function refresh() {
  if (refreshing.value) {
    return;
  }
  refreshing.value = true;
  errorMessage.value = "";
  try {
    const [metricsResult] = await Promise.all([syncHomeMetrics(), reloadUserConfig()]);
    if (!metricsResult?.ok) {
      throw new Error(metricsResult?.error || "加载使用统计失败");
    }
    lastUpdatedAt.value = formatDate(new Date(), true);
  } catch (error) {
    errorMessage.value = toUserError(error);
  } finally {
    refreshing.value = false;
  }
}

function backHome() {
  router.push("/");
}

onMounted(() => {
  void refresh();
  refreshTimer = window.setInterval(() => {
    if (autoRefresh.value) {
      void refresh();
    }
  }, 30_000);
});

onUnmounted(() => {
  if (refreshTimer) {
    window.clearInterval(refreshTimer);
    refreshTimer = null;
  }
});
</script>

<template>
  <div class="stats-page">
    <div class="stats-toolbar">
      <button type="button" class="back-button" @click="backHome">
        <span class="icon-[mdi--arrow-left]" />
        <span>返回首页</span>
      </button>
      <div class="toolbar-actions">
        <span class="refresh-time">{{ lastUpdatedAt ? `刷新于 ${lastUpdatedAt}` : "尚未刷新" }}</span>
        <button type="button" class="auto-refresh" :class="{ 'auto-refresh-on': autoRefresh }" @click="autoRefresh = !autoRefresh">
          <span class="auto-refresh-dot" />
          自动刷新
        </button>
        <label v-if="autoRefresh" class="toolbar-select compact-select">
          <span class="sr-only">自动刷新间隔</span>
          <select aria-label="自动刷新间隔" disabled><option>30s</option></select>
        </label>
        <label class="toolbar-select">
          <span class="sr-only">统计范围</span>
          <select v-model="range" aria-label="统计范围">
            <option v-for="option in rangeOptions" :key="option.value" :value="option.value">{{ option.label }}</option>
          </select>
        </label>
        <label class="toolbar-select">
          <span class="sr-only">中转站</span>
          <select v-model="providerFilter">
            <option value="all">全部中转站</option>
            <option v-for="provider in availableProviders" :key="provider.value" :value="provider.value">
              {{ provider.label }}
            </option>
          </select>
        </label>
        <button type="button" class="refresh-button" :disabled="refreshing" @click="refresh">
          <span class="icon-[mdi--refresh]" :class="{ 'spin-icon': refreshing }" />
          刷新统计
        </button>
      </div>
    </div>

    <div v-if="errorMessage" class="stats-error">{{ errorMessage }}</div>

    <div class="stats-intro">
      <div>
        <h1>使用统计</h1>
        <p>按当前记录统计调用次数、Token 消耗和模型使用情况。成本为估算值，不代表中转站实际账单。</p>
      </div>
    </div>

    <div class="metric-grid">
      <div class="metric-card">
        <span class="metric-label">真实消耗 Tokens</span>
        <strong>{{ formatTokens(totalTokens) }}</strong>
        <small>{{ formatInteger(totalTokens) }}</small>
      </div>
      <div class="metric-card">
        <span class="metric-label">总请求</span>
        <strong>{{ formatCompactInteger(rangeTotals.calls) }}</strong>
        <small>{{ formatInteger(rangeTotals.calls) }} 次中转站调用</small>
      </div>
      <div class="metric-card metric-card-green">
        <span class="metric-label">总成本（估算）</span>
        <strong>{{ formatCost(estimatedCost) }}</strong>
        <small>按当前内置价格口径估算</small>
      </div>
      <div class="metric-card metric-card-purple">
        <span class="metric-label">缓存命中率</span>
        <strong>{{ formatRate(cacheHitRate) }}</strong>
        <small>缓存读取 /（缓存读取 + 普通输入）</small>
      </div>
      <div class="metric-card">
        <span class="metric-label">输入 Tokens</span>
        <strong>{{ formatTokens(rangeTotals.input) }}</strong>
        <small>含缓存写入，不含缓存读取</small>
      </div>
      <div class="metric-card">
        <span class="metric-label">输出 Tokens</span>
        <strong>{{ formatTokens(rangeTotals.output) }}</strong>
        <small>{{ formatInteger(rangeTotals.output) }}</small>
      </div>
    </div>

    <section class="stats-section chart-section">
      <div class="section-heading">
        <div>
          <h2>使用趋势（按日）</h2>
          <p>Token 使用量和估算成本按调用记录归档；没有历史记录的日期不会虚构数据。</p>
        </div>
        <span v-if="dailyRows.length" class="section-count">{{ dailyRows.length }} 天</span>
      </div>
      <div v-if="dailyRows.length" class="chart-wrap">
        <Line :data="chartData" :options="chartOptions" />
      </div>
      <div v-else class="empty-state">当前筛选范围内没有按日统计数据。</div>
    </section>

    <section class="stats-section">
      <div class="section-heading">
        <div>
          <h2>模型统计</h2>
          <p>按已记录的中转站和模型累计汇总。</p>
        </div>
        <span class="section-count">{{ modelRows.length }} 个模型</span>
      </div>
      <div v-if="modelRows.length" class="table-scroll">
        <table class="stats-table">
          <thead>
            <tr><th>模型</th><th>Provider</th><th>请求</th><th>真实 Tokens</th><th>总成本（估算）</th><th>均价/请求</th></tr>
          </thead>
          <tbody>
            <tr v-for="item in modelRows" :key="item.key">
              <td class="primary-cell">{{ item.model }}</td>
              <td>{{ item.provider }}</td>
              <td>{{ formatInteger(item.calls) }}</td>
              <td>{{ formatTokens(item.tokens) }}</td>
              <td class="cost-cell">{{ formatCost(item.cost) }}</td>
              <td>{{ formatCost(item.calls ? item.cost / item.calls : 0) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <div v-else class="empty-state">暂无模型调用记录。</div>
    </section>

    <section class="stats-section">
      <div class="section-heading">
        <div>
          <h2>Provider 统计</h2>
          <p>中转站名称来自模型配置，历史记录不会因删除配置而丢失。</p>
        </div>
      </div>
      <div v-if="providerRows.length" class="table-scroll">
        <table class="stats-table compact-table">
          <thead><tr><th>Provider</th><th>请求</th><th>真实 Tokens</th><th>总成本（估算）</th></tr></thead>
          <tbody>
            <tr v-for="item in providerRows" :key="item.key">
              <td class="primary-cell">{{ item.provider }}</td>
              <td>{{ formatInteger(item.calls) }}</td>
              <td>{{ formatTokens(item.tokens) }}</td>
              <td class="cost-cell">{{ formatCost(item.cost) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <div v-else class="empty-state">暂无 Provider 调用记录。</div>
    </section>

    <section class="stats-section logs-section">
      <div class="section-heading">
        <div>
          <h2>请求日志</h2>
          <p>展示本地保存的最近 {{ appState.homeMetrics.recentEvents.length }} 条调用事件。</p>
        </div>
        <span class="section-count">{{ rangeEvents.length }} 条匹配</span>
      </div>
      <div v-if="rangeEvents.length" class="table-scroll logs-table-scroll">
        <table class="stats-table">
          <thead><tr><th>时间</th><th>模型</th><th>Provider</th><th>输入</th><th>输出</th><th>缓存读</th><th>成本</th><th>状态</th></tr></thead>
          <tbody>
            <tr v-for="event in rangeEvents" :key="event.eventID">
              <td>{{ formatDate(event.at, true) }}</td>
              <td class="primary-cell">{{ event.model || event.modelConfigID || "未知模型" }}</td>
              <td>{{ providerName(event.providerID) }}</td>
              <td>{{ formatTokens(event.inputTokens) }}</td>
              <td>{{ formatTokens(event.outputTokens) }}</td>
              <td>{{ formatTokens(event.cacheReadTokens) }}</td>
              <td class="cost-cell">{{ formatCost(estimateCost(event.inputTokens, event.outputTokens, event.cacheReadTokens, event.cacheWriteTokens)) }}</td>
              <td><span class="status-badge" :class="{ 'status-muted': !event.usagePresent }">{{ event.usagePresent ? "已记录" : "无用量" }}</span></td>
            </tr>
          </tbody>
        </table>
      </div>
      <div v-else class="empty-state">当前筛选范围内没有请求日志。</div>
    </section>
  </div>
</template>

<style scoped>
.stats-page {
  min-height: 100%;
  overflow-y: auto;
  padding: 12px 16px 28px;
  color: #e5e5e5;
}

.stats-toolbar,
.stats-intro,
.section-heading,
.toolbar-actions {
  display: flex;
  align-items: center;
}

.stats-toolbar,
.section-heading {
  justify-content: space-between;
  gap: 16px;
}

.stats-toolbar {
  min-height: 32px;
  margin-bottom: 12px;
}

.back-button,
.refresh-button {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  border: 0;
  border-radius: 6px;
  color: #999;
  background: transparent;
  cursor: pointer;
  font: inherit;
  font-size: 12px;
}

.back-button { padding: 5px 7px; }
.back-button:hover,
.refresh-button:hover { color: #f0f0f0; background: #292929; }
.refresh-button { padding: 5px 8px; border: 1px solid #3c3c3c; background: #242424; }
.refresh-button:disabled { cursor: not-allowed; opacity: 0.6; }

.toolbar-actions { gap: 8px; color: #888; font-size: 12px; }
.refresh-time { white-space: nowrap; }
.auto-refresh { display: inline-flex; align-items: center; gap: 4px; border: 0; padding: 4px 3px; color: #777; background: transparent; cursor: pointer; font: inherit; font-size: 11px; }
.auto-refresh:hover { color: #ddd; }
.auto-refresh-on { color: #47bd7d; }
.auto-refresh-dot { width: 6px; height: 6px; border-radius: 50%; background: currentColor; }
.toolbar-select select {
  height: 26px;
  min-width: 72px;
  border: 1px solid #3b3b3b;
  border-radius: 5px;
  padding: 0 8px;
  color: #d4d4d4;
  background: #242424;
  outline: none;
  font: inherit;
}
.toolbar-select select:focus { border-color: #666; }

.stats-intro { margin: 2px 0 14px; }
.stats-intro h1 { margin: 0; color: #fff; font-size: 19px; font-weight: 600; }
.stats-intro p,
.section-heading p { margin: 5px 0 0; color: #858585; font-size: 11px; line-height: 1.6; }

.stats-error {
  margin-bottom: 12px;
  border: 1px solid #6f3131;
  border-radius: 6px;
  padding: 8px 10px;
  color: #ffb4b4;
  background: #2c1717;
  font-size: 12px;
}

.metric-grid {
  display: grid;
  grid-template-columns: repeat(6, minmax(0, 1fr));
  overflow: hidden;
  margin-bottom: 14px;
  border: 1px solid #333;
  border-radius: 7px;
  background: #222;
}

.metric-card {
  min-width: 0;
  min-height: 96px;
  border-left: 1px solid #333;
  padding: 13px 12px 11px;
}
.metric-card:first-child { border-left: 0; }
.metric-label { display: block; color: #888; font-size: 11px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.metric-card strong { display: block; margin-top: 9px; color: #f2f2f2; font-family: var(--font-num); font-size: 22px; font-weight: 500; line-height: 1; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.metric-card small { display: block; margin-top: 8px; color: #777; font-size: 10px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.metric-card-green strong { color: #20bb6d; }
.metric-card-purple strong { color: #b46af0; }

.stats-section {
  margin-bottom: 14px;
  border: 1px solid #303030;
  border-radius: 7px;
  padding: 13px 13px 11px;
  background: #1d1d1d;
}
.chart-section { min-height: 310px; }
.section-heading { align-items: flex-start; margin-bottom: 10px; }
.section-heading h2 { margin: 0; color: #eaeaea; font-size: 13px; font-weight: 500; }
.section-count { flex: none; color: #777; font-size: 11px; }
.chart-wrap { height: 245px; }
.empty-state { display: flex; min-height: 100px; align-items: center; justify-content: center; color: #777; font-size: 12px; }

.table-scroll { overflow-x: auto; }
.stats-table { width: 100%; min-width: 700px; border-collapse: collapse; color: #b0b0b0; font-size: 11px; }
.stats-table th,
.stats-table td { border-bottom: 1px solid #292929; padding: 8px 7px; text-align: left; white-space: nowrap; }
.stats-table th { color: #858585; background: #242424; font-weight: 400; }
.stats-table th:first-child { border-radius: 4px 0 0 4px; }
.stats-table th:last-child { border-radius: 0 4px 4px 0; }
.stats-table tbody tr:last-child td { border-bottom: 0; }
.stats-table tbody tr:hover { background: #222; }
.primary-cell { color: #e4e4e4; }
.cost-cell { color: #20bb6d; }
.compact-table { min-width: 480px; }
.logs-table-scroll { max-height: 340px; overflow-y: auto; }
.status-badge { color: #43c981; }
.status-muted { color: #777; }

.spin-icon { animation: stats-spin 0.9s linear infinite; }
@keyframes stats-spin { to { transform: rotate(360deg); } }

.sr-only { position: absolute; width: 1px; height: 1px; padding: 0; margin: -1px; overflow: hidden; clip: rect(0, 0, 0, 0); white-space: nowrap; border: 0; }

@media (max-width: 980px) {
  .metric-grid { grid-template-columns: repeat(3, minmax(0, 1fr)); }
  .metric-card:nth-child(4) { border-left: 0; border-top: 1px solid #333; }
  .metric-card:nth-child(n + 4) { border-top: 1px solid #333; }
}

@media (max-width: 680px) {
  .stats-page { padding: 10px 10px 20px; }
  .stats-toolbar { align-items: flex-start; flex-direction: column; }
  .toolbar-actions { width: 100%; flex-wrap: wrap; }
  .refresh-time { width: 100%; }
  .metric-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .metric-card:nth-child(odd) { border-left: 0; }
  .metric-card:nth-child(n + 3) { border-top: 1px solid #333; }
  .section-heading { align-items: flex-start; flex-direction: column; gap: 4px; }
}
</style>