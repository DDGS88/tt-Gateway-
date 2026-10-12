import type { AlertRule } from '../types';
import type { ErrorScanCategory, RawGatewayLog } from '../types';
import type {
  ProbeAvailabilityConfig,
  ProbeQualityConfig,
  ProbeConfigResponse,
  ProbeRun,
  ProbeResult,
} from '../types';

const BASE = '/api/v1';

const TOKEN_KEY = 'tt-inspector-token';

function getToken(): string | null {
  return localStorage.getItem(TOKEN_KEY);
}

export function setToken(token: string) {
  localStorage.setItem(TOKEN_KEY, token);
}

export function clearToken() {
  localStorage.removeItem(TOKEN_KEY);
}

export function hasToken(): boolean {
  return !!getToken();
}

async function request<T>(url: string, options?: RequestInit): Promise<T> {
  const token = getToken();
  const headers: Record<string, string> = { 'Content-Type': 'application/json' };
  if (token) headers['Authorization'] = `Bearer ${token}`;

  const res = await fetch(`${BASE}${url}`, { headers, ...options });
  const data = await res.json();
  if (!res.ok) {
    if (res.status === 401) {
      clearToken();
      window.location.reload();
    }
    throw new Error(data.error || `HTTP ${res.status}`);
  }
  return data;
}

// ── 后端数据模型 ──

interface BackendRule {
  id: number;
  name: string;
  enabled: boolean;
  rule_type: string;
  condition: string;       // JSON 字符串
  window_sec: number;
  threshold: number;
  cooldown_sec: number;
  webhook_url: string;
  description: string;
  created_at: string;
  updated_at: string;
}

function parseCondition(condition: string): { channel_id?: number; pattern?: string; field?: string; model_name?: string; negate?: boolean; exclude_empty?: boolean } {
  try { return JSON.parse(condition); } catch { return {}; }
}

// ── 映射函数 ──

interface SaveRuleData extends Partial<AlertRule> {
  channelId?: number;
  matchField?: string;
}

function backendToFrontend(b: BackendRule): AlertRule {
  const cond = parseCondition(b.condition);
  const channelId = cond.channel_id || 0;
  return {
    id: String(b.id),
    name: b.name,
    type: mapRuleType(b.rule_type),
    enabled: b.enabled,
    channel: channelId > 0 ? `渠道 #${channelId}` : '全部渠道',
    model: cond.model_name || '全部模型',
    windowSeconds: b.window_sec,
    thresholdValue: `> ${b.threshold}`,
    thresholdNumber: b.threshold,
    lastTriggered: '',
    pattern: cond.pattern || '',
    webhookUrl: b.webhook_url,
    description: b.description,
    cooldownSeconds: b.cooldown_sec,
    triggerCount: 0,
    createdAt: b.created_at,
    // 内部字段（编辑回填用）
    _channelId: cond.channel_id,
    _matchField: cond.field || 'content',
    _negate: cond.negate || false,
    _excludeEmpty: cond.exclude_empty || false,
  };
}

function frontendToBackend(r: SaveRuleData): Partial<BackendRule> {
  const condition: Record<string, unknown> = {};
  if (r.channelId) condition.channel_id = r.channelId;
  if (r.matchField) condition.field = r.matchField;
  if (r.pattern) condition.pattern = r.pattern;
  if (r.model && r.model !== '全部模型') condition.model_name = r.model;
  if ((r as any)._negate) condition.negate = true;
  if ((r as any)._excludeEmpty) condition.exclude_empty = true;

  return {
    name: r.name,
    enabled: r.enabled,
    rule_type: r.type ? unmapRuleType(r.type) : undefined,
    condition: JSON.stringify(condition),
    window_sec: r.windowSeconds,
    threshold: r.thresholdNumber,
    cooldown_sec: r.cooldownSeconds,
    webhook_url: r.webhookUrl,
    description: r.description,
  };
}

function mapRuleType(t: string): AlertRule['type'] {
  switch (t) {
    case 'regex': return 'Regex';
    case 'error_rate': return 'Error Rate';
    case 'token_threshold': return 'Token';
    default: return t as AlertRule['type'];
  }
}

function unmapRuleType(t: string): string {
  switch (t) {
    case 'Regex': return 'regex';
    case 'Error Rate': return 'error_rate';
    case 'Token': return 'token_threshold';
    case 'Latency': return 'latency';
    default: return t;
  }
}

// ── 规则 CRUD ──

