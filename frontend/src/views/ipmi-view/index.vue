<script lang="ts" setup>
import { ref, onMounted, onBeforeUnmount, computed } from 'vue';
import { useRoute } from 'vue-router';

import {
  Spin,
  Result,
  Button,
  Tabs,
  TabPane,
  Descriptions,
  DescriptionsItem,
  Table,
  Tag,
  Alert,
  Space,
  Popconfirm,
  Badge,
} from 'ant-design-vue';

import { $t } from 'shell/locales';
import { DeviceService } from '../../api/services';

const route = useRoute();
const deviceId = computed(() => String(route.params.deviceId ?? ''));

const loading = ref(true);
const error = ref('');
const consoleUrl = ref('');
const bmcHost = ref('');
const token = ref('');
const activeTab = ref('console');
const frameEl = ref<HTMLIFrameElement | null>(null);
let focusTimer: ReturnType<typeof setInterval> | undefined;

// ---------------------------------------------------------------------------
// Console keyboard focus (BMC HTML5 viewer captures the keyboard only when the
// iframe/canvas is focused). Same-origin via our proxy, so this is allowed.
// ---------------------------------------------------------------------------
function focusConsole(): void {
  const iframe = frameEl.value;
  if (!iframe) return;
  try {
    iframe.contentWindow?.focus();
    const canvas = iframe.contentDocument?.getElementById('noVNC_canvas');
    if (canvas) {
      if (!canvas.getAttribute('tabindex')) canvas.setAttribute('tabindex', '0');
      (canvas as HTMLElement).focus();
    }
  } catch {
    // viewer not ready yet / cross-origin — ignore.
  }
}

function onFrameLoad(): void {
  if (focusTimer) clearInterval(focusTimer);
  let tries = 0;
  focusTimer = setInterval(() => {
    tries += 1;
    const canvas = frameEl.value?.contentDocument?.getElementById('noVNC_canvas');
    if (canvas) {
      focusConsole();
      clearInterval(focusTimer);
      focusTimer = undefined;
    } else if (tries > 40) {
      clearInterval(focusTimer);
      focusTimer = undefined;
    }
  }, 500);
}

async function start(): Promise<void> {
  loading.value = true;
  error.value = '';
  consoleUrl.value = '';
  try {
    const resp = await DeviceService.startKvmSession(deviceId.value);
    if (!resp.consoleUrl || !resp.token) {
      throw new Error('no console session returned');
    }
    bmcHost.value = resp.bmcHost ?? '';
    token.value = resp.token;
    consoleUrl.value = resp.consoleUrl;
  } catch (e: unknown) {
    error.value = e instanceof Error ? e.message : String(e);
  } finally {
    loading.value = false;
  }
}

// ---------------------------------------------------------------------------
// IPMI LAN management (UDP 623) — reached through the same token-gated /bmc/
// proxy path as the console. Credentials never reach the browser.
// ---------------------------------------------------------------------------
async function ipmiFetch<T>(path: string, init?: RequestInit): Promise<T> {
  const url = `/modules/ipam/bmc/${deviceId.value}/__ipmi/${path}?kvmtoken=${encodeURIComponent(token.value)}`;
  const resp = await fetch(url, init);
  if (!resp.ok) {
    let msg = `HTTP ${resp.status}`;
    try {
      const body = (await resp.json()) as { error?: string };
      if (body?.error) msg = body.error;
    } catch {
      // non-JSON error body — keep the status message.
    }
    throw new Error(msg);
  }
  return (await resp.json()) as T;
}

interface DeviceInfo {
  manufacturer?: string;
  product?: string;
  serialNumber?: string;
  firmwareVersion?: string;
  ipmiVersion?: string;
  deviceId?: number;
}

interface PowerState {
  on: boolean;
  powerRestorePolicy: string;
  powerFault: boolean;
  powerOverload: boolean;
  intrusion: boolean;
  coolingFault: boolean;
  driveFault: boolean;
  identifyActive: boolean;
}

interface SensorReading {
  number: number;
  name: string;
  type: string;
  reading: string;
  value: number;
  unit: string;
  status: string;
  isThreshold: boolean;
  valid: boolean;
  lowerCritical?: number;
  lowerNonCritical?: number;
  upperNonCritical?: number;
  upperCritical?: number;
}

interface SelEntry {
  recordId: number;
  recordType: string;
  timestamp?: string;
  sensorType?: string;
  sensorName?: string;
  description: string;
  severity?: string;
}

// --- Overview -------------------------------------------------------------
const info = ref<DeviceInfo | null>(null);
const infoLoading = ref(false);
const infoError = ref('');

async function loadInfo(): Promise<void> {
  infoLoading.value = true;
  infoError.value = '';
  try {
    info.value = await ipmiFetch<DeviceInfo>('info');
  } catch (e: unknown) {
    infoError.value = e instanceof Error ? e.message : String(e);
  } finally {
    infoLoading.value = false;
  }
}

