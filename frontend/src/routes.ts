import type { RouteRecordRaw } from 'vue-router';

const routes: RouteRecordRaw[] = [
  {
    path: '/ipam',
    name: 'IPAM',
    component: () => import('shell/app-layout'),
    redirect: '/ipam/subnet',
    meta: {
      order: 2020,
      icon: 'lucide:network',
      title: 'ipam.menu.moduleName',
      keepAlive: true,
      authority: ['platform:admin', 'tenant:manager'],
    },
    children: [
      {
        path: 'subnet',
        name: 'IpamSubnets',
        meta: {
          icon: 'lucide:git-branch',
          title: 'ipam.menu.subnet',
          authority: ['platform:admin', 'tenant:manager'],
        },
        component: () => import('./views/subnet/index.vue'),
      },
      {
        path: 'ip-address',
        name: 'IpamAddresses',
        meta: {
          icon: 'lucide:hash',
          title: 'ipam.menu.ipAddress',
          authority: ['platform:admin', 'tenant:manager'],
        },
        component: () => import('./views/ip-address/index.vue'),
      },
      {
        path: 'vlan',
        name: 'IpamVlans',
        meta: {
          icon: 'lucide:layers',
          title: 'ipam.menu.vlan',
          authority: ['platform:admin', 'tenant:manager'],
        },
        component: () => import('./views/vlan/index.vue'),
      },
      {
        path: 'device',
        name: 'IpamDevices',
        meta: {
          icon: 'lucide:server',
          title: 'ipam.menu.device',
          authority: ['platform:admin', 'tenant:manager'],
        },
        component: () => import('./views/device/index.vue'),
      },
      {
        path: 'location',
        name: 'IpamLocations',
        meta: {
          icon: 'lucide:map-pin',
          title: 'ipam.menu.location',
          authority: ['platform:admin', 'tenant:manager'],
        },
        component: () => import('./views/location/index.vue'),
      },
      {
        path: 'group',
        name: 'IpamGroups',
        meta: {
          icon: 'lucide:folder',
          title: 'ipam.menu.groups',
          authority: ['platform:admin', 'tenant:manager'],
        },
        component: () => import('./views/group/index.vue'),
      },
      {
        path: 'host-group',
        name: 'IpamHostGroups',
        meta: {
          icon: 'lucide:users',
          title: 'ipam.menu.hostGroups',
          authority: ['platform:admin', 'tenant:manager'],
        },
        component: () => import('./views/host-group/index.vue'),
      },
    ],
  },
];

export default routes;