export async function listRules(): Promise<AlertRule[]> {
  const res = await request<{ data: BackendRule[] }>('/rules');
  return (res.data || []).map(backendToFrontend);
}

export async function createRule(rule: SaveRuleData): Promise<AlertRule> {
  const res = await request<{ data: BackendRule }>('/rules', {
    method: 'POST',
    body: JSON.stringify(frontendToBackend(rule)),
  });
  return backendToFrontend(res.data);
}

export async function updateRule(id: string, rule: SaveRuleData): Promise<AlertRule> {
  const res = await request<{ data: BackendRule }>(`/rules/${id}`, {
    method: 'PUT',
    body: JSON.stringify(frontendToBackend(rule)),
  });
  return backendToFrontend(res.data);
}

export async function deleteRule(id: string): Promise<void> {
  await request(`/rules/${id}`, { method: 'DELETE' });
}

export async function enableRule(id: string): Promise<void> {
  await request(`/rules/${id}/enable`, { method: 'POST' });
}

export async function disableRule(id: string): Promise<void> {
  await request(`/rules/${id}/disable`, { method: 'POST' });
}

// ── 渠道 / 模型 ──

export interface ChannelOption {
  id: number;
  name: string;
}

export interface ChannelWithModels {
  channel_id: number;
  name: string;
  tag?: string;
  models: string[];
}

export async function listChannels(): Promise<ChannelOption[]> {
  const res = await request<{ data: ChannelOption[] }>('/channels');
  return res.data || [];
}

export async function listChannelsWithModels(): Promise<ChannelWithModels[]> {
  const res = await request<{ data: ChannelWithModels[] }>('/channels/with-models');
  return res.data || [];
}

// ── 错误扫描规则 ──

interface BackendErrorScanRule {
  id: number;
  code: string;
  name: string;
  category: string;
  sub_category: string;
  status_code: number;
  pattern_keyword: string;
  top_model: string;
  top_channel: string;
  top_user: string;
  top_endpoint: string;
  hourly_error_count: number;
  daily_error_count: number;
  alert_threshold: number;
  sample_log: string;
  is_alert_enabled: boolean;
  suppressed: boolean;
  scan_granularity: string;
  alert_granularity: string;
  exclude_channels: string;
  exclude_users: string;
  filter_mode: string;
  severity: string;
  feishu_webhook_url: string;
  raw_logs: string;
  created_at: string;
  updated_at: string;
}

function backendToFrontendScan(b: BackendErrorScanRule): ErrorScanCategory {
  let rawLogs: RawGatewayLog[] = [];
  try { rawLogs = JSON.parse(b.raw_logs || '[]'); } catch { /* empty */ }

  return {
    id: String(b.id),
    code: b.code,
    name: b.name,
    category: b.category as ErrorScanCategory['category'],
    subCategory: b.sub_category,
    statusCode: b.status_code,
    patternKeyword: b.pattern_keyword,
    topModel: b.top_model,
    topChannel: b.top_channel,
    topUser: b.top_user,
    topEndpoint: b.top_endpoint,
    hourlyErrorCount: b.hourly_error_count,
    dailyErrorCount: b.daily_error_count,
    alertThreshold: b.alert_threshold || 10,
    sampleLog: b.sample_log,
    isAlertEnabled: b.is_alert_enabled,
    suppressed: b.suppressed,
    scanGranularity: b.scan_granularity as ErrorScanCategory['scanGranularity'],
    alertGranularity: b.alert_granularity as ErrorScanCategory['alertGranularity'],
    excludeChannels: b.exclude_channels || '',
    excludeUsers: b.exclude_users || '',
    filterMode: b.filter_mode || 'exclude',
    severity: b.severity as ErrorScanCategory['severity'],
    feishuWebhookUrl: b.feishu_webhook_url,
    lastScannedTime: b.created_at,
    rawLogs,
  };
}

function frontendToBackendScan(r: Omit<ErrorScanCategory, 'id' | 'lastScannedTime'>): Record<string, unknown> {
  return {
    code: r.code,
    name: r.name,
    category: r.category,
    sub_category: r.subCategory,
    status_code: r.statusCode,
    pattern_keyword: r.patternKeyword,
    top_model: r.topModel,
    top_channel: r.topChannel,
    top_user: r.topUser,
    top_endpoint: r.topEndpoint,
    hourly_error_count: r.hourlyErrorCount,
    daily_error_count: r.dailyErrorCount,
    alert_threshold: r.alertThreshold,
    sample_log: r.sampleLog,
    is_alert_enabled: r.isAlertEnabled,
    suppressed: r.suppressed,
    scan_granularity: r.scanGranularity,
    alert_granularity: r.alertGranularity,
    exclude_channels: r.excludeChannels,
    exclude_users: r.excludeUsers,
    filter_mode: r.filterMode,
    severity: r.severity,
    feishu_webhook_url: r.feishuWebhookUrl,
    raw_logs: JSON.stringify(r.rawLogs || []),
  };
}

