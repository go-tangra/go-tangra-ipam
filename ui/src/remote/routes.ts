import type { RouteRecordRaw } from 'vue-router'
import '@/main.css'

// Routes mounted by the platform shell under their own error boundary.
export const routes: RouteRecordRaw[] = [
  { path: '/ipam', name: 'ipam-subnets', component: () => import('@/views/subnets/index.vue'), meta: { module: 'ipam' } },
  { path: '/ipam/addresses', name: 'ipam-addresses', component: () => import('@/views/addresses/index.vue'), meta: { module: 'ipam' } },
  { path: '/ipam/devices', name: 'ipam-devices', component: () => import('@/views/devices/index.vue'), meta: { module: 'ipam' } },
  { path: '/ipam/devices/:id', name: 'ipam-device', component: () => import('@/views/devices/detail.vue'), meta: { module: 'ipam' } },
  { path: '/ipam/vlans', name: 'ipam-vlans', component: () => import('@/views/vlans/index.vue'), meta: { module: 'ipam' } },
  { path: '/ipam/locations', name: 'ipam-locations', component: () => import('@/views/locations/index.vue'), meta: { module: 'ipam' } },
  { path: '/ipam/groups', name: 'ipam-groups', component: () => import('@/views/groups/index.vue'), meta: { module: 'ipam' } },
  { path: '/ipam/scans', name: 'ipam-scans', component: () => import('@/views/scans/index.vue'), meta: { module: 'ipam' } },
  { path: '/ipam/dashboard', name: 'ipam-dashboard', component: () => import('@/views/dashboard/index.vue'), meta: { module: 'ipam' } },
]
export default routes
