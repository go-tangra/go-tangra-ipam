import './styles/tailwind.css';
import type { TangraModule } from './sdk';
import routes from './routes';
import { useIpamSubnetStore } from './stores/ipam-subnet.state';
import { useIpamIpAddressStore } from './stores/ipam-ip-address.state';
import { useIpamVlanStore } from './stores/ipam-vlan.state';
import { useIpamDeviceStore } from './stores/ipam-device.state';
import { useIpamLocationStore } from './stores/ipam-location.state';
import { useIpamSystemStore } from './stores/ipam-system.state';
import { useIpamIpScanStore } from './stores/ipam-ip-scan.state';
import { useIpamGroupStore } from './stores/ipam-group.state';
import { useIpamHostGroupStore } from './stores/ipam-host-group.state';
import { useIpamDevicePackageStore } from './stores/ipam-device-package.state';
import enUS from './locales/en-US.json';

const ipamModule: TangraModule = {
  id: 'ipam',
  version: '1.0.0',
  routes,
  stores: {
    'ipam-subnet': useIpamSubnetStore,
    'ipam-ip-address': useIpamIpAddressStore,
    'ipam-vlan': useIpamVlanStore,
    'ipam-device': useIpamDeviceStore,
    'ipam-location': useIpamLocationStore,
    'ipam-system': useIpamSystemStore,
    'ipam-ip-scan': useIpamIpScanStore,
    'ipam-group': useIpamGroupStore,
    'ipam-host-group': useIpamHostGroupStore,
    'ipam-device-package': useIpamDevicePackageStore,
  },
  locales: {
    'en-US': enUS,
  },
};

export default ipamModule;