export async function listErrorScanRules(): Promise<ErrorScanCategory[]> {
  const res = await request<{ data: BackendErrorScanRule[] }>('/error-scan');
  return (res.data || []).map(backendToFrontendScan);
}

export async function createErrorScanRule(rule: Omit<ErrorScanCategory, 'id' | 'lastScannedTime'>): Promise<ErrorScanCategory> {
  const res = await request<{ data: BackendErrorScanRule }>('/error-scan', {
    method: 'POST',
    body: JSON.stringify(frontendToBackendScan(rule)),
  });
  return backendToFrontendScan(res.data);
}

export async function updateErrorScanRule(id: string, rule: Partial<ErrorScanCategory>): Promise<void> {
  const full = frontendToBackendScan(rule as any);
  // 只发送非 undefined 的字段，避免覆盖未提供的字段为空值
  const partial: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(full)) {
    if (v !== undefined) partial[k] = v;
  }
  await request(`/error-scan/${id}`, {
    method: 'PUT',
    body: JSON.stringify(partial),
  });
}

export async function deleteErrorScanRule(id: string): Promise<void> {
  await request(`/error-scan/${id}`, { method: 'DELETE' });
}

export async function toggleErrorScanAlert(id: string): Promise<void> {
  await request(`/error-scan/${id}/toggle-alert`, { method: 'POST' });
}

export async function toggleErrorScanSuppressed(id: string): Promise<void> {
  await request(`/error-scan/${id}/toggle-suppressed`, { method: 'POST' });
}

export async function updateErrorScanGranularity(
  id: string,
  scanGranularity: string,
  alertGranularity: string
): Promise<void> {
  await request(`/error-scan/${id}/granularity`, {
    method: 'POST',
    body: JSON.stringify({ scan_granularity: scanGranularity, alert_granularity: alertGranularity }),
  });
}

export async function updateErrorScanThresholdExcludes(
  id: string,
  alertThreshold: number,
  excludeChannels: string,
  excludeUsers: string,
  filterMode: string
): Promise<void> {
  await request(`/error-scan/${id}/threshold-excludes`, {
    method: 'POST',
    body: JSON.stringify({
      alert_threshold: alertThreshold,
      exclude_channels: excludeChannels,
      exclude_users: excludeUsers,
      filter_mode: filterMode,
    }),
  });
}

export async function getErrorScanPollInterval(): Promise<number> {
  const res = await request<{ data: number }>('/error-scan/poll-interval');
  return res.data;
}

export async function setErrorScanPollInterval(seconds: number): Promise<void> {
  await request('/error-scan/poll-interval', {
    method: 'POST',
    body: JSON.stringify({ seconds }),
  });
}

export interface FeishuAppConfig {
  app_id: string;
  app_secret: string;
  chat_id: string;
}

export async function testErrorScanFeishuApp(
  config: FeishuAppConfig,
): Promise<{ success: boolean; error?: string }> {
  const res = await request<{ success: boolean; error?: string }>('/error-scan/test-webhook', {
    method: 'POST',
    body: JSON.stringify(config),
  });
  return res;
}

export async function triggerErrorScan(): Promise<void> {
  await request('/error-scan/trigger-scan', { method: 'POST' });
}

export interface ErrorSampleItem {
  content: string;
  other: string;
  model_name: string;
  channel_name: string;
  group_name: string;
  cnt: number;
}

export async function getRecentErrorSamples(): Promise<ErrorSampleItem[]> {
  const res = await request<{ data: ErrorSampleItem[] }>('/error-scan/recent-samples');
  return res.data || [];
}

// ── 错误扫描概览 ──

export interface OverviewStats {
  total_errors: number;
  matched_errors: number;
  unmatched_errors: number;
  match_rate: number;
  active_rules: number;
}

export interface UnmatchedSample {
  id: number;
  content: string;
  other: string;
  model_name: string;
  channel_name: string;
  username: string;
  cnt: number;
}

export async function getOverviewStats(windowHours: number = 24): Promise<OverviewStats> {
  const res = await request<{ data: OverviewStats }>(`/error-scan/overview?window=${windowHours}`);
  return res.data;
}

