<script lang="ts" setup>
import { ref, onMounted, computed } from 'vue';
import { useRoute } from 'vue-router';

import { Spin, Result, Button } from 'ant-design-vue';

import { $t } from 'shell/locales';
import { DeviceService } from '../../api/services';

const route = useRoute();
const deviceId = computed(() => String(route.params.deviceId ?? ''));

const loading = ref(true);
const error = ref('');
const consoleUrl = ref('');
const bmcHost = ref('');

async function start(): Promise<void> {
  loading.value = true;
  error.value = '';
  consoleUrl.value = '';
  try {
    const resp = await DeviceService.startKvmSession(deviceId.value);
    if (!resp.consoleUrl) {
      throw new Error('no console URL returned');
    }
    bmcHost.value = resp.bmcHost ?? '';
    consoleUrl.value = resp.consoleUrl;
  } catch (e: unknown) {
    error.value = e instanceof Error ? e.message : String(e);
  } finally {
    loading.value = false;
  }
}

onMounted(start);
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

    <div class="ipmi-view__body">
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

      <iframe
        v-else-if="consoleUrl"
        :src="consoleUrl"
        class="ipmi-view__frame"
        allow="fullscreen"
        title="IPMI KVM Console"
      />
    </div>
  </div>
</template>

<style scoped>
.ipmi-view {
  display: flex;
  flex-direction: column;
  height: 100vh;
  background: #1e1e1e;
}
.ipmi-view__bar {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 6px 12px;
  background: #141414;
  color: #e6e6e6;
  font-size: 13px;
}
.ipmi-view__title {
  font-weight: 600;
}
.ipmi-view__host {
  color: #8c8c8c;
  font-family: monospace;
}
.ipmi-view__spacer {
  flex: 1;
}
.ipmi-view__body {
  flex: 1;
  position: relative;
}
.ipmi-view__center {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
}
.ipmi-view__frame {
  width: 100%;
  height: 100%;
  border: 0;
}
</style>