// --- Sensors --------------------------------------------------------------
const sensors = ref<SensorReading[]>([]);
const sensorsLoading = ref(false);
const sensorsError = ref('');

async function loadSensors(): Promise<void> {
  sensorsLoading.value = true;
  sensorsError.value = '';
  try {
    const resp = await ipmiFetch<{ sensors: SensorReading[] }>('sensors');
    sensors.value = resp.sensors ?? [];
  } catch (e: unknown) {
    sensorsError.value = e instanceof Error ? e.message : String(e);
  } finally {
    sensorsLoading.value = false;
  }
}

function sensorStatusColor(status: string): string {
  switch (status.toLowerCase()) {
    case 'ok':
      return 'green';
    case 'lnc':
    case 'unc':
    case 'nc':
      return 'orange';
    case 'lcr':
    case 'ucr':
    case 'lnr':
    case 'unr':
    case 'cr':
    case 'nr':
      return 'red';
    default:
      return 'default';
  }
}

const sensorColumns = [
  { title: 'Sensor', dataIndex: 'name', key: 'name' },
  { title: 'Reading', key: 'reading' },
  { title: 'Status', key: 'status', width: 110 },
  { title: 'Type', dataIndex: 'type', key: 'type', width: 160 },
];

// --- Power ----------------------------------------------------------------
const power = ref<PowerState | null>(null);
const powerLoading = ref(false);
const powerError = ref('');
const powerActionBusy = ref('');

async function loadPower(): Promise<void> {
  powerLoading.value = true;
  powerError.value = '';
  try {
    power.value = await ipmiFetch<PowerState>('power');
  } catch (e: unknown) {
    powerError.value = e instanceof Error ? e.message : String(e);
  } finally {
    powerLoading.value = false;
  }
}

async function doPowerAction(action: string): Promise<void> {
  powerActionBusy.value = action;
  powerError.value = '';
  try {
    await ipmiFetch('power', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ action }),
    });
    // Give the BMC a moment, then refresh the reported state.
    await new Promise((r) => setTimeout(r, 1500));
    await loadPower();
  } catch (e: unknown) {
    powerError.value = e instanceof Error ? e.message : String(e);
  } finally {
    powerActionBusy.value = '';
  }
}

// --- Event Log ------------------------------------------------------------
const sel = ref<SelEntry[]>([]);
const selLoading = ref(false);
const selError = ref('');

async function loadSel(): Promise<void> {
  selLoading.value = true;
  selError.value = '';
  try {
    const resp = await ipmiFetch<{ entries: SelEntry[] }>('sel');
    // newest first
    sel.value = (resp.entries ?? []).slice().reverse();
  } catch (e: unknown) {
    selError.value = e instanceof Error ? e.message : String(e);
  } finally {
    selLoading.value = false;
  }
}

function severityColor(sev?: string): string {
  switch ((sev ?? '').toLowerCase()) {
    case 'critical':
      return 'red';
    case 'warning':
      return 'orange';
    case 'ok':
    case 'info':
      return 'green';
    default:
      return 'default';
  }
}

const selColumns = [
  { title: 'Time', dataIndex: 'timestamp', key: 'timestamp', width: 180 },
  { title: 'Severity', key: 'severity', width: 110 },
  { title: 'Sensor', dataIndex: 'sensorType', key: 'sensorType', width: 180 },
  { title: 'Description', dataIndex: 'description', key: 'description' },
];

// Lazy-load each tab's data the first time it is opened.
const loaded = ref<Record<string, boolean>>({});
function onTabChange(key: string): void {
  if (key === 'console') return;
  if (loaded.value[key]) return;
  loaded.value[key] = true;
  if (key === 'overview') void loadInfo();
  else if (key === 'sensors') void loadSensors();
  else if (key === 'power') void loadPower();
  else if (key === 'eventlog') void loadSel();
}

onMounted(start);
onBeforeUnmount(() => {
  if (focusTimer) clearInterval(focusTimer);
});
</script>