export async function getUnmatchedSamples(windowHours?: number): Promise<UnmatchedSample[]> {
  const params = windowHours ? `?window=${windowHours}` : '';
  const res = await request<{ data: UnmatchedSample[] }>(`/error-scan/unmatched-samples${params}`);
  return res.data || [];
}

export async function createRuleFromSample(data: {
  content: string;
  other: string;
  model_name: string;
  channel_name: string;
  username: string;
  code: string;
  name: string;
  category: string;
}): Promise<void> {
  await request('/error-scan/create-from-sample', {
    method: 'POST',
    body: JSON.stringify(data),
  });
}

export async function getFallbackConfig(): Promise<{ enabled: boolean; webhook: string }> {
  const res = await request<{ data: { enabled: boolean; webhook: string } }>('/error-scan/fallback-config');
  return res.data;
}

export async function setFallbackConfig(enabled: boolean, webhook: string): Promise<void> {
  await request('/error-scan/fallback-config', {
    method: 'POST',
    body: JSON.stringify({ enabled, webhook }),
  });
}

export async function getAlertAtUsers(): Promise<string[]> {
  const res = await request<{ data: string[] }>('/error-scan/alert-at-users');
  return res.data;
}

export async function setAlertAtUsers(users: string[]): Promise<void> {
  await request('/error-scan/alert-at-users', {
    method: 'POST',
    body: JSON.stringify({ users }),
  });
}

export async function getFeishuAppConfig(): Promise<FeishuAppConfig> {
  const res = await request<{ data: FeishuAppConfig }>('/error-scan/feishu-app-config');
  return res.data;
}

export async function setFeishuAppConfig(config: FeishuAppConfig): Promise<void> {
  await request('/error-scan/feishu-app-config', {
    method: 'POST',
    body: JSON.stringify(config),
  });
}

export interface GroupedStatItem {
  group_value: string;
  count: number;
  raw_key: string;
}

export async function getGroupedStats(dimension: string, windowHours: number): Promise<GroupedStatItem[]> {
  const res = await request<{ data: GroupedStatItem[] }>(
    `/error-scan/grouped-stats?dimension=${dimension}&window=${windowHours}`
  );
  return res.data || [];
}

export interface GroupedErrorDetail {
  id: number;
  content: string;
  other: string;
  model_name: string;
  channel_name: string;
  channel_id: number;
  username: string;
  group_name: string;
  created_at: number;
}

export async function getGroupedSamples(dimension: string, rawKey: string, windowHours: number): Promise<GroupedErrorDetail[]> {
  const res = await request<{ data: GroupedErrorDetail[] }>(
    `/error-scan/grouped-samples?dimension=${dimension}&raw_key=${encodeURIComponent(rawKey)}&window=${windowHours}`
  );
  return res.data || [];
}

// ── 错误日志分类 ──

export interface ClassifyResult {
  category: string;
  sub_category: string;
  status_code: number;
  keyword_matched: string;
  recommendation: string;
  model: string;
  channel: string;
  username: string;
  group: string;
}

export async function classifyLog(log: {
  content: string;
  other: string;
  model_name: string;
  channel_name: string;
  username: string;
  group: string;
}): Promise<ClassifyResult> {
  const res = await request<{ data: ClassifyResult }>('/error-scan/classify-log', {
    method: 'POST',
    body: JSON.stringify(log),
  });
  return res.data;
}

// ── 分类规则管理 ──

export interface ClassifyRule {
  id: number;
  keyword: string;
  status_code: number;
  category: string;
  sub_category: string;
  recommendation: string;
  priority: number;
}

export async function listClassifyRules(): Promise<ClassifyRule[]> {
  const res = await request<{ data: ClassifyRule[] }>('/error-scan/classify-rules');
  return res.data || [];
}

export async function createClassifyRule(rule: Omit<ClassifyRule, 'id'>): Promise<ClassifyRule> {
  const res = await request<{ data: ClassifyRule }>('/error-scan/classify-rules', {
    method: 'POST',
    body: JSON.stringify(rule),
  });
  return res.data;
}

export async function deleteClassifyRule(id: number): Promise<void> {
  await request(`/error-scan/classify-rules/${id}`, { method: 'DELETE' });
}

export async function updateClassifyRule(id: number, rule: Omit<ClassifyRule, 'id' | 'created_at' | 'updated_at'>): Promise<ClassifyRule> {
  const res = await request<{ data: ClassifyRule }>(`/error-scan/classify-rules/${id}`, {
    method: 'PUT',
    body: JSON.stringify(rule),
  });
  return res.data;
}

