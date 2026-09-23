import { computed } from 'vue'
import { zodToFields } from '@freya/ui/forms'
import { deviceSchema } from '@/schemas'
import { useLocations } from '@/stores/locations'

// Field layout for the device create/edit dialog; location and rack pick from
// the loaded locations (racks are locations of type "rack").
export function useDeviceFields() {
  const locations = useLocations()
  if (!locations.items.length) void locations.list()
  return computed(() =>
    zodToFields(deviceSchema, {
      name: { cols: 6 },
      device_type: { label: 'Type', cols: 6 },
      status: { cols: 6 },
      management_ip: { label: 'Management IP', cols: 6 },
      manufacturer: { cols: 6 },
      model: { cols: 6 },
      location_id: { label: 'Location', type: 'select', cols: 12, options: locations.items.filter((l) => l.location_type !== 'rack').map((l) => ({ title: `${l.name} (${l.location_type})`, value: l.id })) },
      rack_id: { label: 'Rack', type: 'select', cols: 6, options: locations.items.filter((l) => l.location_type === 'rack').map((l) => ({ title: `${l.name} (${l.rack_size_u ?? 0}U)`, value: l.id })) },
      rack_position: { label: 'Bottom U', cols: 3 },
      device_height_u: { label: 'Height (U)', cols: 3 },
    }),
  )
}