<template>
  <div class="ipmi-view">
    <div class="ipmi-view__bar">
      <span class="ipmi-view__title">{{ $t('ipam.page.ipmiView.title') }}</span>
      <span v-if="bmcHost" class="ipmi-view__host">{{ bmcHost }}</span>
      <span class="ipmi-view__spacer" />
      <Button size="small" :loading="loading" @click="start">
        {{ $t('ipam.page.ipmiView.reconnect') }}
      </Button>
    </div>

    <div v-if="loading" class="ipmi-view__center">
      <Spin :tip="$t('ipam.page.ipmiView.connecting')" />
    </div>

    <Result
      v-else-if="error"
      status="error"
      :title="$t('ipam.page.ipmiView.failed')"
      :sub-title="error"
    >
      <template #extra>
        <Button type="primary" @click="start">{{ $t('ipam.page.ipmiView.reconnect') }}</Button>
      </template>
    </Result>

    <Tabs
      v-else
      v-model:active-key="activeTab"
      class="ipmi-view__tabs"
      @change="onTabChange"
    >
      <!-- Console -->
      <TabPane key="console" tab="Console" force-render>
        <div class="ipmi-view__frame-wrap">
          <iframe
            v-if="consoleUrl"
            ref="frameEl"
            :src="consoleUrl"
            class="ipmi-view__frame"
            allow="fullscreen"
            title="IPMI KVM Console"
            @load="onFrameLoad"
            @mouseenter="focusConsole"
          />
        </div>
      </TabPane>

      <!-- Overview -->
      <TabPane key="overview" tab="Overview">
        <div class="ipmi-view__panel">
          <div class="ipmi-view__panel-bar">
            <Button size="small" :loading="infoLoading" @click="loadInfo">Refresh</Button>
          </div>
          <Alert v-if="infoError" type="error" :message="infoError" show-icon />
          <Spin v-else-if="infoLoading" />
          <Descriptions v-else-if="info" bordered :column="1" size="small">
            <DescriptionsItem label="Manufacturer">{{ info.manufacturer || '—' }}</DescriptionsItem>
            <DescriptionsItem label="Product">{{ info.product || '—' }}</DescriptionsItem>
            <DescriptionsItem label="Serial Number">{{ info.serialNumber || '—' }}</DescriptionsItem>
            <DescriptionsItem label="BMC Firmware">{{ info.firmwareVersion || '—' }}</DescriptionsItem>
            <DescriptionsItem label="IPMI Version">{{ info.ipmiVersion || '—' }}</DescriptionsItem>
            <DescriptionsItem label="Device ID">{{ info.deviceId ?? '—' }}</DescriptionsItem>
          </Descriptions>
        </div>
      </TabPane>

      <!-- Sensors -->
      <TabPane key="sensors" tab="Sensors">
        <div class="ipmi-view__panel">
          <div class="ipmi-view__panel-bar">
            <span class="ipmi-view__count">{{ sensors.length }} sensors</span>
            <span class="ipmi-view__spacer" />
            <Button size="small" :loading="sensorsLoading" @click="loadSensors">Refresh</Button>
          </div>
          <Alert v-if="sensorsError" type="error" :message="sensorsError" show-icon />
          <Table
            v-else
            :columns="sensorColumns"
            :data-source="sensors"
            :loading="sensorsLoading"
            :pagination="false"
            :row-key="(r: SensorReading) => r.number"
            size="small"
            sticky
          >
            <template #bodyCell="{ column, record }">
              <template v-if="column.key === 'reading'">
                <span v-if="record.valid">{{ record.reading }} {{ record.unit }}</span>
                <span v-else class="ipmi-view__muted">—</span>
              </template>
              <template v-else-if="column.key === 'status'">
                <Tag :color="sensorStatusColor(record.status)">{{ record.status || 'n/a' }}</Tag>
              </template>
            </template>
          </Table>
        </div>
      </TabPane>

      <!-- Power -->
      <TabPane key="power" tab="Power">
        <div class="ipmi-view__panel">
          <div class="ipmi-view__panel-bar">
            <Button size="small" :loading="powerLoading" @click="loadPower">Refresh</Button>
          </div>
          <Alert v-if="powerError" type="error" :message="powerError" show-icon class="ipmi-view__mb" />
          <Spin v-if="powerLoading && !power" />
          <template v-if="power">
            <div class="ipmi-view__power-state">
              <Badge :status="power.on ? 'success' : 'default'" />
              <span class="ipmi-view__power-label">{{ power.on ? 'Powered On' : 'Powered Off' }}</span>
            </div>
            <Descriptions bordered :column="1" size="small" class="ipmi-view__mb">
              <DescriptionsItem label="Restore Policy">{{ power.powerRestorePolicy || '—' }}</DescriptionsItem>
              <DescriptionsItem label="Power Fault">{{ power.powerFault ? 'Yes' : 'No' }}</DescriptionsItem>
              <DescriptionsItem label="Power Overload">{{ power.powerOverload ? 'Yes' : 'No' }}</DescriptionsItem>
              <DescriptionsItem label="Cooling Fault">{{ power.coolingFault ? 'Yes' : 'No' }}</DescriptionsItem>
              <DescriptionsItem label="Drive Fault">{{ power.driveFault ? 'Yes' : 'No' }}</DescriptionsItem>
              <DescriptionsItem label="Chassis Intrusion">{{ power.intrusion ? 'Yes' : 'No' }}</DescriptionsItem>
            </Descriptions>
            <Space wrap>
              <Button
                type="primary"
                :loading="powerActionBusy === 'on'"
                :disabled="power.on"
                @click="doPowerAction('on')"
              >
                Power On
              </Button>
              <Popconfirm title="Graceful shutdown of the OS?" @confirm="doPowerAction('soft')">
                <Button :loading="powerActionBusy === 'soft'" :disabled="!power.on">Soft Shutdown</Button>
              </Popconfirm>
              <Popconfirm title="Force power off? Unsaved data may be lost." @confirm="doPowerAction('off')">
                <Button danger :loading="powerActionBusy === 'off'" :disabled="!power.on">Power Off</Button>
              </Popconfirm>
              <Popconfirm title="Power cycle the chassis?" @confirm="doPowerAction('cycle')">
                <Button danger :loading="powerActionBusy === 'cycle'" :disabled="!power.on">Power Cycle</Button>
              </Popconfirm>
              <Popconfirm title="Hard reset the chassis?" @confirm="doPowerAction('reset')">
                <Button danger :loading="powerActionBusy === 'reset'" :disabled="!power.on">Reset</Button>
              </Popconfirm>
              <Popconfirm title="Send a diagnostic interrupt (NMI)?" @confirm="doPowerAction('diag')">
                <Button :loading="powerActionBusy === 'diag'" :disabled="!power.on">Diag Interrupt</Button>
              </Popconfirm>
            </Space>
          </template>
        </div>
      </TabPane>

      <!-- Event Log -->
      <TabPane key="eventlog" tab="Event Log">
        <div class="ipmi-view__panel">
          <div class="ipmi-view__panel-bar">
            <span class="ipmi-view__count">{{ sel.length }} entries</span>
            <span class="ipmi-view__spacer" />
            <Button size="small" :loading="selLoading" @click="loadSel">Refresh</Button>
          </div>
          <Alert v-if="selError" type="error" :message="selError" show-icon />
          <Table
            v-else
            :columns="selColumns"
            :data-source="sel"
            :loading="selLoading"
            :pagination="{ pageSize: 50, hideOnSinglePage: true }"
            :row-key="(r: SelEntry) => r.recordId"
            size="small"
            sticky
          >
            <template #bodyCell="{ column, record }">
              <template v-if="column.key === 'timestamp'">
                <span>{{ record.timestamp || '—' }}</span>
              </template>
              <template v-else-if="column.key === 'severity'">
                <Tag :color="severityColor(record.severity)">{{ record.severity || '—' }}</Tag>
              </template>
              <template v-else-if="column.key === 'sensorType'">
                <span>{{ record.sensorType || record.recordType || '—' }}</span>
              </template>
            </template>
          </Table>
        </div>
      </TabPane>
    </Tabs>
  </div>