// ── 缓存命中率 ──

export interface CacheHitRateSeries {
  name: string;
  values: (number | null)[];
}

export interface CacheHitRateResult {
  buckets: string[];
  series: CacheHitRateSeries[];
}

export async function getCacheHitRate(
  dimension: 'user' | 'channel' | 'model',
  windowHours: number,
  channelId?: number
): Promise<CacheHitRateResult> {
  const channelQuery = channelId != null ? `&channel_id=${channelId}` : '';
  const res = await request<{ data: CacheHitRateResult }>(
    `/cache-hit-rate?dimension=${dimension}&window=${windowHours}${channelQuery}`
  );
  return res.data;
}

// ── 渠道错误率 ──

export interface ChannelErrorRateSeries {
  name: string;
  values: (number | null)[];
  avg_rate: number;
  requests: number;
  errors: number;
}

export interface ChannelErrorRateResult {
  buckets: string[];
  series: ChannelErrorRateSeries[];
}

export async function getChannelErrorRate(windowHours: number): Promise<ChannelErrorRateResult> {
  const res = await request<{ data: ChannelErrorRateResult }>(`/error-rate?window=${windowHours}`);
  return res.data;
}

// ── 渠道探测 ──

// 获取探测配置（含可用性 / 质量配置与运行时状态）
export async function getProbeConfig(): Promise<ProbeConfigResponse> {
  const res = await request<{ data: ProbeConfigResponse }>('/probe/config');
  return res.data;
}

// 保存可用性探测配置（body 为完整配置对象）
export async function updateProbeAvailabilityConfig(
  cfg: ProbeAvailabilityConfig
): Promise<ProbeAvailabilityConfig> {
  const res = await request<{ data: ProbeAvailabilityConfig }>('/probe/config/availability', {
    method: 'PUT',
    body: JSON.stringify(cfg),
  });
  return res.data;
}

// 保存质量探测配置（body 为完整配置对象）
export async function updateProbeQualityConfig(
  cfg: ProbeQualityConfig
): Promise<ProbeQualityConfig> {
  const res = await request<{ data: ProbeQualityConfig }>('/probe/config/quality', {
    method: 'PUT',
    body: JSON.stringify(cfg),
  });
  return res.data;
}

// 校验 cron 表达式，返回是否合法与下次运行时间
export async function validateProbeCron(
  cron: string
): Promise<{ valid: boolean; next_run: string; error?: string }> {
  const res = await request<{ data: { valid: boolean; next_run: string; error?: string } }>(
    '/probe/validate-cron',
    {
      method: 'POST',
      body: JSON.stringify({ cron }),
    }
  );
  return res.data;
}

// 手动触发可用性探测（异步启动，立即返回）
export async function triggerProbeAvailability(): Promise<void> {
  await request('/probe/availability/run', { method: 'POST' });
}

// 手动触发质量探测（异步启动，立即返回）
export async function triggerProbeQuality(): Promise<void> {
  await request('/probe/quality/run', { method: 'POST' });
}

// 查询探测运行历史（按时间倒序）
export async function listProbeRuns(
  kind: 'availability' | 'quality',
  limit = 20
): Promise<ProbeRun[]> {
  const res = await request<{ data: ProbeRun[] }>(`/probe/runs?kind=${kind}&limit=${limit}`);
  return res.data || [];
}

// 查询探测明细，可按批次 / 类型 / 仅失败过滤
export async function listProbeResults(params: {
  runId?: number;
  kind?: string;
  onlyFailed?: boolean;
  limit?: number;
}): Promise<ProbeResult[]> {
  const query = new URLSearchParams();
  if (params.runId != null) query.set('run_id', String(params.runId));
  if (params.kind) query.set('kind', params.kind);
  if (params.onlyFailed) query.set('only_failed', '1');
  if (params.limit != null) query.set('limit', String(params.limit));
  const res = await request<{ data: ProbeResult[] }>(`/probe/results?${query.toString()}`);
  return res.data || [];
}

// 测试飞书 webhook 是否可用
export async function testProbeWebhook(
  webhookUrl: string
): Promise<{ success: boolean; error?: string }> {
  const res = await request<{ success: boolean; error?: string }>('/probe/test-webhook', {
    method: 'POST',
    body: JSON.stringify({ webhook_url: webhookUrl }),
  });
  return res;
}