</template>

<style scoped>
/* Vben Admin exposes its palette as HSL triplets on :root; wrapping them in
   hsl(...) lets the page track both light and dark themes without hardcoded
   colors bleeding through. */
.ipmi-view {
  display: flex;
  flex-direction: column;
  height: 100vh;
  background: hsl(var(--background));
  color: hsl(var(--foreground));
}
.ipmi-view__bar {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 6px 12px;
  background: hsl(var(--card));
  color: hsl(var(--foreground));
  border-bottom: 1px solid hsl(var(--border));
  font-size: 13px;
}
.ipmi-view__title {
  font-weight: 600;
}
.ipmi-view__host {
  color: hsl(var(--muted-foreground));
  font-family: monospace;
}
.ipmi-view__spacer {
  flex: 1;
}
.ipmi-view__center {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
}
.ipmi-view__tabs {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}
.ipmi-view__tabs :deep(.ant-tabs-content),
.ipmi-view__tabs :deep(.ant-tabs-tabpane) {
  height: 100%;
}
.ipmi-view__tabs :deep(.ant-tabs-content-holder) {
  flex: 1;
  min-height: 0;
}
.ipmi-view__tabs :deep(.ant-tabs-nav) {
  margin: 0;
  padding: 0 12px;
}
.ipmi-view__frame-wrap {
  height: 100%;
  background: #1e1e1e; /* neutral backdrop behind the BMC console during load */
}
.ipmi-view__frame {
  width: 100%;
  height: 100%;
  border: 0;
}
.ipmi-view__panel {
  height: 100%;
  overflow: auto;
  padding: 16px;
}
.ipmi-view__panel-bar {
  display: flex;
  align-items: center;
  margin-bottom: 12px;
}
.ipmi-view__count {
  color: hsl(var(--muted-foreground));
  font-size: 13px;
}
.ipmi-view__muted {
  color: hsl(var(--muted-foreground));
}
.ipmi-view__power-state {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-bottom: 12px;
  font-size: 16px;
  font-weight: 600;
}
.ipmi-view__mb {
  margin-bottom: 12px;
}
</style>
